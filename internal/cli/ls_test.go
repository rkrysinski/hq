package cli

import (
	"encoding/json"
	"errors"
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
	f.tmux.windows[2].DeadAt = f.now.Add(-4 * time.Second)
	f.states["id-gone"] = state.Report{State: state.Working, Since: f.now.Add(-10 * time.Second), Last: "Tests pass"}
	code, out, _ := f.run("ls")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	// AGE is the time in the state; a dead pane is ended with its last
	// message, counted from when the pane died (#73).
	want := "NAME   REPO  BRANCH  STATE     AGE  LAST\n" +
		"old    app   -       done      3m   PR #58 opened\n" +
		"fresh  lib   -       starting  5s   -\n" +
		"gone   lib   -       ended     4s   Tests pass\n"
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
	var l waitOutput
	if code != 0 || json.Unmarshal([]byte(js), &l) != nil || len(l.Agents) != 7 || l.Agents[0].Name != "n" || l.Agents[6].Name != "e" {
		t.Fatalf("json order: %s", js)
	}
}

func TestLsNamesEachAgentsBranch(t *testing.T) {
	f := newLsFakes()
	f.tmux.windows = []tmux.Window{
		agentWindow("@1", "a", "/w/app", f.now.Add(-time.Minute), false),
		agentWindow("@2", "b", "/w/app", f.now.Add(-time.Minute), false),
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
	f.inbox["id-a"] = []string{"one", "two"}
	code, out, _ := f.run("ls", "--json")
	var l struct {
		Agents    []map[string]any `json:"agents"`
		NextSince string           `json:"next_since"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &l) != nil || len(l.Agents) != 1 {
		t.Fatalf("exit %d, %q", code, out)
	}
	// The moment of the look, as far back as hq wait's own (design §3.4).
	if l.NextSince != "2026-09-27T11:59:59.75Z" {
		t.Fatalf("next_since %q", l.NextSince)
	}
	r := l.Agents[0]
	if r["name"] != "a" || r["repo"] != "app" || r["state"] != "question" || r["age_seconds"] != 90.0 || r["branch"] != "" || r["last"] != long ||
		r["since"] != "2026-09-27T11:58:30Z" || r["pending"] != 2.0 {
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
	// Its pane still runs, so nothing dates the end but hq seeing it (#73).
	want := "NAME  REPO  BRANCH  STATE    AGE  LAST\n" +
		"a     app   -       working  1s   -\n" +
		"b     lib   -       ended    0s   Tests pass\n"
	if out != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
	f.now = f.now.Add(6 * time.Second)
	if _, out, _ := f.run("ls"); !strings.Contains(out, "b     lib   -       ended    6s   Tests pass") {
		t.Fatalf("later:\n%s", out)
	}
	// A sandbox sbx no longer lists at all is not running either.
	f.sbx.sandboxes = f.sbx.sandboxes[:1]
	if _, out, _ := f.run("ls"); !strings.Contains(out, "b     lib   -       ended") {
		t.Fatalf("removed sandbox:\n%s", out)
	}
}

func TestLsShowsACancelledDialogDoneFromWhenItWasFirstSeen(t *testing.T) {
	f := newLsFakes()
	rule := strings.Repeat("─", 30)
	f.tmux.windows = []tmux.Window{agentWindow("@1", "a", "/w/app", f.now.Add(-time.Minute), false), agentWindow("@2", "b", "/w/app", f.now.Add(-time.Minute), false)}
	f.tmux.windows[0].Pane, f.tmux.windows[1].Pane = "%1", "%2"
	f.states["id-a"] = state.Report{State: state.NeedsInput, Since: f.now.Add(-20 * time.Second), Last: "Red or blue?"}
	f.states["id-b"] = state.Report{State: state.NeedsInput, Since: f.now.Add(-20 * time.Second), Last: "Green or yellow?"}
	f.tmux.screens = map[string]string{
		"%1": "● User declined to answer questions\n" + rule + "\n❯ \n" + rule + "\n  ⏵⏵ bypass permissions on\n",
		"%2": " ☐ Colour\n❯ 1. Green\n  2. Yellow\nEnter to select · Esc to cancel\n",
	}
	_, out, _ := f.run("ls")
	want := "NAME  REPO  BRANCH  STATE        AGE  LAST\n" +
		"b     app   -       needs input  20s  Green or yellow?\n" +
		"a     app   -       done         0s   User declined to answer questions\n"
	if out != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
	if f.tmux.windows[0].Options["turnend"] == "" || f.tmux.windows[1].Options["turnend"] != "" {
		t.Fatalf("recorded %v", f.tmux.windows)
	}
	// Later, done counts from when it was first seen.
	f.now = f.now.Add(7 * time.Second)
	if _, out, _ := f.run("ls"); !strings.Contains(out, "a     app   -       done         7s   User declined") {
		t.Fatalf("later:\n%s", out)
	}
	// Without the screens, what hq saw stands, and the hooks' states for
	// the rest; the next report replaces it.
	f.tmux.screenErr = errors.New("tmux: no pane")
	if _, out, _ := f.run("ls"); !strings.Contains(out, "a     app   -       done         7s   User declined") || !strings.Contains(out, "b     app   -       needs input  27s  Green or yellow?") {
		t.Fatalf("no screens:\n%s", out)
	}
	f.states["id-a"] = state.Report{State: state.Working, Since: f.now.Add(-2 * time.Second), Last: "earlier reply"}
	if _, out, _ := f.run("ls"); !strings.Contains(out, "a     app   -       working      2s   earlier reply") {
		t.Fatalf("next report:\n%s", out)
	}
}

func TestLsShowsAnInterruptedTurnDoneAndAWorkingOneWorking(t *testing.T) {
	f := newLsFakes()
	rule := strings.Repeat("─", 30)
	f.tmux.windows = []tmux.Window{agentWindow("@1", "a", "/w/app", f.now.Add(-time.Minute), false), agentWindow("@2", "b", "/w/app", f.now.Add(-time.Minute), false)}
	f.tmux.windows[0].Pane, f.tmux.windows[1].Pane = "%1", "%2"
	f.states["id-a"] = state.Report{State: state.Working, Since: f.now.Add(-30 * time.Second), Last: "earlier reply"}
	f.states["id-b"] = state.Report{State: state.Working, Since: f.now.Add(-30 * time.Second), Last: "earlier reply"}
	f.tmux.screens = map[string]string{
		"%1": "  ⎿  Interrupted · What should Claude do instead?\n" + rule + "\n❯ \n" + rule + "\n  ⏵⏵ bypass permissions on\n",
		"%2": "  ⎿  Interrupted · What should Claude do instead?\n❯ go on\n✻ Working…\n" + rule + "\n❯ \n" + rule + "\n  ⏵⏵ bypass permissions on · esc to interrupt\n",
	}
	_, out, _ := f.run("ls")
	want := "NAME  REPO  BRANCH  STATE    AGE  LAST\n" +
		"a     app   -       done     0s   Interrupted\n" +
		"b     app   -       working  30s  earlier reply\n"
	if out != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
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
	if code, out, _ := f.run("ls", "--json"); code != 0 || out != "{\n  \"agents\": [],\n  \"next_since\": \"2026-09-27T11:59:59.75Z\"\n}\n" {
		t.Fatalf("json: exit %d %q", code, out)
	}
}

func TestLsRejectsUnknownArgument(t *testing.T) {
	if code, _, _ := newFakes().run("ls", "-l"); code != ExitUsage {
		t.Fatalf("exit %d", code)
	}
}

func TestLsSaysSessionEndedForAnAgentThatEndedWithoutAMessage(t *testing.T) {
	f := newLsFakes()
	f.tmux.windows = []tmux.Window{agentWindow("@1", "a", "/w/app", f.now.Add(-time.Minute), true)}
	f.tmux.windows[0].DeadAt = f.now.Add(-20 * time.Second)
	_, out, _ := f.run("ls")
	if want := "a     app   -       ended  20s  [session ended]\n"; !strings.HasSuffix(out, want) {
		t.Fatalf("got\n%s\nwant a row\n%s", out, want)
	}
	_, js, _ := f.run("ls", "--json")
	if !strings.Contains(js, `"last": "[session ended]"`) {
		t.Fatalf("json: %s", js)
	}
}

func TestLsShowsARewoundTurnDoneOnceItsScreenStaysAtRest(t *testing.T) {
	f := newLsFakes()
	rule := strings.Repeat("─", 30)
	sent := "Write a poem about the sea."
	f.tmux.windows = []tmux.Window{agentWindow("@1", "a", "/w/app", f.now.Add(-time.Minute), false), agentWindow("@2", "b", "/w/app", f.now.Add(-time.Minute), false)}
	f.tmux.windows[0].Pane, f.tmux.windows[1].Pane = "%1", "%2"
	f.states["id-a"] = state.Report{State: state.Working, Since: f.now.Add(-30 * time.Second), Last: "earlier reply", Prompt: sent}
	f.states["id-b"] = state.Report{State: state.Working, Since: f.now.Add(-30 * time.Second), Last: "earlier reply", Prompt: sent}
	// a was rewound: the prompt is back in its box. b streams its reply in
	// a narrow pane: at rest by the look of one screen, but it moves on.
	streamed := 0
	f.tmux.screens = map[string]string{
		"%1": "✻ Baked for 2s · done\n" + rule + "\n❯ " + sent + "\n" + rule + "\n  ⏵⏵ bypass permissions on\n",
	}
	f.tmux.onScreens = func() {
		streamed++
		f.tmux.screens["%2"] = "● The sea" + strings.Repeat("\nline", streamed) + "\n" + rule + "\n❯ \n" + rule + "\n  ⏵⏵ bypass permissions on\n"
	}
	f.tmux.onScreens()
	_, out, _ := f.run("ls")
	// hq ls looked again after agent.RestDelay; a is done since it first
	// saw its screen at rest.
	want := "NAME  REPO  BRANCH  STATE    AGE  LAST\n" +
		"a     app   -       done     2s   Interrupted\n" +
		"b     app   -       working  32s  earlier reply\n"
	if out != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
	if f.tmux.windows[0].Options["turnend"] == "" || f.tmux.windows[1].Options["turnend"] != "" {
		t.Fatalf("recorded %v", f.tmux.windows)
	}
	// It holds, from the same moment, whatever the screen shows next,
	// until the next report.
	f.now = f.now.Add(5 * time.Second)
	f.tmux.screens["%1"] = "anything"
	if _, out, _ := f.run("ls"); !strings.Contains(out, "a     app   -       done     7s   Interrupted") {
		t.Fatalf("later:\n%s", out)
	}
	f.states["id-a"] = state.Report{State: state.Working, Since: f.now.Add(-time.Second), Last: "earlier reply", Prompt: "next"}
	if _, out, _ := f.run("ls"); !strings.Contains(out, "a     app   -       working  1s   earlier reply") {
		t.Fatalf("next report:\n%s", out)
	}
}

func TestLsShowsWhatAnotherHqStoredFirst(t *testing.T) {
	f := newLsFakes()
	rule := strings.Repeat("─", 30)
	f.tmux.windows = []tmux.Window{agentWindow("@1", "a", "/w/app", f.now.Add(-time.Minute), false), agentWindow("@2", "b", "/w/lib", f.now.Add(-time.Minute), false)}
	f.tmux.windows[0].Pane, f.tmux.windows[1].Pane = "%1", "%2"
	f.tmux.windows[1].Options["sandbox"] = "claude-lib"
	f.sbx.sandboxes = append(f.sbx.sandboxes, sbx.Sandbox{Name: "claude-lib", Status: "stopped"})
	since := f.now.Add(-20 * time.Second)
	f.states["id-a"] = state.Report{State: state.NeedsInput, Since: since, Last: "Red or blue?"}
	f.states["id-b"] = state.Report{State: state.Working, Since: f.now.Add(-time.Second), Last: "Tests pass"}
	f.tmux.screens = map[string]string{"%1": "● User declined to answer questions\n" + rule + "\n❯ \n" + rule + "\n  ⏵⏵ bypass permissions on\n"}
	// Another hq process, the dashboard say, saw both a moment earlier and
	// stored its records between this one's look and its write (#100).
	ns := func(t time.Time) string { return strconv.FormatInt(t.UnixNano(), 10) }
	f.tmux.onKeep = func(id, key string) {
		w := &f.tmux.windows[0]
		if id == "@2" {
			w = &f.tmux.windows[1]
		}
		switch key {
		case "turnend":
			w.Options[key] = ns(since) + " " + ns(f.now.Add(-5*time.Second)) + " User declined to answer questions"
		case "endseen":
			w.Options[key] = w.Options["started"] + " " + ns(f.states["id-b"].Since) + " " + ns(f.now.Add(-9*time.Second))
		}
	}
	_, out, _ := f.run("ls")
	want := "NAME  REPO  BRANCH  STATE  AGE  LAST\n" +
		"a     app   -       done   5s   User declined to answer questions\n" +
		"b     lib   -       ended  9s   Tests pass\n"
	if out != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
	// Later looks keep the first records, and so the ages go on from them.
	f.tmux.onKeep = nil
	f.now = f.now.Add(3 * time.Second)
	if _, out, _ := f.run("ls"); !strings.Contains(out, "a     app   -       done   8s") || !strings.Contains(out, "b     lib   -       ended  12s") {
		t.Fatalf("later:\n%s", out)
	}
}
