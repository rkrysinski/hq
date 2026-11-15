package journal

import (
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/state"
)

func ev(n int64, source, kind string, basis int64) state.Event {
	return state.Event{Number: n, Source: source, Kind: kind, Basis: basis}
}

func TestTheBasisIsTheLastReportOfTheHook(t *testing.T) {
	for _, tc := range []struct {
		events []state.Event
		want   int64
	}{
		{nil, 0},
		{[]state.Event{ev(1, state.SourceHQ, state.KindLaunched, 0)}, 0},
		{[]state.Event{ev(1, state.SourceHook, "SessionStart", 0), ev(2, state.SourceHook, "UserPromptSubmit", 0), ev(3, state.SourceScreen, state.KindAtRest, 2)}, 2},
	} {
		if got := Basis(tc.events); got != tc.want {
			t.Errorf("%+v: %d, want %d", tc.events, got, tc.want)
		}
	}
}

// Each kind of evidence is told from another of its kind by what it saw
// and the report it was based on; the same again is a duplicate, other
// evidence of the same kind replaces it.
func TestEvidenceIsTheSameByItsKindAndWhatItSaw(t *testing.T) {
	at := time.Unix(1000, 0)
	screen := func(kind, fp string, basis int64) state.Event {
		e := ev(0, state.SourceScreen, kind, basis)
		e.Facts.Screen = fp
		return e
	}
	died := func(when time.Time) state.Event {
		e := ev(0, state.SourceTmux, state.KindPaneDied, 1)
		e.Facts.From = when
		return e
	}
	for _, tc := range []struct {
		name        string
		first, then state.Event
		same        bool
	}{
		{"a turn ended, then rewound", screen(state.KindTurnEnded, "", 1), screen(state.KindRewound, "", 1), true},
		{"a turn not taken after another report", screen(state.KindTurnNotTaken, "", 1), screen(state.KindTurnNotTaken, "", 2), false},
		{"the same screen at rest", screen(state.KindAtRest, "f1", 1), screen(state.KindToolCut, "f1", 1), true},
		{"another screen at rest", screen(state.KindAtRest, "f1", 1), screen(state.KindAtRest, "f2", 1), false},
		{"the same dialog", screen(state.KindOwnDialog, "d1", 1), screen(state.KindOwnDialog, "d1", 1), true},
		{"a dialog gone", screen(state.KindOwnDialog, "d1", 1), screen(state.KindOwnDialogGone, "", 1), false},
		{"a dialog gone twice", screen(state.KindOwnDialogGone, "", 1), screen(state.KindOwnDialogGone, "", 1), true},
		{"at work", screen(state.KindAtWork, "", 1), screen(state.KindAtWork, "", 1), true},
		{"a queued prompt taken", screen(state.KindTookQueued, "", 1), screen(state.KindTookQueued, "", 1), true},
		{"a wake-up", screen(state.KindWokeUp, "", 1), screen(state.KindWokeUp, "", 1), true},
		{"the pane's death", died(at), died(at), true},
		{"another pane's death", died(at), died(at.Add(time.Second)), false},
		{"the sandbox not running", ev(0, state.SourceSbx, state.KindNotRunning, 1), ev(0, state.SourceSbx, state.KindNotRunning, 1), true},
		{"an unknown kind of the screen", screen("Other", "", 1), screen("Other", "", 1), false},
		{"an unknown kind of tmux", ev(0, state.SourceTmux, "Other", 1), ev(0, state.SourceTmux, "Other", 1), false},
		{"an unknown kind of sbx", ev(0, state.SourceSbx, "Other", 1), ev(0, state.SourceSbx, "Other", 1), false},
		{"a delivery hq typed", ev(0, state.SourceHQ, state.KindDelivery, 1), ev(0, state.SourceHQ, state.KindDelivery, 1), false},
	} {
		events := []state.Event{ev(1, state.SourceHook, "UserPromptSubmit", 0), tc.first}
		events[1].Number = 2
		if got := Holds(events, tc.then); got != tc.same {
			t.Errorf("%s: holds %v", tc.name, got)
		}
	}
}

// What was set aside holds nothing, and a launch or a relaunch starts every
// kind of evidence over.
func TestHoldsLooksPastWhatWasSetAsideAndNotPastALaunch(t *testing.T) {
	rest := ev(0, state.SourceScreen, state.KindAtRest, 1)
	aside := rest
	aside.Aside = state.AsideOvertaken
	other := rest
	other.Facts.Screen = "f2"
	otherAside := other
	otherAside.Aside = state.AsideDuplicate
	for _, tc := range []struct {
		name   string
		events []state.Event
		want   bool
	}{
		{"only set aside", []state.Event{aside}, false},
		{"another one set aside after it", []state.Event{rest, otherAside}, true},
		{"before a launch", []state.Event{rest, ev(0, state.SourceHQ, state.KindLaunched, 1)}, false},
		{"before a relaunch", []state.Event{rest, ev(0, state.SourceHQ, state.KindRelaunched, 1)}, false},
		{"before another event of hq", []state.Event{rest, ev(0, state.SourceHQ, state.KindTakenDown, 1)}, true},
	} {
		if got := Holds(tc.events, rest); got != tc.want {
			t.Errorf("%s: %v", tc.name, got)
		}
	}
}

// Evidence of the screen and of sbx is overtaken by a report later than
// its basis, not by the report it was based on; the pane's death and what
// hq does never are.
func TestEvidenceIsOvertakenOnlyByALaterReport(t *testing.T) {
	events := []state.Event{ev(1, state.SourceHook, "UserPromptSubmit", 0), ev(2, state.SourceHook, "PostToolUse", 0)}
	for _, tc := range []struct {
		e    state.Event
		want string
	}{
		{ev(0, state.SourceScreen, state.KindTurnEnded, 2), ""},
		{ev(0, state.SourceScreen, state.KindTurnEnded, 1), state.AsideOvertaken},
		{ev(0, state.SourceSbx, state.KindNotRunning, 1), state.AsideOvertaken},
		{ev(0, state.SourceTmux, state.KindPaneDied, 1), ""},
		{ev(0, state.SourceHQ, state.KindDelivery, 1), ""},
	} {
		if got := Aside(events, tc.e); got != tc.want {
			t.Errorf("%s %s on %d: %q, want %q", tc.e.Source, tc.e.Kind, tc.e.Basis, got, tc.want)
		}
	}
	if got := Aside(append(events, ev(3, state.SourceScreen, state.KindAtWork, 2)), ev(0, state.SourceScreen, state.KindAtWork, 2)); got != state.AsideDuplicate {
		t.Errorf("the same again: %q", got)
	}
}
