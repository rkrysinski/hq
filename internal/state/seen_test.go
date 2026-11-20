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
		if seen, resting := f.Sees(ready, early.Add(at)); len(seen) != 0 || resting {
			t.Fatalf("at rest %v later: %+v %v", at, seen, resting)
		}
	}
	r := Reduce(journal, Reply{}, atSecond(9).Add(24*time.Hour))
	if r.State != Starting || r.TurnEnded != 0 || r.Changed.Number != 0 || r.Entered.Number != 1 {
		t.Fatalf("a day later: %s turn ended %d changed %d entered %d", r.State, r.TurnEnded, r.Changed.Number, r.Entered.Number)
	}
	// A dialog of Claude's own in the prompt's way is seen there.
	seen, _ := f.Sees(screen(t, "own-auto-mode"), early)
	if len(seen) != 1 || seen[0].Kind != KindOwnDialog {
		t.Fatalf("a dialog of Claude's own: %+v", seen)
	}
	// Its next report ends the wait, whatever it is: a turn end Claude
	// reports without the prompt reported before it is done.
	if r := Reduce(append(slices.Clone(journal), endOfTurn(3, "t1", false)), Reply{}, atSecond(9)); r.State != Done || r.Entered.Number != 3 {
		t.Fatalf("its next report: %s entered %d", r.State, r.Entered.Number)
	}
}

// An agent whose turn ended while its background subagents run is working
// (spec, Background work), and a look at its pane appends nothing for the
// Claude's agents list with the cursor on it anywhere or for a subagent's
// view open
// there: no dialog of Claude's own, so nothing for hq wait to return
// (#225). The same holds for an agent done or with a question, whose
// screen is looked at for such a dialog too. Each screen is met as the
// look meets it: only at a moment its screen may tell (ScreenMayTell).
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
				if !f.ScreenMayTell(atSecond(3).Add(at)) {
					t.Fatalf("%s, %v later: its screen may not tell", tc.name, at)
				}
				seen, resting := f.Sees(screen, atSecond(3).Add(at))
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
	if !f.ScreenMayTell(atSecond(3)) {
		t.Fatal("the picker: its screen may not tell")
	}
	seen, _ := f.Sees(readScreen(t, "screen-own-model-agents.txt"), atSecond(3))
	if len(seen) != 1 || seen[0].Kind != KindOwnDialog || seen[0].Facts.Text != "Select model" {
		t.Fatalf("the picker: %+v", seen)
	}
	opened := Fold{}.With(userPrompt(1, "t1"), endOfTurn(2, "t1", false, subagentRunning("a1"), subagentRunning("a2")), seenEvent(3, SourceScreen, KindOwnDialog, 2, seen[0].Facts))
	if !opened.ScreenMayTell(atSecond(4)) {
		t.Fatal("the picker closed: its screen may not tell")
	}
	if gone, _ := opened.Sees(readScreen(t, "screen-agents-model-closed.txt"), atSecond(4)); len(gone) != 1 || gone[0].Kind != KindOwnDialogGone {
		t.Fatalf("the picker closed: %+v", gone)
	}
}
