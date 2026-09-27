package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/state"
)

var t0 = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

func sample() []Agent {
	a := func(name, repo, st string, ago time.Duration) Agent {
		return Agent{Name: name, RepoPath: "/w/" + repo, State: st, Since: t0.Add(-ago)}
	}
	return []Agent{
		a("w-old", "zeta", state.Working, time.Hour),
		a("done", "alpha", state.Done, time.Minute),
		a("w-new", "alpha", state.Working, time.Second),
		a("ask", "zeta", state.Question, time.Minute),
		a("end", "alpha", state.Ended, time.Minute),
		a("perm", "zeta", state.NeedsInput, time.Hour),
	}
}

func names(as []Agent) string {
	var n []string
	for _, a := range as {
		n = append(n, a.Name)
	}
	return strings.Join(n, " ")
}

func TestSorts(t *testing.T) {
	for _, tc := range []struct {
		name string
		sort func([]Agent)
		want string
	}{
		{"attention: state, then newest first", SortAttention, "perm ask done w-new w-old end"},
		{"repo: repository, then attention", SortRepo, "done w-new end perm ask w-old"},
		{"state: attention order, then name", SortState, "perm ask done w-new w-old end"},
	} {
		as := sample()
		tc.sort(as)
		if got := names(as); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
	// Inside a state, the state sort does not follow age.
	as := sample()
	as[2].Since = t0.Add(-2 * time.Hour) // w-new is now the oldest
	SortState(as)
	if got := names(as); got != "perm ask done w-new w-old end" {
		t.Errorf("state sort moved by age: %s", got)
	}
}
