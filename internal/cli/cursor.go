package cli

import (
	"strconv"
	"strings"
	"time"

	"github.com/rkrysinski/hq/internal/agent"
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
	// from is, for a wait without --since, the state number each agent
	// of its first look stood at (0 for one with none): a change of such an
	// agent counts when numbered above it, though another process numbered
	// it before the call's cursor was read (#166).
	from map[string]int64
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
	if from, ok := s.from[a.ID]; ok {
		return seq > from
	}
	if s.isCursor {
		return seq > s.cursor
	}
	return a.Entered().After(s.moment)
}

// ahead reports whether s is a cursor hq did not give: above c, the
// counter before the look listed the windows (agent.Seen.Counter), as
// after a lost state directory (#166, decision 11). A counter that could
// not be read refuses nothing.
func (s since) ahead(c agent.Counter) bool {
	return s.isCursor && c.Read && s.cursor > c.N
}

// aheadErr is the refusal of a cursor hq did not give.
func aheadErr(s since) error {
	return usageErr("--since '%s' is ahead of every change hq has numbered on this host (was its state directory lost?): take a new one from hq ls --json", cursorText(s.cursor))
}

// cursorBefore is the cursor for a change an hq command is about to cause
// in agent a (a message, an answer; agent.Looker.CounterBefore).
func cursorBefore(d deps, a agent.Agent) string { return cursorText(d.looker().CounterBefore(a)) }

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
