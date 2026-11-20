package state

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// seenEvent is event n of hq's own from source, of kind, based on the
// hook's event basis, with facts.
func seenEvent(n int64, source, kind string, basis int64, f Facts) Event {
	return Event{Number: n, Source: source, Kind: kind, Time: atSecond(n), Basis: basis, Facts: f}
}

// seesWhenItMayTell is what a look at now sees of screen for the fold f,
// met as the look meets it: it fails the test when the screen may not tell
// at now (ScreenMayTell), a moment at which the look passes no capture to
// Sees. It is the one way a test gets what a look sees (#232):
// go run ./tools/testgate sees fails a test that calls Sees itself (#285).
func seesWhenItMayTell(t testing.TB, f Fold, screen string, now time.Time) (events []Event, resting bool) {
	t.Helper()
	if !f.ScreenMayTell(now) {
		t.Fatalf("its screen may not tell at %s: the look passes no capture to Sees there", now.Format(time.RFC3339Nano))
	}
	return f.Sees(screen, now)
}

// fatalTB is a test that records the message it is failed with and stops
// the goroutine it runs in, as Fatalf stops a test.
type fatalTB struct {
	testing.TB
	failed string
}

func (f *fatalTB) Helper() {}

func (f *fatalTB) Fatalf(format string, args ...any) {
	f.failed = fmt.Sprintf(format, args...)
	runtime.Goexit()
}

// seesIn runs seesWhenItMayTell in a test of its own, and returns what it
// returned and the message the test was failed with, "" for none.
func seesIn(f Fold, screen string, now time.Time) (events []Event, resting bool, failed string) {
	tb := &fatalTB{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		events, resting = seesWhenItMayTell(tb, f, screen, now)
	}()
	<-done
	return events, resting, tb.failed
}

// The helper fails a test that passes a capture at a moment at which the
// screen may not tell, and otherwise returns what Sees returns (#285).
func TestSeesWhenItMayTellFailsAtAMomentTheScreenMayNotTell(t *testing.T) {
	// A turn Claude owes for background work: its screen at rest is seen,
	// and one more look RestDelay later decides.
	owed := func() Fold {
		return Fold{}.With(
			userPrompt(1, "t1"),
			endOfTurn(2, "t1", false, subagentRunning("a1"), subagentRunning("a2")),
			hookEvent(3, "prompt", "UserPromptSubmit", Facts{Turn: "w1", Wake: true, Woke: "a1"}),
			endOfTurn(4, "w1", false),
		)
	}
	for _, tc := range []struct {
		name string
		f    Fold
		now  time.Time
	}{
		{"no session", Fold{}, atSecond(9)},
		{"before the overrule delay", owed(), atSecond(4).Add(OverruleDelay - time.Millisecond)},
	} {
		events, resting, failed := seesIn(tc.f, screen(t, "start-ready"), tc.now)
		if want := "its screen may not tell at " + tc.now.Format(time.RFC3339Nano) + ": the look passes no capture to Sees there"; failed != want || events != nil || resting {
			t.Errorf("%s: failed %q, returned %+v %v", tc.name, failed, events, resting)
		}
	}
	now := atSecond(4).Add(OverruleDelay)
	events, resting, failed := seesIn(owed(), screen(t, "start-ready"), now)
	if failed != "" || len(events) != 1 || events[0].Kind != KindAtRest || !events[0].Time.Equal(now) || !resting {
		t.Fatalf("owed, at rest: failed %q, returned %+v %v", failed, events, resting)
	}
}

// withoutAge is r as it stands whatever time is passed in.
func withoutAge(r Reduced) Reduced {
	r.Age = 0
	return r
}

// A decision that takes time is an event: the first screen at rest
// decides nothing, whatever time is passed in; the look's decision
// RestDelay later makes a rewound turn done with Interrupted at that
// event, counted from when the screen was first seen at rest. An end known
// only from the pane's death counts from that death, and the result gives
// the time of the event that decided it too.
func TestADecisionThatTakesTimeIsAnEventAndTheTimePassedInDecidesNone(t *testing.T) {
	journal := []Event{
		userPrompt(1, "t1"),
		seenEvent(2, SourceScreen, KindAtRest, 1, Facts{Screen: "f1"}),
	}
	for _, now := range []time.Time{atSecond(3), atSecond(5), atSecond(3).Add(24 * time.Hour)} {
		if r := Reduce(journal, Reply{}, now); r.State != Working || !r.Since.Equal(atSecond(1)) || r.Entered.Number != 1 {
			t.Errorf("at rest, at %v: %s since %v, entered %+v", now, r.State, r.Since, r.Entered)
		}
	}
	journal = append(journal, seenEvent(3, SourceScreen, KindRewound, 1, Facts{Screen: "f1", From: atSecond(2)}))
	r := Reduce(journal, Reply{}, atSecond(9))
	if r.State != Done || r.Last != RewoundLast || !r.Since.Equal(atSecond(2)) || r.Entered != (Ref{3, atSecond(3)}) || r.TurnEnded != 0 || !slices.Equal(r.RestsOn, []int64{1, 2, 3}) {
		t.Errorf("rewound: %+v", r)
	}

	// A screen at rest that changed starts over: the decision rests on the
	// screen it was first seen as.
	changed := []Event{
		userPrompt(1, "t1"),
		seenEvent(2, SourceScreen, KindAtRest, 1, Facts{Screen: "f1"}),
		seenEvent(3, SourceScreen, KindAtRest, 1, Facts{Screen: "f2"}),
		seenEvent(4, SourceScreen, KindAtRest, 1, Facts{Screen: "f2"}), // the same again
		seenEvent(5, SourceScreen, KindRewound, 1, Facts{Screen: "f2", From: atSecond(3)}),
	}
	if r := Reduce(changed, Reply{}, atSecond(9)); !r.Since.Equal(atSecond(3)) || !slices.Equal(r.RestsOn, []int64{1, 3, 5}) {
		t.Errorf("a changed screen: since %v, rests on %v", r.Since, r.RestsOn)
	}

	died := []Event{
		userPrompt(1, "t1"),
		endOfTurn(2, "t1", false),
		seenEvent(3, SourceTmux, KindPaneDied, 2, Facts{From: atSecond(2).Add(500 * time.Millisecond)}),
	}
	r = Reduce(died, Reply{}, atSecond(9))
	if r.State != Ended || !r.Since.Equal(atSecond(2).Add(500*time.Millisecond)) || r.Entered != (Ref{3, atSecond(3)}) || r.Last != EndedLast {
		t.Errorf("the pane's death: %+v", r)
	}
	// One tmux gives no time for counts from the event, never from before
	// the latest report.
	died[2].Facts.From = time.Time{}
	if r := Reduce(died, Reply{}, atSecond(9)); r.State != Ended || !r.Since.Equal(atSecond(3)) {
		t.Errorf("an undated death: %+v", r)
	}
	died[2].Facts.From = atSecond(1)
	if r := Reduce(died, Reply{}, atSecond(9)); !r.Since.Equal(atSecond(2)) {
		t.Errorf("a death dated before the report: %+v", r)
	}
}

// Of two sources of ended the first decides the moment; sbx's answer is
// not read for an agent whose latest report is newer than the moment sbx
// was asked, nor for one that has not reported, nor without the moment.
func TestOfTwoSourcesOfEndedTheFirstDecides(t *testing.T) {
	stop := []Event{userPrompt(1, "t1"), endOfTurn(2, "t1", false)}
	notRunning := func(n int64, asked time.Time) Event {
		return seenEvent(n, SourceSbx, KindNotRunning, 2, Facts{Asked: asked})
	}
	died := func(n int64) Event { return seenEvent(n, SourceTmux, KindPaneDied, 2, Facts{From: atSecond(n)}) }
	for _, tc := range []struct {
		name  string
		after []Event
		state string
		since time.Time
		by    int64
	}{
		{"sbx, then the pane", []Event{notRunning(3, atSecond(3)), died(4)}, Ended, atSecond(3), 3},
		{"the pane, then sbx", []Event{died(3), notRunning(4, atSecond(4))}, Ended, atSecond(3), 3},
		{"the session's end, then the pane", []Event{hookEvent(3, "end", "SessionEnd", Facts{}), died(4)}, Ended, atSecond(3), 3},
		{"a restart, then the pane", []Event{seenEvent(3, SourceHQ, KindTakenDown, 2, Facts{}), died(4)}, Ended, atSecond(3), 3},
		{"sbx asked before the latest report", []Event{notRunning(3, atSecond(2).Add(-time.Millisecond))}, Done, atSecond(2), 2},
		{"sbx asked as the latest report came", []Event{notRunning(3, atSecond(2))}, Ended, atSecond(3), 3},
		{"sbx asked before, then the pane", []Event{notRunning(3, atSecond(1)), died(4)}, Ended, atSecond(4), 4},
		{"no moment asked", []Event{notRunning(3, time.Time{})}, Done, atSecond(2), 2},
	} {
		r := Reduce(append(slices.Clone(stop), tc.after...), Reply{}, atSecond(9))
		if r.State != tc.state || !r.Since.Equal(tc.since) || r.Entered.Number != tc.by {
			t.Errorf("%s: %s since %v, entered %+v", tc.name, r.State, r.Since, r.Entered)
		}
	}
	launched := seenEvent(1, SourceHQ, KindLaunched, 0, Facts{Start: "new"})
	if r := Reduce([]Event{launched, notRunning(2, atSecond(2))}, Reply{}, atSecond(9)); r.State != Starting || !r.Since.Equal(atSecond(1)) || r.Entered.Number != 1 || !slices.Equal(r.RestsOn, []int64{1}) {
		t.Errorf("before any report: %+v", r)
	}
	if r := Reduce([]Event{launched, died(2)}, Reply{}, atSecond(9)); r.State != Ended || !r.Since.Equal(atSecond(2)) || r.Last != EndedLast {
		t.Errorf("dead before any report: %+v", r)
	}
}

// An event set aside, as a duplicate or as overtaken, changes nothing: a
// copy of every event of hq's own in the fixture journals, set aside right
// after it, leaves the result as it was after every step, and so does
// evidence a report overtook.
func TestAnEventSetAsideChangesNothing(t *testing.T) {
	now := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	for name, fx := range fixtureJournals(t) {
		var events, doubled []Event
		reply := Reply{}
		for i, step := range fx.Steps {
			if step.Reply != nil {
				reply = *step.Reply
			}
			for _, e := range step.Events {
				events = append(events, e)
				doubled = append(doubled, e)
				if e.Source != SourceHook {
					dup := e
					dup.Number, dup.Aside = e.Number+1_000_000, AsideDuplicate
					doubled = append(doubled, dup)
				}
			}
			want, got := Reduce(events, reply, now), Reduce(doubled, reply, now)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s, step %d:\n got %+v\nwant %+v", name, i, got, want)
			}
		}
	}
	overtaken := seenEvent(3, SourceScreen, KindTurnEnded, 1, Facts{Text: "Interrupted"})
	overtaken.Aside = AsideOvertaken
	journal := []Event{userPrompt(1, "t1"), hookEvent(2, "answer", "PostToolUse", Facts{Turn: "t1", Tool: "Bash"}), overtaken}
	if r, before := Reduce(journal, Reply{}, atSecond(9)), Reduce(journal[:2], Reply{}, atSecond(9)); !reflect.DeepEqual(r, before) || r.State != Working {
		t.Errorf("overtaken:\n%+v\n%+v", r, before)
	}
}

// The dialog verdict says whose the dialog is, since which event, and
// which events opened and closed it; a closed one stays named until the
// next opens.
func TestTheDialogVerdictSaysWhoseSinceWhichEventAndWhatOpenedAndClosedIt(t *testing.T) {
	journal := []Event{
		userPrompt(1, "t1"),
		hookEvent(2, "dialog", "PermissionRequest", Facts{Turn: "t1", Tool: "Bash", Text: "Bash: ls"}),
		seenEvent(3, SourceScreen, KindAtWork, 2, Facts{}),
		endOfTurn(4, "t1", false),
		seenEvent(5, SourceScreen, KindOwnDialog, 4, Facts{Screen: "f1", Text: "Change effort level?"}),
		seenEvent(6, SourceScreen, KindOwnDialog, 4, Facts{Screen: "f1", Text: "Change effort level?"}), // the same, seen again
		seenEvent(7, SourceScreen, KindOwnDialogGone, 4, Facts{}),
		hookEvent(8, "dialog", "PermissionRequest", Facts{Turn: "t1", Tool: "Read", Subagent: true, Text: "Read: a"}),
	}
	for _, tc := range []struct {
		after          int64
		state          string
		dialog         DialogVerdict
		since          time.Time
		last           string
		changed, enter int64
	}{
		{2, NeedsInput, DialogVerdict{Open: true, Whose: DialogHooks, Opened: Ref{2, atSecond(2)}, RestsOn: []int64{2}}, atSecond(2), "Bash: ls", 2, 2},
		{3, Working, DialogVerdict{Whose: DialogHooks, Opened: Ref{2, atSecond(2)}, Closed: Ref{3, atSecond(3)}, RestsOn: []int64{2, 3}}, atSecond(2), "", 3, 3},
		{4, Done, DialogVerdict{Whose: DialogHooks, Opened: Ref{2, atSecond(2)}, Closed: Ref{3, atSecond(3)}, RestsOn: []int64{2, 3}}, atSecond(4), "", 4, 4},
		{6, NeedsInput, DialogVerdict{Open: true, Whose: DialogOwn, Opened: Ref{5, atSecond(5)}, Screen: "f1", RestsOn: []int64{5}}, atSecond(5), "Change effort level?", 5, 5},
		{7, Done, DialogVerdict{Whose: DialogOwn, Opened: Ref{5, atSecond(5)}, Closed: Ref{7, atSecond(7)}, Screen: "f1", RestsOn: []int64{5, 7}}, atSecond(4), "", 7, 4},
		{8, NeedsInput, DialogVerdict{Open: true, Whose: DialogSubagent, Opened: Ref{8, atSecond(8)}, RestsOn: []int64{8}}, atSecond(8), "Read: a", 8, 8},
	} {
		r := reducedAt(journal, tc.after)
		if r.State != tc.state || !reflect.DeepEqual(r.Dialog, tc.dialog) || !r.Since.Equal(tc.since) || r.Last != tc.last || r.Changed.Number != tc.changed || r.Entered.Number != tc.enter {
			t.Errorf("after %d: %s since %v %q, changed %d, entered %d, dialog %+v", tc.after, r.State, r.Since, r.Last, r.Changed.Number, r.Entered.Number, r.Dialog)
		}
	}
	// A dialog of Claude's own is read only while the agent waits at its
	// prompt, and only for the report it was seen at.
	working := []Event{userPrompt(1, "t1"), seenEvent(2, SourceScreen, KindOwnDialog, 1, Facts{Screen: "f1", Text: "Change effort level?"})}
	if r := Reduce(working, Reply{}, atSecond(9)); r.State != Working || r.Dialog.Open {
		t.Errorf("working: %+v", r)
	}
	later := append(slices.Clone(journal[:5]), userPrompt(6, "t2"), endOfTurn(7, "t2", false))
	if r := Reduce(later, Reply{}, atSecond(9)); r.State != Done || r.Dialog.Open {
		t.Errorf("a later report: %+v", r)
	}
	// The screen closes no subagent's dialog.
	theirs := []Event{userPrompt(1, "t1"), hookEvent(2, "dialog", "PermissionRequest", Facts{Subagent: true, Tool: "Read"}), seenEvent(3, SourceScreen, KindAtWork, 2, Facts{})}
	if r := Reduce(theirs, Reply{}, atSecond(9)); r.State != NeedsInput || r.Dialog.Whose != DialogSubagent {
		t.Errorf("a subagent's: %+v", r)
	}
}

// Every state in the fixture journals has an event that stands for it: a
// step that appended no event leaves the result as it was, a state that
// changed did so at an event of the step, and the state was entered at an
// event of the journal (no event before the first report or launch).
func TestEveryStateInTheFixturesHasAnEventThatStandsForIt(t *testing.T) {
	now := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	for name, fx := range fixtureJournals(t) {
		var events []Event
		reply := Reply{}
		var was Reduced
		for i, step := range fx.Steps {
			if step.Reply != nil {
				reply = *step.Reply
			}
			events = append(events, step.Events...)
			r := withoutAge(Reduce(events, reply, now))
			numbers := map[int64]bool{}
			for _, e := range events {
				numbers[e.Number] = true
			}
			switch {
			case len(step.Events) == 0 && i > 0 && !reflect.DeepEqual(r, was):
				t.Errorf("%s, step %d: no event, and the result changed:\n%+v\n%+v", name, i, was, r)
			case r.State != was.State && i > 0 && !slices.ContainsFunc(step.Events, func(e Event) bool { return e.Number == r.Changed.Number }):
				t.Errorf("%s, step %d: %s, changed at %d, not an event of the step", name, i, r.State, r.Changed.Number)
			case r.Entered.Number != 0 && !numbers[r.Entered.Number] || r.Entered.Number == 0 && len(events) > 0 && r.State != Starting:
				t.Errorf("%s, step %d: %s entered at %+v", name, i, r.State, r.Entered)
			}
			was = r
		}
	}
}

// A relaunch withdrawn (its pane could not be respawned) leaves the agent
// as the restart took it down.
func TestARelaunchWithdrawnLeavesTheAgentTakenDown(t *testing.T) {
	journal := []Event{
		userPrompt(1, "t1"),
		endOfTurn(2, "t1", false),
		seenEvent(3, SourceHQ, KindTakenDown, 2, Facts{}),
		seenEvent(4, SourceHQ, KindRelaunched, 2, Facts{}),
	}
	if r := Reduce(journal, Reply{}, atSecond(9)); r.State != Starting || !r.Since.Equal(atSecond(4)) {
		t.Errorf("relaunched: %+v", r)
	}
	// Withdrawn of anything else leaves the relaunch standing.
	if r := Reduce(append(slices.Clone(journal), seenEvent(5, SourceHQ, KindWithdrawn, 2, Facts{Withdrawn: KindAnswer})), Reply{}, atSecond(9)); r.State != Starting {
		t.Errorf("an answer withdrawn: %+v", r)
	}
	journal = append(journal, seenEvent(5, SourceHQ, KindWithdrawn, 2, Facts{Withdrawn: KindRelaunched}))
	if r := Reduce(journal, Reply{}, atSecond(9)); r.State != Ended || !r.Since.Equal(atSecond(3)) {
		t.Errorf("withdrawn: %+v", r)
	}
	// Withdrawn of anything else, or with no launch to withdraw, changes
	// nothing.
	for _, w := range []Event{seenEvent(6, SourceHQ, KindWithdrawn, 2, Facts{Withdrawn: KindAnswer}), seenEvent(6, SourceHQ, KindWithdrawn, 2, Facts{Withdrawn: KindRelaunched})} {
		if r := Reduce(append(slices.Clone(journal), w), Reply{}, atSecond(9)); r.State != Ended || !r.Since.Equal(atSecond(3)) {
			t.Errorf("%+v: %+v", w.Facts, r)
		}
	}
}

// A pane death tmux dates before the agent's latest relaunch is its
// previous session's: appended after the relaunch, by a look that listed
// the windows before the relaunch respawned the pane, it ends nobody,
// whatever the relaunched session reports after it. A death dated after
// the relaunch, within its second (tmux dates to the second), or not
// dated at all ends the agent.
func TestAPaneDeathDatedBeforeTheLatestRelaunchEndsNobody(t *testing.T) {
	relaunched := []Event{
		userPrompt(1, "t1"),
		seenEvent(2, SourceTmux, KindPaneDied, 1, Facts{From: atSecond(2)}),
		seenEvent(3, SourceHQ, KindRelaunched, 1, Facts{}),
	}
	relaunched[2].Time = atSecond(3).Add(500 * time.Millisecond)
	stale := seenEvent(4, SourceTmux, KindPaneDied, 1, Facts{From: atSecond(2)})
	journal := append(slices.Clone(relaunched), stale)
	if r := Reduce(journal, Reply{}, atSecond(9)); r.State != Starting || r.Entered.Number != 3 {
		t.Errorf("the stale death: %+v", r)
	}
	journal = append(journal, hookEvent(5, "prompt", "UserPromptSubmit", Facts{Turn: "t2", Prompt: "next"}))
	if r := Reduce(journal, Reply{}, atSecond(9)); r.State != Working || r.Entered.Number != 5 {
		t.Errorf("the relaunched session at work: %+v", r)
	}
	f := Fold{}.With(relaunched...)
	if !f.DiedBeforeLaunch(atSecond(2)) || f.DiedBeforeLaunch(atSecond(3)) || f.DiedBeforeLaunch(time.Time{}) {
		t.Errorf("died before the launch at second 3.5: second 2 %v, second 3 %v, undated %v", f.DiedBeforeLaunch(atSecond(2)), f.DiedBeforeLaunch(atSecond(3)), f.DiedBeforeLaunch(time.Time{}))
	}
	for _, from := range []time.Time{atSecond(4), atSecond(3), time.Time{}} {
		died := seenEvent(4, SourceTmux, KindPaneDied, 1, Facts{From: from})
		if r := Reduce(append(slices.Clone(relaunched), died), Reply{}, atSecond(9)); r.State != Ended || r.Entered.Number != 4 {
			t.Errorf("a death dated %v: %+v", from, r)
		}
	}
}

// Claude at work on a prompt it took from its queue hands the hook's state
// over to it, working since then, the queued turn's mark of a tool's end
// gone; with no prompt queued nothing is handed over.
func TestClaudeTakingAQueuedPromptHandsTheStateOverToIt(t *testing.T) {
	journal := []Event{
		userPrompt(1, "t1"),
		hookEvent(2, "answer", "PostToolUse", Facts{Turn: "t1", Tool: "Bash"}),
		hookEvent(3, "prompt", "UserPromptSubmit", Facts{Turn: "t1", Prompt: "and then"}), // queued
		seenEvent(4, SourceScreen, KindTookQueued, 3, Facts{}),
	}
	if r := reducedAt(journal, 3); r.State != Working || !r.Queued || !r.Acted {
		t.Fatalf("queued: %+v", r)
	}
	r := reducedAt(journal, 4)
	if r.State != Working || r.Queued || r.Acted || r.Prompt != "and then" || !r.Since.Equal(atSecond(4)) || r.Entered.Number != 4 {
		t.Errorf("taken: %+v", r)
	}
	// The turn end the screen showed before the hand-over is not the
	// taken turn's.
	ended := slices.Insert(slices.Clone(journal), 3, seenEvent(10, SourceScreen, KindTurnEnded, 3, Facts{Text: "Interrupted"}))
	if r := Reduce(ended, Reply{}, atSecond(20)); r.State != Working {
		t.Errorf("a turn end before: %+v", r)
	}
	if r := Reduce([]Event{userPrompt(1, "t1"), seenEvent(2, SourceScreen, KindTookQueued, 1, Facts{})}, Reply{}, atSecond(9)); r.State != Working || !r.Since.Equal(atSecond(1)) {
		t.Errorf("nothing queued: %+v", r)
	}
}

// A turn the user ended at the agent, with its line or rewound, is no
// completed turn: the last completed turn stays the one the hook reported
// before it. A turn Claude owed for background work that does not come
// completes the turn whose end the hook reported and kept working.
func TestATurnTheUserEndedIsNoCompletedTurnButOneClaudeOwedThatDidNotComeIs(t *testing.T) {
	journal := []Event{
		userPrompt(1, "t1"),
		endOfTurn(2, "t1", false),
		userPrompt(3, "t2"),
		seenEvent(4, SourceScreen, KindTurnEnded, 3, Facts{Text: "Interrupted"}),
		seenEvent(5, SourceScreen, KindTurnEnded, 3, Facts{Text: "User declined to answer questions"}), // a later one for the same report
		userPrompt(6, "t3"),
		seenEvent(7, SourceScreen, KindAtRest, 6, Facts{Screen: "f1"}),
		seenEvent(8, SourceScreen, KindRewound, 6, Facts{Screen: "f1", From: atSecond(7)}),
	}
	for _, tc := range []struct {
		after int64
		state string
	}{{2, Done}, {4, Done}, {5, Done}, {6, Working}, {8, Done}} {
		if r := reducedAt(journal, tc.after); r.State != tc.state || r.TurnEnded != 2 {
			t.Errorf("after %d: %s, turn ended at %d", tc.after, r.State, r.TurnEnded)
		}
	}
	if r := reducedAt(journal, 5); r.Last != "Interrupted" || !r.Since.Equal(atSecond(4)) {
		t.Errorf("ended: %+v", r)
	}

	owed := []Event{
		userPrompt(1, "t1"),
		endOfTurn(2, "t1", false, subagentRunning("a1"), subagentRunning("a2")),
		hookEvent(3, "prompt", "UserPromptSubmit", Facts{Turn: "w1", Wake: true, Woke: "a1"}),
		endOfTurn(4, "w1", false), // a2 finished together with a1: Claude owes a turn
		seenEvent(5, SourceScreen, KindAtRest, 4, Facts{Screen: "f1"}),
		seenEvent(6, SourceScreen, KindTurnNotTaken, 4, Facts{Screen: "f1", From: atSecond(5)}),
	}
	if r := reducedAt(owed, 5); r.State != Working || !r.Owed || r.TurnEnded != 0 {
		t.Errorf("owed: %s, owed %v, turn ended at %d", r.State, r.Owed, r.TurnEnded)
	}
	if r := reducedAt(owed, 6); r.State != Done || r.TurnEnded != 6 {
		t.Errorf("not taken: %s, turn ended at %d", r.State, r.TurnEnded)
	}
}

// An agent launched with a first prompt is starting until it reports that
// prompt: its new session's start is no done (spec §5, S2; #191). Without
// one, or on a session resumed or started over, the start is done as ever,
// and a dialog of Claude's own at the start still needs input.
func TestAnAgentLaunchedWithAPromptIsStartingUntilItReportsIt(t *testing.T) {
	launch := func(n int64, prompted bool) Event {
		return seenEvent(n, SourceHQ, KindLaunched, 0, Facts{Start: "new", Prompted: prompted})
	}
	start := func(n int64, source string) Event {
		hookCase := CaseStart
		if source == resumeSource {
			hookCase = CaseResume
		}
		return hookEvent(n, hookCase, KindSessionStart, Facts{Start: source, Session: "s1", Cwd: "/w/app"})
	}
	for _, tc := range []struct {
		name    string
		journal []Event
		state   string
		entered int64
		changed int64
	}{
		{"launched with a prompt, its session started", []Event{launch(1, true), start(2, newSource)}, Starting, 1, 0},
		{"then the prompt", []Event{launch(1, true), start(2, newSource), userPrompt(3, "t1")}, Working, 3, 3},
		{"then its turn's end", []Event{launch(1, true), start(2, newSource), userPrompt(3, "t1"), endOfTurn(4, "t1", false)}, Done, 4, 4},
		{"launched without a prompt", []Event{launch(1, false), start(2, newSource)}, Done, 2, 2},
		{"a launch from before the fact", []Event{seenEvent(1, SourceHQ, KindLaunched, 0, Facts{Start: "new"}), start(2, newSource)}, Done, 2, 2},
		{"a dialog of Claude's own before the prompt", []Event{launch(1, true), start(2, newSource), seenEvent(3, SourceScreen, KindOwnDialog, 2, Facts{Screen: "f1", Text: "Make auto mode your default permission mode?"})}, NeedsInput, 3, 3},
		{"the dialog answered, still before the prompt", []Event{launch(1, true), start(2, newSource), seenEvent(3, SourceScreen, KindOwnDialog, 2, Facts{Screen: "f1", Text: "Make auto mode your default permission mode?"}), seenEvent(4, SourceScreen, KindOwnDialogGone, 2, Facts{})}, Starting, 1, 4},
		{"started over after its first turn", []Event{launch(1, true), start(2, newSource), userPrompt(3, "t1"), endOfTurn(4, "t1", false), start(5, "clear")}, Done, 5, 4},
		{"restored with no prompt of its own", []Event{launch(1, true), start(2, newSource), userPrompt(3, "t1"), endOfTurn(4, "t1", false), seenEvent(5, SourceHQ, KindLaunched, 0, Facts{Start: "restore", Session: "s1"}), start(6, resumeSource)}, Done, 6, 6},
		{"relaunched by a sandbox restart", []Event{launch(1, true), start(2, newSource), userPrompt(3, "t1"), endOfTurn(4, "t1", false), seenEvent(5, SourceHQ, KindRelaunched, 0, Facts{Session: "s1"}), start(6, resumeSource)}, Done, 6, 6},
	} {
		r := Reduce(tc.journal, Reply{}, atSecond(9))
		if r.State != tc.state || r.Entered.Number != tc.entered || r.Changed.Number != tc.changed {
			t.Errorf("%s: %s entered %d changed %d, want %s entered %d changed %d", tc.name, r.State, r.Entered.Number, r.Changed.Number, tc.state, tc.entered, tc.changed)
		}
	}
	// It keeps what its session's start told: the session and the worktree.
	if r := Reduce([]Event{launch(1, true), start(2, newSource)}, Reply{}, atSecond(9)); r.Worktree != "/w/app" || !r.Since.Equal(atSecond(1)) {
		t.Errorf("starting from its launch: %+v", r)
	}
}

// An agent launched with a first prompt that it never reports (a command
// of Claude Code's own, a prompt Claude drops) stays starting until its
// next report, however long its screen stays at rest at the prompt box,
// and no completed turn is counted for it (#191). Its screen is looked at
// all the same, so a dialog of Claude's own in that prompt's way is seen.
func TestAFirstPromptThatNeverComesLeavesTheAgentStartingWithNoCompletedTurn(t *testing.T) {
	ready := screen(t, "start-ready")
	launch := seenEvent(1, SourceHQ, KindLaunched, 0, Facts{Start: "new", Prompted: true})
	start := hookEvent(2, CaseStart, KindSessionStart, Facts{Start: newSource, Session: "s1"})
	journal := []Event{launch, start}
	f := Fold{}.With(journal...)
	early := atSecond(2).Add(OverruleDelay)
	if !f.ScreenMayTell(early) || f.ScreenMayTell(atSecond(2).Add(OverruleDelay-time.Millisecond)) {
		t.Fatal("its screen may tell only once the overrule delay has run")
	}
	for _, at := range []time.Duration{0, RestDelay, time.Hour} {
		if seen, resting := seesWhenItMayTell(t, f, ready, early.Add(at)); len(seen) != 0 || resting {
			t.Fatalf("at rest %v later: %+v %v", at, seen, resting)
		}
	}
	r := Reduce(journal, Reply{}, atSecond(9).Add(24*time.Hour))
	if r.State != Starting || r.TurnEnded != 0 || r.Changed.Number != 0 || r.Entered.Number != 1 {
		t.Fatalf("a day later: %s turn ended %d changed %d entered %d", r.State, r.TurnEnded, r.Changed.Number, r.Entered.Number)
	}
	// A dialog of Claude's own in the prompt's way is seen there.
	seen, _ := seesWhenItMayTell(t, f, screen(t, "own-auto-mode"), early)
	if len(seen) != 1 || seen[0].Kind != KindOwnDialog {
		t.Fatalf("a dialog of Claude's own: %+v", seen)
	}
	// Its next report ends the wait, whatever it is: a turn end Claude
	// reports without the prompt reported before it is done.
	if r := Reduce(append(slices.Clone(journal), endOfTurn(3, "t1", false)), Reply{}, atSecond(9)); r.State != Done || r.Entered.Number != 3 {
		t.Fatalf("its next report: %s entered %d", r.State, r.Entered.Number)
	}
}

// firstPromptJournal is the journal of an agent launched with a first
// prompt whose new session has started and reported nothing since.
func firstPromptJournal() []Event {
	return []Event{
		seenEvent(1, SourceHQ, KindLaunched, 0, Facts{Start: "new", Prompted: true}),
		hookEvent(2, CaseStart, KindSessionStart, Facts{Start: newSource, Session: "s1"}),
	}
}

// tookSlashCommandScreens are the screens of Claude Code 2.1.329 that took the
// first prompt it was started with as a slash command of its own and started no
// turn for it (setup P, #220): /model NAME, and /effort and the picker of
// /model closed with Esc, right after and at rest.
var tookSlashCommandScreens = []string{
	"first-model-name-2329", "first-model-name-2329-rest", "first-model-name-2329-80", "first-model-name-2329-80-rest",
	"first-effort-2329-closed", "first-effort-2329-80-closed",
	"first-model-kept-2329", "first-model-kept-2329-80",
}

// Claude Code 2.1.329 reports nothing for a slash command of its own that
// starts no turn given as the first prompt (setup P: no event followed its
// session's start). Once the screen shows that slash command sent with its
// outcome below it, at the prompt box, the agent is done since the look saw
// it: no completed turn, no last message (#220).
func TestAFirstPromptClaudeTookAsASlashCommandOfItsOwnMakesTheAgentDone(t *testing.T) {
	for _, name := range tookSlashCommandScreens {
		journal := firstPromptJournal()
		f := Fold{}.With(journal...)
		at := atSecond(2).Add(OverruleDelay)
		seen, resting := seesWhenItMayTell(t, f, screen(t, name), at)
		if len(seen) != 1 || seen[0].Kind != KindTookSlashCommand || seen[0].Source != SourceScreen || !seen[0].Time.Equal(at) || resting {
			t.Errorf("%s: %+v %v", name, seen, resting)
			continue
		}
		seen[0].Number, seen[0].Basis = 3, 2
		r := Reduce(append(journal, seen[0]), Reply{}, atSecond(9))
		if r.State != Done || r.TurnEnded != 0 || r.Last != "" || r.Entered.Number != 3 || r.Changed.Number != 3 || !r.Since.Equal(at) || !slices.Contains(r.RestsOn, 3) {
			t.Errorf("%s: %s turn ended %d last %q entered %d changed %d since %v rests on %v", name, r.State, r.TurnEnded, r.Last, r.Entered.Number, r.Changed.Number, r.Since, r.RestsOn)
		}
	}
}

// A first prompt that is only late never makes the agent done (#220): with
// Claude Code 2.1.329 the prompt is drawn sent at once with the spinner
// below it while a SessionStart hook of 4 s runs, then the spinner alone
// once Claude reported taking it (setup P, (a)), and so is /probe, a slash
// command of the repository's own that starts a turn; a slash command of
// Claude's own shows only its header right after the start ((b), (c)); a
// session given no prompt shows its box with a placeholder ((d)); the slider
// of /effort is no dialog hq reads. Each leaves the agent starting, at rest
// or not, 2 s, an hour and a day later, and its prompt reported then makes
// it working.
func TestAFirstPromptThatIsOnlyLateLeavesTheAgentStarting(t *testing.T) {
	for _, name := range []string{
		"first-hook-2329", "first-hook-2329-80", "first-hook-taken-2329", "first-hook-taken-2329-80",
		"first-probe-2329", "first-probe-taken-2329",
		"first-model-name-2329-start", "first-model-name-2329-80-start",
		"first-effort-2329-open", "first-effort-2329-80-open",
		"first-none-2329", "first-none-2329-80",
	} {
		journal := firstPromptJournal()
		f := Fold{}.With(journal...)
		at := atSecond(2).Add(OverruleDelay)
		for _, later := range []time.Duration{0, RestDelay, time.Hour, 24 * time.Hour} {
			if seen, resting := seesWhenItMayTell(t, f, screen(t, name), at.Add(later)); len(seen) != 0 || resting {
				t.Errorf("%s, %v later: %+v %v", name, later, seen, resting)
			}
		}
		if r := Reduce(journal, Reply{}, at.Add(24*time.Hour)); r.State != Starting || r.Entered.Number != 1 || r.TurnEnded != 0 {
			t.Errorf("%s, a day later: %s entered %d turn ended %d", name, r.State, r.Entered.Number, r.TurnEnded)
		}
		if r := Reduce(append(journal, userPrompt(3, "t1")), Reply{}, atSecond(9)); r.State != Working || r.Entered.Number != 3 {
			t.Errorf("%s, its prompt reported: %s entered %d", name, r.State, r.Entered.Number)
		}
	}
}

// The picker of /model as the first prompt (Claude Code 2.1.329, setup P)
// is a dialog of Claude's own in that prompt's way: needs input while it
// is open, then, closed with Esc and no turn started, the agent is done by
// TookSlashCommand, in the same look that sees the dialog gone (#220).
func TestTheModelPickerAsTheFirstPromptNeedsInputThenTheAgentIsDone(t *testing.T) {
	for _, size := range []string{"", "-80"} {
		journal := firstPromptJournal()
		f := Fold{}.With(journal...)
		at := atSecond(3)
		opened, _ := seesWhenItMayTell(t, f, screen(t, "own-first-model-2329"+size), at)
		if len(opened) != 1 || opened[0].Kind != KindOwnDialog || opened[0].Facts.Text != "Select model" {
			t.Fatalf("%s opened: %+v", size, opened)
		}
		opened[0].Number, opened[0].Basis = 3, 2
		journal = append(journal, opened[0])
		if r := Reduce(journal, Reply{}, atSecond(9)); r.State != NeedsInput || !r.Dialog.Open || r.Dialog.Whose != DialogOwn {
			t.Fatalf("%s opened: %s %+v", size, r.State, r.Dialog)
		}
		f = Fold{}.With(journal...)
		closed, _ := seesWhenItMayTell(t, f, screen(t, "first-model-kept-2329"+size), atSecond(4))
		if len(closed) != 2 || closed[0].Kind != KindOwnDialogGone || closed[1].Kind != KindTookSlashCommand {
			t.Fatalf("%s closed: %+v", size, closed)
		}
		for i := range closed {
			closed[i].Number, closed[i].Basis = int64(4+i), 2
		}
		journal = append(journal, closed...)
		if r := Reduce(journal, Reply{}, atSecond(9)); r.State != Done || r.Dialog.Open || r.TurnEnded != 0 || r.Entered.Number != 5 || r.Last != "" {
			t.Fatalf("%s closed: %s dialog %+v turn ended %d entered %d last %q", size, r.State, r.Dialog, r.TurnEnded, r.Entered.Number, r.Last)
		}
	}
}

// Of every screen in testdata only those of a slash command of Claude's own
// taken as the first prompt show the sign TookSlashCommand reads (setup P,
// #220): a slash command after a turn shows other prompts sent above it.
// Every other screen at the prompt box at rest, a session given no prompt
// (d) among them, changes nothing for an agent waiting for its first prompt,
// at once, RestDelay or an hour later.
func TestOnlyASlashCommandOfClaudesOwnAsTheFirstPromptShowsTheSign(t *testing.T) {
	names, _ := filepath.Glob(filepath.Join("testdata", "screen-*.txt"))
	if len(names) < 20 {
		t.Fatalf("only %d screens", len(names))
	}
	var signs []string
	idle := 0
	f := Fold{}.With(firstPromptJournal()...)
	for _, name := range names {
		b, _ := os.ReadFile(name)
		s, base := string(b), strings.TrimSuffix(strings.TrimPrefix(filepath.Base(name), "screen-"), ".txt")
		if TookSlashCommand(s) {
			signs = append(signs, base)
			continue
		}
		if !Idle(s) {
			continue
		}
		idle++
		for _, later := range []time.Duration{0, RestDelay, time.Hour} {
			if seen, resting := seesWhenItMayTell(t, f, s, atSecond(3).Add(later)); len(seen) != 0 || resting {
				t.Errorf("%s, %v later: %+v %v", base, later, seen, resting)
			}
		}
	}
	want := slices.Sorted(slices.Values(tookSlashCommandScreens))
	if slices.Sort(signs); !slices.Equal(signs, want) {
		t.Errorf("the sign on %v, want %v", signs, want)
	}
	if idle < 20 {
		t.Errorf("only %d screens at rest", idle)
	}
}

// Once the agent is done by TookSlashCommand, the screens that told it change
// nothing more, and it is as any done agent: a prompt reported next makes
// it working and ends as a completed turn, a dialog of Claude's own opened
// next needs input (#220). The decision stands only for the session's
// start it was seen at: a new session's start awaits its first prompt
// anew only after a launch with one.
func TestAnAgentDoneByTookSlashCommandIsAsAnyDoneAgent(t *testing.T) {
	// tookSlash is what the look sees of a screen of a slash command taken,
	// as event n.
	tookSlash := func(n int64) Event {
		t.Helper()
		seen, _ := seesWhenItMayTell(t, Fold{}.With(firstPromptJournal()...), screen(t, "first-model-name-2329"), atSecond(3))
		if len(seen) != 1 || seen[0].Kind != KindTookSlashCommand {
			t.Fatalf("the slash command taken: %+v", seen)
		}
		seen[0].Number, seen[0].Basis = n, 2
		return seen[0]
	}
	journal := append(firstPromptJournal(), tookSlash(3))
	f := Fold{}.With(journal...)
	for _, name := range tookSlashCommandScreens {
		if seen, _ := seesWhenItMayTell(t, f, screen(t, name), atSecond(4)); len(seen) != 0 {
			t.Errorf("%s again: %+v", name, seen)
		}
	}
	// The same decision appended twice, by two hq processes, is one.
	if r := Reduce(append(slices.Clone(journal), tookSlash(4)), Reply{}, atSecond(9)); r.Entered.Number != 3 || r.Changed.Number != 3 {
		t.Errorf("twice: entered %d changed %d", r.Entered.Number, r.Changed.Number)
	}
	turn := append(slices.Clone(journal), userPrompt(4, "t1"))
	if r := Reduce(turn, Reply{}, atSecond(9)); r.State != Working || r.Entered.Number != 4 {
		t.Errorf("a prompt next: %s entered %d", r.State, r.Entered.Number)
	}
	turn = append(turn, endOfTurn(5, "t1", false))
	if r := Reduce(turn, Reply{}, atSecond(9)); r.State != Done || r.TurnEnded != 5 {
		t.Errorf("its turn's end: %s turn ended %d", r.State, r.TurnEnded)
	}
	seen, _ := seesWhenItMayTell(t, f, screen(t, "own-first-model-2329"), atSecond(4))
	if len(seen) != 1 || seen[0].Kind != KindOwnDialog {
		t.Fatalf("a dialog next: %+v", seen)
	}
	seen[0].Number, seen[0].Basis = 4, 2
	if r := Reduce(append(slices.Clone(journal), seen[0]), Reply{}, atSecond(9)); r.State != NeedsInput {
		t.Errorf("a dialog next: %s", r.State)
	}
	// The session started over (/clear) is done at its start, the decision
	// of the session before it standing for nothing.
	over := append(slices.Clone(journal), hookEvent(4, CaseStart, KindSessionStart, Facts{Start: "clear", Session: "s2"}))
	if r := Reduce(over, Reply{}, atSecond(9)); r.State != Done || r.Entered.Number != 4 {
		t.Errorf("started over: %s entered %d", r.State, r.Entered.Number)
	}
	// A relaunch with a first prompt waits for it anew.
	again := append(slices.Clone(journal), seenEvent(4, SourceHQ, KindLaunched, 0, Facts{Start: "new", Prompted: true}), hookEvent(5, CaseStart, KindSessionStart, Facts{Start: newSource, Session: "s2"}))
	if r := Reduce(again, Reply{}, atSecond(9)); r.State != Starting || r.Entered.Number != 4 {
		t.Errorf("launched again: %s entered %d", r.State, r.Entered.Number)
	}
}

// An agent whose turn ended while its background subagents run is working
// (spec, Background work), and a look at its pane appends nothing for
// Claude's agents list with the cursor on it anywhere or for a subagent's
// view open there: no dialog of Claude's own, so nothing for hq wait to
// return (#225). The same holds for an agent done or with a question,
// whose screen is looked at for such a dialog too. Each screen is met as the
// look meets it: only at a moment its screen may tell (seesWhenItMayTell).
func TestALookAtClaudesAgentsListOrASubagentsViewLeavesTheAgentAsItWas(t *testing.T) {
	for _, tc := range []struct {
		name    string
		journal []Event
		state   string
	}{
		{"working on background work", []Event{userPrompt(1, "t1"), endOfTurn(2, "t1", false, subagentRunning("a1"), subagentRunning("a2"))}, Working},
		{"done", []Event{userPrompt(1, "t1"), endOfTurn(2, "t1", false)}, Done},
		{"with a question", []Event{userPrompt(1, "t1"), endOfTurn(2, "t1", true)}, Question},
	} {
		f := Fold{}.With(tc.journal...)
		before := Reduce(tc.journal, Reply{}, atSecond(9))
		if before.State != tc.state || before.Background != (tc.state == Working) {
			t.Fatalf("%s: %s, background %v", tc.name, before.State, before.Background)
		}
		looked := slices.Clone(tc.journal)
		for name, screen := range agentsScreens(t) {
			for _, at := range []time.Duration{0, RestDelay, time.Hour} {
				seen, resting := seesWhenItMayTell(t, f, screen, atSecond(3).Add(at))
				if len(seen) != 0 || resting {
					t.Errorf("%s, %s %v later: %+v %v", tc.name, name, at, seen, resting)
				}
				looked = append(looked, seen...)
			}
		}
		if after := Reduce(looked, Reply{}, atSecond(9)); after.Dialog.Open || after.Changed != before.Changed || after.State != tc.state {
			t.Errorf("%s: %+v", tc.name, after)
		}
	}
	// The picker of /model opened over the list is a dialog all the same.
	f := Fold{}.With(userPrompt(1, "t1"), endOfTurn(2, "t1", false, subagentRunning("a1"), subagentRunning("a2")))
	seen, _ := seesWhenItMayTell(t, f, readScreen(t, "screen-own-model-agents.txt"), atSecond(3))
	if len(seen) != 1 || seen[0].Kind != KindOwnDialog || seen[0].Facts.Text != "Select model" {
		t.Fatalf("the picker: %+v", seen)
	}
	opened := Fold{}.With(userPrompt(1, "t1"), endOfTurn(2, "t1", false, subagentRunning("a1"), subagentRunning("a2")), seenEvent(3, SourceScreen, KindOwnDialog, 2, seen[0].Facts))
	if gone, _ := seesWhenItMayTell(t, opened, readScreen(t, "screen-agents-model-closed.txt"), atSecond(4)); len(gone) != 1 || gone[0].Kind != KindOwnDialogGone {
		t.Fatalf("the picker closed: %+v", gone)
	}
}
