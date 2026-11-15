package cli

import (
	"github.com/rkrysinski/hq/internal/journal"
	"github.com/rkrysinski/hq/internal/state"
)

// appendEvent appends an event of hq's own, of kind kind with facts, to the
// journal of agent id (design §3.4, #195): something hq did to the agent,
// at d's now, based on the last event of the agent's hook in its journal.
// hq appends it before it acts, so whatever the agent then reports comes
// after it in the journal. A journal that cannot be read or written fails
// nothing hq does.
func appendEvent(d deps, id, kind string, facts state.Facts) {
	j, err := d.journal.Read(id)
	if err != nil {
		return
	}
	_, _ = d.journal.Append(id, state.Event{Source: state.SourceHQ, Kind: kind, Time: d.now(), Basis: journal.Basis(j.Events), Facts: facts})
}

// appendWithdrawn appends that what hq appended of kind (a delivery, an answer,
// a launch) did not reach the agent after all: the typing failed and the
// messages went back into the inbox, the answer was not taken, the session
// did not start.
func appendWithdrawn(d deps, id, kind string, messages []string) {
	appendEvent(d, id, state.KindWithdrawn, state.Facts{At: kind, Messages: messages})
}

// messageNames are the names msgs were left under in the inbox.
func messageNames(msgs []state.Message) []string {
	names := make([]string, len(msgs))
	for i, m := range msgs {
		names[i] = m.Name
	}
	return names
}
