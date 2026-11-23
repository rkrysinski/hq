package cli

import (
	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/journal"
	"github.com/rkrysinski/hq/internal/state"
)

// appendEvent appends an event of hq's own, of kind kind with facts, to the
// journal of agent a (design §3.4, #195): something hq did to the agent,
// at d's now, based on the last event of the agent's hook in its journal.
// hq appends it before it acts, so whatever the agent then reports comes
// after it in the journal. To a journal that is not there (never begun, or
// removed) it is appended only while the agent's home window stands,
// asked of tmux under the store's lock with the append (#318): hq kill
// kills the window before it removes the journal, so a command that found
// the agent before a kill writes no journal of it back, which nothing would
// remove. The launch is the exception (appendLaunched). A journal that
// cannot be read or written fails nothing hq does.
func appendEvent(d deps, a agent.Agent, kind string, facts state.Facts) {
	e, ok := ownEvent(d, a.ID, kind, facts)
	if !ok {
		return
	}
	_, _ = d.journal.AppendWhileWindowStands(a.ID, func() bool { return d.sources().WindowStands(a.Window) }, e)
}

// appendLaunched appends agent id's launch, with facts, as appendEvent
// appends hq's other events, but whatever tmux says of its home window:
// the launch begins the agent's journal, and its window, just made, is
// the agent's (#318).
func appendLaunched(d deps, id string, facts state.Facts) {
	e, ok := ownEvent(d, id, state.KindLaunched, facts)
	if !ok {
		return
	}
	_, _ = d.journal.Append(id, e)
}

// ownEvent is the event of hq's own of kind with facts for agent id, at
// d's now and based on its journal, and whether that journal could be read.
func ownEvent(d deps, id, kind string, facts state.Facts) (state.Event, bool) {
	j, err := d.journal.Read(id)
	if err != nil {
		return state.Event{}, false
	}
	return state.Event{Source: state.SourceHQ, Kind: kind, Time: d.now(), Basis: journal.Basis(j.Events), Facts: facts}, true
}

// appendWithdrawn appends that what hq appended of kind (a delivery, an answer,
// a launch) did not reach agent a after all: the typing failed and the
// messages went back into the inbox, the answer was not taken, the session
// did not start.
func appendWithdrawn(d deps, a agent.Agent, kind string, messages []string) {
	appendEvent(d, a, state.KindWithdrawn, state.Facts{Withdrawn: kind, Messages: messages})
}

// messageNames are the names msgs were left under in the inbox.
func messageNames(msgs []state.Message) []string {
	names := make([]string, len(msgs))
	for i, m := range msgs {
		names[i] = m.Name
	}
	return names
}
