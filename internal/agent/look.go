package agent

import (
	"time"

	"github.com/rkrysinski/hq/internal/journal"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
)

// A Store is where the look reads the agents' screens and keeps what it
// saw on their windows (design §3.4). The tmux client is one. A Store that
// is also Journals keeps it in the agents' journals too.
type Store interface {
	// Screens returns what the panes show, by pane id, in one call.
	Screens(panes []string) (map[string]string, error)
	// KeepFirst stores value as the window's option unless the option
	// already holds a value that starts with prefix, and returns what the
	// option holds then: the first value stored for a prefix wins.
	KeepFirst(window, option, prefix, value string) (string, error)
}

// Journals are the agents' journals as the look reads them and appends to
// them (journal.Store): every hq command's look has them; one without them
// (a test of the window records alone) appends no event.
type Journals interface {
	// Read is the journal of agent id.
	Read(id string) (journal.Journal, error)
	// Append appends events of hq's own to the journal of agent id, setting
	// aside a duplicate or an overtaken one.
	Append(id string, events ...state.Event) ([]state.Event, error)
}

// A Taker hands an agent's state over to the prompt Claude took from its
// queue after the user interrupted the turn it was queued in (#88), as
// state.TakeQueued does; nil for a look that cannot (see settle).
type Taker func(root, id string) error

// Look is the look at the agents' screens: the agents of the home windows
// ws, collected with what their state files report and sbx's answer
// (Collect), then settled by what their screens show at now that no hook
// reports (design §3.4), as hq ls, the list and the chords show them. It
// captures the screens of every agent the hooks leave unsettled in one
// call, and keeps on the agents' windows, first-wins, what it saw first
// (a turn the user ended, a screen at rest, a dialog of Claude's own, an
// end nothing dates, Claude's wake-up), so that every hq process shows the
// same moment; a record another process stored first is the one applied.
// One thing it saw goes to the agent's state files instead: Claude at work
// on a prompt it took from its queue after an Esc, which take hands the
// state over to, once for all processes, and the look reads the report
// again (#88).
//
// Whatever it saw that no hook reports, and the pane's death and sbx's
// answer that end an agent, it appends to the agent's journal as an event
// of the screen, tmux or sbx, based on the last event of the agent's hook
// in its journal, unless the journal holds that evidence already
// (journal.Holds): a screen that stays the same adds nothing. Of looks
// that append the same evidence at once, the first wins and the others'
// is set aside (#195). The window records still decide the state.
//
// It never fails. When the capture fails, the hooks' states stand, and a
// turn end, a dialog answered or a dialog of Claude's own hq saw earlier
// still applies; when a record cannot be written, it is not applied, and
// the agent shows what this look saw; a journal that cannot be read or
// written gets no event.
//
// One look may not be enough: an agent Resting needs one more, RestDelay
// later, which is the caller's to take.
func Look(ws []tmux.Window, read func(root, id string) (state.Report, bool), take Taker, sandboxes Sandboxes, now time.Time, store Store) []Agent {
	as := Collect(ws, read, sandboxes)
	for i := range as {
		as[i].readJournal(store)
		as[i].seeEnded(store, sandboxes, now)
	}
	settleScreens(as, read, take, now, store)
	for i := range as {
		offer(store, &as[i], as[i].seeEnd(now), now)
		offer(store, &as[i], as[i].seeWake(), now)
		// Collect kept them for the hooks' state; the screens may have
		// overruled it since.
		as[i].keepOwnTasks()
		as[i].journal = nil
	}
	return as
}

// readJournal reads the agent's journal for the look, which bases what it
// appends on the last event of the agent's hook there. An outdated agent's
// journal is not this hq's to read.
func (a *Agent) readJournal(store Store) {
	js, ok := store.(Journals)
	if !ok || a.Outdated() {
		return
	}
	j, err := js.Read(a.ID)
	if err != nil {
		return
	}
	a.journal, a.journaled, a.basis = j.Events, true, journal.Basis(j.Events)
}

// seeEnded appends the evidence that ends the agent: its pane's death, as
// tmux dates it (zero when tmux does not), and its sandbox not running,
// with the moment sbx was asked, when that answer ends it (stopped). An
// answer sbx did not give (an sbx too old, one that failed or did not
// answer in time) ends nobody and is no event.
func (a *Agent) seeEnded(store Store, sandboxes Sandboxes, now time.Time) {
	if !a.Alive {
		e := state.Event{Source: state.SourceTmux, Kind: state.KindPaneDied, Time: now}
		e.Facts.From = a.deadAt
		a.witness(store, e)
	}
	if sandboxes.stopped(*a) && !sandboxes.Asked.IsZero() {
		e := state.Event{Source: state.SourceSbx, Kind: state.KindNotRunning, Time: now}
		e.Facts.Asked = sandboxes.Asked
		a.witness(store, e)
	}
}

// screenEvent is an event of what the look saw on the screen at now.
func screenEvent(kind string, now time.Time) state.Event {
	return state.Event{Source: state.SourceScreen, Kind: kind, Time: now}
}

// witness appends e, evidence the look saw, to the agent's journal, based
// on the last event of its hook there, unless the journal holds it
// already; the store sets it aside when another process appended it
// first. It keeps what it appended, so one look appends nothing twice.
func (a *Agent) witness(store Store, e state.Event) {
	if !a.journaled {
		return
	}
	e.Basis = a.basis
	if journal.Holds(a.journal, e) {
		return
	}
	if out, err := store.(Journals).Append(a.ID, e); err == nil {
		a.journal = append(a.journal, out...)
	}
}

// settleScreens looks at the screens of the agents whose turn the user may
// have ended, or that wait at their prompt, in one capture.
func settleScreens(as []Agent, read func(root, id string) (state.Report, bool), take Taker, now time.Time, store Store) {
	var panes []string
	for i := range as {
		if as[i].unsettled(now) {
			panes = append(panes, as[i].Pane)
		}
	}
	if len(panes) == 0 {
		return
	}
	screens, err := store.Screens(panes)
	if err != nil {
		// What hq saw earlier stands without the screen: a turn end, a
		// dialog of the hooks' answered, a dialog of Claude's own.
		for i := range as {
			if as[i].unsettled(now) {
				as[i].recallSeen()
			}
		}
		return
	}
	for i := range as {
		if s, ok := screens[as[i].Pane]; ok && as[i].unsettled(now) {
			r := as[i].settle(s, now)
			if as[i].takeQueued {
				as[i].witness(store, screenEvent(state.KindTookQueued, now))
				as[i].handOver(read, take)
				continue
			}
			offer(store, &as[i], r, now)
		}
	}
}

// handOver hands the agent's state over to the prompt Claude took from its
// queue (settle, Taker) and applies the report that gives: working on it
// since now. Without a taker, or when the hand-over fails (a hook has just
// taken the copy away, another process got there first), the hooks' word
// stands for this look.
func (a *Agent) handOver(read func(root, id string) (state.Report, bool), take Taker) {
	a.takeQueued = false
	if take == nil || take(a.RepoPath, a.ID) != nil {
		return
	}
	a.Apply(read(a.RepoPath, a.ID))
}

// offer offers r to the agent's window and applies what it holds then,
// unless the write failed, and appends its event, seen at now, to the
// agent's journal (witness), whichever process stored the record first.
func offer(store Store, a *Agent, r record, now time.Time) {
	if r.Value == "" {
		return
	}
	if stored, err := store.KeepFirst(a.Window, r.Option, r.Key, r.Value); err == nil {
		a.adopt(r, stored)
	}
	if r.Event.Kind != "" {
		if r.Event.Time.IsZero() {
			r.Event.Time = now
		}
		a.witness(store, r.Event)
	}
}
