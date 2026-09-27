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

// SortRepo sorts agents by repository name, in attention order inside a
// repository (spec §6.2).
func SortRepo(as []Agent) {
	SortAttention(as)
	sort.SliceStable(as, func(i, j int) bool { return as[i].Repo() < as[j].Repo() })
}

// SortState sorts agents in attention order, by name inside a state, so rows
// do not move as agents age (spec §6.2).
func SortState(as []Agent) {
	sort.SliceStable(as, func(i, j int) bool {
		ri, rj := AttentionRank(as[i].State), AttentionRank(as[j].State)
		if ri != rj {
			return ri < rj
		}
		return as[i].Name < as[j].Name
	})
}
