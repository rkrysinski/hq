package testutil

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// process is a process as ps lists it: its pid, its parent's pid, its
// process group and its command line.
type process struct {
	pid, ppid, pgid int
	command         string
}

// within is the processes of a ps listing, a line each with its pid, its
// parent's pid, its process group, its state and its command line, that
// are of pids: one of them, below one of them, or in a process group one
// of them leads, which a process keeps when its parent exits before it
// (#191). A process that has exited and waits to be reaped (state Z) is
// none. They keep their order in the listing.
func within(listing string, pids []int) []process {
	var ps []process
	for _, line := range strings.Split(listing, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || strings.HasPrefix(f[3], "Z") {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		pgid, err3 := strconv.Atoi(f[2])
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		ps = append(ps, process{pid, ppid, pgid, strings.Join(f[4:], " ")})
	}
	of := map[int]bool{}
	for _, pid := range pids {
		of[pid] = true
	}
	for grown := true; grown; {
		grown = false
		for _, p := range ps {
			if !of[p.pid] && (of[p.ppid] || of[p.pgid]) {
				of[p.pid], grown = true, true
			}
		}
	}
	var out []process
	for _, p := range ps {
		if of[p.pid] {
			out = append(out, p)
		}
	}
	return out
}

// goneLooks and goneEvery bound how long waitGone waits: goneLooks looks,
// goneEvery apart, about 5 s in all.
const (
	goneLooks = 50
	goneEvery = 100 * time.Millisecond
)

// waitGone waits for what of pids runs (running) to exit, sleeping
// goneEvery between two looks, for goneLooks looks at most. What still
// runs at the last look it names through logf, each process by its pid and
// command, so a process a test leaves running in its panes' groups is seen
// rather than only paid for in time (#191).
func waitGone(pids []int, running func([]int) []process, sleep func(time.Duration), logf func(format string, args ...any)) {
	for range goneLooks - 1 {
		if len(running(pids)) == 0 {
			return
		}
		sleep(goneEvery)
	}
	left := running(pids)
	if len(left) == 0 {
		return
	}
	named := make([]string, len(left))
	for i, p := range left {
		named[i] = fmt.Sprintf("%d %q", p.pid, p.command)
	}
	logf("killServer: stopped waiting, still running: %s", strings.Join(named, ", "))
}
