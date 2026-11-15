package agent

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/journal/journaltest"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
	"github.com/rkrysinski/hq/internal/version"
)

func TestNameChars(t *testing.T) {
	for name, ok := range map[string]bool{"42": true, "agent-17": true, "a_b": true, strings.Repeat("a", 40): true, "": false, "a b": false, "a/b": false, "ą": false, "rel-1.10.5-213": true, "a:b": false} {
		if NameChars(name) != ok {
			t.Errorf("NameChars(%q) != %v", name, ok)
		}
	}
}

// Dots, dashes and underscores alone make no name (#119).
func TestNameHasLetterOrDigit(t *testing.T) {
	for name, ok := range map[string]bool{"42": true, "a": true, ".5": true, "rel-1.10.5-213": true, ".": false, "..": false, "-": false, "_": false, "_.-": false, "": false} {
		if NameHasLetterOrDigit(name) != ok {
			t.Errorf("NameHasLetterOrDigit(%q) != %v", name, ok)
		}
	}
}

func TestAge(t *testing.T) {
	for d, want := range map[time.Duration]string{3 * time.Second: "3s", 2 * time.Minute: "2m", 90 * time.Minute: "1h", 50 * time.Hour: "2d"} {
		if got := Age(d); got != want {
			t.Errorf("Age(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestFromWindowsSkipsNonAgentsAndMarksDeadPanesEnded(t *testing.T) {
	as := FromWindows([]tmux.Window{
		{ID: "@0", Name: "hq", Options: map[string]string{}},
		{ID: "@1", Name: "a", PaneDead: true, Options: map[string]string{GenerationOption: gen, "id": "x", "name": "a", "started": "100"}},
	})
	if len(as) != 1 || as[0].State != state.Ended || as[0].Alive || as[0].Started.Unix() != 100 {
		t.Fatalf("%+v", as)
	}
}

func TestFromWindowsKnowsTheDockedAgent(t *testing.T) {
	as := FromWindows([]tmux.Window{
		{ID: "@1", Name: "a", Docked: true, Options: map[string]string{GenerationOption: gen, "id": "x"}},
		{ID: "@2", Name: "b", Options: map[string]string{GenerationOption: gen, "id": "y"}},
	})
	if !as[0].Docked || as[1].Docked || !as[0].Alive {
		t.Fatalf("%+v", as)
	}
}

// An agent whose home window carries another generation than this hq's,
// or none (started before hq marked one), is outdated: listed with no state
// of its own and the remedy as its last message, whatever its pane does and

// An agent whose home window carries another generation than this hq's,
// or none (started before hq marked one), is outdated: listed with no state
// of its own and the remedy as its last message, whatever its pane does and
// its journal says (#194).
func TestAnAgentOfAnotherGenerationIsOutdated(t *testing.T) {
	older, newer := "started by an older hq: kill it and start it again", "started by a newer hq: kill it and start it again"
	current := strconv.Itoa(version.Generation)
	for _, c := range []struct {
		gen       string
		dead      bool
		state     string
		last      string
		generated int
	}{
		{gen: current, state: state.Working, last: "on it", generated: version.Generation},
		{gen: current, dead: true, state: state.Ended, last: "on it", generated: version.Generation},
		{gen: "", state: state.Outdated, last: older},
		{gen: "1", state: state.Outdated, last: older, generated: 1},
		{gen: strconv.Itoa(version.Generation - 1), dead: true, state: state.Outdated, last: older, generated: version.Generation - 1},
		{gen: strconv.Itoa(version.Generation + 1), state: state.Outdated, last: newer, generated: version.Generation + 1},
		{gen: "x", state: state.Outdated, last: older},
	} {
		ws := []tmux.Window{{ID: "@1", Name: "a", PaneDead: c.dead, Options: map[string]string{"id": "a1", "repo": "/w/app", "started": "100", "new": "1", GenerationOption: c.gen}}}
		s := newStore(nil)
		s.Memory.Report("/w/app", "a1", state.Report{State: state.Working, Since: time.Unix(200, 0), Branch: "main"}, state.Detail{Reply: "on it"})
		s.Memory.Report("/w/app", "a1", state.Report{State: state.Working, Since: time.Unix(201, 0), Branch: "main", Prompt: "go on"}, state.Detail{})
		for _, a := range []Agent{Collect(ws, s, time.Unix(300, 0), nil)[0], Look(ws, Sandboxes{}, time.Unix(300, 0), s, nil, nil)[0]} {
			if a.State != c.state || a.Last != c.last || a.Generation != c.generated || a.Outdated() != (c.state == state.Outdated) {
				t.Errorf("generation %q, dead %v: %s %q generation %d outdated %v, want %s %q generation %d", c.gen, c.dead, a.State, a.Last, a.Generation, a.Outdated(), c.state, c.last, c.generated)
			}
			if a.Outdated() && (a.Branch != "" || a.New || a.OwnTasks != nil || !a.Since.Equal(a.Started) || a.Detail() != (state.Detail{}) || a.Session() != "") {
				t.Errorf("generation %q: an outdated agent shows branch %q, new %v, own tasks %v, since %v, detail %+v", c.gen, a.Branch, a.New, a.OwnTasks, a.Since, a.Detail())
			}
		}
	}
}

func TestNewIDIsFresh(t *testing.T) {
	if a, b := NewID(), NewID(); a == b || len(a) != 12 {
		t.Fatalf("%q %q", a, b)
	}
}

// sbxSays is sbx's answer that the sandboxes of running run, asked after
// every report of a test's agents.
func sbxSays(running map[string]bool) Sandboxes {
	return Sandboxes{Running: running, Asked: time.Unix(1<<40, 0)}
}

// sbxWindows are the home windows of agents a (a1, in claude-app) and b
// (b2, in claude-lib).
func sbxWindows() []tmux.Window {
	return []tmux.Window{
		{ID: "@1", Pane: "%1", Options: map[string]string{GenerationOption: gen, "id": "a1", "name": "a", "repo": "/w/app", "sandbox": "claude-app", "started": "100"}},
		{ID: "@2", Pane: "%2", Options: map[string]string{GenerationOption: gen, "id": "b2", "name": "b", "repo": "/w/app", "sandbox": "claude-lib", "started": "100"}},
	}
}

func states(as []Agent) string {
	var s []string
	for _, a := range as {
		s = append(s, a.Name+"="+a.State+"/"+a.Last)
	}
	return strings.Join(s, " ")
}

func TestTheLookEndsAgentsWhoseSandboxIsNotRunning(t *testing.T) {
	now := time.Unix(1000, 0)
	rs := map[string]state.Report{
		"a1": {State: state.Done, Since: time.Unix(200, 0), Last: "on it a"},
		"b2": {State: state.Done, Since: time.Unix(200, 0), Last: "on it b"},
	}
	s := newStore(nil)
	if got := states(s.look(sbxWindows(), rs, Sandboxes{}, now)); got != "a=done/on it a b=done/on it b" {
		t.Fatalf("sbx could not say: %s", got)
	}
	if got := states(s.look(sbxWindows(), rs, sbxSays(map[string]bool{"claude-app": true}), now)); got != "a=done/on it a b=ended/on it b" {
		t.Fatalf("claude-lib stopped: %s", got)
	}
	// What the journal holds stands for a look that cannot ask sbx.
	if got := states(Collect(sbxWindows(), s, now, nil)); got != "a=done/on it a b=ended/on it b" {
		t.Fatalf("collected: %s", got)
	}
}

func TestAnAnswerOfSbxOlderThanAnAgentsReportDoesNotEndIt(t *testing.T) {
	// hq asked sbx while claude-lib was still starting; b's session started
	// in it and reported after that (#48). a reported before the question:
	// its sandbox has stopped.
	asked := time.Unix(200, 0)
	rs := map[string]state.Report{
		"a1": {State: state.Done, Since: asked.Add(-time.Second), Last: "Done."},
		"b2": {State: state.Done, Since: asked.Add(time.Millisecond), Last: "Done."},
	}
	s := newStore(nil)
	as := s.look(sbxWindows(), rs, Sandboxes{Running: map[string]bool{}, Asked: asked}, asked.Add(time.Second))
	if as[0].State != state.Ended || as[1].State != state.Done {
		t.Fatalf("a %s, b %s; want a ended and b done", as[0].State, as[1].State)
	}
	// The next answer, asked after b's report, counts for b too.
	as = s.look(sbxWindows(), rs, Sandboxes{Running: map[string]bool{"claude-lib": true}, Asked: asked.Add(time.Second)}, asked.Add(2*time.Second))
	if as[1].State != state.Done {
		t.Fatalf("b in a running sandbox: %s", as[1].State)
	}
	as = s.look(sbxWindows(), rs, Sandboxes{Running: map[string]bool{}, Asked: asked.Add(time.Second)}, asked.Add(2*time.Second))
	if as[1].State != state.Ended {
		t.Fatalf("b once sbx says, after its report, that its sandbox does not run: %s", as[1].State)
	}
	// A turn end that left background work running is a report too.
	s = newStore(nil)
	bg := map[string]state.Report{"a1": {State: state.Working, Since: asked.Add(-time.Minute), Latest: asked.Add(time.Millisecond), Background: true}}
	if a := s.look(sbxWindows()[:1], bg, Sandboxes{Running: map[string]bool{}, Asked: asked}, asked.Add(time.Second))[0]; a.State != state.Working {
		t.Fatalf("an agent whose turn end is newer than the answer: %s", a.State)
	}
}

func TestTheLookLeavesAnAgentStartingWhileItsSandboxHasNotStarted(t *testing.T) {
	// hq new has released c's session into claude-lib, stopped or just
	// created; sbx run is starting it and c has not reported yet (S2).
	ws := []tmux.Window{
		{ID: "@1", Pane: "%1", Options: map[string]string{GenerationOption: gen, "id": "c3", "name": "c", "repo": "/w/app", "sandbox": "claude-lib", "new": "1"}},
		{ID: "@2", Pane: "%2", PaneDead: true, Options: map[string]string{GenerationOption: gen, "id": "d4", "name": "d", "repo": "/w/app", "sandbox": "claude-lib"}},
	}
	s := newStore(nil)
	as := s.look(ws, nil, sbxSays(map[string]bool{}), time.Unix(1000, 0))
	if as[0].State != state.Starting || !as[0].New || len(s.journalOf(t, "c3")) != 0 {
		t.Fatalf("c while its sandbox starts: %+v", as[0])
	}
	// Its pane dying (the launch failed, or sbx run returned) still ends it.
	if as[1].State != state.Ended || as[1].Last != EndedLast {
		t.Fatalf("d with a dead pane: %+v", as[1])
	}
}

func TestTheLookTakesTheReportedStateWhileThePaneLives(t *testing.T) {
	since := time.Unix(500, 0)
	r := state.Report{State: state.Question, Since: since, Last: "Shall I?", Branch: "feat/42-x", Cwd: "/w/app/.claude/worktrees/x"}
	ws := []tmux.Window{lookWindow("1"), lookWindow("2")}
	ws[1].PaneDead, ws[1].DeadAt = true, time.Unix(550, 0)
	s := newStore(nil)
	if a := s.look(ws[:1], nil, Sandboxes{}, time.Unix(400, 0))[0]; a.State != state.Starting || a.Since.Unix() != 100 {
		t.Fatalf("before any report: %+v", a)
	}
	as := s.look(ws, map[string]state.Report{"a1": r, "a2": r}, Sandboxes{}, time.Unix(600, 0))
	if a := as[0]; a.State != state.Question || a.Since != since || a.Last != "Shall I?" || a.Branch != "feat/42-x" || a.Worktree != "/w/app/.claude/worktrees/x" {
		t.Fatalf("%+v", a)
	}
	if a := as[1]; a.State != state.Ended || a.Last != "Shall I?" || !a.Since.Equal(time.Unix(550, 0)) {
		t.Fatalf("dead pane: %+v", a)
	}
}

func TestAnAgentIsNewUntilItsFirstReport(t *testing.T) {
	ws := []tmux.Window{
		{ID: "@1", Name: "a", Options: map[string]string{GenerationOption: gen, "id": "a1", "repo": "/w/app", "new": "1"}},
		{ID: "@2", Name: "b", Options: map[string]string{GenerationOption: gen, "id": "b2", "repo": "/w/app", "new": "1"}},
		{ID: "@3", Name: "c", PaneDead: true, Options: map[string]string{GenerationOption: gen, "id": "c3", "repo": "/w/app", "new": "1"}},
		{ID: "@4", Name: "d", Options: map[string]string{GenerationOption: gen, "id": "d4", "repo": "/w/app", "new": "1", "sandbox": "booting"}},
		{ID: "@5", Name: "e", Options: map[string]string{GenerationOption: gen, "id": "e5", "repo": "/w/app"}}, // relaunched, not new
	}
	s := newStore(nil)
	var got []string
	for _, a := range s.look(ws, map[string]state.Report{"b2": {State: state.Working, Since: time.Unix(200, 0)}}, sbxSays(map[string]bool{"": true}), time.Unix(300, 0)) {
		if a.New {
			got = append(got, a.Name)
		}
	}
	if strings.Join(got, " ") != "a d" {
		t.Fatalf("new: %v, want a d (b reported, c ended, d's sandbox not started yet, e relaunched)", got)
	}
}

func TestAnEndedAgentWithoutAMessageSaysItsSessionEnded(t *testing.T) {
	ws := []tmux.Window{
		{ID: "@1", Name: "dead", PaneDead: true, Options: map[string]string{GenerationOption: gen, "id": "a1", "repo": "/w/app", "sandbox": "claude-app"}},
		{ID: "@2", Name: "stopped", Options: map[string]string{GenerationOption: gen, "id": "b2", "repo": "/w/app", "sandbox": "claude-lib"}},
		{ID: "@3", Name: "said", PaneDead: true, Options: map[string]string{GenerationOption: gen, "id": "c3", "repo": "/w/app", "sandbox": "claude-app"}},
		{ID: "@4", Name: "quiet", Options: map[string]string{GenerationOption: gen, "id": "d4", "repo": "/w/app", "sandbox": "claude-app"}},
		{ID: "@5", Name: "silent", PaneDead: true, Options: map[string]string{GenerationOption: gen, "id": "e5", "repo": "/w/app", "sandbox": "claude-app"}},
	}
	rs := map[string]state.Report{
		"c3": {State: state.Done, Since: time.Unix(200, 0), Last: "hi"},
		// reported, but never a message
		"b2": {State: state.Working, Since: time.Unix(200, 0)},
		"e5": {State: state.Working, Since: time.Unix(200, 0)},
	}
	var got []string
	for _, a := range newStore(nil).look(ws, rs, sbxSays(map[string]bool{"claude-app": true}), time.Unix(300, 0)) {
		got = append(got, a.Name+"="+a.Last)
	}
	if want := "dead=[session ended] stopped=[session ended] said=hi quiet= silent=[session ended]"; strings.Join(got, " ") != want {
		t.Fatalf("got  %s\nwant %s", strings.Join(got, " "), want)
	}
}

func TestAnAgentStartedWithoutAPromptIsDoneOnceClaudeWaitsAtItsPrompt(t *testing.T) {
	ws := []tmux.Window{{ID: "@1", Name: "a", Options: map[string]string{GenerationOption: gen, "id": "a1", "repo": "/w/app", "new": "1", "started": "100"}}}
	s := newStore(nil)
	if a := Collect(ws, s, time.Unix(101, 0), nil)[0]; a.State != state.Starting || !a.New {
		t.Fatalf("launched: %+v", a)
	}
	// Claude's session started (SessionStart, before any prompt).
	s.Leave("/w/app", "a1", "000000001.start.1", journaltest.Hook("main", map[string]any{"session_id": "s", "cwd": "/w/app", "hook_event_name": "SessionStart", "source": "startup"}), time.Unix(102, 0))
	if a := Collect(ws, s, time.Unix(103, 0), nil)[0]; a.State != state.Done || a.New || a.Last != "" || a.Branch != "main" || a.Since.Unix() != 102 {
		t.Fatalf("session started: %+v", a)
	}
	// The user's first prompt makes it work as usual.
	s.Leave("/w/app", "a1", "000000002.prompt.1", journaltest.Hook("main", map[string]any{"session_id": "s", "cwd": "/w/app", "hook_event_name": "UserPromptSubmit", "prompt": "go"}), time.Unix(104, 0))
	if a := Collect(ws, s, time.Unix(105, 0), nil)[0]; a.State != state.Working || a.New {
		t.Fatalf("prompted: %+v", a)
	}
}

// After hq relaunches or restores an agent, its journal goes on: its last
// message, branch and worktree show while it starts, its session is the
// one to continue, and its first report of the new session counts.
func TestAReportFromBeforeALaunchGivesTheMessageButNotTheState(t *testing.T) {
	ws := []tmux.Window{lookWindow("1")}
	ws[0].Options["started"] = "900"
	s := newStore(nil)
	s.Memory.Report("/w/app", "a1", state.Report{State: state.Done, Since: time.Unix(500, 0), Last: "PR #58 opened", Branch: "feat/42", Cwd: "/w/app/.claude/worktrees/42", SessionID: "s1"}, state.Detail{})
	if err := s.Ingest("/w/app", "a1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append("a1", state.Event{Source: state.SourceHQ, Kind: state.KindRelaunched, Time: time.Unix(900, 0), Basis: 1}); err != nil {
		t.Fatal(err)
	}
	a := Collect(ws, s, time.Unix(950, 0), nil)[0]
	if a.State != state.Starting || a.Since.Unix() != 900 || a.Last != "PR #58 opened" || a.Branch != "feat/42" || a.Worktree != "/w/app/.claude/worktrees/42" || a.Session() != "s1" {
		t.Fatalf("relaunched: %+v, session %q", a, a.Session())
	}
	s.report("a1", state.Report{State: state.Working, Since: time.Unix(901, 0), Branch: "feat/42", SessionID: "s1"})
	if a := Collect(ws, s, time.Unix(950, 0), nil)[0]; a.State != state.Working || a.Since.Unix() != 901 {
		t.Fatalf("after its first report: %+v", a)
	}
	if es := s.journalOf(t, "a1"); es[len(es)-1].Number != int64(len(es)) || len(es) != 3 {
		t.Fatalf("numbered %s", journaltest.Kinds(es))
	}
}

// Entered is the moment the event that decided the state was appended:
// the hooks' report for a state they report, the look's own time for a
// turn end it saw.
func TestEnteredIsWhenTheStateCanBeSeen(t *testing.T) {
	asked := time.Unix(1000, 0)
	s := newStore(map[string]string{"%2": declinedScreen()})
	ws := []tmux.Window{lookWindow("1"), lookWindow("2")}
	seen := asked.Add(5 * time.Second)
	as := s.look(ws, map[string]state.Report{
		"a1": {State: state.Question, Since: asked, Last: "Shall I?"},
		"a2": {State: state.NeedsInput, Since: asked, Last: "Pick one"},
	}, Sandboxes{}, seen)
	if !as[0].Entered().Equal(asked) {
		t.Fatalf("question: %v", as[0].Entered())
	}
	if as[1].State != state.Done || !as[1].Entered().Equal(seen) {
		t.Fatalf("cancelled dialog: %+v entered %v", as[1], as[1].Entered())
	}
	if a := FromWindows(ws[:1])[0]; !a.Entered().Equal(a.Started) {
		t.Fatalf("no event yet: %v", a.Entered())
	}
}

func TestEndsTellsWhatIsLeftToEndInASandbox(t *testing.T) {
	// hq kill asks whether anything of an agent is left to end in its
	// sandbox: nothing when the sandbox does not run and the agent's
	// session went with it, by the list's rule (its pane dead, or its
	// report older than the answer); sbx exec would start the sandbox again
	// (#87, #101). An agent starting in a sandbox that has not started yet
	// (S2) has a session on its way, which hq kill must still end.
	asked := time.Unix(200, 0)
	before := &state.Report{State: state.Done, Since: asked.Add(-time.Second)}
	after := &state.Report{State: state.Done, Since: asked.Add(time.Millisecond)}
	live := sbxWindows()[0]
	dead := sbxWindows()[0]
	dead.PaneDead = true
	stopped := Sandboxes{Running: map[string]bool{}, Asked: asked}
	for _, c := range []struct {
		name string
		w    tmux.Window
		r    *state.Report
		s    Sandboxes
		want bool
	}{
		{"live pane, reported, sandbox stopped", live, before, stopped, true},
		{"dead pane, sandbox stopped", dead, nil, stopped, true},
		{"dead pane, sandbox not listed at all", dead, nil, Sandboxes{Running: map[string]bool{"claude-lib": true}, Asked: asked}, true},
		{"live pane, never reported, sandbox not started (S2)", live, nil, stopped, false},
		{"live pane, reported after the answer", live, after, stopped, false},
		{"live pane, reported, sandbox running", live, before, Sandboxes{Running: map[string]bool{"claude-app": true}, Asked: asked}, false},
		{"dead pane, sandbox running (an orphan)", dead, nil, Sandboxes{Running: map[string]bool{"claude-app": true}, Asked: asked}, false},
		{"sbx could not say", dead, before, Sandboxes{}, false},
	} {
		s := newStore(nil)
		if c.r != nil {
			s.report("a1", *c.r)
		}
		// hq kill reads the journals as they are (ReadStates), then asks.
		as := FromWindows([]tmux.Window{c.w})
		ReadStates(as, s, asked.Add(time.Second), nil)
		if got := c.s.Ends(as[0]); got != c.want {
			t.Errorf("%s: Ends %v, want %v", c.name, got, c.want)
		}
	}
}

func TestOwnTasksAreCountedInClaudesFooterWording(t *testing.T) {
	shell, monitor := state.Task{Type: "shell"}, state.Task{Type: "monitor"}
	for _, tc := range []struct {
		tasks []state.Task
		want  string
	}{
		{nil, ""},
		{[]state.Task{shell}, "1 shell · "},
		{[]state.Task{shell, shell}, "2 shells · "},
		{[]state.Task{monitor}, "1 monitor · "},
		{[]state.Task{monitor, shell, monitor}, "1 shell, 2 monitors · "},
	} {
		if got := OwnPrefix(tc.tasks); got != tc.want {
			t.Errorf("%+v: %q, want %q", tc.tasks, got, tc.want)
		}
	}
}

// gen is this hq's generation, as a home window holds it.
var gen = strconv.Itoa(version.Generation)
