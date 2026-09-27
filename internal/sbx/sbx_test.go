package sbx

import (
	"errors"
	"reflect"
	"testing"
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
	all, err := Client{Run: r, Bin: "sbx"}.List()
	if err != nil || len(all) != 1 || all[0].Name != "claude-hq" || all[0].Running() || all[0].Workspaces[0] != "/Users/r/hq" {
		t.Fatalf("%+v %v", all, err)
	}
	if !reflect.DeepEqual(r.args, []string{"sbx", "ls", "--json"}) {
		t.Fatalf("args %q", r.args)
	}
}

func TestListReportsFailure(t *testing.T) {
	if _, err := (Client{Run: &fakeRun{err: errors.New("boom")}, Bin: "sbx"}).List(); err == nil {
		t.Fatal("want error")
	}
	if _, err := (Client{Run: &fakeRun{out: "not json"}, Bin: "sbx"}).List(); err == nil {
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
	got := Client{Bin: "sbx.exe"}.RunArgv("claude-app", "--settings", "{}", "hi")
	want := []string{"sbx.exe", "run", "--name", "claude-app", "--", "--settings", "{}", "hi"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%q", got)
	}
}
