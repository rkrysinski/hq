package cli

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/state"
)

// DefaultWaitTimeout is how long hq wait waits when --timeout is not given:
// a little below the 60 s an MCP client commonly allows a tool call (the MCP
// SDKs' default request timeout), so hq wait called as a tool returns
// "nothing yet" before its caller gives up on it. The hq mcp issue (#136)
// sets it from Claude Desktop's measured limit.
const DefaultWaitTimeout = 50 * time.Second

// How hq wait looks (design §3.4 "Entered after a moment", §5.1).
const (
	// waitEvery is how often it looks, as the list does.
	waitEvery = 250 * time.Millisecond
	// waitSettle is how long after a moment a look is sure to see every
	// state entered by then: a state file's time is set as the hook writes
	// it, a moment before it is moved into place, and a moment hq saw is
	// taken just before it is stored on the window. A look reports only
	// what entered its state at least this long before it began, and its
	// moment, the next call's --since, is that far back, so what a look
	// could not see yet falls to the next one.
	waitSettle = 250 * time.Millisecond
	// waitSbxEvery is how often it asks sbx which sandboxes run, as the
	// list does; the answer comes in the background, so a slow sbx never
	// holds up a look.
	waitSbxEvery = time.Second
)

// waitStates are the states hq wait returns an agent in (spec §4.1).
var waitStates = []string{state.Done, state.Question, state.NeedsInput, state.Ended}

// waitOutput is what hq wait --json prints.
type waitOutput struct {
	Agents    []lsRow   `json:"agents"`
	NextSince time.Time `json:"next_since"`
}

type waitArgs struct {
	names    []string
	since    time.Time // zero: from the call's start
	timeout  time.Duration
	asJSON   bool
	sinceArg bool
}

const waitUsage = "hq wait [NAME...] [--since TIME] [--timeout DURATION] [--json]"

func parseWait(args []string, now time.Time) (waitArgs, error) {
	w := waitArgs{timeout: DefaultWaitTimeout}
	for i := 0; i < len(args); i++ {
		a := args[i]
		flag, value, inline := strings.Cut(a, "=")
		switch flag {
		case "--json":
			if inline {
				return w, usageErr("unexpected argument '%s' (usage: %s)", a, waitUsage)
			}
			w.asJSON = true
			continue
		case "--since", "--timeout":
			if !inline {
				if i+1 == len(args) {
					return w, usageErr("%s needs a value (usage: %s)", flag, waitUsage)
				}
				i++
				value = args[i]
			}
			var err error
			if flag == "--since" {
				w.since, err = parseSince(value, now)
				w.sinceArg = true
			} else {
				w.timeout, err = parseTimeout(value)
			}
			if err != nil {
				return w, err
			}
			continue
		}
		if strings.HasPrefix(a, "-") {
			return w, usageErr("unexpected argument '%s' (usage: %s)", a, waitUsage)
		}
		if !slices.Contains(w.names, a) {
			w.names = append(w.names, a)
		}
	}
	return w, nil
}

// parseSince reads --since: a moment as hq wait prints it (RFC 3339), or a
// duration back from now (10m).
func parseSince(s string, now time.Time) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	if d, err := time.ParseDuration(s); err == nil && d >= 0 {
		return now.Add(-d), nil
	}
	return time.Time{}, usageErr("--since '%s' is neither a moment as hq wait prints it (2026-09-28T10:25:01.25Z) nor a duration back from now (10m)", s)
}

// parseTimeout reads --timeout: a duration (30s, 5m) or whole seconds; 0
// waits with no limit.
func parseTimeout(s string) (time.Duration, error) {
	if n, err := strconv.Atoi(s); err == nil && n >= 0 {
		return time.Duration(n) * time.Second, nil
	}
	if d, err := time.ParseDuration(s); err == nil && d >= 0 {
		return d, nil
	}
	return 0, usageErr("--timeout '%s' is not a duration (30s, 5m; 0 waits with no limit)", s)
}

// runWait returns as soon as an agent it waits on enters done, question,
// needs input or ended after --since (spec §4.1, design §3.4), or at the
// timeout with nothing. It looks as the list does, through the same
// collection (design §3.8), and leaves nothing running when it returns.
func runWait(env Env, d deps, args []string) error {
	start := d.now()
	w, err := parseWait(args, start)
	if err != nil {
		return err
	}
	if err := checkTmux(d.tmux); err != nil {
		return err
	}
	sbxs := &sbxPoll{ask: func() map[string]bool { return runningSandboxes(d) }}
	var seen map[string]agent.Agent // the agents waited on at the last look, by id
	for first := true; ; first = false {
		at := d.now()
		as, err := collectWith(d, sbxs.running(at, first))
		if err != nil {
			return err
		}
		if first {
			for _, n := range w.names {
				if _, ok := agent.Find(as, n); !ok {
					return notFoundErr("no agent '%s' (see hq ls)", n)
				}
			}
			// An agent already in its state when the call starts is not
			// returned, or a caller waiting in a loop would spin.
			if !w.sinceArg {
				w.since = d.now()
			}
		}
		watched := waitedOn(as, w.names)
		next := later(w.since, at.Add(-waitSettle))
		hits := arrived(watched, w.since, next)
		hits = append(hits, gone(seen, watched, d.now())...)
		if len(hits) > 0 || (w.timeout > 0 && !d.now().Before(start.Add(w.timeout))) {
			agent.SortAttention(hits)
			return printWait(env.Stdout, w, lsRows(hits, d.now()), next)
		}
		seen = map[string]agent.Agent{}
		for _, a := range watched {
			seen[a.ID] = a
		}
		d.sleep(waitEvery)
	}
}

// later is the later of two moments.
func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// waitedOn are the agents named, or all when none are.
func waitedOn(as []agent.Agent, names []string) []agent.Agent {
	if len(names) == 0 {
		return as
	}
	var ws []agent.Agent
	for _, a := range as {
		if slices.Contains(names, a.Name) {
			ws = append(ws, a)
		}
	}
	return ws
}

// arrived are the agents that entered a state hq wait returns after since
// and by until (design §3.4, "Entered after a moment").
func arrived(as []agent.Agent, since, until time.Time) []agent.Agent {
	var hits []agent.Agent
	for _, a := range as {
		e := a.Entered()
		if slices.Contains(waitStates, a.State) && e.After(since) && !e.After(until) {
			hits = append(hits, a)
		}
	}
	return hits
}

// gone are the agents waited on at the last look that no longer exist
// (killed), as ended now with what they last showed.
func gone(seen map[string]agent.Agent, now []agent.Agent, at time.Time) []agent.Agent {
	var out []agent.Agent
	for id, a := range seen {
		if !slices.ContainsFunc(now, func(b agent.Agent) bool { return b.ID == id }) {
			a.State, a.Since = state.Ended, at
			if a.Last == "" {
				a.Last = agent.EndedLast
			}
			out = append(out, a)
		}
	}
	return out
}

func printWait(out io.Writer, w waitArgs, rows []lsRow, next time.Time) error {
	next = next.UTC()
	if w.asJSON {
		return writeJSON(out, waitOutput{Agents: rows, NextSince: next})
	}
	if len(rows) == 0 {
		fmt.Fprintln(out, "nothing yet")
	} else if err := writeTable(out, rows); err != nil {
		return err
	}
	names := strings.Join(w.names, " ")
	if names != "" {
		names += " "
	}
	_, err := fmt.Fprintf(out, "next: hq wait %s--since %s\n", names, next.Format(time.RFC3339Nano))
	return err
}

// sbxPoll asks sbx which sandboxes run at most every waitSbxEvery, in the
// background, and gives the latest answer (nil: sbx could not say, which
// changes no agent).
type sbxPoll struct {
	ask     func() map[string]bool
	answers chan map[string]bool
	asked   time.Time
	busy    bool
	latest  map[string]bool
}

// running is sbx's latest answer at now; wait asks and waits for the answer.
func (p *sbxPoll) running(now time.Time, wait bool) map[string]bool {
	if wait {
		p.latest, p.asked = p.ask(), now
		return p.latest
	}
	if p.busy {
		select {
		case p.latest = <-p.answers:
			p.busy = false
		default:
		}
	}
	if !p.busy && now.Sub(p.asked) >= waitSbxEvery {
		if p.answers == nil {
			p.answers = make(chan map[string]bool, 1)
		}
		p.busy, p.asked = true, now
		go func() { p.answers <- p.ask() }()
	}
	return p.latest
}
