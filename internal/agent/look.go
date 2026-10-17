package agent

import (
	"time"

	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
)

// A Store is where the look reads the agents' screens and keeps what it
// saw on their windows (design §3.4). The tmux client is one.
type Store interface {
	// Screens returns what the panes show, by pane id, in one call.
	Screens(panes []string) (map[string]string, error)
	// KeepFirst stores value as the window's option unless the option
	// already holds a value that starts with prefix, and returns what the
	// option holds then: the first value stored for a prefix wins.
	KeepFirst(window, option, prefix, value string) (string, error)
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
// It never fails. When the capture fails, the hooks' states stand, and a
// turn end, a dialog answered or a dialog of Claude's own hq saw earlier
// still applies; when a record cannot be written, it is not applied, and
// the agent shows what this look saw.
//
// One look may not be enough: an agent Resting needs one more, RestDelay
// later, which is the caller's to take.
func Look(ws []tmux.Window, read func(root, id string) (state.Report, bool), take Taker, sandboxes Sandboxes, now time.Time, store Store) []Agent {
	as := Collect(ws, read, sandboxes)
	settleScreens(as, read, take, now, store)
	for i := range as {
		offer(store, &as[i], as[i].seeEnd(now))
		offer(store, &as[i], as[i].seeWake())
	}
	return as
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
				as[i].handOver(read, take)
				continue
			}
			offer(store, &as[i], r)
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
// unless the write failed.
func offer(store Store, a *Agent, r record) {
	if r.Value == "" {
		return
	}
	if stored, err := store.KeepFirst(a.Window, r.Option, r.Key, r.Value); err == nil {
		a.adopt(r, stored)
	}
}
