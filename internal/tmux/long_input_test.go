package tmux

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

const (
	// longInputBytes is what the long input probe has tmux type.
	longInputBytes = 5000
	// longInputLimit is what reaches a pane's program of it in a Docker
	// sandbox (kernel 7.0), with every tmux from 3.4 to 3.7c, the rest
	// staying behind until more input comes: that machine's limit (#41).
	longInputLimit = 4095
)

// A longInputVerdict is what the paste test does after the long input probe.
type longInputVerdict int

const (
	runTheTest longInputVerdict = iota
	skipTheTest
	failTheTest
)

// judgeLongInput gives the verdict on the bytes of the long input probe that
// reached the pane's program within bound, and the reason of a skip or a
// failure. All of them run the paste test, the count of the limit seen in a
// Docker sandbox skips it, and any other count fails it: it is not that
// limit, so it may be a broken paste path (#270).
func judgeLongInput(got int, bound time.Duration) (longInputVerdict, string) {
	switch got {
	case longInputBytes:
		return runTheTest, ""
	case longInputLimit:
		return skipTheTest, fmt.Sprintf("a pane's program got %d of %d bytes of a long input tmux typed, without hq, within %v: the limit of tmux in a Docker sandbox (#41)", got, longInputBytes, bound)
	}
	return failTheTest, fmt.Sprintf("a pane's program got %d of %d bytes of a long input tmux typed, without hq, within %v: neither all of them nor the %d of the limit of tmux in a Docker sandbox (#41)", got, longInputBytes, bound, longInputLimit)
}

func TestTheLongInputVerdictRunsOnAllBytesSkipsOnTheMachinesLimitAndFailsOnAnyOtherCount(t *testing.T) {
	for _, c := range []struct {
		got  int
		want longInputVerdict
	}{
		{5000, runTheTest},
		{4095, skipTheTest},
		{0, failTheTest},
		{1, failTheTest},
		{4094, failTheTest},
		{4096, failTheTest},
		{4999, failTheTest},
		{5001, failTheTest},
	} {
		v, reason := judgeLongInput(c.got, 5*time.Second)
		if v != c.want {
			t.Errorf("%d bytes: verdict %d, want %d (%q)", c.got, v, c.want, reason)
		}
		if c.want == runTheTest {
			continue
		}
		if figures := fmt.Sprintf("%d of 5000 bytes", c.got); !strings.Contains(reason, figures) {
			t.Errorf("%d bytes: reason %q does not say %q", c.got, reason, figures)
		}
		if !strings.Contains(reason, "within 5s") {
			t.Errorf("%d bytes: reason %q does not name the bound", c.got, reason)
		}
	}
}
