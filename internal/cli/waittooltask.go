package cli

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/rkrysinski/hq/internal/agent"
)

// A waitToolTask is a wait task of the wait tool (waitTask): a wait that
// saw no change within its hold, waiting on for one (ADR 0022).
type waitToolTask struct {
	// taskHeader holds the cursor of its call's last look, which a change
	// has to come after, and its call's timeout as its deadline.
	taskHeader
	// names are the agents it waits for, every agent when none.
	names []string
	// covered are the agents its call's last look waited on, each with its
	// id and name alone, so one killed since is returned ended (gone) with
	// what the trace of kills says of it (traced): the id is printed into
	// the supervisor's context, twice, so it carries nothing more. One
	// that look saw ended with no number is not among them, since it is
	// not returned again (gone).
	covered []agent.Agent
	// nonce tells apart tasks of the same terms created at the same moment.
	nonce string
}

// waitToolTaskKind starts the id of every wait task of the wait tool: its
// kind and version.
const waitToolTaskKind = "w1"

// terms are what t's task id carries, which taskid.Sign signs: its header
// (taskHeader.fields), nonce, names and covered agents (each its name, =
// and its id), between colons, the names and the agents between commas,
// which neither a name nor an agent's id holds.
func (t waitToolTask) terms() string {
	covered := make([]string, len(t.covered))
	for i, a := range t.covered {
		covered[i] = a.Name + "=" + a.ID
	}
	return strings.Join(append(t.fields(), t.nonce, strings.Join(t.names, ","), strings.Join(covered, ",")), ":")
}

// parseWaitToolTask is the wait tool's task whose id carries the terms f,
// or false for terms no such task's id carries.
func parseWaitToolTask(f []string) (waitToolTask, bool) {
	h, ok := parseTaskHeader(f, waitToolTaskKind, 7)
	if !ok {
		return waitToolTask{}, false
	}
	t := waitToolTask{taskHeader: h, nonce: f[4]}
	if f[5] != "" {
		t.names = strings.Split(f[5], ",")
	}
	if f[6] != "" {
		for _, c := range strings.Split(f[6], ",") {
			name, id, named := strings.Cut(c, "=")
			ok = ok && named && id != "" && checkName(name) == nil
			t.covered = append(t.covered, agent.Agent{ID: id, Name: name})
		}
	}
	return t, ok && !slices.ContainsFunc(t.names, func(n string) bool { return checkName(n) != nil })
}

// look takes a look for t as a wait's call takes each look (waitFor, after
// its first), delivering what waits as each look of a wait does, and
// prints the wait's result to out, as the wait tool prints it, once it
// returns: after a look that finds agents it takes one look more, so
// agents that change together are returned together (#116), and once the
// deadline has passed it returns none. done says that it returned, or
// failed as the call would, tmux gone or a look failing: the task has then
// completed.
func (t waitToolTask) look(out io.Writer, d deps) (done bool, err error) {
	ongoing := ongoingWait{terms: waitTerms{names: t.names, since: since{cursor: t.cursor, isCursor: true}, covered: t.covered, deadline: t.deadline}}
	var r waitEnd
	err = lookFor(d, func(s agent.Seen, next int64, first bool) (bool, error) {
		deliverWaiting(d, s.Agents)
		if first {
			ongoing.terms.covered = traced(d, ongoing.terms.covered, s.Agents)
		}
		r, done = ongoing.at(s, next, d.now())
		return !done && ongoing.found != nil, nil
	})
	if err != nil {
		return true, err
	}
	if !done {
		return false, nil
	}
	return true, printWait(out, waitArgs{names: t.names, asJSON: true}, lsRows(d, r.agents), r.cursor)
}

// carried are the agents a wait task covers of those the last look of its
// call waited on (waitToolTask.covered): each with its id and name, but one
// ended with no number, which gone does not return again.
func carried(covered []agent.Agent) []agent.Agent {
	var out []agent.Agent
	for _, a := range covered {
		if _, numbered, ok := returnable(a, 0); ok && !numbered {
			continue
		}
		out = append(out, agent.Agent{ID: a.ID, Name: a.Name})
	}
	return out
}

// newNonce is a nonce for a wait task's id: two calls of wait in one
// message may go on as tasks of the same terms in the same millisecond.
func newNonce() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b) // never fails, the package says
	return base64.RawURLEncoding.EncodeToString(b)
}

// waitOrHandOff is hq wait with args for a call of the wait tool that may
// go on as a task (mcptasks.Offered), args holding the call's timeout, 0
// for none: what it returns within waitHold, or within its timeout when
// that is shorter, as runWait prints it; or, when the hold passes with no
// agents, nothing, the call going on as a wait task from the cursor of its
// last look, which stands for every change that look saw (ADR 0022), until
// its timeout when it has one, its id signed with the key of hq's state
// directory, which the first such task makes; or no agents, as at a
// timeout, when the hold has ended at or past it; or the error of a key it
// can neither read nor make, since no hq mcp could tell its task from one
// made up.
func waitOrHandOff(ctx context.Context, env Env, d deps, args []string) error {
	start := d.now()
	w, err := parseWait(args, start)
	if err != nil {
		return err
	}
	hold := w
	hold.timeout = waitHold
	if w.timeout > 0 {
		hold.timeout = min(waitHold, w.timeout)
	}
	r, err := waitFor(d, hold, start)
	if err != nil {
		return err
	}
	if len(r.agents) > 0 {
		return printWait(env.Stdout, w, lsRows(d, r.agents), r.cursor)
	}
	h, due := newTaskHeader(waitToolTaskKind, r.cursor, d.now(), start, w.timeout)
	if due {
		return printWait(env.Stdout, w, lsRows(d, nil), r.cursor)
	}
	t := waitToolTask{taskHeader: h, names: w.names, covered: carried(r.covered), nonce: newNonce()}
	if err := handOff(ctx, d, t); err != nil {
		return fmt.Errorf("the wait cannot go on as a task: %w", err)
	}
	return nil
}
