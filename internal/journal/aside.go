package journal

import (
	"strconv"

	"github.com/rkrysinski/hq/internal/state"
)

// Basis is the number of the last event of the hook among events, 0 when
// there is none: what an event of hq's own a process appends now is based
// on (state.Event.Basis).
func Basis(events []state.Event) int64 {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Source == state.SourceHook {
			return events[i].Number
		}
	}
	return 0
}

// Aside is why e would be set aside if it were appended after events, ""
// when it stands (spec #165, the glossary's event). Evidence of the screen
// or of sbx is read against the hook's reports, so it is overtaken when
// events hold a report of the hook later than its basis. Evidence of the
// screen, tmux or sbx is a duplicate when the same evidence stands in
// events already: of hq processes that see the same thing at once, the
// first to append it wins. What hq does itself is never set aside.
func Aside(events []state.Event, e state.Event) string {
	if (e.Source == state.SourceScreen || e.Source == state.SourceSbx) && Basis(events) > e.Basis {
		return state.AsideOvertaken
	}
	if Holds(events, e) {
		return state.AsideDuplicate
	}
	return ""
}

// Holds reports whether events hold the evidence e already, so that a look
// appends it only when they do not: the latest event standing of the same
// kind of record (a turn's end, a screen at rest, a dialog of Claude's own,
// ...) is the same evidence, since the agent was last launched. A screen
// that changes and comes back, or a dialog that closes and opens again, is
// new evidence; a launch or a relaunch starts every kind over.
func Holds(events []state.Event, e state.Event) bool {
	slot, key := identity(e)
	if slot == "" {
		return false
	}
	for i := len(events) - 1; i >= 0; i-- {
		x := events[i]
		switch {
		case x.Aside != "":
		case x.Source == state.SourceHQ && (x.Kind == state.KindLaunched || x.Kind == state.KindRelaunched):
			return false
		default:
			if s, k := identity(x); s == slot {
				return k == key
			}
		}
	}
	return false
}

// identity is the kind of record an event of evidence is, slot, which a
// later one of the same slot replaces, and what tells it from another of
// that slot, key; slot is "" for any other event.
func identity(e state.Event) (slot, key string) {
	basis := strconv.FormatInt(e.Basis, 10)
	switch e.Source {
	case state.SourceScreen:
		switch e.Kind {
		case state.KindTurnEnded, state.KindRewound, state.KindTurnNotTaken:
			return "turn end", basis
		case state.KindAtRest, state.KindToolCut:
			return "at rest", basis + " " + e.Facts.Screen
		case state.KindOwnDialog:
			return "own dialog", basis + " " + e.Facts.Screen
		case state.KindOwnDialogGone:
			return "own dialog", basis + " -"
		case state.KindAtWork:
			return "at work", basis
		case state.KindTookQueued:
			return "took queued", basis
		case state.KindWokeUp:
			return "woke up", basis
		}
	case state.SourceTmux:
		if e.Kind == state.KindPaneDied {
			return "pane died", strconv.FormatInt(e.Facts.From.UnixNano(), 10)
		}
	case state.SourceSbx:
		if e.Kind == state.KindNotRunning {
			return "not running", basis
		}
	}
	return "", ""
}
