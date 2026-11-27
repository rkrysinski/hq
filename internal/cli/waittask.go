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
	// created is when the call went on as the task, to the millisecond, and
	// nonce tells apart tasks of the same terms created at the same moment.
	created time.Time
	nonce   string
}

// waitTaskKind starts the id of every wait task: its kind and version.
const waitTaskKind = "w1"

// terms are what t's task id carries, which taskid.Sign signs: its kind,
// cursor, creation (milliseconds in base 36), nonce and names, between
// colons, the names between commas, which no name holds.
func (t waitTask) terms() string {
	return strings.Join([]string{waitTaskKind, cursorText(t.cursor), strconv.FormatInt(t.created.UnixMilli(), 36), t.nonce, strings.Join(t.names, ",")}, ":")
}

// parseWaitTask is the wait task whose id carries terms, or false for
// terms no wait task's id carries.
func parseWaitTask(terms string) (waitTask, bool) {
	f := strings.Split(terms, ":")
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

// task is t as the MCP server knows it, by its id.
func (t waitTask) task(id string) mcptasks.Task {
	return mcptasks.Task{ID: id, Created: t.created, PollInterval: waitTaskPoll}
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
// (ADR 0022), its id signed with the key of hq's state directory, which
// the first such task makes; or the error of a key it can neither read nor
// make, since no hq mcp could tell its task from one made up.
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
	key, err := d.taskKey()
	if err != nil {
		return fmt.Errorf("the wait cannot go on as a task: %w", err)
	}
	t := waitTask{names: w.names, cursor: r.cursor, created: time.UnixMilli(d.now().UnixMilli()), nonce: newNonce()}
	mcptasks.HandOff(ctx, t.task(taskid.Sign(key, t.terms())))
	return nil
}

// waitTasks answers for the wait tasks of hq's MCP server (mcptasks.Tasks).
type waitTasks struct{ d deps }

// Task is the wait task id stands for: none when the key of hq's state
// directory did not sign it, or cannot be read, as when it went with the
// directory; reading it makes none.
func (w waitTasks) Task(id string) (mcptasks.Task, bool) {
	key, err := w.d.readTaskKey()
	if err != nil {
		return mcptasks.Task{}, false
	}
	terms, signed := taskid.Verify(key, id)
	t, ok := parseWaitTask(terms)
	return t.task(id), signed && ok
}

// Result is what wait task t has come to at a look taken now: the wait
// tool's result once the look finds agents to return, or fails; nil while
// it finds none.
func (w waitTasks) Result(_ context.Context, task mcptasks.Task) *mcp.CallToolResult {
	t, _ := parseWaitTask(taskid.Terms(task.ID))
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
