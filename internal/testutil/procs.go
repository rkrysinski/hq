package testutil

import (
	"strconv"
	"strings"
)

// within is the processes of a ps listing, a line each with its pid, its
// parent's pid, its process group and its state, that are of pids: one of
// them, below one of them, or in a process group one of them leads, which
// a process keeps when its parent exits before it (#191). A process that
// has exited and waits to be reaped (state Z) is none. The pids keep their
// processes' order in the listing.
func within(listing string, pids []int) []int {
	type process struct{ pid, ppid, pgid int }
	var ps []process
	for _, line := range strings.Split(listing, "\n") {
		f := strings.Fields(line)
		if len(f) != 4 || strings.HasPrefix(f[3], "Z") {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		pgid, err3 := strconv.Atoi(f[2])
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		ps = append(ps, process{pid, ppid, pgid})
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
	var out []int
	for _, p := range ps {
		if of[p.pid] {
			out = append(out, p.pid)
		}
	}
	return out
}
