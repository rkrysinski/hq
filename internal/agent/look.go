package agent

import (
	"slices"
	"sync"
	"time"

	"github.com/rkrysinski/hq/internal/journal"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
)

// Sources are where a look reads and appends (design §3.4): the journals'
// store, and tmux for the screens.
type Sources interface {
	journal.Store
	// Screens returns what the panes show, by pane id, in one call.
	Screens(panes []string) (map[string]string, error)
}

// Folds are the agents' journals as one hq process keeps them folded
// (state.Fold), so that each look reads and folds only the events appended
// since the one before (design §5.1): the list and hq wait keep one for as
// long as they run. A nil Folds folds every journal from its start. One
// look at a time uses it.
type Folds struct {
	mu sync.Mutex
	by map[string]*folded
}

// NewFolds is an empty Folds.
func NewFolds() *Folds { return &Folds{by: map[string]*folded{}} }

// folded is an agent's journal folded up to position next: the fold, the
// journal's reply and ask, what decides whether evidence appended now
// stands (journal.Standing), and what the counter held at its last read
// (journal.Journal.Through).
type folded struct {
	next     int64
	through  int64
	fold     state.Fold
	reply    state.Reply
	ask      state.Asked
	standing journal.Standing
}

// newFolded is an empty journal folded.
func newFolded() *folded { return &folded{standing: journal.NewStanding()} }

// lock takes the Folds for one look and returns what lets it go.
func (fs *Folds) lock() func() {
	if fs == nil {
		return func() {}
	}
	fs.mu.Lock()
	return fs.mu.Unlock
}

// of is the folded journal of agent id, fresh for a nil Folds.
func (fs *Folds) of(id string) *folded {
	if fs == nil {
		return newFolded()
	}
	f, ok := fs.by[id]
	if !ok {
		f = newFolded()
		fs.by[id] = f
	}
	return f
}

// update folds the events appended to agent id's journal since f's
// position; a journal that no longer reaches it (removed meanwhile) is
// folded anew from its start.
func (f *folded) update(store journal.Store, id string) error {
	j, err := store.ReadFrom(id, f.next)
	if err != nil {
		return err
	}
	if j.From != f.next {
		*f = *newFolded()
	}
	f.fold = f.fold.With(j.Events...)
	f.standing.Follow(j.Events...)
	if j.Reply.Number != 0 {
		f.reply = j.Reply
	}
	if j.Ask.Number != 0 {
		f.ask = j.Ask
	}
	f.next, f.through = j.Next, j.Through
	return nil
}

// witness appends e, evidence the look saw at now, to agent id's journal,
// based on the last event of its hook there, unless the journal holds it
// already; the store sets it aside when another process appended it first,
// or a later report overtook it. A journal that cannot be written gets no
// event.
func (f *folded) witness(store journal.Store, id string, e state.Event, now time.Time) {
	e.Basis = f.standing.Basis
	if e.Time.IsZero() {
		e.Time = now
	}
	if f.standing.Evidence.Holds(e) {
		return
	}
	// The fold, its evidence included, follows the append at the next
	// update, which the look takes before it witnesses the same kind again.
	_, _ = store.Append(id, e)
}

// witnessDeath appends the death of a's pane, as tmux dates it, at now
// (witness); a death tmux dates before the agent's latest launch or
// relaunch is its previous session's, seen by a look that listed the
// windows before the relaunch respawned the pane, and is not appended.
func (f *folded) witnessDeath(store journal.Store, a Agent, now time.Time) {
	if f.fold.DiedBeforeLaunch(a.deadAt) {
		return
	}
	e := state.Event{Source: state.SourceTmux, Kind: state.KindPaneDied}
	e.Facts.From = a.deadAt
	f.witness(store, a.ID, e, now)
}

// read gives agent a its state from its journal, the event files its hook
// left ingested first: a journal that cannot be read leaves the agent as
// its window says.
func (a *Agent) read(store journal.Store, f *folded) bool {
	if a.Outdated() {
		return false
	}
	_ = store.Ingest(a.RepoPath, a.ID)
	if f.update(store, a.ID) != nil {
		a.unread()
		return false
	}
	return true
}

// FromJournals is the agents of the home windows ws with their state as
// their journals give it, the event files their hooks left ingested first,
// at now: of what tmux, sbx and the screen show, only a dead pane's death
// is appended, and no screen is read (design §3.4). hq go, hq code and the
// dashboard's dock take it; the commands that show or act on an agent's
// state take a Look.
func FromJournals(ws []tmux.Window, store journal.Store, now time.Time, folds *Folds) []Agent {
	as := FromWindows(ws)
	ReadStates(as, store, now, folds)
	return as
}

// ReadStates gives the agents their state from their journals, as
// FromJournals does: the death of the pane of an agent with a home window
// (FromWindows) is appended first, as tmux dates it, so the ended state
// rests on an event as any other does (story 43 of #165). A lost agent has
// no pane to have died: its journal alone gives its state.
func ReadStates(as []Agent, store journal.Store, now time.Time, folds *Folds) {
	defer folds.lock()()
	for i := range as {
		a := &as[i]
		f := folds.of(a.ID)
		if !a.read(store, f) {
			continue
		}
		if !a.Alive && a.Window != "" {
			f.witnessDeath(store, *a, now)
			_ = f.update(store, a.ID)
		}
		a.apply(f, now, "", false)
	}
}

// Look is the look at the agents of the home windows ws at now (GLOSSARY.md,
// design §3.4), as hq ls, hq read, hq wait, hq send, the list and the
// chords take it: for each agent it ingests the event files its hook left
// into its journal and folds the journal (state.Fold), appends what tmux,
// sbx and its screen show that the journal does not hold yet, and folds
// what it appended, so every hq process shows the same state from the
// same journal. Of looks that append the same evidence at once, the first
// wins and the others' is set aside (journal.Standing).
//
// What it appends: the pane's death, as tmux dates it; sbx's answer that
// the agent's sandbox does not run, with the moment sbx was asked, when
// that answer ends the agent (state.Fold.Stopped; an answer sbx did not
// give ends nobody and is no event); what the screens that may show what
// no hook reports show (state.Fold.Sees), all captured in one call;
// and Claude's wake-up after a turn the user ended (state.Fold.WakeUp).
// It reads none of the hook's working files and writes none: what it sees
// of Claude taking a queued prompt after an Esc is an event like any other
// (#88).
//
// It never fails: a capture that fails appends nothing of the screens,
// and what the journal holds still stands; a journal that cannot be read
// leaves the agent as its window says, and one that cannot be written gets
// no event. One look may not be enough: an agent Resting needs one more,
// RestDelay later, which is the caller's to take.
func Look(ws []tmux.Window, sandboxes Sandboxes, now time.Time, store Sources, folds *Folds) []Agent {
	defer folds.lock()()
	as := FromWindows(ws)
	fs := make([]*folded, len(as))
	wakes := make([]*state.Event, len(as))
	for i := range as {
		a := &as[i]
		f := folds.of(a.ID)
		if !a.read(store, f) {
			continue
		}
		fs[i] = f
		if e, ok := f.fold.WakeUp(a.Alive); ok {
			wakes[i] = &e
		}
		if !a.Alive {
			f.witnessDeath(store, *a, now)
		}
		if sandboxes.notRunning(*a) && f.fold.Stopped(sandboxes.Asked) {
			e := state.Event{Source: state.SourceSbx, Kind: state.KindNotRunning}
			e.Facts.Asked = sandboxes.Asked
			f.witness(store, a.ID, e, now)
		}
		_ = f.update(store, a.ID)
	}
	screens := lookAtScreens(as, fs, now, store)
	for i := range as {
		a, f := &as[i], fs[i]
		if f == nil {
			continue
		}
		screen, captured := screens[a.Pane]
		if captured {
			events, resting := f.fold.Sees(screen, now)
			a.resting = resting
			for _, e := range events {
				f.witness(store, a.ID, e, now)
				if e.Kind == state.KindTookQueued {
					// The prompt Claude took is no wake-up.
					wakes[i] = nil
				}
			}
		}
		if wakes[i] != nil {
			f.witness(store, a.ID, *wakes[i], now)
		}
		_ = f.update(store, a.ID)
		a.apply(f, now, screen, captured)
	}
	return as
}

// lookAtScreens captures, in one call, the screens of the agents whose
// turn the user may have ended or that wait at their prompt
// (state.Fold.ScreenMayTell), by pane; none when there are none or the capture
// fails.
func lookAtScreens(as []Agent, fs []*folded, now time.Time, store Sources) map[string]string {
	var panes []string
	for i := range as {
		if fs[i] != nil && as[i].Alive && fs[i].fold.ScreenMayTell(now) {
			panes = append(panes, as[i].Pane)
		}
	}
	if len(panes) == 0 {
		return nil
	}
	screens, err := store.Screens(panes)
	if err != nil {
		return nil
	}
	for p := range screens {
		if !slices.Contains(panes, p) {
			delete(screens, p)
		}
	}
	return screens
}
