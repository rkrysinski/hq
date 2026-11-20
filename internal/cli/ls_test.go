package cli

import (
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/journal/journaltest"
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

// sbxNotFound is a missing sbx as sbx.Client reports it: what the
// platform says was not found.
func sbxNotFound() error { return &proc.Error{Name: "sbx", Msg: "sbx not found", NotFound: true} }

func agentWindow(id, name, repo string, started time.Time, dead bool) tmux.Window {
	return tmux.Window{ID: id, Name: name, PaneDead: dead, Options: map[string]string{
		"id": "id-" + name, "name": name, "repo": repo, "sandbox": "claude-x", "started": strconv.FormatInt(started.Unix(), 10), agent.GenerationOption: generation,
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
	f.states["id-old"] = journaltest.HookState{State: state.Done, Since: f.now.Add(-3 * time.Minute), Last: "PR #58 opened"}
	f.tmux.windows[2].DeadAt = f.now.Add(-4 * time.Second)
	f.states["id-gone"] = journaltest.HookState{State: state.Working, Since: f.now.Add(-10 * time.Second), Last: "Tests pass"}
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
	report := func(st string, ago time.Duration) journaltest.HookState {
		return journaltest.HookState{State: st, Since: f.now.Add(-ago)}
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
	f.states["id-a"] = journaltest.HookState{State: state.Working, Since: f.now.Add(-time.Second), Branch: "feat/42-x", Cwd: "/w/app/.claude/worktrees/feat-42-x"}
	f.states["id-b"] = journaltest.HookState{State: state.Working, Since: f.now.Add(-2 * time.Second), Branch: "fix/7-y"}
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
	f.states["id-a"] = journaltest.HookState{State: state.Question, Since: f.now.Add(-90 * time.Second), Last: long}
	f.inbox["id-a"] = []string{"one", "two"}
	code, out, _ := f.run("ls", "--json")
	var l struct {
		Agents    []map[string]any `json:"agents"`
		NextSince string           `json:"next_since"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &l) != nil || len(l.Agents) != 1 {
		t.Fatalf("exit %d, %q", code, out)
	}
	// The look's cursor (ADR 0019): it saw every change up to the one
	// event, the turn end, that put a in its question and completed its
	// turn.
	if l.NextSince != "c1" {
		t.Fatalf("next_since %q", l.NextSince)
	}
	r := l.Agents[0]
	if r["state_seq"] != 1.0 || r["completion_seq"] != 1.0 {
		t.Fatalf("numbers %v %v", r["state_seq"], r["completion_seq"])
	}
	if r["name"] != "a" || r["repo"] != "app" || r["state"] != "question" || r["age_seconds"] != 90.0 || r["branch"] != "" || r["last"] != long ||
		r["since"] != "2026-09-27T11:58:30Z" || r["pending"] != 2.0 {
		t.Fatalf("row %v", r)
	}
}

// Each row carries its setup, read off its sandbox's name: default for any
// name but hq-<setup>-<dir>, a sandbox made by hand included (#135).
func TestLsJSONCarriesTheSetup(t *testing.T) {
	f := newLsFakes()
	f.tmux.windows = nil
	for i, c := range []struct{ repo, sandbox string }{
		{"/w/app", "claude-app"}, {"/w/app", "hq-local-app"}, {"/w/app", "my-own"}, {"/w/foo-local", "claude-foo-local"},
		{"/w/foo-local", "hq-local-foo-local"}, {"/w/My_Repo x", "hq-local-my-repo-x"}, {"/w/app", "hq-gone-app"},
	} {
		w := agentWindow("@"+strconv.Itoa(i), "a"+strconv.Itoa(i), c.repo, f.now, false)
		w.Options["sandbox"] = c.sandbox
		f.tmux.windows = append(f.tmux.windows, w)
	}
	_, out, _ := f.run("ls", "--json")
	var l struct {
		Agents []struct{ Sandbox, Setup string } `json:"agents"`
	}
	if err := json.Unmarshal([]byte(out), &l); err != nil || len(l.Agents) != 7 {
		t.Fatalf("%v %s", err, out)
	}
	want := map[string]string{"claude-app": "default", "hq-local-app": "local", "my-own": "default", "claude-foo-local": "default",
		"hq-local-foo-local": "local", "hq-local-my-repo-x": "local", "hq-gone-app": "gone"}
	for _, r := range l.Agents {
		if r.Setup != want[r.Sandbox] {
			t.Errorf("%s: setup %q", r.Sandbox, r.Setup)
		}
	}
}

// hq ls --json says what each agent asks, as hq read does: an MCP form's
// server, message and fields while it needs input at one (#100), the
// question while it has one, and null otherwise.
func TestLsJSONSaysWhatEachAgentAsks(t *testing.T) {
	f := newLsFakes()
	f.tmux.windows = []tmux.Window{
		agentWindow("@1", "a", "/w/app", f.now.Add(-5*time.Minute), false),
		agentWindow("@2", "b", "/w/app", f.now.Add(-5*time.Minute), false),
		agentWindow("@3", "c", "/w/app", f.now.Add(-5*time.Minute), false),
	}
	f.states["id-a"] = journaltest.HookState{State: state.NeedsInput, Since: f.now.Add(-time.Second), Last: "Which color should I use?"}
	f.details["id-a"] = state.Detail{Ask: &state.Ask{Server: "elicit", Message: "Which color should I use?", Fields: []state.AskField{
		{Name: "color", Title: "Color", Description: "Pick one", Type: "string", Required: true, Options: []state.AskFieldOption{{Value: "red"}, {Value: "blue"}}},
	}}}
	f.states["id-b"] = journaltest.HookState{State: state.Question, Since: f.now.Add(-time.Minute), Last: "Done. Shall I open the PR?"}
	f.details["id-b"] = state.Detail{Reply: "Done.\n\nShall I open the PR?"}
	f.states["id-c"] = journaltest.HookState{State: state.Done, Since: f.now.Add(-time.Minute), Last: "Done."}
	f.details["id-c"] = state.Detail{Reply: "Done.", Ask: &state.Ask{Message: "Claude needs your permission"}}
	_, out, _ := f.run("ls", "--json")
	var l struct {
		Agents []struct {
			Name string          `json:"name"`
			Asks json.RawMessage `json:"asks"`
		} `json:"agents"`
	}
	if err := json.Unmarshal([]byte(out), &l); err != nil || len(l.Agents) != 3 {
		t.Fatalf("%v\n%s", err, out)
	}
	want := map[string]string{
		"a": `{"server":"elicit","message":"Which color should I use?","fields":[{"name":"color","title":"Color","description":"Pick one","type":"string","required":true,"options":[{"value":"red"},{"value":"blue"}]}]}`,
		"b": `{"message":"Shall I open the PR?"}`,
		"c": `null`,
	}
	for _, r := range l.Agents {
		var got, norm any
		_ = json.Unmarshal(r.Asks, &got)
		_ = json.Unmarshal([]byte(want[r.Name]), &norm)
		if mustJSON(got) != mustJSON(norm) {
			t.Errorf("%s asks %s, want %s", r.Name, r.Asks, want[r.Name])
		}
	}
}

func TestLsCutsTheLastMessageToOneColumn(t *testing.T) {
	f := newLsFakes()
	f.tmux.windows = []tmux.Window{agentWindow("@1", "a", "/w/app", f.now, false)}
	f.states["id-a"] = journaltest.HookState{State: state.Done, Since: f.now, Last: strings.Repeat("x", 100)}
	_, out, _ := f.run("ls")
	if !strings.Contains(out, strings.Repeat("x", 59)+"…\n") || strings.Contains(out, strings.Repeat("x", 60)) {
		t.Fatalf("%q", out)
	}
}

func TestLsDoesNotEndAnAgentThatReportedAfterSbxWasAsked(t *testing.T) {
	// hq asked sbx while a's sandbox was still starting, and a's session
	// reported before hq read the agents (#48): the answer is older than
	// the report. old's last report is older than the answer.
	f := newFakes()
	f.sbx.sandboxes = []sbx.Sandbox{{Name: "claude-x", Status: "stopped"}}
	f.tmux.windows = []tmux.Window{agentWindow("@1", "a", "/w/app", f.now.Add(-time.Second), false), agentWindow("@2", "old", "/w/app", f.now.Add(-time.Hour), false)}
	f.states["id-a"] = journaltest.HookState{State: state.Done, Since: f.now.Add(time.Millisecond), Last: "Done: hello"}
	f.states["id-old"] = journaltest.HookState{State: state.Done, Since: f.now.Add(-time.Minute), Last: "PR #58 opened"}
	_, out, _ := f.run("ls")
	want := "NAME  REPO  BRANCH  STATE  AGE  LAST\n" +
		"a     app   -       done   0s   Done: hello\n" +
		"old   app   -       ended  0s   PR #58 opened\n"
	if out != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
}

func TestLsShowsTheAgentsOfAStoppedSandboxEnded(t *testing.T) {
	f := newLsFakes()
	f.tmux.windows = []tmux.Window{agentWindow("@1", "a", "/w/app", f.now.Add(-time.Minute), false), agentWindow("@2", "b", "/w/lib", f.now.Add(-time.Minute), false)}
	f.tmux.windows[1].Options["sandbox"] = "claude-lib"
	f.sbx.sandboxes = append(f.sbx.sandboxes, sbx.Sandbox{Name: "claude-lib", Status: "stopped"})
	f.states["id-a"] = journaltest.HookState{State: state.Working, Since: f.now.Add(-time.Second)}
	f.states["id-b"] = journaltest.HookState{State: state.Working, Since: f.now.Add(-time.Second), Last: "Tests pass"}
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
	f.states["id-a"] = journaltest.HookState{State: state.NeedsInput, Since: f.now.Add(-20 * time.Second), Last: "Red or blue?"}
	f.states["id-b"] = journaltest.HookState{State: state.NeedsInput, Since: f.now.Add(-20 * time.Second), Last: "Green or yellow?"}
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
	if a, b := f.eventsOf("id-a", state.SourceScreen), f.eventsOf("id-b", state.SourceScreen); a != state.KindTurnEnded || b != "" {
		t.Fatalf("seen on a's screen %q, on b's %q", a, b)
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
	f.states["id-a"] = journaltest.HookState{State: state.Working, Since: f.now.Add(-2 * time.Second), Last: "earlier reply"}
	if _, out, _ := f.run("ls"); !strings.Contains(out, "a     app   -       working      2s   earlier reply") {
		t.Fatalf("next report:\n%s", out)
	}
}

func TestLsShowsAnInterruptedTurnDoneAndAWorkingOneWorking(t *testing.T) {
	f := newLsFakes()
	rule := strings.Repeat("─", 30)
	f.tmux.windows = []tmux.Window{agentWindow("@1", "a", "/w/app", f.now.Add(-time.Minute), false), agentWindow("@2", "b", "/w/app", f.now.Add(-time.Minute), false)}
	f.tmux.windows[0].Pane, f.tmux.windows[1].Pane = "%1", "%2"
	f.states["id-a"] = journaltest.HookState{State: state.Working, Since: f.now.Add(-30 * time.Second), Last: "earlier reply"}
	f.states["id-b"] = journaltest.HookState{State: state.Working, Since: f.now.Add(-30 * time.Second), Last: "earlier reply"}
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
	f.states["id-a"] = journaltest.HookState{State: state.Done, Since: f.now.Add(-time.Second), Last: "ok"}
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
	if code, out, _ := f.run("ls", "--json"); code != 0 || out != "{\n  \"agents\": [],\n  \"next_since\": \"c0\"\n}\n" {
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
	f.states["id-a"] = journaltest.HookState{State: state.Working, Since: f.now.Add(-30 * time.Second), Last: "earlier reply", Prompt: sent}
	f.states["id-b"] = journaltest.HookState{State: state.Working, Since: f.now.Add(-30 * time.Second), Last: "earlier reply", Prompt: sent}
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
	if a, b := f.eventsOf("id-a", state.SourceScreen), f.eventsOf("id-b", state.SourceScreen); a != state.KindAtRest+" "+state.KindRewound || strings.Contains(b, state.KindRewound) {
		t.Fatalf("seen on a's screen %q, on b's %q", a, b)
	}
	// It holds, from the same moment, whatever the screen shows next,
	// until the next report.
	f.now = f.now.Add(5 * time.Second)
	f.tmux.screens["%1"] = "anything"
	if _, out, _ := f.run("ls"); !strings.Contains(out, "a     app   -       done     7s   Interrupted") {
		t.Fatalf("later:\n%s", out)
	}
	f.states["id-a"] = journaltest.HookState{State: state.Working, Since: f.now.Add(-time.Second), Last: "earlier reply", Prompt: "next"}
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
	f.states["id-a"] = journaltest.HookState{State: state.NeedsInput, Since: since, Last: "Red or blue?"}
	f.states["id-b"] = journaltest.HookState{State: state.Working, Since: since, Last: "Tests pass"}
	f.tmux.screens = map[string]string{"%1": "● User declined to answer questions\n" + rule + "\n❯ \n" + rule + "\n  ⏵⏵ bypass permissions on\n"}
	// Another hq process, the dashboard say, saw both a moment earlier and
	// appended it between this one's look and its append (#100): the first
	// append wins.
	f.onAppend = func(id string, events []state.Event) {
		for _, e := range events {
			switch {
			case id == "id-a" && e.Kind == state.KindTurnEnded:
				f.seen("/w/app", id, state.SourceScreen, e.Kind, e.Facts, f.now.Add(-5*time.Second))
			case id == "id-b" && e.Kind == state.KindNotRunning:
				f.seen("/w/lib", id, state.SourceSbx, e.Kind, state.Facts{Asked: f.now.Add(-10 * time.Second)}, f.now.Add(-9*time.Second))
			}
		}
	}
	_, out, _ := f.run("ls")
	want := "NAME  REPO  BRANCH  STATE  AGE  LAST\n" +
		"a     app   -       done   5s   User declined to answer questions\n" +
		"b     lib   -       ended  9s   Tests pass\n"
	if out != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
	// Later looks keep the first events, and so the ages go on from them.
	f.onAppend = nil
	f.now = f.now.Add(3 * time.Second)
	if _, out, _ := f.run("ls"); !strings.Contains(out, "a     app   -       done   8s") || !strings.Contains(out, "b     lib   -       ended  12s") {
		t.Fatalf("later:\n%s", out)
	}
}

// An agent done with its own tasks running starts LAST with their count,
// cut with LAST to its column; --json keeps last the message alone and
// lists them in own_tasks, [] when none (#78).
func TestLsShowsAnAgentsOwnTasksBeforeItsLastMessage(t *testing.T) {
	f := newLsFakes()
	f.tmux.windows = []tmux.Window{
		agentWindow("@1", "a", "/w/app", f.now.Add(-time.Hour), false),
		agentWindow("@2", "b", "/w/app", f.now.Add(-time.Hour), false),
		agentWindow("@3", "c", "/w/app", f.now.Add(-time.Hour), false),
	}
	f.states["id-a"] = journaltest.HookState{State: state.Done, Since: f.now.Add(-2 * time.Minute), Last: "Waiting for the background task to report back.",
		OwnTasks: []state.Task{{Type: "shell", Text: "sleep 120"}}}
	f.states["id-b"] = journaltest.HookState{State: state.Question, Since: f.now.Add(-3 * time.Minute), Last: strings.Repeat("x", 100) + "?",
		OwnTasks: []state.Task{{Type: "shell", Text: "npm run dev"}, {Type: "monitor", Text: "Wait for CI"}}}
	f.states["id-c"] = journaltest.HookState{State: state.Done, Since: f.now.Add(-4 * time.Minute), Last: "PR #58 opened"}
	_, out, _ := f.run("ls")
	want := "NAME  REPO  BRANCH  STATE     AGE  LAST\n" +
		"b     app   -       question  3m   1 shell, 1 monitor · " + strings.Repeat("x", 38) + "…\n" +
		"a     app   -       done      2m   1 shell · Waiting for the background task to report back.\n" +
		"c     app   -       done      4m   PR #58 opened\n"
	if out != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
	_, out, _ = f.run("ls", "--json")
	var l struct {
		Agents []struct {
			Name     string       `json:"name"`
			Last     string       `json:"last"`
			OwnTasks []state.Task `json:"own_tasks"`
		} `json:"agents"`
	}
	if err := json.Unmarshal([]byte(out), &l); err != nil || len(l.Agents) != 3 {
		t.Fatalf("%v\n%s", err, out)
	}
	a := l.Agents[1]
	if a.Name != "a" || a.Last != "Waiting for the background task to report back." || len(a.OwnTasks) != 1 || a.OwnTasks[0] != (state.Task{Type: "shell", Text: "sleep 120"}) {
		t.Fatalf("a: %+v", a)
	}
	if !strings.Contains(out, `"own_tasks": []`) || l.Agents[2].OwnTasks == nil {
		t.Fatalf("c has none: an empty list, not null:\n%s", out)
	}
}

// An sbx too old for hq is an answer, not a failure to give one: hq ls says
// so and exits 3, where an sbx that fails leaves the states it has (#172).
func TestLsSaysAnSbxIsTooOld(t *testing.T) {
	f := newFakes()
	f.sbx.err = &sbx.TooOldError{Version: "0.47.0"}
	f.tmux.windows = []tmux.Window{agentWindow("@1", "a", "/w/app", f.now.Add(-time.Minute), false)}
	code, out, errOut := f.run("ls")
	if code != ExitEnvironment || out != "" || errOut != "hq: sbx 0.47.0 is too old, 0.48.0 or newer needed; update Docker Sandboxes\n" {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
}

// hq kill waits for an agent's session to end, then kills its home window
// and removes its journal: a look of hq ls can list the window, its pane
// dead, and find no journal after (#239). It leaves the agent out, as the
// next look does, rather than show it as one that ended without a word.
func TestLsBetweenTheStepsOfAKillLeavesTheAgentOut(t *testing.T) {
	f := newWaitFakes()
	f.states["id-a"] = journaltest.HookState{State: state.Done, Since: f.now.Add(-time.Minute), Last: "Done: hi"}
	f.tmux.windows[1].PaneDead, f.tmux.windows[1].DeadAt = true, f.now.Add(-time.Second)
	killed := false
	f.onRead = func(id string) {
		if id != "id-a" || killed {
			return
		}
		killed = true
		if code, _, errOut := f.run("kill", "a", "-y"); code != 0 {
			t.Errorf("kill: %s", errOut)
		}
	}
	code, out, _ := f.run("ls", "--json")
	var ls struct {
		Agents []lsRow `json:"agents"`
	}
	if err := json.Unmarshal([]byte(out), &ls); !killed || code != 0 || err != nil || len(ls.Agents) != 1 || ls.Agents[0].Name != "b" {
		t.Fatalf("killed %v, exit %d:\n%s", killed, code, out)
	}
}

// The look of hq ls that reads a's journal after hq kill removed it, its
// pane dead, writes no journal of a back (#263): nothing would ever remove
// one that holds no launch.
func TestLsBetweenTheStepsOfAKillWritesNoJournalBack(t *testing.T) {
	f := newWaitFakes()
	f.states["id-a"] = journaltest.HookState{State: state.Done, Since: f.now.Add(-time.Minute), Last: "Done: hi"}
	f.tmux.windows[1].PaneDead, f.tmux.windows[1].DeadAt = true, f.now.Add(-time.Second)
	killed := false
	f.onRead = func(id string) {
		if id != "id-a" || killed {
			return
		}
		killed = true
		if code, _, errOut := f.run("kill", "a", "-y"); code != 0 {
			t.Errorf("kill: %s", errOut)
		}
	}
	if code, _, errOut := f.run("ls"); !killed || code != 0 {
		t.Fatalf("killed %v, exit %d: %s", killed, code, errOut)
	}
	f.noJournalOf(t, "id-a")
}

// An agent whose journal is not there while its home window stands (a
// lost state directory) still has its dead pane's death appended, begun
// as its journal, and shows ended (#263); so it does when tmux cannot say
// at that moment whether the window stands, as before.
func TestLsAppendsADeadPanesDeathToAJournalNotThereWhileItsWindowStands(t *testing.T) {
	for _, tmuxFails := range []bool{false, true} {
		f := newWaitFakes()
		delete(f.states, "id-a")
		f.tmux.windows[1].PaneDead, f.tmux.windows[1].DeadAt = true, f.now.Add(-time.Second)
		f.onAppend = func(id string, _ []state.Event) {
			if id == "id-a" && tmuxFails {
				f.tmux.windowsErr = errors.New("tmux gone")
			}
		}
		code, out, errOut := f.run("ls")
		j, err := f.journal.Read(journalID("id-a"))
		i := slices.IndexFunc(strings.Split(out, "\n"), func(l string) bool { return strings.HasPrefix(l, "a ") })
		if code != 0 || i < 0 || !strings.Contains(strings.Split(out, "\n")[i], state.Ended+" ") || err != nil || journaltest.Kinds(j.Events) != state.KindPaneDied {
			t.Fatalf("tmux fails %v: exit %d %s\n%s\njournal %s %v", tmuxFails, code, errOut, out, journaltest.Kinds(j.Events), err)
		}
	}
}

// Nor does the look that read a's journal whole before hq kill removed it,
// and appends the death of its pane after (#263); it leaves a out of what
// it shows, as the look that finds the journal gone does (#239).
func TestLsThatAppendsAfterAKillRemovedTheJournalItReadWritesNoJournalBack(t *testing.T) {
	f := newWaitFakes()
	f.states["id-a"] = journaltest.HookState{State: state.Done, Since: f.now.Add(-time.Minute), Last: "Done: hi"}
	f.tmux.windows[1].PaneDead, f.tmux.windows[1].DeadAt = true, f.now.Add(-time.Second)
	killed := false
	f.onAppend = func(id string, events []state.Event) {
		if id != "id-a" || killed || len(events) != 1 || events[0].Kind != state.KindPaneDied {
			return
		}
		killed = true
		if code, _, errOut := f.run("kill", "a", "-y"); code != 0 {
			t.Errorf("kill: %s", errOut)
		}
	}
	code, out, _ := f.run("ls", "--json")
	var ls struct {
		Agents []lsRow `json:"agents"`
	}
	if err := json.Unmarshal([]byte(out), &ls); !killed || code != 0 || err != nil || len(ls.Agents) != 1 || ls.Agents[0].Name != "b" {
		t.Fatalf("killed %v, exit %d:\n%s", killed, code, out)
	}
	f.noJournalOf(t, "id-a")
}
