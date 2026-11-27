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

// DefaultWaitTimeout is how long hq wait waits when --timeout is not given,
// and the most the MCP wait tool waits: below the 60 s Claude Desktop allows
// a tool call of a local server (design §3.12), so hq wait called as a tool
// returns "nothing yet" before its caller gives up on it.
const DefaultWaitTimeout = 50 * time.Second

// waitStates are the states hq wait returns an agent in (spec §4.1).
var waitStates = []string{state.Done, state.Question, state.NeedsInput, state.Ended}

// waitOutput is what hq wait --json prints.
type waitOutput struct {
	Agents []lsRow `json:"agents"`
	// NextSince is the look's cursor (ADR 0019), to pass back as given.
	NextSince string `json:"next_since"`
}

type waitArgs struct {
	names    []string
	since    since // without sinceArg: from the call's first look
	timeout  time.Duration
	asJSON   bool
	sinceArg bool
}

const waitUsage = "hq wait [NAME...] [--since SINCE] [--timeout DURATION] [--json]"

func parseWait(args []string, now time.Time) (waitArgs, error) {
	w := waitArgs{timeout: DefaultWaitTimeout}
	f := flagsOf("wait")
	for i := 0; i < len(args); i++ {
		a := args[i]
		flag, value, inline := strings.Cut(a, "=")
		switch {
		case f.is(flag, "--json"):
			if inline {
				return w, unknownOption(a, waitUsage)
			}
			w.asJSON = true
			continue
		case f.is(flag, "--since", "--timeout"):
			value, err := optionValue(args, &i, flag, value, inline, waitUsage)
			if err != nil {
				return w, err
			}
			if f.is(flag, "--since") {
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
		var err error
		if w.names, err = addName(w.names, a, waitUsage); err != nil {
			return w, err
		}
	}
	return w, nil
}

// addName adds a, an argument that is none of the command's options, to
// the agents' names it takes (hq wait, hq ls): an argument that looks like
// an option is an unknown one, refused with the command's usage, and a
// name given twice counts once.
func addName(names []string, a, usage string) ([]string, error) {
	if strings.HasPrefix(a, "-") {
		return names, unknownOption(a, usage)
	}
	if !slices.Contains(names, a) {
		names = append(names, a)
	}
	return names, nil
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

// runWait returns as soon as an agent it waits on changes into done,
// question, needs input or ended after --since (spec §4.1, design §3.4),
// with those that do so by its next look, or at the timeout with nothing:
// it asks each look for the wait's result (waitTerms.resultAt). Every
// result carries the cursor of its look. It takes its looks from the look
// loop (agent.Looker.Loop), in the list's rhythm (design §3.8, §5.1).
func runWait(env Env, d deps, args []string) error {
	start := d.now()
	w, err := parseWait(args, start)
	if err != nil {
		return err
	}
	if err := checkTmux(d.tmux); err != nil {
		return err
	}
	terms := waitTerms{names: w.names, since: w.since}
	if w.timeout > 0 {
		terms.deadline = start.Add(w.timeout)
	}
	var found []agent.Agent // what the first look with a hit returned
	k := d.looker()
	err = k.Loop(func(s agent.Seen, first bool) (bool, error) {
		next := k.Cursor(s) // taken before what waits is typed in
		as := s.Agents
		deliverWaiting(d, as)
		if first {
			if terms.since.ahead(s.Counter()) {
				return true, aheadErr(terms.since)
			}
			for _, n := range w.names {
				if _, err := s.Usable(n); err != nil {
					return true, refusalErr(err)
				}
			}
			// An agent already in its state when the call starts is not
			// returned, or a caller waiting in a loop would spin: the call
			// starts each agent of its first look from the state number that
			// look showed for it, so a change another process numbers after
			// that look, while deliverWaiting types, is still returned. An
			// agent the first look did not show starts after everything
			// numbered once that look is done, including what it appended
			// for an agent after it read another, which its cursor does not
			// yet stand for.
			if !w.sinceArg {
				terms.since = since{cursor: next, isCursor: true, from: map[string]int64{}}
				for _, a := range as {
					terms.since.from[a.ID], _ = a.StateSeq()
				}
				if n, err := d.journal.Through(); err == nil {
					terms.since.cursor = n
				}
			}
		}
		r := terms.resultAt(s, next, d.now())
		if found != nil {
			hits := joined(found, r.agents)
			agent.SortAttention(hits)
			return true, printWait(env.Stdout, w, lsRows(d, hits), next)
		}
		if len(r.agents) > 0 {
			// One look more, so agents that change together are returned
			// together (#116).
			found = r.agents
		} else if r.late {
			return true, printWait(env.Stdout, w, lsRows(d, nil), next)
		}
		terms.covered = r.covered
		return false, nil
	})
	return lookErr(err)
}

// waitTerms are what a wait waits for (#358): all that its result at a
// look depends on but that look. The call holds them from one look to the
// next (runWait), and any look can be asked for the result of terms no
// call holds, as a wait task's will be (ADR 0022).
type waitTerms struct {
	// names are the agents waited on: every agent but the outdated ones
	// when none are named (waitedOn).
	names []string
	// since is what a change has to come after to be returned (arrived).
	since since
	// covered are the agents the previous look waited on, none before the
	// first look: one a look no longer shows, or shows ended with no
	// number where it had one, is returned ended (gone).
	covered []agent.Agent
	// deadline is when the wait returns with no agents; zero for none.
	deadline time.Time
}

// waitResult is what a wait finds at a look (waitTerms.resultAt).
type waitResult struct {
	// agents are the agents to return, none while nothing has changed.
	agents []agent.Agent
	// late says the deadline has passed with no agent to return: the wait
	// returns none.
	late bool
	// covered are the agents the look waited on, for the next look's
	// terms.
	covered []agent.Agent
}

// resultAt is what a wait with terms t finds at look s, whose cursor is
// through, at now (design §3.4, "Changes after a cursor"): the agents it
// waits on that changed after since into a state it returns, by a change
// numbered at most through (arrived), and those covered that are gone
// (gone); or, with none, whether the deadline has passed. A change after a
// cursor is one numbered above it and seen by the look, so it is returned
// with no settle time; after a moment, one entered after it. A call that
// finds agents returns them with those of its next look (runWait).
func (t waitTerms) resultAt(s agent.Seen, through int64, now time.Time) waitResult {
	watched := waitedOn(s.Agents, t.names)
	hits := append(arrived(watched, t.since, through), gone(t.covered, watched, now)...)
	late := len(hits) == 0 && !t.deadline.IsZero() && !now.Before(t.deadline)
	return waitResult{agents: hits, late: late, covered: watched}
}

// joined are the agents of the last look, and those of the look before it
// that the last one no longer returns: one that has moved on still entered
// its state after since.
func joined(before, last []agent.Agent) []agent.Agent {
	out := slices.Clone(last)
	for _, a := range before {
		if !slices.ContainsFunc(last, func(b agent.Agent) bool { return b.ID == a.ID }) {
			out = append(out, a)
		}
	}
	return out
}

// waitedOn are the agents named, or all when none are but the outdated
// ones, whose state hq cannot read (spec §4.1).
func waitedOn(as []agent.Agent, names []string) []agent.Agent {
	var ws []agent.Agent
	for _, a := range as {
		if len(names) == 0 && !a.Outdated() || slices.Contains(names, a.Name) {
			ws = append(ws, a)
		}
	}
	return ws
}

// returnable reports whether a look whose cursor is through may return
// agent a's last change (design §3.4, "Changes after a cursor"), and gives
// its number: a is in a state hq wait returns, by a change numbered at most
// through, so no change is returned that a result's cursor does not stand
// for; or a has ended with no number (its journal can no longer be read),
// whatever the cursor, which only a call that saw it before returns (gone).
// hq wait and hq send --wait both decide by it.
func returnable(a agent.Agent, through int64) (seq int64, numbered, ok bool) {
	seq, numbered = a.StateSeq()
	ok = slices.Contains(waitStates, a.State) && (numbered && seq <= through || !numbered && a.State == state.Ended)
	return seq, numbered, ok
}

// arrived are the agents whose last change, numbered at most through, the
// look's cursor, put them in a state hq wait returns after s (returnable).
func arrived(as []agent.Agent, s since, through int64) []agent.Agent {
	var hits []agent.Agent
	for _, a := range as {
		if seq, numbered, ok := returnable(a, through); ok && numbered && s.changedAfter(a, seq) {
			hits = append(hits, a)
		}
	}
	return hits
}

// gone are the agents waited on at the last look that ended with no event
// behind it since (#166, decision 10 and 3A): those that no longer exist
// (killed), as ended now with what they last showed (agent.Gone), and those
// whose journal can no longer be read and whose pane is dead. They have no
// number to compare, so only a wait that covered them returns them.
func gone(covered, now []agent.Agent, at time.Time) []agent.Agent {
	var out []agent.Agent
	for _, a := range covered {
		i := slices.IndexFunc(now, func(b agent.Agent) bool { return b.ID == a.ID })
		if i < 0 {
			out = append(out, a.Gone(at))
			continue
		}
		_, numbered, ok := returnable(now[i], 0)
		if _, was := a.StateSeq(); ok && !numbered && (was || a.State != state.Ended) {
			out = append(out, now[i])
		}
	}
	return out
}

func printWait(out io.Writer, w waitArgs, rows []lsRow, cursor int64) error {
	next := cursorText(cursor)
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
	_, err := fmt.Fprintf(out, "next: hq wait %s--since %s\n", names, next)
	return err
}
