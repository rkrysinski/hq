package testutil

import (
	"fmt"
	"slices"
	"testing"
	"time"
)

// What a pane's program started counts as its own until it has exited:
// what runs below it and what is left of its process group once its parent
// has gone, as a hook the fake Claude started after the server's hangup
// (#191); not another program's, nor a process that waits to be reaped.
func TestWhatAPanesProgramStartedCountsUntilItHasExited(t *testing.T) {
	const others = `
    1     0     1 Ss   /sbin/init
  200     1   200 Ss   sleep 60
  201   200   200 S+   sh -c true
`
	for _, c := range []struct {
		name, listing string
		want          []process
	}{
		{"the program runs", `
  100    90   100 Ss   hq session
  101   100   100 S+   claude --settings {}
  102   101   102 S    git branch --show-current
  103   100   100 Z+   [sh] <defunct>
  x     100   100 S    sh
  104     x   100 S    sh
  105   100     x S    sh
  106   100   100 S
`, []process{{100, 90, 100, "hq session"}, {101, 100, 100, "claude --settings {}"}, {102, 101, 102, "git branch --show-current"}}},
		{"the program has exited", `
  101     1   100 S    claude
  107   101   100 S    sh -c eval "$HQ_HOOK" hq-hook stop
  108     1   100 R    awk
  109   108   109 S    sleep 1
`, []process{{101, 1, 100, "claude"}, {107, 101, 100, `sh -c eval "$HQ_HOOK" hq-hook stop`}, {108, 1, 100, "awk"}, {109, 108, 109, "sleep 1"}}},
		{"nothing of it is left", ``, nil},
	} {
		if got := within(others+c.listing, []int{100}); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// The cleanup waits for what runs in a test's panes, looking again every
// 100 ms; what still runs at its last look it names in the test's log,
// each process by its pid and command, rather than return as if all had
// ended (#191).
func TestTheCleanupNamesWhatStillRunsWhenItStopsWaiting(t *testing.T) {
	left := []process{{4242, 1, 4200, "sh -c trap '' HUP; sleep 60"}, {4243, 4242, 4200, "sleep 60"}}
	for _, c := range []struct {
		name        string
		runs, looks int
		logged      []string
	}{
		{"gone at the third look", 2, 3, nil},
		{"gone at the last look", goneLooks - 1, goneLooks, nil},
		{"still running at the last look", goneLooks, goneLooks,
			[]string{`killServer: stopped waiting, still running: 4242 "sh -c trap '' HUP; sleep 60", 4243 "sleep 60"`}},
	} {
		looks := 0
		var slept []time.Duration
		var logged []string
		running := func(pids []int) []process {
			if looks++; looks > c.runs || !slices.Equal(pids, []int{4200}) {
				return nil
			}
			return left
		}
		sleep := func(d time.Duration) { slept = append(slept, d) }
		logf := func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }
		waitGone([]int{4200}, running, sleep, logf)
		if looks != c.looks || len(slept) != c.looks-1 || slices.ContainsFunc(slept, func(d time.Duration) bool { return d != 100*time.Millisecond }) {
			t.Errorf("%s: %d looks, slept %v", c.name, looks, slept)
		}
		if !slices.Equal(logged, c.logged) {
			t.Errorf("%s: logged %q, want %q", c.name, logged, c.logged)
		}
	}
}
