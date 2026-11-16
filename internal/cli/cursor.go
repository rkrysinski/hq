package cli

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/tmux"
)

// A cursor is what next_since holds (ADR 0019): an opaque string standing
// for a number N of the host's one counter (ADR 0018) such that the look
// that gave it saw every change numbered up to N. It is the letter c and
// the number, so that it reads neither as a moment nor as a duration.
const cursorLetter = "c"

// cursorText is the cursor for number n.
func cursorText(n int64) string { return cursorLetter + strconv.FormatInt(n, 10) }

// since is what --since says (hq wait, hq read): a cursor as hq printed it,
// exact; or a moment (RFC 3339, as an older hq printed next_since) or a
// duration back from now, both compared with the time of a change and so
// approximate by nature.
type since struct {
	cursor   int64
	isCursor bool
	moment   time.Time
}

// parseSince reads --since at now: a cursor, a moment or a duration.
func parseSince(s string, now time.Time) (since, error) {
	if digits, ok := strings.CutPrefix(s, cursorLetter); ok && digits != "" && strings.Trim(digits, "0123456789") == "" {
		if n, err := strconv.ParseInt(digits, 10, 64); err == nil {
			return since{cursor: n, isCursor: true}, nil
		}
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return since{moment: t}, nil
	}
	if d, err := time.ParseDuration(s); err == nil && d >= 0 {
		return since{moment: now.Add(-d)}, nil
	}
	return since{}, usageErr("--since '%s' is neither a next_since as hq printed it (c1234), a moment (2026-09-28T10:25:01.25Z) nor a duration back from now (10m)", s)
}

// changedAfter reports whether agent a's last change, numbered seq, came after
// s: numbered above the cursor, or, for a moment, entered after it.
func (s since) changedAfter(a agent.Agent, seq int64) bool {
	if s.isCursor {
		return seq > s.cursor
	}
	return a.Entered().After(s.moment)
}

// ahead reports whether s is a cursor hq did not give: above the counter,
// as after a lost state directory (#166, decision 11). A counter that could
// not be read refuses nothing.
func (s since) ahead(l looked) bool {
	return s.isCursor && l.counted && s.cursor > l.counter
}

// aheadErr is the refusal of a cursor hq did not give.
func aheadErr(s since) error {
	return usageErr("--since '%s' is ahead of every change hq has numbered on this host (was its state directory lost?): take a new one from hq ls --json", cursorText(s.cursor))
}

// looked is a look's agents, with what its cursor is built from: the home
// windows it looked at, and what the counter held before they were listed.
type looked struct {
	agents  []agent.Agent
	windows []tmux.Window
	counter int64
	counted bool // the counter could be read
}

// cursor is the number the look's cursor stands for (ADR 0019): the lowest
// counter among its last reads of the agents' journals, so it saw every
// change of theirs numbered up to it, whatever other processes numbered
// meanwhile. It is the counter before the look listed the windows when
// that is all it can vouch for: no journal was read, one could not be, or
// an agent's window appeared while it looked (listed again, for that),
// whose changes it did not see but which are all numbered after that.
func (l looked) cursor(d deps) int64 {
	n, ok := agent.SeenThrough(l.agents)
	if !ok || n <= l.counter {
		return l.counter
	}
	ws, err := d.tmux.Windows()
	if err != nil || agentAppeared(l.windows, ws) {
		return l.counter
	}
	return n
}

// agentAppeared reports whether after holds the home window of an agent that
// before does not.
func agentAppeared(before, after []tmux.Window) bool {
	for _, w := range after {
		id := w.Options["id"]
		if id != "" && !slices.ContainsFunc(before, func(b tmux.Window) bool { return b.Options["id"] == id }) {
			return true
		}
	}
	return false
}

// counterNow is the cursor for a change an hq command is about to cause in
// agent a (a message, an answer): what the counter holds now, so what the
// agent does with it is numbered above it; the counter of a's read when
// the counter cannot be read now.
func counterNow(d deps, a agent.Agent) string { return cursorText(counterAt(d, a)) }

// counterAt is the number counterNow's cursor stands for.
func counterAt(d deps, a agent.Agent) int64 {
	n, err := d.journal.Through()
	if err != nil {
		n = a.Through()
	}
	return n
}

// seqs are an agent's two numbers as JSON gives them: null for none.
func seqs(a agent.Agent) (stateSeq, completionSeq *int64) {
	if n, ok := a.StateSeq(); ok {
		stateSeq = &n
	}
	if n, ok := a.CompletionSeq(); ok {
		completionSeq = &n
	}
	return stateSeq, completionSeq
}
