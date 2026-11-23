package agent

import (
	"time"

	"github.com/rkrysinski/hq/internal/state"
)

// Dialog is an agent's dialog verdict, as the look decided it: whether the
// agent is at a dialog, whose, since when, and, for a dialog of Claude's
// own, what it asks. An agent needs input exactly while it is at one.
type Dialog struct {
	// Whose is whose the open dialog is, as the reducer names it:
	// state.DialogHooks, state.DialogSubagent or state.DialogOwn; "" when
	// the agent is at none.
	Whose string
	// Since is when the dialog opened: its report's time for one the hooks
	// reported, when hq first saw it for one of Claude's own.
	Since time.Time
	own   *state.Ask // what a dialog of Claude's own asks, as its screen showed it; nil when this look did not see it
	// scrolls says the list of a dialog of Claude's own scrolls to options
	// its screen did not show (state.OwnView); fingerprint names it as the
	// event that opened it does (state.OwnFingerprint).
	scrolls     bool
	fingerprint string
}

// Open reports whether the agent is at a dialog.
func (d Dialog) Open() bool { return d.Whose != "" }

// ID is the open dialog's id, of the moment it opened (state.DialogID),
// which hq answer names the dialog by; empty when no dialog is open.
func (d Dialog) ID() string {
	if !d.Open() {
		return ""
	}
	return state.DialogID(d.Since)
}

// Asks is what the open dialog asks: for one the hooks reported, reported,
// what the detail of the agent's report holds (state.Detail.Ask, read only
// by the caller that shows it); for one of Claude's own, what its screen
// showed; nil when no dialog is open.
func (d Dialog) Asks(reported *state.Ask) *state.Ask {
	switch d.Whose {
	case state.DialogHooks, state.DialogSubagent:
		return reported
	case state.DialogOwn:
		return d.own
	}
	return nil
}

// Scrolls reports whether the open dialog is one of Claude's own whose list
// scrolls to options its screen did not show, as the picker of /model
// does: hq answer does not answer it (#120).
func (d Dialog) Scrolls() bool { return d.Whose == state.DialogOwn && d.scrolls }

// Shows reports whether a screen's dialog of Claude's own, v, is the open one:
// the same title and text, wherever Claude Code's cursor is, as the event that
// opened it names it (state.OwnFingerprint), whether or not this look saw the
// screen.
func (d Dialog) Shows(v state.OwnView) bool {
	return d.Whose == state.DialogOwn && d.fingerprint != "" && state.OwnFingerprint(v.Ask) == d.fingerprint
}

// Dialog is the agent's dialog verdict, as the reducer folded it from its
// journal: open while the agent needs input.
func (a Agent) Dialog() Dialog {
	v := a.reduced.Dialog
	if a.State != state.NeedsInput || !v.Open {
		return Dialog{}
	}
	return Dialog{Whose: v.Whose, Since: v.Opened.Time, own: a.ownAsk, scrolls: a.ownScroll, fingerprint: v.Screen}
}
