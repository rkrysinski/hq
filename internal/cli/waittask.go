package cli

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/kills"
	"github.com/rkrysinski/hq/internal/mcptasks"
	"github.com/rkrysinski/hq/internal/taskid"
)

// waitHold is how long the wait tool waits in its call under Claude Code
// before it goes on as a wait task (ADR 0022): as long as a wait lasted
// there before #359, so a change within it comes back in the same call and
// turn, well within the 120 s after which Claude Code moves a call to the
// background.
const waitHold = DefaultWaitTimeout

// waitTaskPoll is how often Claude Code is told to ask for a wait task
// (pollIntervalMs, which it takes from 250 ms to 30 s), set by measurement
// (#361): Claude Code 2.1.332 asked every 5.01 s, never with a model
// request, and hq answered each poll with one look and one sbx ls in 6 to
// 22 ms, about 14 ms of CPU, against the stub sbx. So a task's result comes
// at most 5 s after the change it returns, little beside the minutes of an
// agent's turn, for about 720 looks an hour; and a poll whose sbx ls takes
// all of sbxPollTimeout, as long, ends before the next one comes.
const waitTaskPoll = 5 * time.Second

// A waitTask is a wait that goes on after its call as an MCP task
// (GLOSSARY.md, ADR 0022). Its id carries all it waits for, signed with
// the key of hq's state directory (taskid), so any hq mcp answers for it by
// taking a look, none holds it in memory, and no one builds one from the
// terms alone.
type waitTask struct {
	// names are the agents it waits for, every agent when none, and cursor
	// what a change has to come after: the cursor of its call's last look.
	names  []string
	cursor int64
	// covered are the agents its call's last look waited on, each with its
	// id and name alone, so one killed since is returned ended (gone) with
	// what the trace of kills says of it (traced): the id is printed into
	// the supervisor's context, twice, so it carries nothing more. One
	// that look saw ended with no number is not among them, since it is
	// not returned again (gone).
	covered []agent.Agent
	// deadline is when it completes with no agents: its call's timeout,
	// to the millisecond; zero for none.
	deadline time.Time
	// created is when the call went on as the task, to the millisecond, and
	// nonce tells apart tasks of the same terms created at the same moment.
	created time.Time
	nonce   string
}

// waitTaskKind starts the id of every wait task: its kind and version.
const waitTaskKind = "w1"

// terms are what t's task id carries, which taskid.Sign signs: its kind,
// cursor, creation and deadline (milliseconds in base 36, the deadline
// empty for none), nonce, names and covered agents (each its name, = and
// its id), between colons, the names and the agents between commas, which
// neither a name nor an agent's id holds.
func (t waitTask) terms() string {
	covered := make([]string, len(t.covered))
	for i, a := range t.covered {
		covered[i] = a.Name + "=" + a.ID
	}
	return strings.Join([]string{waitTaskKind, cursorText(t.cursor), millis(t.created), millis(t.deadline), t.nonce, strings.Join(t.names, ","), strings.Join(covered, ",")}, ":")
}

// millis is a moment as a task's terms carry it: milliseconds in base 36,
// empty for the zero time (no deadline).
func millis(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return strconv.FormatInt(t.UnixMilli(), 36)
}

// parseMillis is the moment millis gave s for, the zero time for "".
func parseMillis(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	ms, err := strconv.ParseInt(s, 36, 64)
	return time.UnixMilli(ms), err
}

// parseWaitTask is the wait task whose id carries terms, or false for
// terms no wait task's id carries.
func parseWaitTask(terms string) (waitTask, bool) {
	f := strings.Split(terms, ":")
	if len(f) != 7 || f[0] != waitTaskKind {
		return waitTask{}, false
	}
	s, sinceErr := parseSince(f[1], time.Time{})
	created, createdErr := parseMillis(f[2])
	deadline, deadlineErr := parseMillis(f[3])
	t := waitTask{cursor: s.cursor, created: created, deadline: deadline, nonce: f[4]}
	if f[5] != "" {
		t.names = strings.Split(f[5], ",")
	}
	coveredOK := true
	if f[6] != "" {
		for _, c := range strings.Split(f[6], ",") {
			name, id, ok := strings.Cut(c, "=")
			coveredOK = coveredOK && ok && id != "" && checkName(name) == nil
			t.covered = append(t.covered, agent.Agent{ID: id, Name: name})
		}
	}
	ok := sinceErr == nil && s.isCursor && f[2] != "" && createdErr == nil && deadlineErr == nil && coveredOK && !slices.ContainsFunc(t.names, func(n string) bool { return checkName(n) != nil })
	return t, ok
}

// task is t as the MCP server knows it, by its id.
func (t waitTask) task(id string) mcptasks.Task { return taskOf(id, t.created, t.deadline) }

// taskOf is the wait task known by id, created at created, as the MCP
// server knows it: one with a deadline lives until then, as the client is
// told.
func taskOf(id string, created, deadline time.Time) mcptasks.Task {
	mt := mcptasks.Task{ID: id, Created: created, PollInterval: waitTaskPoll}
	if !deadline.IsZero() {
		mt.TTL = deadline.Sub(created)
	}
	return mt
}

// A pendingWait is a wait task of either kind, as its id carries it: a
// wait's (waitTask) or a send's with wait (sendWaitTask).
type pendingWait interface {
	// terms are what its id carries, which taskid.Sign signs.
	terms() string
	// task is it as the MCP server knows it, by its id.
	task(id string) mcptasks.Task
	// poll takes a look for it, printing its result to out once done.
	poll(out io.Writer, d deps) (done bool, err error)
}

// parseTask is the wait task whose id carries terms, of either kind, or
// false for terms no wait task's id carries.
func parseTask(terms string) (pendingWait, bool) {
	if t, ok := parseWaitTask(terms); ok {
		return t, true
	}
	return parseSendWaitTask(terms)
}

// handOff makes the tool call of ctx go on as the wait task p, its id
// signed with the key of hq's state directory, which the first such task
// makes; or it is the error of a key hq can neither read nor make, since
// no hq mcp could tell its task from one made up.
func handOff(ctx context.Context, d deps, p pendingWait) error {
	key, err := d.taskKey()
	if err != nil {
		return err
	}
	mcptasks.HandOff(ctx, p.task(taskid.Sign(key, p.terms())))
	return nil
}

// carried are the agents a wait task covers of those the last look of its
// call waited on (waitTask.covered): each with its id and name, but one
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
	t := waitTask{names: w.names, cursor: r.cursor, covered: carried(r.covered), created: time.UnixMilli(d.now().UnixMilli()), nonce: newNonce()}
	if w.timeout > 0 {
		t.deadline = time.UnixMilli(start.Add(w.timeout).UnixMilli())
		if !t.created.Before(t.deadline) {
			return printWait(env.Stdout, w, lsRows(d, nil), r.cursor)
		}
	}
	if err := handOff(ctx, d, t); err != nil {
		return fmt.Errorf("the wait cannot go on as a task: %w", err)
	}
	return nil
}

// waitTasks answers for the wait tasks of hq's MCP server (mcptasks.Tasks),
// of either kind (pendingWait),
// which steps aside once the hq on disk is of another generation (disk),
// as each tool call does (stepAside), with the client's line (updated).
type waitTasks struct {
	d       deps
	disk    *onDisk
	updated string
}

// Task is the wait task id stands for: none when the key of hq's state
// directory did not sign it, or cannot be read, as when it went with the
// directory; reading it makes none.
func (w waitTasks) Task(id string) (mcptasks.Task, bool) {
	key, err := w.d.readTaskKey()
	if err != nil {
		return mcptasks.Task{}, false
	}
	terms, signed := taskid.Verify(key, id)
	p, ok := parseTask(terms)
	if !signed || !ok {
		return mcptasks.Task{}, false
	}
	return p.task(id), true
}

// Result is what wait task t has come to at a look taken now: the result
// of its tool once its poll is done (pendingWait), the wait's once the
// look finds agents to return, its deadline has passed, or it fails; nil
// while it is not. Once the hq on disk is of
// another generation it is the line every tool call fails with, as a tool
// error, and no look is taken: a JSON-RPC error would carry no message to
// the supervisor (#363).
func (w waitTasks) Result(_ context.Context, task mcptasks.Task) *mcp.CallToolResult {
	if w.disk.otherGeneration() {
		return updatedResult(w.updated)
	}
	p, _ := parseTask(taskid.Terms(task.ID))
	done := false
	r, _, _ := callCommand(w.d, func(env Env, d deps, _ []string) error {
		var err error
		done, err = p.poll(env.Stdout, d)
		return err
	})
	if !done {
		return nil
	}
	return r
}

// poll takes a look for wait task t as a wait's call takes each look
// (waitFor, after its first), delivering what waits as each look of a wait
// does, and prints the wait's result to out, as the wait tool prints it,
// once it returns: after a look that finds agents it takes one look more,
// so agents that change together are returned together (#116), and once
// the deadline has passed it returns none. done says that it returned, or
// failed as the call would, tmux gone or a look failing: the task has then
// completed.
func (t waitTask) poll(out io.Writer, d deps) (done bool, err error) {
	if err := checkTmux(d.tmux); err != nil {
		return true, err
	}
	w := waiting{terms: waitTerms{names: t.names, since: since{cursor: t.cursor, isCursor: true}, covered: t.covered, deadline: t.deadline}}
	var r waited
	k := d.looker()
	err = k.Loop(func(s agent.Seen, first bool) (bool, error) {
		next := k.Cursor(s) // taken before what waits is typed in
		deliverWaiting(d, s.Agents)
		if first {
			w.terms.covered = traced(d, w.terms.covered, s.Agents)
		}
		r, done = w.at(s, next, d.now())
		return done || w.found == nil, nil
	})
	if err != nil {
		return true, lookErr(err)
	}
	if !done {
		return false, nil
	}
	return true, printWait(out, waitArgs{names: t.names, asJSON: true}, lsRows(d, r.agents), r.cursor)
}

// traced are a wait task's covered agents, each that the look's agents as
// no longer show given its repository and sandbox from the trace of kills
// when hq killed it (#147): its id carries a covered agent's id and name
// alone, so one closed by hand is returned with neither.
func traced(d deps, covered, as []agent.Agent) []agent.Agent {
	var gone []int
	for i, a := range covered {
		if !slices.ContainsFunc(as, func(b agent.Agent) bool { return b.ID == a.ID }) {
			gone = append(gone, i)
		}
	}
	if len(gone) == 0 {
		return covered
	}
	ks, _ := d.readKills() // what it cannot read leaves the agents as they are
	out := slices.Clone(covered)
	for _, i := range gone {
		j := slices.IndexFunc(ks, func(k kills.Kill) bool { return k.ID == out[i].ID })
		if j >= 0 {
			out[i].RepoPath, out[i].Sandbox = ks[j].Repo, ks[j].Sandbox
		}
	}
	return out
}
