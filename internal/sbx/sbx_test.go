package sbx

import (
	"errors"
	"reflect"
	"strings"
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

func same(a, b string) bool { return a == b }

func TestFindMatchesPrimaryWorkspaceOnly(t *testing.T) {
	all := []Sandbox{{Name: "docs", Workspaces: []string{"/w/docs", "/w/app"}}, {Name: "app", Workspaces: []string{"/w/app"}}}
	s, ok := Find(all, "/w/app", "default", same)
	if !ok || s.Name != "app" {
		t.Fatalf("%+v %v", s, ok)
	}
}

// A repository has a sandbox per setup: the default is the first on the
// workspace whose name does not start with hq-, whatever its name; another
// setup's is the one named hq-<setup>-<dir> on it (#135).
func TestFindTellsTheSetupsOfAWorkspaceApart(t *testing.T) {
	all := []Sandbox{
		{Name: "hq-local-app", Workspaces: []string{"/w/app"}},
		{Name: "hq-gone-app", Workspaces: []string{"/w/app"}},
		{Name: "my-own", Workspaces: []string{"/w/app"}},
		{Name: "claude-app", Workspaces: []string{"/w/app"}},
		{Name: "hq-local-app", Workspaces: []string{"/x/app"}},
		{Name: "hq-other-app", Workspaces: []string{"/x/app"}},
	}
	for _, c := range []struct{ path, setup, want string }{
		{"/w/app", "default", "my-own"},
		{"/w/app", "local", "hq-local-app"},
		{"/w/app", "gone", "hq-gone-app"},
		{"/w/app", "other", ""},
		{"/x/app", "default", ""}, // an hq- sandbox is never the default
		{"/x/app", "other", "hq-other-app"},
	} {
		s, ok := Find(all, c.path, c.setup, same)
		if c.want == "" {
			if ok {
				t.Errorf("%s %s: found %s", c.path, c.setup, s.Name)
			}
			continue
		}
		if !ok || s.Name != c.want || s.Workspaces[0] != c.path {
			t.Errorf("%s %s: %+v %v, want %s", c.path, c.setup, s, ok, c.want)
		}
	}
}

// Every sandbox of a repository is its default and each setup's on its
// workspace, the default first: neither a second sandbox made by hand on
// it, which hq never uses, nor another workspace's (#136).
func TestOfRepositoryIsTheDefaultThenEachSetupsSandbox(t *testing.T) {
	all := []Sandbox{
		{Name: "hq-local-app", Workspaces: []string{"/w/app"}},
		{Name: "my-own", Workspaces: []string{"/w/app"}},
		{Name: "docs", Workspaces: []string{"/w/docs", "/w/app"}},
		{Name: "second", Workspaces: []string{"/w/app"}},
		{Name: "hq-gone-app", Workspaces: []string{"/w/app"}},
		{Name: "hq-local-lib", Workspaces: []string{"/w/app"}},
		{Name: "hq-local-app", Workspaces: []string{"/x/app"}},
	}
	for _, c := range []struct{ path, want string }{
		{"/w/app", "my-own hq-local-app hq-gone-app"},
		{"/x/app", "hq-local-app"},
		{"/w/lib", ""},
	} {
		var got []string
		for _, s := range OfRepository(all, c.path, same) {
			if s.Workspaces[0] != c.path {
				t.Errorf("%s: %+v", c.path, s)
			}
			got = append(got, s.Name)
		}
		if strings.Join(got, " ") != c.want {
			t.Errorf("%s: %q, want %q", c.path, got, c.want)
		}
	}
}

// A setup's sandbox is hq-<setup>-<dir>, the directory cleaned as sbx
// cleans it for its own names: lowercased, anything but letters, digits,
// hyphens and periods a hyphen.
func TestSetupSandboxName(t *testing.T) {
	for _, c := range []struct{ setup, dir, want string }{
		{"local", "app", "hq-local-app"},
		{"local", "My_Repo x", "hq-local-my-repo-x"},
		{"local", "foo-local", "hq-local-foo-local"},
		{"qwen.big", "v1.2", "hq-qwen.big-v1.2"},
	} {
		if got := SandboxName(c.setup, c.dir); got != c.want {
			t.Errorf("%s %s: %q, want %q", c.setup, c.dir, got, c.want)
		}
	}
	if got := SandboxName("default", "app"); got != "" {
		t.Errorf("the default's name is sbx's own: %q", got)
	}
}

// sbx names a Claude sandbox it creates without a name claude-<dir>, dir
// cleaned the same way (verified with sbx v0.47.0).
func TestDefaultName(t *testing.T) {
	for dir, want := range map[string]string{"app": "claude-app", "My_Repo x": "claude-my-repo-x", "v1.2": "claude-v1.2"} {
		if got := DefaultName(dir); got != want {
			t.Errorf("%s: %q, want %q", dir, got, want)
		}
	}
}

// A directory with letters beyond ASCII is cleaned as sbx v0.47.0 cleaned
// it for the names of shell sandboxes it created in /tmp: lowercased as
// Unicode lowercases it (the Kelvin sign and İ become k and i), then each
// byte that is not a lowercase ASCII letter, a digit, a hyphen or a period
// a hyphen, so a two-byte letter is two hyphens and a letter whose
// lowercase takes three bytes (Ⱥ, ⱥ) three.
func TestNamesOfADirectoryBeyondASCII(t *testing.T) {
	for dir, want := range map[string]string{
		"Zażółć_Ünï x": "za-----------n---x",
		"xİy":          "xiy",
		"a\u212Ab":     "akb",
		"aȺb":          "a---b",
	} {
		if got := DefaultName(dir); got != "claude-"+want {
			t.Errorf("%q: %q, want %q", dir, got, "claude-"+want)
		}
		if got := SandboxName("local", dir); got != "hq-local-"+want {
			t.Errorf("%q: %q, want %q", dir, got, "hq-local-"+want)
		}
		if got := SetupOf("hq-local-"+want, dir); got != "local" {
			t.Errorf("%q: setup %q", dir, got)
		}
	}
}

// The setup of a sandbox is read off its name: what stands between hq- and
// -<dir>; default for any other name.
func TestSetupOf(t *testing.T) {
	for _, c := range []struct{ name, dir, want string }{
		{"claude-app", "app", "default"},
		{"my-own", "app", "default"},
		{"hq-local-app", "app", "local"},
		{"hq-a-b-app", "app", "a-b"},
		{"claude-foo-local", "foo-local", "default"},
		{"hq-local-foo-local", "foo-local", "local"},
		{"hq-local-my-repo-x", "My_Repo x", "local"},
		{"hq-app", "app", "default"},
		{"hq--app", "app", "default"},
		{"hq-local-lib", "app", "default"},
	} {
		if got := SetupOf(c.name, c.dir); got != c.want {
			t.Errorf("%s in %s: %q, want %q", c.name, c.dir, got, c.want)
		}
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
	c := Client{Run: r, Platform: platform.WSL{SbxSide: platform.WindowsSbx{Run: &fakeRun{err: errors.New("wslpath: boom")}}}}
	if _, err := c.List(); err == nil {
		t.Fatal("want the wslpath error")
	}
}

func TestCreateGivesSbxItsOwnPathAndCommand(t *testing.T) {
	r := &fakeRun{}
	if err := (Client{Run: r, Platform: platformtest.Fake{Sbx: "sbx.exe"}}).Create("/w/app", Options{}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"sbx.exe", "create", "--quiet", "claude", `F:\w\app`}; !reflect.DeepEqual(r.args, want) {
		t.Fatalf("args %q", r.args)
	}
	c := Client{Run: r, Platform: platform.WSL{SbxSide: platform.WindowsSbx{Run: &fakeRun{err: errors.New("wslpath: boom")}}}}
	if err := c.Create("/w/app", Options{}); err == nil {
		t.Fatal("want the wslpath error")
	}
}

// The template and static MCP servers go to sbx create only when set (#58).
func TestCreateGivesTheTemplateAndStaticMCPServersWhenSet(t *testing.T) {
	for _, c := range []struct {
		name string
		o    Options
		want []string
	}{
		{"template", Options{Template: "sbx-image:local"}, []string{"--template", "sbx-image:local"}},
		{"servers", Options{StaticMCP: []string{"pencil", "docs"}}, []string{"--static-mcp", "pencil,docs"}},
		{"both", Options{Template: "ghcr.io/o/i:latest", StaticMCP: []string{"pencil"}}, []string{"--template", "ghcr.io/o/i:latest", "--static-mcp", "pencil"}},
	} {
		r := &fakeRun{}
		if err := (Client{Run: r, Platform: platformtest.Fake{Sbx: "sbx"}}).Create("/w/app", c.o); err != nil {
			t.Fatal(err)
		}
		want := append(append([]string{"sbx", "create", "--quiet"}, c.want...), "claude", `F:\w\app`)
		if !reflect.DeepEqual(r.args, want) {
			t.Errorf("%s: args %q", c.name, r.args)
		}
	}
}

// A setup's sandbox is created under its name; one with a model through
// sbx run --detached, as sbx create takes no --model (#135).
func TestCreateANamedSandboxAndOneWithAModel(t *testing.T) {
	for _, c := range []struct {
		name string
		o    Options
		want []string
	}{
		{"named", Options{Name: "hq-local-app", Template: "img:x"},
			[]string{"sbx", "create", "--quiet", "--name", "hq-local-app", "--template", "img:x", "claude", `F:\w\app`}},
		{"with a model", Options{Name: "hq-local-app", Template: "img:x", StaticMCP: []string{"docs"}, Model: "qwen-local", Provider: "lmstudio"},
			[]string{"sbx", "run", "--detached", "--name", "hq-local-app", "--template", "img:x", "--static-mcp", "docs", "--model", "qwen-local", "--provider", "lmstudio", "claude", `F:\w\app`}},
		{"a provider alone", Options{Name: "hq-p-app", Provider: "lmstudio"},
			[]string{"sbx", "run", "--detached", "--name", "hq-p-app", "--provider", "lmstudio", "claude", `F:\w\app`}},
	} {
		r := &fakeRun{}
		if err := (Client{Run: r, Platform: platformtest.Fake{Sbx: "sbx"}}).Create("/w/app", c.o); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(r.args, c.want) {
			t.Errorf("%s: args %q", c.name, r.args)
		}
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
		"create": func() error { return c.Create("/w/app", Options{}) },
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

	// Without stderr (no answer in time) the error stays as it is.
	r.err = &proc.Error{Name: "sbx.exe", Msg: "signal: killed"}
	var pe *proc.Error
	if _, err := c.List(); !errors.As(err, &pe) || pe.Msg != "signal: killed" {
		t.Fatalf("%v", err)
	}
}

// sbx missing: the platform says what was not found, on WSL both sbx and
// sbx.exe (#153).
func TestAMissingSbxIsSaidAsThePlatformSaysIt(t *testing.T) {
	r := &fakeRun{err: &proc.Error{Name: "sbx.exe", Msg: "executable file not found in $PATH", NotFound: true}}
	var pe *proc.Error
	_, err := Client{Run: r, Platform: platform.WSL{SbxSide: platform.WindowsSbx{}}}.List()
	if !errors.As(err, &pe) || !pe.NotFound || pe.Msg != "neither sbx nor sbx.exe found" {
		t.Fatalf("WSL: %v", err)
	}
	r.err = &proc.Error{Name: "sbx", Msg: "executable file not found in $PATH", NotFound: true}
	if _, err := (Client{Run: r, Platform: platform.Native{}}).List(); !errors.As(err, &pe) || pe.Msg != "sbx not found" {
		t.Fatalf("native: %v", err)
	}
	if r.err.(*proc.Error).Msg != "executable file not found in $PATH" {
		t.Fatalf("the runner's own error was changed: %v", r.err)
	}
}

func TestOutputRunsTheCommandInTheSandboxAndReturnsWhatItPrinted(t *testing.T) {
	r := &fakeRun{out: "printed\n"}
	out, err := Client{Run: r, Platform: platform.Native{}}.Output("claude-app", "cat", "/etc/x")
	if err != nil || string(out) != "printed\n" {
		t.Fatalf("%q %v", out, err)
	}
	if !reflect.DeepEqual(r.args, []string{"sbx", "exec", "claude-app", "cat", "/etc/x"}) {
		t.Fatalf("args %q", r.args)
	}
	if _, err := (Client{Run: &fakeRun{err: errors.New("boom")}, Platform: platform.Native{}}).Output("claude-app", "false"); err == nil {
		t.Fatal("want error")
	}
}

// sbx mcp inspect --json gives a local server's command line whole, as sbx
// v0.47.0 prints it.
func TestMCPCommandReadsTheRegisteredCommandLine(t *testing.T) {
	r := &fakeRun{out: `{"name":"hq","type":"local","command":["/usr/bin/env","PATH=/usr/bin:/bin","/u/.local/bin/hq","mcp"],"requires_oauth":false,"resolved_command":"/usr/bin/env"}`}
	argv, ok, err := Client{Run: r, Platform: platform.Native{}}.MCPCommand("hq")
	if err != nil || !ok || !reflect.DeepEqual(argv, []string{"/usr/bin/env", "PATH=/usr/bin:/bin", "/u/.local/bin/hq", "mcp"}) {
		t.Fatalf("%q %v %v", argv, ok, err)
	}
	if !reflect.DeepEqual(r.args, []string{"sbx", "mcp", "inspect", "hq", "--json"}) {
		t.Fatalf("args %q", r.args)
	}
}

// A name sbx does not know fails the inspect: not registered. sbx itself
// missing is still an error.
func TestMCPCommandOfAnUnregisteredServer(t *testing.T) {
	r := &fakeRun{err: &proc.Error{Stderr: "error: MCP server \"hq\" not found\n"}}
	if argv, ok, err := (Client{Run: r, Platform: platform.Native{}}).MCPCommand("hq"); ok || err != nil || argv != nil {
		t.Fatalf("%q %v %v", argv, ok, err)
	}
	r = &fakeRun{err: &proc.Error{NotFound: true}}
	if _, _, err := (Client{Run: r, Platform: platform.Native{}}).MCPCommand("hq"); err == nil {
		t.Fatal("sbx missing: want error")
	}
	// Any other failure of sbx is not taken for "not registered".
	r = &fakeRun{err: &proc.Error{Stderr: "error: cannot reach sandboxd\n"}}
	if _, _, err := (Client{Run: r, Platform: platform.Native{}}).MCPCommand("hq"); err == nil {
		t.Fatal("sandboxd down: want error")
	}
	r = &fakeRun{out: "not json"}
	if _, _, err := (Client{Run: r, Platform: platform.Native{}}).MCPCommand("hq"); err == nil {
		t.Fatal("not json: want error")
	}
}

func TestMCPAddAndLoadRunSbx(t *testing.T) {
	r := &fakeRun{}
	c := Client{Run: r, Platform: platformtest.Fake{}}
	if err := c.MCPAdd("hq", "/usr/bin/env", []string{"PATH=/usr/bin", "/u/hq", "mcp", "sbx"}); err != nil {
		t.Fatal(err)
	}
	if want := []string{c.bin(), "mcp", "add", "hq", "--command", "/usr/bin/env", "--args", "PATH=/usr/bin,/u/hq,mcp,sbx"}; !reflect.DeepEqual(r.args, want) {
		t.Fatalf("add %q", r.args)
	}
	if err := c.MCPLoad("hq", "claude-app"); err != nil {
		t.Fatal(err)
	}
	if want := []string{c.bin(), "mcp", "load", "hq", "--sandbox", "claude-app"}; !reflect.DeepEqual(r.args, want) {
		t.Fatalf("load %q", r.args)
	}
}
