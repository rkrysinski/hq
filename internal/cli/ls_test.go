package cli

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/proc"
	"github.com/rkrysinski/hq/internal/sbx"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
)

func sandboxesFor(paths ...string) []sbx.Sandbox {
	var s []sbx.Sandbox
	for _, p := range paths {
		parts := strings.Split(p, "/")
		s = append(s, sbx.Sandbox{Name: "claude-" + parts[len(parts)-1], Status: "running", Workspaces: []string{p}})
	}
	return s
}

func sbxNotFound() error { return &proc.Error{Name: "sbx", Msg: "not found", NotFound: true} }

func agentWindow(id, name, repo string, started time.Time, dead bool) tmux.Window {
	return tmux.Window{ID: id, Name: name, PaneDead: dead, Options: map[string]string{
		"id": "id-" + name, "name": name, "repo": repo, "sandbox": "claude-x", "started": strconv.FormatInt(started.Unix(), 10),
	}}
}

func TestLsListsAgentsInAttentionOrder(t *testing.T) {
	f := newFakes()
	f.tmux.windows = []tmux.Window{
		{ID: "@0", Name: "hq", Options: map[string]string{}},
		agentWindow("@1", "old", "/w/app", f.now.Add(-2*time.Hour), false),
		agentWindow("@2", "gone", "/w/lib", f.now.Add(-time.Minute), true),
		agentWindow("@3", "fresh", "/w/lib", f.now.Add(-5*time.Second), false),
	}
	f.states["id-old"] = state.Report{State: state.Done, Since: f.now.Add(-3 * time.Minute), Last: "PR #58 opened"}
	f.states["id-gone"] = state.Report{State: state.Working, Since: f.now.Add(-10 * time.Second), Last: "Tests pass"}
	code, out, _ := f.run("ls")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	// AGE is the time in the state; a dead pane is ended with its last
	// message, counted from its last report.
	want := "NAME   REPO  BRANCH  STATE     AGE  LAST\n" +
		"fresh  lib   -       starting  5s   -\n" +
		"old    app   -       done      3m   PR #58 opened\n" +
		"gone   lib   -       ended     10s  Tests pass\n"
	if out != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
}

func TestLsJSONHasTheSameFields(t *testing.T) {
	f := newFakes()
	f.tmux.windows = []tmux.Window{agentWindow("@1", "a", "/w/app", f.now.Add(-5*time.Minute), false)}
	long := strings.Repeat("word ", 30) + "end?"
	f.states["id-a"] = state.Report{State: state.Question, Since: f.now.Add(-90 * time.Second), Last: long}
	code, out, _ := f.run("ls", "--json")
	var rows []map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &rows) != nil || len(rows) != 1 {
		t.Fatalf("exit %d, %q", code, out)
	}
	r := rows[0]
	if r["name"] != "a" || r["repo"] != "app" || r["state"] != "question" || r["age_seconds"] != 90.0 || r["branch"] != "" || r["last"] != long ||
		r["since"] != "2026-09-27T11:58:30Z" {
		t.Fatalf("row %v", r)
	}
}

func TestLsCutsTheLastMessageToOneColumn(t *testing.T) {
	f := newFakes()
	f.tmux.windows = []tmux.Window{agentWindow("@1", "a", "/w/app", f.now, false)}
	f.states["id-a"] = state.Report{State: state.Done, Since: f.now, Last: strings.Repeat("x", 100)}
	_, out, _ := f.run("ls")
	if !strings.Contains(out, strings.Repeat("x", 59)+"…\n") || strings.Contains(out, strings.Repeat("x", 60)) {
		t.Fatalf("%q", out)
	}
}

func TestLsWithNoAgentsPrintsNothing(t *testing.T) {
	f := newFakes()
	if code, out, _ := f.run("ls"); code != 0 || out != "" {
		t.Fatalf("exit %d %q", code, out)
	}
	if code, out, _ := f.run("ls", "--json"); code != 0 || out != "[]\n" {
		t.Fatalf("json: exit %d %q", code, out)
	}
}

func TestLsRejectsUnknownArgument(t *testing.T) {
	if code, _, _ := newFakes().run("ls", "-l"); code != ExitUsage {
		t.Fatalf("exit %d", code)
	}
}
