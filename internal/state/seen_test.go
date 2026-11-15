package state

import (
	"reflect"
	"slices"
	"testing"
	"time"
)

// seenEvent is event n of hq's own from source, of kind, based on the
// hook's event basis, with facts.
func seenEvent(n int64, source, kind string, basis int64, f Facts) Event {
	return Event{Number: n, Source: source, Kind: kind, Time: atSecond(n), Basis: basis, Facts: f}
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
	if r.State != Done || r.Last != RewoundLast || !r.Since.Equal(atSecond(2)) || r.Entered != (Mark{3, atSecond(3)}) || r.TurnEnded != 3 || !slices.Equal(r.RestsOn, []int64{1, 2, 3}) {
		t.Errorf("rewound: %+v", r)
	}

	died := []Event{
		userPrompt(1, "t1"),
		endOfTurn(2, "t1", false),
		seenEvent(3, SourceTmux, KindPaneDied, 2, Facts{From: atSecond(2).Add(500 * time.Millisecond)}),
	}
	r = Reduce(died, Reply{}, atSecond(9))
	if r.State != Ended || !r.Since.Equal(atSecond(2).Add(500*time.Millisecond)) || r.Entered != (Mark{3, atSecond(3)}) || r.Last != EndedLast {
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
	if r := Reduce([]Event{launched, notRunning(2, atSecond(2))}, Reply{}, atSecond(9)); r.State != Starting || !r.Since.Equal(atSecond(1)) || r.Entered.Number != 1 {
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
		{2, NeedsInput, DialogVerdict{Open: true, Whose: DialogHooks, Opened: Mark{2, atSecond(2)}, RestsOn: []int64{2}}, atSecond(2), "Bash: ls", 2, 2},
		{3, Working, DialogVerdict{Whose: DialogHooks, Opened: Mark{2, atSecond(2)}, Closed: Mark{3, atSecond(3)}, RestsOn: []int64{2, 3}}, atSecond(2), "", 3, 3},
		{4, Done, DialogVerdict{Whose: DialogHooks, Opened: Mark{2, atSecond(2)}, Closed: Mark{3, atSecond(3)}, RestsOn: []int64{2, 3}}, atSecond(4), "", 4, 4},
		{6, NeedsInput, DialogVerdict{Open: true, Whose: DialogOwn, Opened: Mark{5, atSecond(5)}, RestsOn: []int64{5}}, atSecond(5), "Change effort level?", 5, 5},
		{7, Done, DialogVerdict{Whose: DialogOwn, Opened: Mark{5, atSecond(5)}, Closed: Mark{7, atSecond(7)}, RestsOn: []int64{5, 7}}, atSecond(4), "", 7, 4},
		{8, NeedsInput, DialogVerdict{Open: true, Whose: DialogSubagent, Opened: Mark{8, atSecond(8)}, RestsOn: []int64{8}}, atSecond(8), "Read: a", 8, 8},
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
	journal = append(journal, seenEvent(5, SourceHQ, KindWithdrawn, 2, Facts{At: KindRelaunched}))
	if r := Reduce(journal, Reply{}, atSecond(9)); r.State != Ended || !r.Since.Equal(atSecond(3)) {
		t.Errorf("withdrawn: %+v", r)
	}
	// Withdrawn of anything else, or with no launch to withdraw, changes
	// nothing.
	for _, w := range []Event{seenEvent(6, SourceHQ, KindWithdrawn, 2, Facts{At: KindAnswer}), seenEvent(6, SourceHQ, KindWithdrawn, 2, Facts{At: KindRelaunched})} {
		if r := Reduce(append(slices.Clone(journal), w), Reply{}, atSecond(9)); r.State != Ended || !r.Since.Equal(atSecond(3)) {
			t.Errorf("%+v: %+v", w.Facts, r)
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

// The turn end of the last completed turn may be one the screen showed:
// a turn the user ended, rewound, or one Claude owed that did not come.
func TestATurnEndTheScreenShowedEndsTheLastCompletedTurn(t *testing.T) {
	journal := []Event{
		userPrompt(1, "t1"),
		seenEvent(2, SourceScreen, KindTurnEnded, 1, Facts{Text: "Interrupted"}),
		seenEvent(3, SourceScreen, KindTurnEnded, 1, Facts{Text: "User declined to answer questions"}), // a later one for the same report
		userPrompt(4, "t2"),
	}
	if r := reducedAt(journal, 3); r.State != Done || r.TurnEnded != 2 || r.Last != "Interrupted" || !r.Since.Equal(atSecond(2)) {
		t.Errorf("ended: %+v", r)
	}
	if r := reducedAt(journal, 4); r.State != Working || r.TurnEnded != 2 {
		t.Errorf("the next turn: %+v", r)
	}
}
