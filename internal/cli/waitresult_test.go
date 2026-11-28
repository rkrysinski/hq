package cli

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/journal/journaltest"
	"github.com/rkrysinski/hq/internal/state"
)

// resultAtALook is the wait result for terms at a look of f now, taken as
// a call takes each of its looks, with that look's cursor.
func resultAtALook(t *testing.T, f *fakes, terms waitTerms) (waitFound, int64) {
	t.Helper()
	k := f.deps().looker()
	s, err := k.Look(agent.Acting)
	if err != nil {
		t.Fatalf("look: %v", err)
	}
	next := k.Cursor(s)
	return terms.resultAt(s, next, f.now), next
}

// returned are the agents of a wait result as "name state last", in name
// order.
func returned(as []agent.Agent) string {
	var out []string
	for _, a := range as {
		out = append(out, a.Name+" "+a.State+" "+a.Last)
	}
	slices.Sort(out)
	return strings.Join(out, ", ")
}

// callBound is the most a call of theCallReturns may wait on f's clock:
// past the default timeout, which no call of these tests goes beyond.
const callBound = 2 * DefaultWaitTimeout

// theCallReturns are the agents hq wait with args returns from f, as
// returned gives them. It fails the test once the call has waited past
// callBound on f's clock, beside what f does at each sleep.
func theCallReturns(t *testing.T, f *fakes, args ...string) string {
	t.Helper()
	start, onSleep := f.now, f.onSleep
	f.onSleep = func() {
		if onSleep != nil {
			onSleep()
		}
		if f.now.Sub(start) > callBound {
			t.Fatalf("hq wait %s still waits after %v", strings.Join(args, " "), f.now.Sub(start))
		}
	}
	defer func() { f.onSleep = onSleep }()
	w := waitJSON(t, mustRun(t, f, append([]string{"wait", "--json"}, args...)...))
	var out []string
	for _, r := range w.Agents {
		out = append(out, r.Name+" "+r.State+" "+r.Last)
	}
	slices.Sort(out)
	return strings.Join(out, ", ")
}

// fromCursor are the terms of a wait on names from cursor c, none covered
// yet and with no deadline: as a call from c starts.
func fromCursor(c int64, names ...string) waitTerms {
	return waitTerms{names: names, since: since{cursor: c, isCursor: true}}
}

// An agent that changes into a state wait returns after the cursor comes
// back, at the look that sees it, as a call from that cursor returns it.
func TestWaitResultReturnsAnAgentChangedAfterTheCursor(t *testing.T) {
	f := newWaitFakes()
	r, c := resultAtALook(t, f, fromCursor(0))
	if len(r.agents) != 0 || r.late {
		t.Fatalf("nothing changed yet: %s late %v", returned(r.agents), r.late)
	}
	f.now = f.now.Add(time.Second)
	f.states["id-b"] = journaltest.HookState{State: state.Done, Since: f.now, Last: "PR #58 opened"}
	r, _ = resultAtALook(t, f, fromCursor(c))
	if got := returned(r.agents); got != "b done PR #58 opened" || r.late {
		t.Fatalf("got %q late %v", got, r.late)
	}
	if call := theCallReturns(t, f, "--since", cursorText(c), "--timeout", "1s"); call != returned(r.agents) {
		t.Fatalf("the call returns %q, the result %q", call, returned(r.agents))
	}
}

// A change numbered at or below the cursor is not returned: the result
// that gave the cursor stood for it, as a call from that cursor knows.
func TestWaitResultDoesNotReturnAChangeBeforeTheCursor(t *testing.T) {
	f := newWaitFakes()
	f.states["id-b"] = journaltest.HookState{State: state.Done, Since: f.now, Last: "PR #58 opened"}
	r, c := resultAtALook(t, f, fromCursor(0))
	if got := returned(r.agents); got != "b done PR #58 opened" {
		t.Fatalf("from the start: %q", got)
	}
	f.now = f.now.Add(time.Second)
	r, _ = resultAtALook(t, f, fromCursor(c))
	if len(r.agents) != 0 || r.late {
		t.Fatalf("from its cursor: %s late %v", returned(r.agents), r.late)
	}
	if call := theCallReturns(t, f, "--since", cursorText(c), "--timeout", "1s"); call != "" {
		t.Fatalf("the call returns %q", call)
	}
}

// An agent the previous look covered that this look no longer shows was
// killed meanwhile: it is returned ended with what it last showed, as a
// call that saw it before returns it; terms that do not cover it return
// nothing, since a killed agent has no number to compare.
func TestWaitResultReturnsAnAgentKilledSinceThePreviousLookAsEnded(t *testing.T) {
	f := newWaitFakes()
	terms := fromCursor(0, "b")
	r, c := resultAtALook(t, f, terms)
	if len(r.agents) != 0 || returned(r.covered) != "b working on it" {
		t.Fatalf("before the kill: %s, covered %s", returned(r.agents), returned(r.covered))
	}
	terms.since.cursor, terms.covered = c, r.covered
	f.now = f.now.Add(time.Second)
	f.tmux.windows = f.tmux.windows[:2]
	r, _ = resultAtALook(t, f, terms)
	if got := returned(r.agents); got != "b ended on it" || r.late {
		t.Fatalf("after the kill: %q late %v", got, r.late)
	}
	if _, numbered := r.agents[0].StateSeq(); numbered || !r.agents[0].Since.Equal(f.now) {
		t.Fatalf("ended with a number, or not now: %+v", r.agents[0])
	}
	if r, _ := resultAtALook(t, f, fromCursor(c, "b")); len(r.agents) != 0 {
		t.Fatalf("not covered: %s", returned(r.agents))
	}
	f = newWaitFakes()
	_, c = resultAtALook(t, f, fromCursor(0))
	f.after(time.Second, func() { f.tmux.windows = f.tmux.windows[:2] })
	if call := theCallReturns(t, f, "b", "--since", cursorText(c)); call != "b ended on it" {
		t.Fatalf("the call returns %q", call)
	}
}

// An agent the previous look covered with a number, whose journal can no
// longer be read and whose pane is dead, is ended with no event behind it:
// returned as a killed one, by terms that cover it alone, as a call that
// saw it before returns it.
func TestWaitResultReturnsAnAgentWhoseJournalCannotBeReadAsToday(t *testing.T) {
	f := newWaitFakes()
	f.unreadable = map[string]bool{}
	terms := fromCursor(0)
	r, c := resultAtALook(t, f, terms)
	if len(r.agents) != 0 || returned(r.covered) != "a working on it, b working on it" {
		t.Fatalf("before: %s, covered %s", returned(r.agents), returned(r.covered))
	}
	terms.since.cursor, terms.covered = c, r.covered
	f.now = f.now.Add(time.Second)
	f.unreadable["id-b"] = true
	f.tmux.windows[2].PaneDead = true
	r, _ = resultAtALook(t, f, terms)
	if len(r.agents) != 1 || r.agents[0].Name != "b" || r.agents[0].State != state.Ended || r.late {
		t.Fatalf("unreadable: %s late %v", returned(r.agents), r.late)
	}
	if _, numbered := r.agents[0].StateSeq(); numbered {
		t.Fatalf("ended with a number: %+v", r.agents[0])
	}
	if r, _ := resultAtALook(t, f, fromCursor(c)); len(r.agents) != 0 {
		t.Fatalf("not covered: %s", returned(r.agents))
	}
	want := returned(r.agents)
	f = newWaitFakes()
	f.unreadable = map[string]bool{}
	_, c = resultAtALook(t, f, fromCursor(0))
	f.after(time.Second, func() {
		f.unreadable["id-b"] = true
		f.tmux.windows[2].PaneDead = true
	})
	if call := theCallReturns(t, f, "--since", cursorText(c)); call != want {
		t.Fatalf("the call returns %q, the result %q", call, want)
	}
}

// Once the deadline has passed with nothing to return, the result is late
// with no agents, as a call returns at its timeout; before it, or with no
// deadline, it is not late; a change found after the deadline is returned,
// not late.
func TestWaitResultIsLateWithNoAgentsOnceTheDeadlineHasPassed(t *testing.T) {
	for _, tc := range []struct {
		name     string
		none     bool          // no deadline
		deadline time.Duration // from now
		late     bool
	}{
		{"no deadline", true, 0, false},
		{"a moment before it", false, time.Millisecond, false},
		{"at it", false, 0, true},
		{"after it", false, -time.Second, true},
	} {
		f := newWaitFakes()
		_, c := resultAtALook(t, f, fromCursor(0))
		terms := fromCursor(c)
		if !tc.none {
			terms.deadline = f.now.Add(tc.deadline)
		}
		if r, _ := resultAtALook(t, f, terms); len(r.agents) != 0 || r.late != tc.late {
			t.Errorf("%s: %s late %v, want late %v", tc.name, returned(r.agents), r.late, tc.late)
		}
	}
	f := newWaitFakes()
	_, c := resultAtALook(t, f, fromCursor(0))
	start := f.now
	if call := theCallReturns(t, f, "--since", cursorText(c), "--timeout", "2s"); call != "" || f.now.Before(start.Add(2*time.Second)) {
		t.Fatalf("the call returns %q after %v", call, f.now.Sub(start))
	}
	f.states["id-a"] = journaltest.HookState{State: state.Done, Since: f.now, Last: "a done"}
	terms := fromCursor(c)
	terms.deadline = f.now.Add(-time.Second)
	if r, _ := resultAtALook(t, f, terms); returned(r.agents) != "a done a done" || r.late {
		t.Fatalf("a change after the deadline: %s late %v", returned(r.agents), r.late)
	}
}

// A wait on every agent leaves an outdated agent out: it is not covered,
// so neither its kill nor anything else of it is returned, as a call on
// every agent leaves it out; the others come back.
func TestWaitResultOnEveryAgentLeavesAnOutdatedAgentOut(t *testing.T) {
	f := outdatedFakes()
	terms := fromCursor(0)
	r, c := resultAtALook(t, f, terms)
	if len(r.agents) != 0 || returned(r.covered) != "a working on it" {
		t.Fatalf("before: %s, covered %s", returned(r.agents), returned(r.covered))
	}
	terms.since.cursor, terms.covered = c, r.covered
	f.now = f.now.Add(time.Second)
	f.tmux.windows = f.tmux.windows[:2]
	if r, _ := resultAtALook(t, f, terms); len(r.agents) != 0 {
		t.Fatalf("old killed: %s", returned(r.agents))
	}
	f.states["id-a"] = journaltest.HookState{State: state.Done, Since: f.now, Last: "PR #10 opened"}
	r, _ = resultAtALook(t, f, terms)
	if got := returned(r.agents); got != "a done PR #10 opened" {
		t.Fatalf("a done: %q", got)
	}
	want := returned(r.agents)
	f = outdatedFakes()
	_, c = resultAtALook(t, f, fromCursor(0))
	f.onSleep = chain(f, time.Second, func() { f.tmux.windows = f.tmux.windows[:2] }, 2*time.Second, func() {
		f.states["id-a"] = journaltest.HookState{State: state.Done, Since: f.now, Last: "PR #10 opened"}
	})
	if call := theCallReturns(t, f, "--since", cursorText(c)); call != want {
		t.Fatalf("the call returns %q, the result %q", call, want)
	}
}
