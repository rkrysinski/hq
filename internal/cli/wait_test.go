package cli

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/journal/journaltest"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
)

// newWaitFakes has two agents, a and b, working in app since a minute ago.
func newWaitFakes() *fakes {
	f := newLsFakes()
	start := f.now.Add(-time.Hour)
	f.tmux.windows = []tmux.Window{
		{ID: "@0", Name: "hq", Options: map[string]string{}},
		agentWindow("@1", "a", "/w/app", start, false),
		agentWindow("@2", "b", "/w/app", start, false),
	}
	for _, id := range []string{"id-a", "id-b"} {
		f.states[id] = journaltest.HookState{State: state.Working, Since: f.now.Add(-time.Minute), Last: "on it"}
	}
	return f
}

// after runs step once the fake clock has passed d from now.
func (f *fakes) after(d time.Duration, step func()) {
	at, done := f.now.Add(d), false
	f.onSleep = func() {
		if !done && !f.now.Before(at) {
			done = true
			step()
		}
	}
}

func waitJSON(t *testing.T, out string) waitOutput {
	t.Helper()
	var w waitOutput
	if err := json.Unmarshal([]byte(out), &w); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	return w
}

func TestWaitReturnsAnAgentThatTurnsDoneWithItsRowAndTheNextSince(t *testing.T) {
	f := newWaitFakes()
	start := f.now
	var doneAt time.Time
	f.after(10*time.Second, func() {
		doneAt = f.now
		f.states["id-b"] = journaltest.HookState{State: state.Done, Since: f.now, Last: "PR #58 opened", Branch: "feat/58"}
	})
	code, out, errOut := f.run("wait")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	// It waited while the agents only worked, and returned with the look
	// after the one that saw the change (#116), with no settle time.
	if waited := f.now.Sub(doneAt); f.now.Sub(start) < 10*time.Second || waited < agent.LoopEvery || waited > 2*agent.LoopEvery {
		t.Fatalf("returned %v after the change", waited)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "NAME") || !strings.HasPrefix(lines[1], "b ") ||
		!strings.Contains(lines[1], "done") || !strings.Contains(lines[1], "PR #58 opened") || !strings.Contains(lines[1], "feat/58") {
		t.Fatalf("output:\n%s", out)
	}
	// The next line passes the look's cursor on, at or above b's change.
	next, err := parseSince(strings.TrimPrefix(lines[2], "next: hq wait --since "), f.now)
	if err != nil || !next.isCursor || next.cursor < f.cursorOf(t, "id-b") {
		t.Fatalf("next line %q: %+v %v", lines[2], next, err)
	}
}

func TestWaitDoesNotReturnAnAgentAlreadyDoneWhenItStartsAndTimesOutWithNothing(t *testing.T) {
	f := newWaitFakes()
	f.states["id-a"] = journaltest.HookState{State: state.Done, Since: f.now.Add(-time.Second), Last: "Done: hello"}
	start := f.now
	code, out, _ := f.run("wait", "--timeout", "3s")
	if code != 0 || !strings.HasPrefix(out, "nothing yet\nnext: hq wait --since ") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if took := f.now.Sub(start); took < 3*time.Second || took > 3*time.Second+agent.LoopEvery {
		t.Fatalf("returned after %v", took)
	}
	code, out, _ = f.run("wait", "--timeout=1", "--json")
	w := waitJSON(t, out)
	if code != 0 || w.Agents == nil || len(w.Agents) != 0 || !strings.HasPrefix(w.NextSince, "c") || !strings.Contains(out, `"agents": []`) {
		t.Fatalf("json: exit %d\n%s", code, out)
	}
}

func TestWaitSinceTheLastCursorReportsWhatChangedBetweenCalls(t *testing.T) {
	f := newWaitFakes()
	code, out, _ := f.run("wait", "--timeout", "1s", "--json")
	first := waitJSON(t, out)
	if code != 0 || len(first.Agents) != 0 {
		t.Fatalf("first call: %s", out)
	}
	// Between the calls a turns question, and b is done and working again.
	f.now = f.now.Add(5 * time.Second)
	f.states["id-a"] = journaltest.HookState{State: state.Question, Since: f.now, Last: "Shall I go on?"}
	f.states["id-b"] = journaltest.HookState{State: state.Working, Since: f.now, Last: "on it"}
	f.now = f.now.Add(10 * time.Second)
	start := f.now
	code, out, _ = f.run("wait", "--since", first.NextSince, "--json")
	second := waitJSON(t, out)
	if code != 0 || len(second.Agents) != 1 || second.Agents[0].Name != "a" || second.Agents[0].State != state.Question || f.now.Sub(start) > agent.LoopEvery {
		t.Fatalf("second call: %s (took %v)", out, f.now.Sub(start))
	}
	// Reported once: the next call from its cursor waits on.
	code, out, _ = f.run("wait", "--since", second.NextSince, "--timeout", "2s", "--json")
	if third := waitJSON(t, out); code != 0 || len(third.Agents) != 0 {
		t.Fatalf("third call: %s", out)
	}
	// Without --since the same change is no news.
	if code, out, _ := f.run("wait", "--timeout", "1s"); code != 0 || !strings.HasPrefix(out, "nothing yet") {
		t.Fatalf("no since: %s", out)
	}
	// --since also takes a duration back from now, and a moment as an
	// older hq printed next_since; either way the result carries a cursor.
	for _, s := range []string{"1m", f.now.Add(-time.Minute).UTC().Format(time.RFC3339Nano)} {
		code, out, _ := f.run("wait", "--since", s, "--json")
		if w := waitJSON(t, out); code != 0 || len(w.Agents) != 1 || w.Agents[0].Name != "a" || !strings.HasPrefix(w.NextSince, "c") {
			t.Fatalf("since %s: %s", s, out)
		}
	}
	// A moment after the change: nothing to return.
	if code, out, _ := f.run("wait", "--since", f.now.Add(-time.Second).UTC().Format(time.RFC3339Nano), "--timeout", "1s", "--json"); code != 0 || len(waitJSON(t, out).Agents) != 0 {
		t.Fatalf("since a moment after: %s", out)
	}
}

// A change at the very end of a call, after its last look, is the next
// call's: numbered above the cursor the first one gave, never missed and
// never twice. A change its look sees is not held back for being recent.
func TestWaitReportsAChangeAfterItsLastLookInTheNextCall(t *testing.T) {
	f := newWaitFakes()
	code, out, _ := f.run("wait", "--timeout", "1s", "--json")
	w := waitJSON(t, out)
	if code != 0 || len(w.Agents) != 0 {
		t.Fatalf("first call: %s", out)
	}
	// Dated a moment before the first call's end, reported after it.
	f.states["id-a"] = journaltest.HookState{State: state.Done, Since: f.now.Add(-time.Millisecond), Last: "Done"}
	first := w.NextSince
	code, out, _ = f.run("wait", "--since", first, "--timeout", "1s", "--json")
	if w = waitJSON(t, out); code != 0 || len(w.Agents) != 1 || w.Agents[0].Name != "a" || w.NextSince == first {
		t.Fatalf("second call: %s", out)
	}
	if again := waitJSON(t, mustRun(t, f, "wait", "--since", w.NextSince, "--timeout", "1s", "--json")); len(again.Agents) != 0 {
		t.Fatalf("again: %+v", again)
	}
}

func TestWaitOnNamedAgentsIgnoresTheOthers(t *testing.T) {
	f := newWaitFakes()
	f.after(time.Second, func() {
		f.states["id-b"] = journaltest.HookState{State: state.Done, Since: f.now, Last: "b done"}
	})
	code, out, _ := f.run("wait", "a", "--timeout", "3s")
	if code != 0 || !strings.HasPrefix(out, "nothing yet\nnext: hq wait a --since ") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	code, _, errOut := f.run("wait", "a", "nobody")
	if code != ExitNotFound || errOut != noAgent("nobody") {
		t.Fatalf("unknown name: exit %d %q", code, errOut)
	}
}

func TestWaitReturnsAnAgentKilledWhileWaitedOnAsEnded(t *testing.T) {
	f := newWaitFakes()
	f.after(time.Second, func() { f.tmux.windows = f.tmux.windows[:2] })
	code, out, _ := f.run("wait", "b", "--json")
	w := waitJSON(t, out)
	if code != 0 || len(w.Agents) != 1 || w.Agents[0].Name != "b" || w.Agents[0].State != state.Ended || w.Agents[0].Last != "on it" {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}

// A look of hq wait can list an agent's window before hq kill removes it
// and read its journal after the kill removed that too (#239): the agent
// still comes back ended with what it last showed, not as one that never
// said anything.
func TestWaitReturnsAnAgentKilledBetweenTheStepsOfItsLookWithItsLastMessage(t *testing.T) {
	f := newWaitFakes()
	f.states["id-a"] = journaltest.HookState{State: state.Done, Since: f.now.Add(-time.Minute), Last: "Done: hi"}
	killAt, killed := f.now.Add(time.Second), false
	f.onRead = func(id string) {
		if id != "id-a" || killed || f.now.Before(killAt) {
			return
		}
		// The look has listed the windows; the whole kill runs before it
		// reads a's journal.
		killed = true
		if code, _, errOut := f.run("kill", "a", "-y"); code != 0 {
			t.Errorf("kill: %s", errOut)
		}
	}
	code, out, _ := f.run("wait", "a", "--json")
	w := waitJSON(t, out)
	if !killed || code != 0 || len(w.Agents) != 1 || w.Agents[0].Name != "a" || w.Agents[0].State != state.Ended || w.Agents[0].Last != "Done: hi" {
		t.Fatalf("killed %v, exit %d:\n%s", killed, code, out)
	}
}

func TestWaitReturnsEachStateThatNeedsAttention(t *testing.T) {
	for _, st := range []string{state.Done, state.Question, state.NeedsInput, state.Ended} {
		f := newWaitFakes()
		f.after(time.Second, func() {
			f.states["id-a"] = journaltest.HookState{State: st, Since: f.now, Last: "x"}
		})
		code, out, _ := f.run("wait", "--json")
		if w := waitJSON(t, out); code != 0 || len(w.Agents) != 1 || w.Agents[0].State != st {
			t.Errorf("%s: %s", st, out)
		}
	}
	// A pane that dies ends its agent, dated by tmux to the whole second.
	f := newWaitFakes()
	f.after(1500*time.Millisecond, func() {
		f.tmux.windows[1].PaneDead, f.tmux.windows[1].DeadAt = true, f.now.Truncate(time.Second)
	})
	died := f.now.Add(1500 * time.Millisecond)
	code, out, _ := f.run("wait", "--json")
	if w := waitJSON(t, out); code != 0 || len(w.Agents) != 1 || w.Agents[0].State != state.Ended || f.now.Sub(died) > 2*time.Second {
		t.Errorf("pane died: %s after %v", out, f.now.Sub(died))
	}
}

func TestWaitRefusesBadArguments(t *testing.T) {
	f := newWaitFakes()
	for _, args := range [][]string{
		{"wait", "--timeout"},
		{"wait", "--timeout", "soon"},
		{"wait", "--timeout", "-1s"},
		{"wait", "--since", "yesterday"},
		{"wait", "--json=yes"},
		{"wait", "-x"},
	} {
		if code, _, errOut := f.run(args...); code != ExitUsage || !strings.HasPrefix(errOut, "hq: ") {
			t.Errorf("%v: exit %d %q", args, code, errOut)
		}
	}
	// The words of hq send and hq read (#166, #170).
	for args, want := range map[string]string{
		"wait -x":         "hq: unknown option '-x' (usage: " + waitUsage + ")\n",
		"wait --json=yes": "hq: unknown option '--json=yes' (usage: " + waitUsage + ")\n",
		"wait --timeout":  "hq: --timeout needs a value (usage: " + waitUsage + ")\n",
	} {
		if _, _, errOut := f.run(strings.Fields(args)...); errOut != want {
			t.Errorf("%s: %q, want %q", args, errOut, want)
		}
	}
	f.tmux.missing = true
	if code, _, _ := f.run("wait"); code != ExitEnvironment {
		t.Errorf("no tmux: exit %d", code)
	}
}

func TestParseWaitReadsNamesAndOptions(t *testing.T) {
	now := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	w, err := parseWait([]string{"42", "--since=2026-09-28T09:59:30.5Z", "agent-17", "--timeout", "90", "42", "--json"}, now)
	if err != nil || strings.Join(w.names, " ") != "42 agent-17" || !w.asJSON || w.timeout != 90*time.Second ||
		!w.since.moment.Equal(now.Add(-29500*time.Millisecond)) || w.since.isCursor || !w.sinceArg {
		t.Fatalf("%+v %v", w, err)
	}
	w, err = parseWait([]string{"--timeout", "0", "--since", "10m"}, now)
	if err != nil || w.timeout != 0 || !w.since.moment.Equal(now.Add(-10*time.Minute)) || len(w.names) != 0 {
		t.Fatalf("%+v %v", w, err)
	}
	if w, _ := parseWait(nil, now); w.timeout != DefaultWaitTimeout || w.sinceArg {
		t.Fatalf("defaults: %+v", w)
	}
}

// A dialog Claude opens after a turn, as /effort does, fires no hook: hq
// wait returns the agent needs input from the moment a look first saw it
// (#66).
func TestWaitReturnsAnAgentAtADialogClaudeOpensAfterItsTurn(t *testing.T) {
	f := newWaitFakes()
	f.tmux.windows[1].Pane = "%1"
	f.states["id-a"] = journaltest.HookState{State: state.Done, Since: f.now.Add(-time.Hour), Last: "hi"}
	rule := strings.Repeat("─", 40)
	f.tmux.screens = map[string]string{"%1": "● hi\n" + rule + "\n❯ \n" + rule + "\n  footer\n"}
	var openedAt time.Time
	f.after(10*time.Second, func() {
		openedAt = f.now
		f.tmux.screens["%1"] = "● hi\n" + strings.Repeat("▔", 40) + "\n   Change effort level?\n\n   ❯ 1. Yes, switch to max\n     2. No, go back\n"
	})
	code, out, errOut := f.run("wait", "a", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	w := waitJSON(t, out)
	if len(w.Agents) != 1 || w.Agents[0].Name != "a" || w.Agents[0].State != "needs input" || w.Agents[0].Last != "Change effort level?" {
		t.Fatalf("agents: %+v", w.Agents)
	}
	if waited := f.now.Sub(openedAt); waited > time.Second {
		t.Fatalf("returned %v after the dialog opened", waited)
	}
}

// An agent done with its own task running is returned as any done one,
// with the task in own_tasks, so a supervisor knows another turn comes
// (#78).
func TestWaitReturnsADoneAgentWithItsOwnTasks(t *testing.T) {
	f := newWaitFakes()
	f.after(time.Second, func() {
		f.states["id-a"] = journaltest.HookState{State: state.Done, Since: f.now, Last: "Waiting for it.", OwnTasks: []state.Task{{Type: "shell", Text: "sleep 120"}}}
	})
	code, out, _ := f.run("wait", "--json")
	if w := waitJSON(t, out); code != 0 || len(w.Agents) != 1 || w.Agents[0].State != state.Done || w.Agents[0].Last != "Waiting for it." ||
		len(w.Agents[0].OwnTasks) != 1 || w.Agents[0].OwnTasks[0] != (state.Task{Type: "shell", Text: "sleep 120"}) {
		t.Fatalf("%s", out)
	}
}

// Two agents whose turns end within one look of each other come back in
// the same call: wait looks once more after its first hit, so a supervisor
// never waits on an agent that finished beside the one returned (#116).
func TestWaitReturnsAgentsThatFinishWithinALookOfEachOtherTogether(t *testing.T) {
	f := newWaitFakes()
	at := f.now.Add(10 * time.Second)
	f.onSleep = func() {
		switch {
		case f.states["id-a"].State == state.Working && !f.now.Before(at):
			f.states["id-a"] = journaltest.HookState{State: state.Done, Since: f.now, Last: "a done"}
		case f.states["id-b"].State == state.Working && !f.now.Before(at.Add(agent.LoopEvery)):
			// b's report lands a look later, dated within that look.
			f.states["id-b"] = journaltest.HookState{State: state.Done, Since: f.now.Add(-agent.LoopEvery / 5), Last: "b done"}
		}
	}
	code, out, _ := f.run("wait", "--json")
	w := waitJSON(t, out)
	var names []string
	for _, a := range w.Agents {
		names = append(names, a.Name)
	}
	if slices.Sort(names); code != 0 || strings.Join(names, " ") != "a b" {
		t.Fatalf("want a and b, got %s", out)
	}
	if waited := f.now.Sub(at); waited > time.Second {
		t.Fatalf("returned %v after a's turn ended", waited)
	}
	// Neither comes again from the call's moment.
	code, out, _ = f.run("wait", "--since", w.NextSince, "--timeout", "2s", "--json")
	if again := waitJSON(t, out); code != 0 || len(again.Agents) != 0 {
		t.Fatalf("again: %s", out)
	}
}

// An agent found by the first look is returned although it has moved on by
// the look after it: it did enter its state after --since.
func TestWaitKeepsAnAgentFoundByItsFirstHitThatMovesOnBeforeItReturns(t *testing.T) {
	f := newWaitFakes()
	at := f.now.Add(time.Second)
	movedOn := false
	f.onSleep = func() {
		switch {
		case f.states["id-a"].State == state.Working && !movedOn && !f.now.Before(at):
			f.states["id-a"] = journaltest.HookState{State: state.Done, Since: f.now, Last: "a done"}
		case f.states["id-a"].State == state.Done && !f.now.Before(at.Add(2*agent.LoopEvery)):
			// The look after the hit: a message was typed in, a works.
			movedOn = true
			f.states["id-a"] = journaltest.HookState{State: state.Working, Since: f.now, Last: "on it"}
		}
	}
	code, out, _ := f.run("wait", "--json")
	if w := waitJSON(t, out); code != 0 || len(w.Agents) != 1 || w.Agents[0].Name != "a" || w.Agents[0].State != state.Done || w.Agents[0].Last != "a done" {
		t.Fatalf("want a done, got %s", out)
	}
}

// A wait without --since starts each agent from the state number its
// first look showed: a turn end another process numbers after that look,
// before the call reads where it starts (while deliverWaiting types), is
// still returned (#166: "Without --since, a hq wait starts from its first
// look").
func TestWaitWithoutSinceReturnsAChangeNumberedRightAfterItsFirstLook(t *testing.T) {
	f := newWaitFakes()
	// The first read of the counter is the first look's own, before it
	// lists the windows; the second is the call's, for where it starts.
	reads := 0
	f.onThrough = func() {
		if reads++; reads != 2 {
			return
		}
		// The list of another hq process ingests b's turn end.
		f.states["id-b"] = journaltest.HookState{State: state.Done, Since: f.now, Last: "Done: in between"}
		_ = f.deps().journal.Ingest("/w/app", "id-b")
	}
	code, out, errOut := f.run("wait", "--timeout", "3s", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if w := waitJSON(t, out); len(w.Agents) != 1 || w.Agents[0].Name != "b" || w.Agents[0].Last != "Done: in between" {
		t.Fatalf("want b done, got %s", out)
	}
}

// An agent working only on background work, whose pane shows Claude's agents
// list with Claude Code's cursor on a subagent's row of it and then a
// subagent's view, is not returned: neither is a dialog of Claude's own
// (Claude Code 2.1.326, #225). The picker of /model opened over that list is.
func TestWaitDoesNotReturnAnAgentAtClaudesAgentsListOrASubagentsView(t *testing.T) {
	f := newWaitFakes()
	f.tmux.windows[1].Pane = "%1"
	f.states["id-a"] = journaltest.HookState{State: state.Working, Since: f.now.Add(-time.Hour), Latest: f.now.Add(-time.Minute), Background: true, Last: "launched"}
	f.tmux.screens = map[string]string{"%1": readFixture(t, "screen-agents-last-80.txt")}
	f.after(5*time.Second, func() { f.tmux.screens["%1"] = readFixture(t, "screen-agents-view-last-80-notice.txt") })
	code, out, errOut := f.run("wait", "a", "--timeout", "15s")
	if code != 0 || !strings.HasPrefix(out, "nothing yet\n") {
		t.Fatalf("exit %d: %s%s", code, out, errOut)
	}
	f.after(5*time.Second, func() { f.tmux.screens["%1"] = readFixture(t, "screen-own-model-agents.txt") })
	code, out, errOut = f.run("wait", "a", "--json", "--timeout", "15s")
	if w := waitJSON(t, out); code != 0 || len(w.Agents) != 1 || w.Agents[0].State != "needs input" || w.Agents[0].Last != "Select model" {
		t.Fatalf("the picker: exit %d %s%s", code, out, errOut)
	}
}
