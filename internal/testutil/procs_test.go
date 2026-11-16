package testutil

import (
	"slices"
	"testing"
)

// What a pane's program started counts as its own until it has exited:
// what runs below it and what is left of its process group once its parent
// has gone, as a hook the fake Claude started after the server's hangup
// (#191); not another program's, nor a process that waits to be reaped.
func TestWhatAPanesProgramStartedCountsUntilItHasExited(t *testing.T) {
	const others = `
    1     0     1 Ss
  200     1   200 Ss
  201   200   200 S+
`
	for _, c := range []struct {
		name, listing string
		want          []int
	}{
		{"the program runs", `
  100    90   100 Ss
  101   100   100 S+
  102   101   102 S
  103   100   100 Z+
  x     100   100 S
  104     x   100 S
  105   100     x S
  106   100   100
`, []int{100, 101, 102}},
		{"the program has exited", `
  101     1   100 S
  107   101   100 S
  108     1   100 R
  109   108   109 S
`, []int{101, 107, 108, 109}},
		{"nothing of it is left", ``, nil},
	} {
		if got := within(others+c.listing, []int{100}); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}
