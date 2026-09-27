package agent

import (
	"sort"

	"github.com/rkrysinski/hq/internal/state"
)

// AttentionOrder lists the states the user should look at first first
// (spec §6.2).
var AttentionOrder = []string{state.NeedsInput, state.Question, state.Done, state.Working, state.Starting, state.Ended}

// AttentionRank is a state's place in AttentionOrder; unknown states come last.
func AttentionRank(s string) int {
	for i, o := range AttentionOrder {
		if s == o {
			return i
		}
	}
	return len(AttentionOrder)
}

// SortAttention sorts agents in attention order; within a state, the one
// that entered it last comes first.
func SortAttention(as []Agent) {
	sort.SliceStable(as, func(i, j int) bool {
		ri, rj := AttentionRank(as[i].State), AttentionRank(as[j].State)
		if ri != rj {
			return ri < rj
		}
		return as[i].Since.After(as[j].Since)
	})
}

// NeedsYou reports whether the agent waits for the user (spec §3, attention
// state).
func (a Agent) NeedsYou() bool { return a.State == state.Question || a.State == state.NeedsInput }
