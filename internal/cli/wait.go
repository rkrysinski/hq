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

// How hq wait looks (design §3.4 "Changes after a cursor", §5.1).
const (
	// waitEvery is how often it looks, as the list does.
	waitEvery = 250 * time.Millisecond
	// waitSbxEvery is how often it asks sbx which sandboxes run, as the
	// list does; the answer comes in the background, so a slow sbx never
	// holds up a look.
	waitSbxEvery = time.Second
)

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
// with those that do so by its next look, or at the timeout with nothing.
// A change after a cursor is one numbered above it and seen by the look,
// so it is returned with no settle time; after a moment, one entered after
// it. Every result carries the cursor of its look. It looks as the list
// does, through the same collection (design §3.8), and leaves nothing
// running when it returns.
func runWait(env Env, d deps, args []string) error {
	start := d.now()
	w, err := parseWait(args, start)
	if err != nil {
		return err
	}
	if err := checkTmux(d.tmux); err != nil {
		return err
	}
	sbxs := &sbxPoll{ask: func() agent.Sandboxes { return runningSandboxes(d) }}
	var seen map[string]agent.Agent // the agents waited on at the last look, by id
	var found []agent.Agent         // what the first look with a hit returned
	for first := true; ; first = false {
		l, err := lookWith(d, sbxs.running(d.now(), first))
		if err != nil {
			return err
		}
		as, next := l.agents, l.cursor(d)
		deliverWaiting(d, as)
		if first {
			if w.since.ahead(l) {
				return aheadErr(w.since)
			}
			for _, n := range w.names {
				a, ok := agent.Find(as, n)
				if !ok {
					return notFoundErr("no agent '%s' (see hq ls)", n)
				}
				if a.Outdated() {
					return outdatedErr(a)
				}
			}
			// An agent already in its state when the call starts is not
			// returned, or a caller waiting in a loop would spin: the call
			// starts after everything its first look showed, including what
			// that look appended for an agent after it read another, which
			// its cursor does not yet stand for.
			if !w.sinceArg {
				w.since = since{cursor: next, isCursor: true}
				if n, err := d.journal.Through(); err == nil {
					w.since.cursor = n
				}
			}
		}
		watched := waitedOn(as, w.names)
		hits := arrived(watched, w.since, next)
		hits = append(hits, gone(seen, watched, d.now())...)
		if found != nil {
			hits = joined(found, hits)
			agent.SortAttention(hits)
			return printWait(env.Stdout, w, lsRows(d, hits), next)
		}
		if len(hits) > 0 {
			// One look more, so agents that change together are returned
			// together (#116).
			found = hits
		} else if w.timeout > 0 && !d.now().Before(start.Add(w.timeout)) {
			return printWait(env.Stdout, w, lsRows(d, nil), next)
		}
		seen = map[string]agent.Agent{}
		for _, a := range watched {
			seen[a.ID] = a
		}
		d.sleep(waitEvery)
	}
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

// arrived are the agents whose last change, numbered at most through, put
// them in a state hq wait returns after s (design §3.4, "Changes after a
// cursor"): through is the look's cursor, so no change is returned that a
// result's cursor does not stand for.
func arrived(as []agent.Agent, s since, through int64) []agent.Agent {
	var hits []agent.Agent
	for _, a := range as {
		seq, ok := a.StateSeq()
		if ok && seq <= through && slices.Contains(waitStates, a.State) && s.changedAfter(a, seq) {
			hits = append(hits, a)
		}
	}
	return hits
}

// gone are the agents waited on at the last look that ended with no event
// behind it since (#166, decision 10 and 3A): those that no longer exist
// (killed), as ended now with what they last showed (agent.Gone), and those
// whose journal can no longer be read and whose pane is dead. They have no
// number to compare, so only the call that saw them before returns them.
func gone(seen map[string]agent.Agent, now []agent.Agent, at time.Time) []agent.Agent {
	var out []agent.Agent
	for id, a := range seen {
		i := slices.IndexFunc(now, func(b agent.Agent) bool { return b.ID == id })
		if i < 0 {
			out = append(out, a.Gone(at))
			continue
		}
		_, numbered := now[i].StateSeq()
		if _, was := a.StateSeq(); now[i].State == state.Ended && !numbered && (was || a.State != state.Ended) {
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

// sbxPoll asks sbx which sandboxes run at most every waitSbxEvery, in the
// background, and gives the latest answer (which says nothing when sbx
// could not say, and changes no agent then).
type sbxPoll struct {
	ask     func() agent.Sandboxes
	answers chan agent.Sandboxes
	asked   time.Time
	busy    bool
	latest  agent.Sandboxes
}

// running is sbx's latest answer at now; wait asks and waits for the answer.
func (p *sbxPoll) running(now time.Time, wait bool) agent.Sandboxes {
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
			p.answers = make(chan agent.Sandboxes, 1)
		}
		p.busy, p.asked = true, now
		go func() { p.answers <- p.ask() }()
	}
	return p.latest
}
