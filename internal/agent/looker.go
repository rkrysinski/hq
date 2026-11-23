package agent

import (
	"errors"
	"slices"
	"time"

	"github.com/rkrysinski/hq/internal/sbx"
	"github.com/rkrysinski/hq/internal/tmux"
)

// A Looker takes the looks of the commands that take one and return (hq
// ls, hq read, hq explain, hq send, hq answer, hq update, the chords and
// docking as the dashboard is entered; design §3.8): the whole protocol of
// a look, in the variant of the command, so that no command puts it
// together itself. Its dependencies are those the command tests already
// fake: tmux, the journals' store, sbx's answer and the clock.
type Looker struct {
	// Windows lists the home windows, tmux asked now.
	Windows func() ([]tmux.Window, error)
	// Sources are where the look reads and appends.
	Sources Sources
	// Sandboxes asks sbx which sandboxes there are, within a time limit.
	Sandboxes func() ([]sbx.Sandbox, error)
	Now       func() time.Time
	Sleep     func(time.Duration)
	// Folds keeps the journals folded for this process (Look).
	Folds *Folds
}

// A Variant is how a command's look differs from another's: whether it
// asks sbx and what it makes of an answer sbx does not give, and whether a
// working agent's screen at rest gets the second look RestDelay later.
// The commands take one of the variants below, each what the command did
// before the Looker (sbx: design §3.6, *The oldest sbx*).
type Variant struct {
	sbx   sbxMode
	again bool
}

// sbxMode is whether a look asks sbx, and what it makes of an answer sbx
// does not give.
type sbxMode int

const (
	// sbxAsked: an sbx too old for hq fails the look; one that fails or
	// does not answer in time gives no answer, which ends nobody.
	sbxAsked sbxMode = iota
	// sbxTolerated: an sbx too old, failing or slow gives no answer.
	sbxTolerated
	// sbxRequired: an sbx too old, failing or slow fails the look.
	sbxRequired
	// sbxNotAsked: sbx is not asked; the look reads only what tmux, the
	// journals and the screens show.
	sbxNotAsked
)

var (
	// Shown is the look of hq ls and hq read: sbx asked, and the second
	// look at a screen at rest, so a single command shows a turn's end
	// at once, as the list does.
	Shown = Variant{sbx: sbxAsked, again: true}
	// Explained is the look of hq explain, which explains the journal on
	// the host: sbx tolerated, and the second look.
	Explained = Variant{sbx: sbxTolerated, again: true}
	// Acting is the look of hq send and hq answer at the agent they act
	// on, and each look of their waits: sbx asked, one look.
	Acting = Variant{sbx: sbxAsked}
	// Updating is the look of hq update across generations, which has to
	// know every agent, lost ones included: sbx required, one look.
	Updating = Variant{sbx: sbxRequired}
	// Docking is the look of the chords and of docking as the dashboard
	// is entered: sbx not asked, one look.
	Docking = Variant{sbx: sbxNotAsked}
)

// SbxError is sbx failing a look: too old for hq, or in Updating any
// failure; Err is sbx's own error.
type SbxError struct{ Err error }

func (e *SbxError) Error() string { return e.Err.Error() }

func (e *SbxError) Unwrap() error { return e.Err }

// TmuxError is tmux failing to list the home windows for a look; Err is
// tmux's own error.
type TmuxError struct{ Err error }

func (e *TmuxError) Error() string { return e.Err.Error() }

func (e *TmuxError) Unwrap() error { return e.Err }

// NotFoundError refuses a name no agent of the look has.
type NotFoundError struct{ Name string }

func (e *NotFoundError) Error() string { return "no agent '" + e.Name + "' (see hq ls)" }

// OutdatedError refuses an outdated agent (Agent.Outdated): every command
// but those that open or kill it.
type OutdatedError struct{ Agent Agent }

func (e *OutdatedError) Error() string { return e.Agent.OutdatedErr() }

// Seen is what one look saw, in the variant it was taken in.
type Seen struct {
	// Agents are the agents of the look, those killed while it looked
	// left out.
	Agents []Agent
	// Windows are the home windows it looked at, and Listed the sandboxes
	// sbx listed for it: none when sbx was not asked or gave no answer.
	Windows []tmux.Window
	Listed  []sbx.Sandbox
	// counter is what the journals' counter held before the windows were
	// listed, counted whether it could be read.
	counter int64
	counted bool
	looker  Looker
	variant Variant
}

// Look takes a look in variant v (GLOSSARY.md, design §3.4): it asks sbx
// as v says, noting the moment before the question, so a report newer than
// the answer does not end the agent (#48); reads the journals' counter
// before it lists the windows, for the look's cursor (Seen.Cursor); looks
// at the agents of those windows, leaving out an agent killed while it
// looked (#239); and, in a variant that takes it, looks again RestDelay
// later at a working agent's screen at rest (design §3.4). It writes in no
// pane: what a command does with the agents is the command's.
func (k Looker) Look(v Variant) (Seen, error) {
	s, err := k.once(v)
	if err != nil || !v.again || !slices.ContainsFunc(s.Agents, Agent.Resting) {
		return s, err
	}
	k.Sleep(RestDelay)
	return k.once(v)
}

// once is one look in variant v, with no second look.
func (k Looker) once(v Variant) (Seen, error) {
	running, listed, err := k.askSbx(v.sbx)
	if err != nil {
		return Seen{}, err
	}
	counter, err := k.Sources.Through()
	counted := err == nil
	ws, err := k.Windows()
	if err != nil {
		return Seen{}, &TmuxError{err}
	}
	as := k.withoutKilled(Look(ws, running, k.Now(), k.Sources, k.Folds))
	return Seen{Agents: as, Windows: ws, Listed: listed, counter: counter, counted: counted, looker: k, variant: v}, nil
}

// askSbx asks sbx which sandboxes run, as mode says, noting the moment
// before the question: what an agent reports from then on is newer than
// the answer (#48). It gives the answer, the sandboxes sbx listed, and
// the error that fails the look.
func (k Looker) askSbx(mode sbxMode) (Sandboxes, []sbx.Sandbox, error) {
	if mode == sbxNotAsked {
		return Sandboxes{}, nil, nil
	}
	asked := k.Now()
	all, err := k.Sandboxes()
	if err == nil {
		return SandboxesOf(all, asked), all, nil
	}
	var old *sbx.TooOldError
	if mode == sbxRequired || mode == sbxAsked && errors.As(err, &old) {
		return Sandboxes{}, nil, &SbxError{err}
	}
	return Sandboxes{}, nil, nil
}

// withoutKilled are the agents of a look but those whose journal it found
// gone (Agent.JournalGone) and whose home windows are gone by now: hq kill
// kills an agent's window before it removes its journal, so windows listed
// after a look that found the journal of an agent it was removing gone
// show its window gone, and the agent is left out as the next look leaves
// it out (#239). tmux is asked again only when the look found a journal
// gone; when it cannot say, every agent stays.
func (k Looker) withoutKilled(as []Agent) []Agent {
	if !slices.ContainsFunc(as, Agent.JournalGone) {
		return as
	}
	ws, err := k.Windows()
	if err != nil {
		return as
	}
	return slices.DeleteFunc(as, func(a Agent) bool {
		return a.JournalGone() && !slices.ContainsFunc(ws, func(w tmux.Window) bool { return w.ID == a.Window })
	})
}

// Counter is what the journals' counter held before the look listed the
// windows, and whether it could be read.
func (s Seen) Counter() (int64, bool) { return s.counter, s.counted }

// Cursor is the number the look's cursor stands for (ADR 0019): the lowest
// counter among its last reads of the agents' journals, so it saw every
// change of theirs numbered up to it, whatever other processes numbered
// meanwhile. It is the counter before the look listed the windows when
// that is all it can vouch for: no journal was read, one could not be, or
// an agent's window appeared while it looked (listed again, for that),
// whose changes it did not see but which are all numbered after that.
func (s Seen) Cursor() int64 {
	n, ok := SeenThrough(s.Agents)
	if !ok || n <= s.counter {
		return s.counter
	}
	ws, err := s.looker.Windows()
	if err != nil || Appeared(s.Windows, ws) {
		return s.counter
	}
	return n
}

// CoverLooks bounds the looks of Covered: agents that keep changing still
// get a list.
const CoverLooks = 3

// Covered is the look, or a later one in the same variant, whose cursor
// stands for every agent it shows, with that cursor (hq ls --json): a look
// may show an agent at a change numbered above its cursor (evidence it
// appended for that agent after it read another), which a wait from that
// cursor would return again, though the list showed it (#166). It looks
// again, with no second look, up to CoverLooks looks in all, and gives
// the last. A command delivers what waits before it covers, so the looks
// again show what the delivery changed.
func (s Seen) Covered() (Seen, int64, error) {
	cursor := s.Cursor()
	for n := 1; n < CoverLooks && above(s.Agents, cursor); n++ {
		var err error
		if s, err = s.looker.once(s.variant); err != nil {
			return Seen{}, 0, err
		}
		cursor = s.Cursor()
	}
	return s, cursor, nil
}

// above reports whether an agent of as stands at a change numbered above
// cursor.
func above(as []Agent, cursor int64) bool {
	return slices.ContainsFunc(as, func(a Agent) bool {
		seq, ok := a.StateSeq()
		return ok && seq > cursor
	})
}

// Appeared reports whether after holds the home window of an agent that
// before does not.
func Appeared(before, after []tmux.Window) bool {
	for _, w := range after {
		id := w.Options["id"]
		if id != "" && !slices.ContainsFunc(before, func(b tmux.Window) bool { return b.Options["id"] == id }) {
			return true
		}
	}
	return false
}

// CursorOf is the number agent a's own cursor stands for (hq read, ADR
// 0019): the counter of the look's read of its journal, so what it does
// after the look is numbered above it; the counter before the look listed
// the windows when its journal was not read.
func (s Seen) CursorOf(a Agent) int64 {
	if n := a.Through(); n != 0 {
		return n
	}
	return s.counter
}

// Named is the agent of the look named name, or the refusal that no agent
// is (NotFoundError).
func (s Seen) Named(name string) (Agent, error) {
	a, ok := Find(s.Agents, name)
	if !ok {
		return Agent{}, &NotFoundError{Name: name}
	}
	return a, nil
}

// Usable is the agent of the look named name, or the refusal that no agent
// is (NotFoundError) or that it is outdated (OutdatedError).
func (s Seen) Usable(name string) (Agent, error) {
	a, err := s.Named(name)
	if err != nil {
		return a, err
	}
	return a, a.Refusal()
}

// Refusal is the refusal of an outdated agent (OutdatedError), nil for
// any other.
func (a Agent) Refusal() error {
	if a.Outdated() {
		return &OutdatedError{Agent: a}
	}
	return nil
}

// CounterBefore is the counter for a change an hq command is about to
// cause in agent a (a message, an answer; ADR 0019): what the counter
// holds now, so what the agent does with it is numbered above it; the
// counter of a's read when the counter cannot be read now.
func (k Looker) CounterBefore(a Agent) int64 {
	n, err := k.Sources.Through()
	if err != nil {
		n = a.Through()
	}
	return n
}
