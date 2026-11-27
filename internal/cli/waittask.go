package cli

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/mcptasks"
)

// waitHold is how long the wait tool waits in its call under Claude Code
// before it goes on as a wait task (ADR 0022): as long as a wait lasted
// there before #359, so a change within it comes back in the same call and
// turn, well within the 120 s after which Claude Code moves a call to the
// background.
const waitHold = DefaultWaitTimeout

// waitTaskPoll is how often Claude Code is told to ask for a wait task:
// each time is one look of hq's, no model request, and the most the
// task's result comes later than the change it returns (#361).
const waitTaskPoll = 5 * time.Second

// A waitTask is a wait that goes on after its call as an MCP task
// (GLOSSARY.md, ADR 0022). Its id carries all it waits for, so any hq mcp
// answers for it by taking a look, and none holds it in memory.
type waitTask struct {
	// names are the agents it waits for, every agent when none, and cursor
	// what a change has to come after: the cursor of its call's last look.
	names  []string
	cursor int64
	// created is when the call went on as the task, to the millisecond, and
	// nonce tells apart tasks of the same terms created at the same moment.
	created time.Time
	nonce   string
}

// waitTaskKind starts the id of every wait task: its kind and version.
const waitTaskKind = "w1"

// id is t's task id: its kind, cursor, creation (milliseconds in base 36),
// nonce and names, between colons, the names between commas, which no name
// holds.
func (t waitTask) id() string {
	return strings.Join([]string{waitTaskKind, cursorText(t.cursor), strconv.FormatInt(t.created.UnixMilli(), 36), t.nonce, strings.Join(t.names, ",")}, ":")
}

// parseWaitTask is the wait task id stands for, or false for an id no wait
// task has.
func parseWaitTask(id string) (waitTask, bool) {
	f := strings.Split(id, ":")
	if len(f) != 5 || f[0] != waitTaskKind {
		return waitTask{}, false
	}
	s, sinceErr := parseSince(f[1], time.Time{})
	ms, msErr := strconv.ParseInt(f[2], 36, 64)
	t := waitTask{cursor: s.cursor, created: time.UnixMilli(ms), nonce: f[3]}
	if f[4] != "" {
		t.names = strings.Split(f[4], ",")
	}
	ok := sinceErr == nil && s.isCursor && msErr == nil && !slices.ContainsFunc(t.names, func(n string) bool { return checkName(n) != nil })
	return t, ok
}

// task is t as the MCP server hands it off.
func (t waitTask) task() mcptasks.Task {
	return mcptasks.Task{ID: t.id(), Created: t.created, PollInterval: waitTaskPoll}
}

// newNonce is a nonce for a wait task's id: two calls of wait in one
// message may go on as tasks of the same terms in the same millisecond.
func newNonce() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b) // never fails, the package says
	return base64.RawURLEncoding.EncodeToString(b)
}

// waitOrHandOff is hq wait with args for a call of the wait tool that may
// go on as a task (mcptasks.Offered), args holding waitHold as its timeout:
// what it returns within the hold, as runWait prints it; or, when the hold
// passes with no agents, nothing, the call going on as a wait task from
// the cursor of its last look, which stands for every change that look saw
// (ADR 0022).
func waitOrHandOff(ctx context.Context, env Env, d deps, args []string) error {
	start := d.now()
	w, err := parseWait(args, start)
	if err != nil {
		return err
	}
	r, err := waitFor(d, w, start)
	if err != nil {
		return err
	}
	if len(r.agents) > 0 {
		return printWait(env.Stdout, w, lsRows(d, r.agents), r.cursor)
	}
	t := waitTask{names: w.names, cursor: r.cursor, created: time.UnixMilli(d.now().UnixMilli()), nonce: newNonce()}
	mcptasks.HandOff(ctx, t.task())
	return nil
}

// waitTasks answers for the wait tasks of hq's MCP server (mcptasks.Tasks).
type waitTasks struct{ d deps }

// Task is the wait task id stands for.
func (w waitTasks) Task(id string) (mcptasks.Task, bool) {
	t, ok := parseWaitTask(id)
	return t.task(), ok
}

// Result is what wait task t has come to at a look taken now: the wait
// tool's result once the look finds agents to return, or fails; nil while
// it finds none.
func (w waitTasks) Result(_ context.Context, task mcptasks.Task) *mcp.CallToolResult {
	t, _ := parseWaitTask(task.ID)
	done := false
	r, _, _ := callCommand(w.d, func(env Env, d deps, _ []string) error {
		var err error
		done, err = t.poll(env.Stdout, d)
		return err
	})
	if !done {
		return nil
	}
	return r
}

// poll takes a look for wait task t as the first look of its call would
// (agent.Looker.Loop), delivering what waits as each look of a wait does,
// and prints the wait's result to out, as the wait tool prints it, when the
// look finds agents to return. done says that it found them, or failed: the
// task has then completed.
func (t waitTask) poll(out io.Writer, d deps) (done bool, err error) {
	terms := waitTerms{names: t.names, since: since{cursor: t.cursor, isCursor: true}}
	var found waitFound
	var next int64
	k := d.looker()
	err = k.Loop(func(s agent.Seen, _ bool) (bool, error) {
		next = k.Cursor(s) // taken before what waits is typed in
		deliverWaiting(d, s.Agents)
		found = terms.resultAt(s, next, d.now())
		return true, nil
	})
	if err != nil {
		return true, lookErr(err)
	}
	if len(found.agents) == 0 {
		return false, nil
	}
	agent.SortAttention(found.agents)
	return true, printWait(out, waitArgs{names: t.names, asJSON: true}, lsRows(d, found.agents), next)
}
