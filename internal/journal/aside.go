package journal

import (
	"maps"
	"strconv"

	"github.com/rkrysinski/hq/internal/state"
)

// Standing is what a journal holds that decides whether an event of hq's
// own appended after it stands or is set aside (spec #165, the glossary's
// event): the number of the hook's last event, Basis, which an event of
// hq's own appended now is based on (state.Event.Basis), and the evidence
// that stands. It is kept event by event (Follow), so a long-running
// program, or an append that reads on from the one before, need not keep
// the events themselves.
type Standing struct {
	Basis    int64    `json:"basis"`
	Evidence Evidence `json:"evidence"`
}

// NewStanding is what an empty journal holds.
func NewStanding() Standing { return Standing{Evidence: Evidence{}} }

// StandingOf is what the journal events hold.
func StandingOf(events []state.Event) Standing {
	s := NewStanding()
	s.Follow(events...)
	return s
}

// Follow follows events of the journal, in its order.
func (s *Standing) Follow(events ...state.Event) {
	for _, e := range events {
		s.Evidence.Add(e)
		if e.Source == state.SourceHook {
			s.Basis = e.Number
		}
	}
}

// Aside is why e would be set aside if it were appended after the journal,
// "" when it stands. Evidence of the screen or of sbx is read against the
// hook's reports, so it is overtaken when the journal holds a report of
// the hook later than its basis. Evidence of the screen, tmux or sbx is a
// duplicate when the same evidence stands already: of hq processes that
// see the same thing at once, the first to append it wins. What hq does
// itself is never set aside.
func (s Standing) Aside(e state.Event) string {
	if (e.Source == state.SourceScreen || e.Source == state.SourceSbx) && s.Basis > e.Basis {
		return state.AsideOvertaken
	}
	if s.Evidence.Holds(e) {
		return state.AsideDuplicate
	}
	return ""
}

// SetAside is events, to be appended after the journal, each marked with
// why it is set aside, if it is (Aside): against the journal and the
// events before it. s stays as it was.
func (s Standing) SetAside(events []state.Event) []state.Event {
	s.Evidence = maps.Clone(s.Evidence)
	out := make([]state.Event, 0, len(events))
	for _, e := range events {
		e.Aside = s.Aside(e)
		out = append(out, e)
		s.Follow(e)
	}
	return out
}

// Basis is the number of the last event of the hook among events, 0 when
// there is none (Standing.Basis).
func Basis(events []state.Event) int64 {
	return latest(events, func(e state.Event) bool { return e.Source == state.SourceHook })
}

// Evidence is the evidence a journal holds (GLOSSARY.md): for each kind of
// evidence (a turn's end, a screen at rest, a dialog of Claude's own,
// ...), what tells the latest event of that kind that stands from another
// (identity). The same evidence is appended once, since the agent was last
// launched: a screen that changes and comes back, or a dialog that closes
// and opens again, is new evidence; a launch or a relaunch starts every
// kind over, and so does Claude taking a queued prompt, which hands the
// hook's report over to it with no event of the hook (#88).
type Evidence map[string]string

// Add follows event e of the journal.
func (ev Evidence) Add(e state.Event) {
	switch {
	case e.Aside != "":
	case e.Source == state.SourceHQ && (e.Kind == state.KindLaunched || e.Kind == state.KindRelaunched):
		clear(ev)
	default:
		kind, key := identity(e)
		if e.Kind == state.KindTookQueued && e.Source == state.SourceScreen {
			clear(ev)
		}
		if kind != "" {
			ev[kind] = key
		}
	}
}

// Holds reports whether the journal Add followed holds the evidence e
// already.
func (ev Evidence) Holds(e state.Event) bool {
	kind, key := identity(e)
	got, ok := ev[kind]
	return kind != "" && ok && got == key
}

// identity is the kind of evidence an event is, kind, which a later one of
// the same kind replaces, and what tells it from another of that kind,
// key; kind is "" for an event that is no evidence. The kinds of hq's own
// events are unique across their sources, so the event's kind alone tells
// it, once it is not the hook's.
func identity(e state.Event) (kind, key string) {
	if e.Source == state.SourceHook || e.Source == state.SourceHQ {
		return "", ""
	}
	basis := strconv.FormatInt(e.Basis, 10)
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
	case state.KindPaneDied:
		return "pane died", strconv.FormatInt(e.Facts.From.UnixNano(), 10)
	case state.KindNotRunning:
		return "not running", basis
	}
	return "", ""
}
