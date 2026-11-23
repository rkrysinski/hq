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
// docking as the dashboard is entered; design §3.8), in the variant of the
// command, and the look loop of hq wait and hq send --wait (Loop): the
// whole protocol of a look, so that no command puts it together itself.
// The list takes from it the look loop's rhythm, its asking of sbx and its
// look at the windows it listed (Agents). Its dependencies are those the
// command tests already fake: tmux, the journals' store, sbx's answer and
// the clock.
type Looker struct {
	// Sources are where the look reads and appends, and the home windows
	// it looks at.
	Sources LookerSources
	// Sandboxes asks sbx which sandboxes there are, within a time limit.
	Sandboxes func() ([]sbx.Sandbox, error)
	Now       func() time.Time
	Sleep     func(time.Duration)
	// Folds keeps the journals folded for this process (Look).
	Folds *Folds
}

// LookerSources are where a Looker's look reads and appends (Sources),
// with the home windows it looks at.
type LookerSources interface {
	Sources
	// Windows lists the home windows, tmux asked now.
	Windows() ([]tmux.Window, error)
}

// A Variant is how a command's look differs from another's: whether it
// asks sbx and what it makes of an answer sbx does not give, whether a
// working agent's screen at rest gets the second look RestDelay later, and
// whether it reads the journals' counter before it lists the windows (for
// its cursor, Looker.Cursor, and Seen.Counter). The commands take one of
// the variants below, each what the command did before the Looker (sbx:
// design §3.6, *The oldest sbx*).
type Variant struct {
	sbx    sbxMode
	again  bool
	counts bool
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
	Shown = Variant{sbx: sbxAsked, again: true, counts: true}
	// Explained is the look of hq explain, which explains the journal on
	// the host: sbx tolerated, and the second look.
	Explained = Variant{sbx: sbxTolerated, again: true, counts: true}
	// Acting is the look of hq send and hq answer at the agent they act
	// on, and each look of their waits and of the look loop: sbx asked,
	// one look.
	Acting = Variant{sbx: sbxAsked, counts: true}
	// Updating is the look of hq update across generations, which has to
	// know every agent, lost ones included: sbx required, one look, and
	// no counter read, so it never waits on the counter's lock.
	Updating = Variant{sbx: sbxRequired}
	// Docking is the look of the chords and of docking as the dashboard
	// is entered: sbx not asked, one look, and no counter read, so a key
	// pressed never waits on the counter's lock.
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

// A Counter is what the journals' counter held before a look listed the
// windows (Seen.Counter): N, when Read says the look read it. A look in a
// variant that reads no counter, or one that could not read it, read none.
type Counter struct {
	N    int64
	Read bool
}

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
	// listed.
	counter Counter
	variant Variant
}

// The look loop's rhythm (GLOSSARY.md, design §5.1): Loop's, and the
// list's, which reads them so the two cannot differ.
const (
	// LoopEvery is how often the look loop looks.
	LoopEvery = 250 * time.Millisecond
	// SbxEvery is how often it asks sbx which sandboxes run; the answer
	// comes in the background, so a slow sbx never holds up a look.
	SbxEvery = time.Second
)

// Look takes a look in variant v (GLOSSARY.md, design §3.4): it asks sbx
// as v says, noting the moment before the question, so a report newer than
// the answer does not end the agent (#48); in a variant that reads it,
// reads the journals' counter before it lists the windows, for the look's
// cursor (Looker.Cursor); looks at the agents of those windows, leaving
// out an agent killed while it looked (#239); and, in a variant that takes
// it, looks again RestDelay later at a working agent's screen at rest
// (design §3.4). It writes in no pane: what a command does with the agents
// is the command's.
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
	return k.see(v, running, listed)
}

// see is a look with sbx's answer given: running, from the sandboxes sbx
// listed. In a variant that reads it, it reads the journals' counter
// before it lists the windows, for the look's cursor (Looker.Cursor).
func (k Looker) see(v Variant, running Sandboxes, listed []sbx.Sandbox) (Seen, error) {
	var c Counter
	if v.counts {
		n, err := k.Sources.Through()
		c = Counter{N: n, Read: err == nil}
	}
	ws, err := k.Sources.Windows()
	if err != nil {
		return Seen{}, &TmuxError{err}
	}
	return Seen{Agents: k.Agents(ws, running), Windows: ws, Listed: listed, counter: c, variant: v}, nil
}

// Agents are the agents of the home windows ws, with sbx's answer
// running (Look), those killed while it looked left out (withoutKilled):
// the look of the list, which lists the windows and asks sbx itself.
func (k Looker) Agents(ws []tmux.Window, running Sandboxes) []Agent {
	return k.withoutKilled(Look(ws, running, k.Now(), k.Sources, k.Folds))
}

// askSbx asks sbx which sandboxes run, as mode says (ask). It gives the
// answer, the sandboxes sbx listed, and the error that fails the look.
func (k Looker) askSbx(mode sbxMode) (Sandboxes, []sbx.Sandbox, error) {
	if mode == sbxNotAsked {
		return Sandboxes{}, nil, nil
	}
	running, all, err := k.ask()
	if err == nil {
		return running, all, nil
	}
	var old *sbx.TooOldError
	if mode == sbxRequired || mode == sbxAsked && errors.As(err, &old) {
		return Sandboxes{}, nil, &SbxError{err}
	}
	return Sandboxes{}, nil, nil
}

// AskSbx asks sbx which sandboxes run, noting the moment before the
// question (ask): the list's own asking in the background takes its
// answers from here. The answer says nothing when sbx fails, does not
// answer in time or is too old, and the error says why.
func (k Looker) AskSbx() (Sandboxes, error) {
	running, _, err := k.ask()
	return running, err
}

// ask asks sbx which sandboxes run, noting the moment before the
// question: what an agent reports from then on is newer than the answer
// (#48). It gives the answer and the sandboxes sbx listed, or sbx's error
// and an answer that says nothing.
func (k Looker) ask() (Sandboxes, []sbx.Sandbox, error) {
	asked := k.Now()
	all, err := k.Sandboxes()
	if err != nil {
		return Sandboxes{}, nil, err
	}
	return SandboxesOf(all, asked), all, nil
}

// Loop is the look loop of hq wait and hq send --wait (GLOSSARY.md, design
// §5.1): a look every LoopEvery, as the list looks, until step says it is
// done. sbx is asked before the first look, which waits for its answer,
// and after it in the background at most every SbxEvery, each look taking
// its latest answer. step is given each look, in the variant of Acting but
// for how sbx was asked, and whether it is the first; a step that needs the
// look's cursor takes it (Looker.Cursor) first, before it acts on the look.
// A look that fails ends the loop with its error (TmuxError), and
// so does step. It leaves nothing running but an answer of sbx it waits
// for, which ends with sbx's time limit and then goes unread.
func (k Looker) Loop(step func(s Seen, first bool) (done bool, err error)) error {
	answers := &sbxInBackground{ask: func() Sandboxes {
		running, _ := k.AskSbx()
		return running
	}}
	for first := true; ; first = false {
		s, err := k.see(Acting, answers.running(k.Now(), first), nil)
		if err != nil {
			return err
		}
		if done, err := step(s, first); done || err != nil {
			return err
		}
		k.Sleep(LoopEvery)
	}
}

// sbxInBackground asks sbx which sandboxes run at most every SbxEvery, in
// the background, and gives the latest answer (which says nothing when sbx
// could not say, and changes no agent then).
type sbxInBackground struct {
	ask     func() Sandboxes
	answers chan Sandboxes
	asked   time.Time
	busy    bool
	latest  Sandboxes
}

// running is sbx's latest answer at now; wait asks and waits for the answer.
func (b *sbxInBackground) running(now time.Time, wait bool) Sandboxes {
	if wait {
		b.latest, b.asked = b.ask(), now
		return b.latest
	}
	if b.busy {
		select {
		case b.latest = <-b.answers:
			b.busy = false
		default:
		}
	}
	if !b.busy && now.Sub(b.asked) >= SbxEvery {
		if b.answers == nil {
			b.answers = make(chan Sandboxes, 1)
		}
		b.busy, b.asked = true, now
		go func() { b.answers <- b.ask() }()
	}
	return b.latest
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
	ws, err := k.Sources.Windows()
	if err != nil {
		return as
	}
	return slices.DeleteFunc(as, func(a Agent) bool {
		return a.JournalGone() && !slices.ContainsFunc(ws, func(w tmux.Window) bool { return w.ID == a.Window })
	})
}

// Counter is what the journals' counter held before the look listed the
// windows.
func (s Seen) Counter() Counter { return s.counter }

// Cursor is the number the cursor of look s stands for (ADR 0019): the
// lowest counter among its last reads of the agents' journals, so it saw
// every change of theirs numbered up to it, whatever other processes
// numbered meanwhile, once tmux, asked again, lists no agent's window that
// s did not. It is the counter before s listed the windows when that is
// all it can vouch for: its reads are not past it (with tmux not asked),
// as when no journal was read or one could not be, or an agent's window
// appeared while it looked, whose changes it did not see but which are all
// numbered after that.
func (k Looker) Cursor(s Seen) int64 {
	n := SeenThrough(s.Agents)
	if n <= s.counter.N {
		return s.counter.N
	}
	ws, err := k.Sources.Windows()
	if err != nil || appeared(s.Windows, ws) {
		return s.counter.N
	}
	return n
}

// CoverLooks bounds the looks of Cover: agents that keep changing still
// get a list.
const CoverLooks = 3

// Cover gives look s, or a later one in the same variant, whose cursor
// stands for every agent it shows, with that cursor (hq ls --json): a look
// may show an agent at a change numbered above its cursor (evidence it
// appended for that agent after it read another), which a wait from that
// cursor would return again, though the list showed it (#166). It looks
// again, with no second look, up to CoverLooks looks in all, and gives
// the last. A command delivers what waits before it covers, so the looks
// again show what the delivery changed.
func (k Looker) Cover(s Seen) (Seen, int64, error) {
	cursor := k.Cursor(s)
	for n := 1; n < CoverLooks && above(s.Agents, cursor); n++ {
		var err error
		if s, err = k.once(s.variant); err != nil {
			return Seen{}, 0, err
		}
		cursor = k.Cursor(s)
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

// appeared reports whether after holds the home window of an agent that
// before does not.
func appeared(before, after []tmux.Window) bool {
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
	return s.counter.N
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
