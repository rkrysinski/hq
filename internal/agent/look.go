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

// Look is the look at the agents' screens: the agents of the home windows
// ws, collected with what their state files report and sbx's answer
// (Collect), then settled by what their screens show at now that no hook
// reports (design §3.4), as hq ls, the list and the chords show them. It
// captures the screens of every agent the hooks leave unsettled in one
// call, and keeps on the agents' windows, first-wins, what it saw first
// (a turn the user ended, a screen at rest, a dialog of Claude's own, an
// end nothing dates, Claude's wake-up), so that every hq process shows the
// same moment; a record another process stored first is the one applied.
//
// It never fails. When the capture fails, the hooks' states stand, and a
// turn end, a dialog answered or a dialog of Claude's own hq saw earlier
// still applies; when a record cannot be written, it is not applied, and
// the agent shows what this look saw.
//
// One look may not be enough: an agent Resting needs one more, RestDelay
// later, which is the caller's to take.
func Look(ws []tmux.Window, read func(root, id string) (state.Report, bool), sandboxes Sandboxes, now time.Time, store Store) []Agent {
	as := Collect(ws, read, sandboxes)
	settleScreens(as, now, store)
	for i := range as {
		keepIn(store, &as[i], as[i].seeEnd(now))
		keepIn(store, &as[i], as[i].seeWake())
	}
	return as
}

// settleScreens looks at the screens of the agents whose turn the user may
// have ended, or that wait at their prompt, in one capture.
func settleScreens(as []Agent, now time.Time, store Store) {
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
			keepIn(store, &as[i], as[i].settle(s, now))
		}
	}
}

// keepIn offers r to the agent's window and applies what it holds then,
// unless the write failed.
func keepIn(store Store, a *Agent, r record) {
	if r.Value == "" {
		return
	}
	if stored, err := store.KeepFirst(a.Window, r.Option, r.Key, r.Value); err == nil {
		a.keep(r, stored)
	}
}
