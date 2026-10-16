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
	own   *state.Ask // what a dialog of Claude's own asks, as its screen showed it
}

// Open reports whether the agent is at a dialog.
func (d Dialog) Open() bool { return d.Whose != NoDialog }

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

// Dialog is the agent's dialog verdict.
func (a Agent) Dialog() Dialog {
	switch {
	case a.State != state.NeedsInput:
		return Dialog{}
	case a.ownAsk != nil:
		return Dialog{Whose: OwnDialog, Since: a.Since, own: a.ownAsk}
	case a.subDialog:
		return Dialog{Whose: SubagentDialog, Since: a.Since}
	}
	return Dialog{Whose: HooksDialog, Since: a.Since}
}
