package agent

import (
	"time"

	"github.com/rkrysinski/hq/internal/state"
)

// Whose is whose dialog an agent is at, in its dialog verdict.
type Whose int

const (
	// NoDialog: the agent is at no dialog.
	NoDialog Whose = iota
	// HooksDialog: a dialog the agent's hooks reported, a question
	// dialog, a permission prompt or Claude's Notification (design §3.4).
	HooksDialog
	// OwnDialog: a dialog of Claude's own in place of the prompt box,
	// which no hook reports, read off the agent's screen (#29, #66).
	OwnDialog
	// SubagentDialog: a dialog one of the agent's subagents opened, as its
	// hooks reported it.
	SubagentDialog
)

// Dialog is an agent's dialog verdict, as the look decided it: whether the
// agent is at a dialog, whose, since when, and, for a dialog of Claude's
// own, what it asks. An agent needs input exactly while it is at one.
type Dialog struct {
	Whose Whose
	// Since is when the dialog opened: its report's time for one the hooks
	// reported, when hq first saw it for one of Claude's own.
	Since time.Time
	own   *state.Ask // what a dialog of Claude's own asks, as its screen showed it; nil when this look did not see it
	// scrolls says the list of a dialog of Claude's own scrolls to options
	// its screen did not show (state.OwnView); print names it as its record
	// on the agent's window does (ownFingerprint).
	scrolls bool
	print   string
}

// Open reports whether the agent is at a dialog.
func (d Dialog) Open() bool { return d.Whose != NoDialog }

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
	case HooksDialog, SubagentDialog:
		return reported
	case OwnDialog:
		return d.own
	}
	return nil
}

// Scrolls reports whether the open dialog is one of Claude's own whose list
// scrolls to options its screen did not show, as the picker of /model
// does: hq answer does not answer it (#120).
func (d Dialog) Scrolls() bool { return d.Whose == OwnDialog && d.scrolls }

// Shows reports whether a screen's dialog of Claude's own, v, is the open
// one: the same title and text, wherever its cursor is, as its record on
// the agent's window names it (ownFingerprint), whether this look saw the
// screen or recalled the record.
func (d Dialog) Shows(v state.OwnView) bool {
	return d.Whose == OwnDialog && d.print != "" && ownFingerprint(v.Ask) == d.print
}

// Dialog is the agent's dialog verdict.
func (a Agent) Dialog() Dialog {
	if a.State != state.NeedsInput {
		return Dialog{}
	}
	return Dialog{Whose: a.dialog, Since: a.Since, own: a.ownAsk, scrolls: a.ownScrolls, print: a.ownPrint}
}
