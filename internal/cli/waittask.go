package cli

import (
	"context"
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

// waitHold is the hold (GLOSSARY.md): how long the wait tool, and the send
// tool with wait, wait in their call under Claude Code before they go on
// as a wait task (ADR 0022): as long as a wait lasted there before #359,
// so a change within it comes back in the same call and turn, well within
// the 120 s after which Claude Code moves a call to the background.
const waitHold = DefaultWaitTimeout

// waitTaskPoll is how often Claude Code is told to ask for a wait task
// (pollIntervalMs, which it takes from 250 ms to 30 s), set by measurement
// (#361): Claude Code 2.1.332 asked every 5.01 s, never with a model
// request, and hq answered each tasks/get with one look and one sbx ls in
// 6 to 22 ms, about 14 ms of CPU, against the stub sbx. So a task's result
// comes at most 5 s after the change it returns, little beside the minutes
// of an agent's turn, for about 720 looks an hour; and a look whose sbx ls
// takes all of sbxPollTimeout, as long, ends before the next tasks/get.
const waitTaskPoll = 5 * time.Second

// A waitTask is a wait that goes on after its call as an MCP task
// (GLOSSARY.md, ADR 0022), of either kind: a wait's (waitToolTask) or a
// send's with wait (sendToolTask). Its id carries all it waits for, signed
// with the key of hq's state directory (taskid), so any hq mcp answers for
// it by taking a look, none holds it in memory, and no one builds one from
// the terms alone.
type waitTask interface {
	// terms are what its id carries, which taskid.Sign signs.
	terms() string
	// task is it as the MCP server knows it, by its id.
	task(id string) mcptasks.Task
	// look takes a look for it, as its call took each look, printing its
	// result to out once done: it returned, or failed as the call would.
	look(out io.Writer, d deps) (done bool, err error)
}

// A taskHeader is what the id of a wait task of either kind starts with.
type taskHeader struct {
	// kind is its kind and version.
	kind string
	// cursor is what it waits from: a change has to come after it.
	cursor int64
	// created is when the call went on as the task, and deadline when it
	// completes as at a timeout, zero for none, both to the millisecond.
	created, deadline time.Time
}

// newTaskHeader is the header of a wait task of kind that waits from
// cursor, the call that started at start with timeout (0 for none) going
// on as the task at now; due says that the deadline, to the millisecond,
// has come by then, so the call ends as at its timeout instead.
func newTaskHeader(kind string, cursor int64, now, start time.Time, timeout time.Duration) (h taskHeader, due bool) {
	h = taskHeader{kind: kind, cursor: cursor, created: time.UnixMilli(now.UnixMilli())}
	if timeout > 0 {
		h.deadline = time.UnixMilli(start.Add(timeout).UnixMilli())
		due = !h.created.Before(h.deadline)
	}
	return h, due
}

// fields are the first terms of an id with header h: its kind, cursor,
// creation and deadline, the moments in milliseconds in base 36, the
// deadline empty for none.
func (h taskHeader) fields() []string {
	return []string{h.kind, cursorText(h.cursor), millis(h.created), millis(h.deadline)}
}

// parseTaskHeader is the header of an id of kind whose terms, between
// colons, are f, n of them in all; false for terms no such id carries.
func parseTaskHeader(f []string, kind string, n int) (taskHeader, bool) {
	if len(f) != n || f[0] != kind {
		return taskHeader{}, false
	}
	s, sinceErr := parseSince(f[1], time.Time{})
	created, createdErr := parseMillis(f[2])
	deadline, deadlineErr := parseMillis(f[3])
	h := taskHeader{kind: kind, cursor: s.cursor, created: created, deadline: deadline}
	return h, sinceErr == nil && s.isCursor && f[2] != "" && createdErr == nil && deadlineErr == nil
}

// task is the wait task with header h known by id, as the MCP server
// knows it: one with a deadline lives until then, as the client is told.
func (h taskHeader) task(id string) mcptasks.Task {
	mt := mcptasks.Task{ID: id, Created: h.created, PollInterval: waitTaskPoll}
	if !h.deadline.IsZero() {
		mt.TTL = h.deadline.Sub(h.created)
	}
	return mt
}

// passed reports whether the deadline has passed at now.
func (h taskHeader) passed(now time.Time) bool {
	return !h.deadline.IsZero() && !now.Before(h.deadline)
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

// parseWaitTask is the wait task whose id carries terms, of either kind,
// or false for terms no wait task's id carries.
func parseWaitTask(terms string) (waitTask, bool) {
	f := strings.Split(terms, ":")
	if t, ok := parseWaitToolTask(f); ok {
		return t, true
	}
	return parseSendToolTask(f)
}

// handOff makes the tool call of ctx go on as the wait task p, its id
// signed with the key of hq's state directory, which the first such task
// makes; or it is the error of a key hq can neither read nor make, since
// no hq mcp could tell its task from one made up.
func handOff(ctx context.Context, d deps, p waitTask) error {
	key, err := d.taskKey()
	if err != nil {
		return err
	}
	mcptasks.HandOff(ctx, p.task(taskid.Sign(key, p.terms())))
	return nil
}

// lookFor takes the looks of a wait task's tasks/get, as its call took
// each look after its first (agent.Looker.Loop): each with the look's
// cursor, taken before what waits is typed in, until each says to take no
// more or fails. It fails as the call would: tmux gone, or a look that
// fails.
func lookFor(d deps, each func(s agent.Seen, next int64, first bool) (more bool, err error)) error {
	if err := checkTmux(d.tmux); err != nil {
		return err
	}
	k := d.looker()
	return lookErr(k.Loop(func(s agent.Seen, first bool) (bool, error) {
		more, err := each(s, k.Cursor(s), first)
		return !more, err
	}))
}

// waitTasks answers for the wait tasks of hq's MCP server (mcptasks.Tasks),
// of either kind (waitTask), which steps aside once the hq on disk is of
// another generation (disk), as each tool call does (stepAside), with the
// client's line (updated).
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
	p, ok := parseWaitTask(terms)
	if !signed || !ok {
		return mcptasks.Task{}, false
	}
	return p.task(id), true
}

// Result is what wait task t has come to at a look taken now: the result
// of its tool once its look is done (waitTask), nil while it is not. Once
// the hq on disk is of another generation it is the line every tool call
// fails with, as a tool error, and no look is taken: a JSON-RPC error
// would carry no message to the supervisor (#363).
//
// It works the result out again at each tasks/get, from the id alone, as
// a call asks each look (ADR 0019): a task asked again after it completed
// answers what a look then finds, working again once the agents it
// returned have moved on (design 3.12).
func (w waitTasks) Result(_ context.Context, task mcptasks.Task) *mcp.CallToolResult {
	if w.disk.otherGeneration() {
		return updatedResult(w.updated)
	}
	p, _ := parseWaitTask(taskid.Terms(task.ID))
	done := false
	r, _, _ := callCommand(w.d, func(env Env, d deps, _ []string) error {
		var err error
		done, err = p.look(env.Stdout, d)
		return err
	})
	if !done {
		return nil
	}
	return r
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
