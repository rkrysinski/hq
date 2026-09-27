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

// newLsFakes has the agents' sandbox, claude-x, running.
func newLsFakes() *fakes {
	f := newFakes()
	f.sbx.sandboxes = []sbx.Sandbox{{Name: "claude-x", Status: "running"}}
	return f
}

func TestLsListsAgentsInAttentionOrder(t *testing.T) {
	f := newLsFakes()
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
		"old    app   -       done      3m   PR #58 opened\n" +
		"fresh  lib   -       starting  5s   -\n" +
		"gone   lib   -       ended     10s  Tests pass\n"
	if out != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
}

func TestLsPutsWhatNeedsTheUserFirstAndNewestFirstWithinAState(t *testing.T) {
	f := newLsFakes()
	start := f.now.Add(-time.Hour)
	for i, name := range []string{"w", "d1", "n", "q", "d2", "s"} {
		f.tmux.windows = append(f.tmux.windows, agentWindow("@"+strconv.Itoa(i), name, "/w/app", start, false))
	}
	f.tmux.windows = append(f.tmux.windows, agentWindow("@9", "e", "/w/app", start, true))
	report := func(st string, ago time.Duration) state.Report {
		return state.Report{State: st, Since: f.now.Add(-ago)}
	}
	f.states["id-w"] = report(state.Working, time.Second)
	f.states["id-d1"] = report(state.Done, 5*time.Minute)
	f.states["id-n"] = report(state.NeedsInput, time.Hour/2)
	f.states["id-q"] = report(state.Question, 2*time.Minute)
	f.states["id-d2"] = report(state.Done, time.Minute)
	f.states["id-e"] = report(state.Working, 10*time.Second) // its pane died: ended
	_, out, _ := f.run("ls")
	var got []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n")[1:] {
		got = append(got, strings.Fields(l)[0])
	}
	if strings.Join(got, " ") != "n q d2 d1 w s e" {
		t.Fatalf("order %v\n%s", got, out)
	}
	code, js, _ := f.run("ls", "--json")
	var rows []lsRow
	if code != 0 || json.Unmarshal([]byte(js), &rows) != nil || len(rows) != 7 || rows[0].Name != "n" || rows[6].Name != "e" {
		t.Fatalf("json order: %s", js)
	}
}

func TestLsNamesEachAgentsBranch(t *testing.T) {
	f := newLsFakes()
	f.tmux.windows = []tmux.Window{
		agentWindow("@1", "a", "/w/app", f.now, false),
		agentWindow("@2", "b", "/w/app", f.now, false),
		agentWindow("@3", "c", "/w/app", f.now, false),
	}
	f.states["id-a"] = state.Report{State: state.Working, Since: f.now.Add(-time.Second), Branch: "feat/42-x", Cwd: "/w/app/.claude/worktrees/feat-42-x"}
	f.states["id-b"] = state.Report{State: state.Working, Since: f.now.Add(-2 * time.Second), Branch: "fix/7-y"}
	_, out, _ := f.run("ls")
	want := "NAME  REPO  BRANCH     STATE     AGE  LAST\n" +
		"a     app   feat/42-x  working   1s   -\n" +
		"b     app   fix/7-y    working   2s   -\n" +
		"c     app   -          starting  0s   -\n"
	if out != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
	_, js, _ := f.run("ls", "--json")
	if !strings.Contains(js, `"branch": "feat/42-x"`) {
		t.Fatalf("json: %s", js)
	}
}

func TestLsJSONHasTheSameFields(t *testing.T) {
	f := newLsFakes()
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
	f := newLsFakes()
	f.tmux.windows = []tmux.Window{agentWindow("@1", "a", "/w/app", f.now, false)}
	f.states["id-a"] = state.Report{State: state.Done, Since: f.now, Last: strings.Repeat("x", 100)}
	_, out, _ := f.run("ls")
	if !strings.Contains(out, strings.Repeat("x", 59)+"…\n") || strings.Contains(out, strings.Repeat("x", 60)) {
		t.Fatalf("%q", out)
	}
}

func TestLsShowsTheAgentsOfAStoppedSandboxEnded(t *testing.T) {
	f := newLsFakes()
	f.tmux.windows = []tmux.Window{agentWindow("@1", "a", "/w/app", f.now.Add(-time.Minute), false), agentWindow("@2", "b", "/w/lib", f.now.Add(-time.Minute), false)}
	f.tmux.windows[1].Options["sandbox"] = "claude-lib"
	f.sbx.sandboxes = append(f.sbx.sandboxes, sbx.Sandbox{Name: "claude-lib", Status: "stopped"})
	f.states["id-a"] = state.Report{State: state.Working, Since: f.now.Add(-time.Second)}
	f.states["id-b"] = state.Report{State: state.Working, Since: f.now.Add(-time.Second), Last: "Tests pass"}
	_, out, _ := f.run("ls")
	want := "NAME  REPO  BRANCH  STATE    AGE  LAST\n" +
		"a     app   -       working  1s   -\n" +
		"b     lib   -       ended    1s   Tests pass\n"
	if out != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
	// A sandbox sbx no longer lists at all is not running either.
	f.sbx.sandboxes = f.sbx.sandboxes[:1]
	if _, out, _ := f.run("ls"); !strings.Contains(out, "b     lib   -       ended") {
		t.Fatalf("removed sandbox:\n%s", out)
	}
}

func TestLsWithSbxFailingShowsTheStatesItHas(t *testing.T) {
	f := newFakes()
	f.sbx.err = sbxNotFound()
	f.tmux.windows = []tmux.Window{agentWindow("@1", "a", "/w/app", f.now.Add(-time.Minute), false)}
	f.states["id-a"] = state.Report{State: state.Done, Since: f.now.Add(-time.Second), Last: "ok"}
	code, out, errOut := f.run("ls")
	if code != 0 || errOut != "" || !strings.Contains(out, "a     app   -       done   1s   ok") {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
}

func TestLsWithNoAgentsPrintsNothing(t *testing.T) {
	f := newLsFakes()
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
