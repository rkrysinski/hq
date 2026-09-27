package main

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// coverage returns the statement coverage, in percent, of a profile written by
// go test -coverprofile. A block listed several times (profiles appended from
// several runs) counts as covered when any run covered it.
func coverage(r io.Reader) (float64, error) {
	type block struct {
		stmts   int
		covered bool
	}
	blocks := map[string]*block{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 3 {
			return 0, fmt.Errorf("bad profile line %q", line)
		}
		stmts, err1 := strconv.Atoi(f[1])
		count, err2 := strconv.Atoi(f[2])
		if err1 != nil || err2 != nil {
			return 0, fmt.Errorf("bad profile line %q", line)
		}
		b := blocks[f[0]]
		if b == nil {
			b = &block{stmts: stmts}
			blocks[f[0]] = b
		}
		b.covered = b.covered || count > 0
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	total, covered := 0, 0
	for _, b := range blocks {
		total += b.stmts
		if b.covered {
			covered += b.stmts
		}
	}
	if total == 0 {
		return 100, nil
	}
	return 100 * float64(covered) / float64(total), nil
}
