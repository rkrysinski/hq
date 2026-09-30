package sbx

import (
	"errors"
	"reflect"
	"testing"

	"github.com/rkrysinski/hq/internal/platform"
	"github.com/rkrysinski/hq/internal/platform/platformtest"
	"github.com/rkrysinski/hq/internal/proc"
)

type fakeRun struct {
	out  string
	err  error
	args []string
}

func (f *fakeRun) Run(name string, args ...string) ([]byte, error) {
	f.args = append([]string{name}, args...)
	return []byte(f.out), f.err
}

func TestListParsesRealSbxJSON(t *testing.T) {
	r := &fakeRun{out: `{"sandboxes":[{"name":"claude-hq","id":"97a9","agent":"claude","status":"stopped","last_used_at":"2026-07-27T10:11:29Z","workspaces":["/Users/r/hq"],"created_at":"2026-07-27T10:11:29Z"}]}`}
	all, err := Client{Run: r, Platform: platform.Native{}}.List()
	if err != nil || len(all) != 1 || all[0].Name != "claude-hq" || all[0].Running() || all[0].Workspaces[0] != "/Users/r/hq" {
		t.Fatalf("%+v %v", all, err)
	}
	if !reflect.DeepEqual(r.args, []string{"sbx", "ls", "--json"}) {
		t.Fatalf("args %q", r.args)
	}
}

func TestListReportsFailure(t *testing.T) {
	if _, err := (Client{Run: &fakeRun{err: errors.New("boom")}, Platform: platform.Native{}}).List(); err == nil {
		t.Fatal("want error")
	}
	if _, err := (Client{Run: &fakeRun{out: "not json"}, Platform: platform.Native{}}).List(); err == nil {
		t.Fatal("want error")
	}
}

func TestByWorkspaceMatchesPrimaryWorkspaceOnly(t *testing.T) {
	all := []Sandbox{{Name: "docs", Workspaces: []string{"/w/docs", "/w/app"}}, {Name: "app", Workspaces: []string{"/w/app"}}}
	s, ok := ByWorkspace(all, "/w/app", func(a, b string) bool { return a == b })
	if !ok || s.Name != "app" {
		t.Fatalf("%+v %v", s, ok)
	}
}

func TestRunArgvPassesAgentArgumentsAfterSeparator(t *testing.T) {
	got := Client{Platform: platformtest.Fake{Sbx: "sbx.exe"}}.RunArgv("claude-app", "--settings", "{}", "hi")
	want := []string{"sbx.exe", "run", "--name", "claude-app", "--", "--settings", "{}", "hi"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%q", got)
	}
}

func TestListReportsWorkspacesAsHqSeesThem(t *testing.T) {
	r := &fakeRun{out: `{"sandboxes":[{"name":"claude-app","status":"running","workspaces":["F:\\w\\app","F:\\w\\docs"]}]}`}
	all, err := Client{Run: r, Platform: platformtest.Fake{}}.List()
	if err != nil || !reflect.DeepEqual(all[0].Workspaces, []string{"/w/app", "/w/docs"}) {
		t.Fatalf("%+v %v", all, err)
	}
}

func TestListFailsWhenAWorkspaceCannotBeMapped(t *testing.T) {
	r := &fakeRun{out: `{"sandboxes":[{"name":"claude-app","workspaces":["C:\\w\\app"]}]}`}
	c := Client{Run: r, Platform: platform.WSL{Run: &fakeRun{err: errors.New("wslpath: boom")}}}
	if _, err := c.List(); err == nil {
		t.Fatal("want the wslpath error")
	}
}

func TestCreateGivesSbxItsOwnPathAndCommand(t *testing.T) {
	r := &fakeRun{}
	if err := (Client{Run: r, Platform: platformtest.Fake{Sbx: "sbx.exe"}}).Create("/w/app"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"sbx.exe", "create", "--quiet", "claude", `F:\w\app`}; !reflect.DeepEqual(r.args, want) {
		t.Fatalf("args %q", r.args)
	}
	c := Client{Run: r, Platform: platform.WSL{Run: &fakeRun{err: errors.New("wslpath: boom")}}}
	if err := c.Create("/w/app"); err == nil {
		t.Fatal("want the wslpath error")
	}
}

func TestFailureIsSbxsErrorLineWithItsRemedy(t *testing.T) {
	hv := "error: the Windows Hypervisor Platform is unavailable: either the optional feature is not enabled"
	for _, c := range []struct{ name, stderr, want string }{
		{"a warning comes first", "WARN: mcp gateway teardown\n" + hv, hv},
		{"the remedy is on the next line", "error: Not authenticated to Docker\n  try: sbx login", "error: Not authenticated to Docker; try: sbx login"},
		{"as sbx.exe writes it", "WARN: x\r\nerror: Not authenticated to Docker\r\n  try: sbx login\r\n", "error: Not authenticated to Docker; try: sbx login"},
		{"progress before the error", "Starting sandboxd daemon...\nerror: global network policy has not been initialized\n\ttry: sbx policy init balanced", "error: global network policy has not been initialized; try: sbx policy init balanced"},
		{"only what is indented under the error", "error: boom\n  try: this\n  or: that\nunrelated\n  more", "error: boom; try: this; or: that"},
		{"a blank line ends the remedy", "error: boom\n\n  try: this", "error: boom"},
		{"an error with nothing under it", "error: boom", "error: boom"},
		{"the prefix in capitals", "Error: boom", "Error: boom"},
		{"no error line: the first line that is no warning", "WARN: a\nwarning: b\nstub: no sandbox x\nmore", "stub: no sandbox x"},
		{"only warnings: the first of them", "\nWARN: a\nWARN: b", "WARN: a"},
		{"nothing", "", ""},
		{"blank", " \n", ""},
	} {
		if got := Failure(c.stderr); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestAFailedCallReportsSbxsErrorAndKeepsWhetherSbxIsInstalled(t *testing.T) {
	r := &fakeRun{err: &proc.Error{Name: "sbx.exe", Msg: "WARN: mcp gateway teardown", Stderr: "WARN: mcp gateway teardown\nerror: no virtualization\n  try: sbx diagnose"}}
	c := Client{Run: r, Platform: platformtest.Fake{Sbx: "sbx.exe"}}
	want := "sbx.exe: error: no virtualization; try: sbx diagnose"
	if _, err := c.List(); err == nil || err.Error() != want {
		t.Fatalf("ls: %v", err)
	}
	for name, call := range map[string]func() error{
		"create": func() error { return c.Create("/w/app") },
		"stop":   func() error { return c.Stop("claude-app") },
		"rm":     func() error { return c.Remove("claude-app") },
		"exec":   func() error { return c.Exec("claude-app", "true") },
	} {
		if err := call(); err == nil || err.Error() != want {
			t.Errorf("%s: %v", name, err)
		}
	}
	if r.err.Error() != "sbx.exe: WARN: mcp gateway teardown" {
		t.Fatalf("the runner's own error was changed: %v", r.err)
	}

	// Without stderr (sbx missing, no answer in time) the error stays as it is.
	r.err = &proc.Error{Name: "sbx.exe", Msg: "executable file not found", NotFound: true}
	var pe *proc.Error
	if _, err := c.List(); !errors.As(err, &pe) || !pe.NotFound || pe.Msg != "executable file not found" {
		t.Fatalf("%v", err)
	}
}
