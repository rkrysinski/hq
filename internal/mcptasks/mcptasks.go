// Package mcptasks is the MCP Tasks extension (io.modelcontextprotocol/tasks,
// its release v0.2.3, for protocol 2026-07-28), written by hand on the public
// API of the Go MCP SDK in use (v1.8.0), which has no tasks (ADR 0022). It is
// the one place hq speaks the extension: its declaration in the answer to
// server/discover, the task handle a tool call answers with when it goes on
// as a task, tasks/get and tasks/cancel. When the SDK has tasks, this package
// is what gives way to it.
//
// A server that serves it keeps no task: each is known by its id alone
// (Tasks), so any process of the server answers for any task, also one that
// another process handed off.
package mcptasks

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Extension is the extension's identifier: a request declares it among its
// client capabilities, and a server in its answer to server/discover.
const Extension = "io.modelcontextprotocol/tasks"

// A Task is a tool call that goes on as a task.
type Task struct {
	// ID is what the client asks for the task by.
	ID string
	// Created is when the call went on as the task.
	Created time.Time
	// PollInterval is how often the client is told to ask for it.
	PollInterval time.Duration
	// TTL is how long from Created the client is told the task lives
	// (ttlMs): zero for no limit, the task standing until it completes.
	TTL time.Duration
}

// Tasks answers for the tasks of a server's tool calls, which the server
// keeps nowhere: each is known by its id alone.
type Tasks interface {
	// Task is the task id stands for, or false for an id the server knows
	// no task by. It changes nothing.
	Task(id string) (Task, bool)
	// Result is what task t has come to by now: nil while it works, the
	// tool call's result once it has completed, a tool error included.
	Result(ctx context.Context, t Task) *mcp.CallToolResult
}

// Serve makes s serve the extension, answering for its tasks with ts and
// telling the time with now:
//   - it declares the extension in its answer to server/discover;
//   - a tools/call whose own request declares the extension may go on as a
//     task (Offered, HandOff), and is then answered with its handle;
//   - tasks/get answers with the task, working until ts gives its result,
//     then completed with that result;
//   - tasks/cancel is acknowledged and changes nothing: nothing runs that
//     could be stopped;
//   - tasks/get and tasks/cancel refuse a request that does not declare the
//     extension (-32021), and an id ts knows no task by (-32602).
func Serve(s *mcp.Server, ts Tasks, now func() time.Time) {
	s.AddReceivingMiddleware(handOffs)
	err := errors.Join(
		mcp.AddReceivingCustomMethod(s, "tasks/get", func(ctx context.Context, _ *mcp.ServerSession, p *taskParams) (*getResult, error) {
			t, err := find(ts, p)
			if err != nil {
				return nil, err
			}
			r := &getResult{ResultType: complete, task: working(t)}
			if res := ts.Result(ctx, t); res != nil {
				r.Status, r.LastUpdatedAt, r.Result = "completed", stamp(now()), res
			}
			return r, nil
		}),
		mcp.AddReceivingCustomMethod(s, "tasks/cancel", func(_ context.Context, _ *mcp.ServerSession, p *taskParams) (*ackResult, error) {
			if _, err := find(ts, p); err != nil {
				return nil, err
			}
			return &ackResult{ResultType: complete}, nil
		}),
	)
	if err != nil {
		// Only when the SDK has a method of the extension's own: this
		// package then gives way to the SDK.
		panic(err)
	}
}

// handOffs lets a tool call whose own request declares the extension go on
// as a task, answering it with the handle of the task it was handed off as
// (HandOff); and declares the extension in the answer to server/discover,
// the one place the extension names for it.
func handOffs(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		switch method {
		case "server/discover":
			res, err := next(ctx, method, req)
			if r, ok := res.(*mcp.DiscoverResult); ok {
				r.Capabilities.AddExtension(Extension, nil)
			}
			return res, err
		case "tools/call":
			if declares(req.GetParams().GetMeta()) {
				h := &handOff{}
				res, err := next(context.WithValue(ctx, handOffKey{}, h), method, req)
				if h.task != nil {
					return &createResult{ResultType: "task", task: working(*h.task)}, nil
				}
				return res, err
			}
		}
		return next(ctx, method, req)
	}
}

type handOffKey struct{}

// handOff is where a tool call that may go on as a task leaves the task it
// goes on as.
type handOff struct{ task *Task }

// Offered reports whether the tool call of ctx may go on as a task: its own
// request declares the extension, on a server that serves it (Serve).
func Offered(ctx context.Context) bool {
	_, ok := ctx.Value(handOffKey{}).(*handOff)
	return ok
}

// HandOff makes the tool call of ctx go on as task t, which it may only when
// Offered: the server answers the call with t's handle, whatever the tool's
// handler returns, and answers for t from then on with its Tasks.
func HandOff(ctx context.Context, t Task) {
	if h, ok := ctx.Value(handOffKey{}).(*handOff); ok {
		h.task = &t
	}
}

// declares reports whether a request whose _meta is meta declares the
// extension: on that request itself, never inferred from another one.
func declares(meta map[string]any) bool {
	caps, _ := meta["io.modelcontextprotocol/clientCapabilities"].(map[string]any)
	exts, _ := caps["extensions"].(map[string]any)
	_, ok := exts[Extension]
	return ok
}

// find is the task a tasks/get or tasks/cancel asks for; or the refusal of
// a request that does not declare the extension, or of an id ts knows no
// task by, which a server answers alike whether it never knew the task or
// knows it no longer.
func find(ts Tasks, p *taskParams) (Task, error) {
	if p == nil || !declares(p.Meta) {
		return Task{}, &jsonrpc.Error{Code: mcp.CodeMissingRequiredClientCapabilities, Message: "Missing required client capability", Data: required}
	}
	t, ok := ts.Task(p.TaskID)
	if !ok {
		return Task{}, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "Failed to retrieve task: Task not found"}
	}
	return t, nil
}

// required is the data of the refusal of a request that does not declare
// the extension: what it lacks.
var required = json.RawMessage(`{"requiredCapabilities":{"extensions":{"` + Extension + `":{}}}}`)

// complete is the resultType of every result but a task handle's.
const complete = "complete"

// taskParams are the params of tasks/get and tasks/cancel.
type taskParams struct {
	mcp.ParamsBase
	TaskID string `json:"taskId"`
}

// task is a Task as the extension's Task carries it.
type task struct {
	TaskID        string `json:"taskId"`
	Status        string `json:"status"`
	CreatedAt     string `json:"createdAt"`
	LastUpdatedAt string `json:"lastUpdatedAt"`
	// TTLMs is null for a task with no limit.
	TTLMs          *int64 `json:"ttlMs"`
	PollIntervalMs int64  `json:"pollIntervalMs"`
}

// working is t as it stands until it completes.
func working(t Task) task {
	at := stamp(t.Created)
	w := task{TaskID: t.ID, Status: "working", CreatedAt: at, LastUpdatedAt: at, PollIntervalMs: t.PollInterval.Milliseconds()}
	if t.TTL > 0 {
		ms := t.TTL.Milliseconds()
		w.TTLMs = &ms
	}
	return w
}

// stamp is a moment as the extension's timestamps give it: ISO 8601, in UTC,
// to the millisecond.
func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z07:00") }

// createResult is CreateTaskResult: a tool call's answer when it goes on as
// a task, the task's handle.
type createResult struct {
	mcp.ResultBase
	ResultType string `json:"resultType"`
	task
}

// getResult is GetTaskResult: the task, with the tool call's result once it
// has completed.
type getResult struct {
	mcp.ResultBase
	ResultType string `json:"resultType"`
	task
	Result *mcp.CallToolResult `json:"result,omitempty"`
}

// ackResult is CancelTaskResult: an empty acknowledgement.
type ackResult struct {
	mcp.ResultBase
	ResultType string `json:"resultType"`
}
