package sbx

import (
	"errors"
	"reflect"
	"testing"

	"github.com/rkrysinski/hq/internal/platform"
	"github.com/rkrysinski/hq/internal/platform/platformtest"
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
