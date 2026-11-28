package cli

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/journal/journaltest"
	"github.com/rkrysinski/hq/internal/kills"
	"github.com/rkrysinski/hq/internal/mcptasks"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
	"github.com/rkrysinski/hq/internal/version"
)

// rawClient speaks JSON-RPC to hq's MCP server in memory, in lines of JSON,
// as Claude Code 2.1.332 speaks to it over standard input and output
// (#361): protocol 2026-07-28, server/discover and no initialize, each
// request carrying its client's capabilities in _meta, the MCP Tasks
// extension declared or not. It is the seam for what the go-sdk client
// cannot see: a tool call answered with a task handle, and the task methods.
type rawClient struct {
	t     *testing.T
	w     io.Writer
	lines chan string
	n     int
}

// rawBound is how long the raw client waits for an answer, for the server
// to read a request and for the server to end, in real time: the server's
// waits run on the fakes' clock.
const rawBound = 10 * time.Second

// rawConnect connects a raw client to s.
func rawConnect(t *testing.T, s *mcp.Server) *rawClient {
	t.Helper()
	cr, sw := io.Pipe()
	sr, cw := io.Pipe()
	ss, err := s.Connect(context.Background(), &mcp.IOTransport{Reader: sr, Writer: sw}, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &rawClient{t: t, w: cw, lines: make(chan string, 64)}
	go func() {
		sc := bufio.NewScanner(cr)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			c.lines <- sc.Text()
		}
		close(c.lines)
	}()
	t.Cleanup(func() {
		_ = cw.Close()
		ended := make(chan struct{})
		go func() { _ = ss.Wait(); close(ended) }()
		select {
		case <-ended:
		case <-time.After(rawBound):
			t.Errorf("the server did not end within %v of its input's end", rawBound)
		}
		_ = cr.Close()
	})
	return c
}

// writeBounded writes b to w, failing the test when w does not take it
// within rawBound: a pipe's write waits for its reader.
func writeBounded(t *testing.T, w io.Writer, b []byte) {
	t.Helper()
	wrote := make(chan error, 1)
	go func() { _, err := w.Write(b); wrote <- err }()
	select {
	case err := <-wrote:
		if err != nil {
			t.Fatalf("write: %v", err)
		}
	case <-time.After(rawBound):
		t.Fatalf("the server did not read a request within %v", rawBound)
	}
}

// rawError is a JSON-RPC error as the raw client reads it.
type rawError struct {
	Code    int64           `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// rawResult is a result as the raw client reads it: a tool's, a task's
// handle or a task, as its fields say, and the line it came in.
type rawResult struct {
	ResultType     string          `json:"resultType"`
	TaskID         string          `json:"taskId"`
	Status         string          `json:"status"`
	CreatedAt      string          `json:"createdAt"`
	LastUpdatedAt  string          `json:"lastUpdatedAt"`
	TTLMs          json.RawMessage `json:"ttlMs"`
	PollIntervalMs int64           `json:"pollIntervalMs"`
	// Result is a completed task's.
	Result *rawToolResult `json:"result"`
	rawToolResult
	// raw is the result whole, and line the line it came in.
	raw  json.RawMessage
	line string
}

// rawToolResult is a tool's result.
type rawToolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError"`
}

// text is the tool result's text.
func (r rawToolResult) text() string {
	var b strings.Builder
	for _, c := range r.Content {
		b.WriteString(c.Text)
	}
	return b.String()
}

// write sends a request for method with params, declaring the extension
// when tasks, and returns its id.
func (c *rawClient) write(method string, params map[string]any, tasks bool) int {
	c.t.Helper()
	caps := map[string]any{"roots": map[string]any{"listChanged": true}}
	if tasks {
		caps["extensions"] = map[string]any{mcptasks.Extension: map[string]any{}}
	}
	p := map[string]any{"_meta": map[string]any{
		"io.modelcontextprotocol/protocolVersion":    "2026-07-28",
		"io.modelcontextprotocol/clientInfo":         map[string]any{"name": "claude-code", "version": "2.1.332"},
		"io.modelcontextprotocol/clientCapabilities": caps,
	}}
	maps.Copy(p, params)
	c.n++
	b, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": c.n, "method": method, "params": p})
	if err != nil {
		c.t.Fatalf("%s: %v", method, err)
	}
	writeBounded(c.t, c.w, append(b, '\n'))
	return c.n
}

// read is the answer to request id: its result, or its error. It fails the
// test when none comes within rawBound.
func (c *rawClient) read(id int) (rawResult, *rawError) {
	c.t.Helper()
	timeout := time.After(rawBound)
	for {
		select {
		case l, ok := <-c.lines:
			if !ok {
				c.t.Fatalf("the server closed its output before answering request %d", id)
			}
			var m struct {
				ID     *int            `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  *rawError       `json:"error"`
			}
			if err := json.Unmarshal([]byte(l), &m); err != nil {
				c.t.Fatalf("not JSON: %v: %s", err, l)
			}
			if m.ID == nil || *m.ID != id {
				continue
			}
			var r rawResult
			if m.Result != nil {
				if err := json.Unmarshal(m.Result, &r); err != nil {
					c.t.Fatalf("result: %v: %s", err, l)
				}
			}
			r.raw, r.line = m.Result, l
			return r, m.Error
		case <-timeout:
			c.t.Fatalf("no answer to request %d within %v", id, rawBound)
		}
	}
}

// ok sends a request and returns its result, failing the test on an error.
func (c *rawClient) ok(method string, params map[string]any, tasks bool) rawResult {
	c.t.Helper()
	r, err := c.read(c.write(method, params, tasks))
	if err != nil {
		c.t.Fatalf("%s %v: error %+v", method, params, *err)
	}
	return r
}

// refused sends a request and returns its error, failing the test when it
// has a result.
func (c *rawClient) refused(method string, params map[string]any, tasks bool) rawError {
	c.t.Helper()
	r, err := c.read(c.write(method, params, tasks))
	if err == nil {
		c.t.Fatalf("%s %v: no error: %s", method, params, r.line)
	}
	return *err
}

// call calls tool with args.
func (c *rawClient) call(tool string, args map[string]any, tasks bool) rawResult {
	c.t.Helper()
	return c.ok("tools/call", map[string]any{"name": tool, "arguments": args}, tasks)
}

// get asks for task id.
func (c *rawClient) get(id string) rawResult {
	c.t.Helper()
	return c.ok("tasks/get", map[string]any{"taskId": id}, true)
}

// taskSession is a raw client of hq's MCP server for Claude Code on the
// wait fakes, a and b working, that has said server/discover, and the
// next_since of a list.
func taskSession(t *testing.T) (*fakes, *rawClient, string) {
	t.Helper()
	f := newWaitFakes()
	f.tty = false
	c := sessionOn(t, f)
	return f, c, waitJSON(t, c.call("list", nil, true).text()).NextSince
}

// sessionOn is a raw client of a new hq MCP server for Claude Code on the
// fakes f, that has said server/discover.
func sessionOn(t *testing.T, f *fakes) *rawClient {
	t.Helper()
	c := rawConnect(t, newMCPServer(f.deps(), claudeCode))
	c.ok("server/discover", nil, true)
	return c
}

// keyed is a raw client of a new hq MCP server for Claude Code on wait
// fakes of its own whose state directory holds key, a copy of it, that has
// said server/discover: a case of a table test that asks for a task hq
// gave builds its own session with it.
func keyed(t *testing.T, key []byte) *rawClient {
	t.Helper()
	f := newWaitFakes()
	f.tty = false
	f.taskKey = slices.Clone(key)
	return sessionOn(t, f)
}

// isHandle reports whether r is a task's handle.
func isHandle(r rawResult) bool { return r.ResultType == "task" || r.TaskID != "" }

// stampOf is a moment as the extension's timestamps give it.
func stampOf(at time.Time) string { return at.UTC().Format("2006-01-02T15:04:05.000Z07:00") }

// completedWith is the wait's result that task answer g completed with,
// not a tool error; it fails the test when g is anything else.
func completedWith(t *testing.T, g rawResult) waitOutput {
	t.Helper()
	if g.Status != "completed" || g.Result == nil || g.Result.IsError {
		t.Fatalf("not completed with a result: %s", g.line)
	}
	return waitJSON(t, g.Result.text())
}

// Claude Code's hq declares the MCP Tasks extension in its answer to
// server/discover, where the extension has it declared (#361); Claude
// Desktop's and the gateway's, whose waits never become tasks, do not.
func TestClaudeCodesHqDeclaresTheTasksExtensionInItsAnswerToDiscover(t *testing.T) {
	for _, client := range mcpClients {
		f := newWaitFakes()
		f.tty = false
		c := rawConnect(t, newMCPServer(f.deps(), client))
		r := c.ok("server/discover", nil, true)
		var d struct {
			Capabilities struct {
				Tools      any            `json:"tools"`
				Extensions map[string]any `json:"extensions"`
			} `json:"capabilities"`
		}
		if err := json.Unmarshal(r.raw, &d); err != nil {
			t.Fatal(err)
		}
		ext, ok := d.Capabilities.Extensions[mcptasks.Extension]
		if ok != (client == claudeCode) || d.Capabilities.Tools == nil {
			t.Errorf("%s: %s", client.name, r.line)
		}
		if ok && !strings.Contains(r.line, `"extensions":{"`+mcptasks.Extension+`":{}}`) {
			t.Errorf("%s: the extension's settings %v are not {}: %s", client.name, ext, r.line)
		}
	}
}

// A change within the hold comes back in the same call as an ordinary
// result, though the request declares tasks (#361, story 2).
func TestAWaitThatSeesAChangeWithinItsHoldReturnsItInTheCall(t *testing.T) {
	f, c, since := taskSession(t)
	start := f.now
	f.after(20*time.Second, func() {
		f.states["id-b"] = journaltest.HookState{State: state.Done, Since: f.now, Last: "PR #58 opened"}
	})
	r := c.call("wait", map[string]any{"since": since}, true)
	w := waitJSON(t, r.text())
	if isHandle(r) || r.IsError || len(w.Agents) != 1 || w.Agents[0].Name != "b" || w.NextSince == "" {
		t.Fatalf("%s", r.line)
	}
	if took := f.now.Sub(start); took < 20*time.Second || took > 21*time.Second {
		t.Fatalf("returned after %v", took)
	}
}

// After the hold with nothing to return, a wait whose request declares
// tasks is answered with a wait task's handle (#361, stories 1, 3): working,
// with no time to live, at the poll interval hq names. One whose request
// does not declare them goes on in its call, and returns the change when it
// comes, or ends at its timeout; one given a timeout within the hold ends at
// it in its call, as before, whatever its request declares (#363).
func TestAWaitGoesOnAsAWaitTaskAfterItsHoldOnlyWhenItsRequestDeclaresTasks(t *testing.T) {
	f, c, since := taskSession(t)
	start := f.now
	h := c.call("wait", map[string]any{"since": since, "timeout": -5}, true)
	if h.ResultType != "task" || h.Status != "working" || h.TaskID == "" || string(h.TTLMs) != "null" || h.PollIntervalMs != waitTaskPoll.Milliseconds() || len(h.Content) != 0 {
		t.Fatalf("handle %s", h.line)
	}
	if h.CreatedAt != stampOf(f.now) || h.LastUpdatedAt != h.CreatedAt || f.now.Sub(start) != waitHold {
		t.Fatalf("handed off after %v: %s", f.now.Sub(start), h.line)
	}

	start = f.now
	f.after(80*time.Second, func() {
		f.states["id-b"] = journaltest.HookState{State: state.Done, Since: f.now, Last: "PR #58 opened"}
	})
	r := c.call("wait", map[string]any{"since": since}, false)
	w := waitJSON(t, r.text())
	if isHandle(r) || len(w.Agents) != 1 || w.Agents[0].Name != "b" {
		t.Fatalf("not declaring tasks: %s", r.line)
	}
	if took := f.now.Sub(start); took < 80*time.Second || took > 81*time.Second {
		t.Fatalf("not declaring tasks: returned after %v", took)
	}

	for timeout, tasks := range map[int]bool{120: false, 50: true, 30: true} {
		f, c, since := taskSession(t)
		start := f.now
		r := c.call("wait", map[string]any{"since": since, "timeout": timeout}, tasks)
		if got := waitJSON(t, r.text()); isHandle(r) || r.IsError || got.Agents == nil || len(got.Agents) != 0 || got.NextSince == "" || f.now.Sub(start) != time.Duration(timeout)*time.Second {
			t.Errorf("timeout %d, declaring tasks %v: after %v: %s", timeout, tasks, f.now.Sub(start), r.line)
		}
	}
}

// A wait task given a timeout completes at it with no agents and the
// cursor of the look that saw it pass, not an error (#363, story 9): its
// id carries the deadline, and the time to live hq gives the client is
// the time from the handoff to it, also past the hour a call may last. A
// change after its next_since comes back from a wait from it.
func TestAWaitTaskGivenATimeoutCompletesAtItWithNoAgents(t *testing.T) {
	f, c, since := taskSession(t)
	start := f.now
	h := c.call("wait", map[string]any{"since": since, "timeout": 120}, true)
	if !isHandle(h) || h.Status != "working" || string(h.TTLMs) != "70000" || h.CreatedAt != stampOf(start.Add(waitHold)) {
		t.Fatalf("handle: %s", h.line)
	}
	f.now = start.Add(120*time.Second - time.Millisecond)
	if g := c.get(h.TaskID); g.Status != "working" || string(g.TTLMs) != "70000" || g.Result != nil {
		t.Fatalf("a moment before the deadline: %s", g.line)
	}
	f.now = start.Add(123 * time.Second)
	g := c.get(h.TaskID)
	got := completedWith(t, g)
	if g.LastUpdatedAt != stampOf(f.now) || string(g.TTLMs) != "70000" {
		t.Fatalf("after the deadline: %s", g.line)
	}
	if got.Agents == nil || len(got.Agents) != 0 || got.NextSince == "" {
		t.Fatalf("after the deadline: %s", g.Result.text())
	}
	f.now = f.now.Add(time.Second)
	f.states["id-a"] = journaltest.HookState{State: state.Question, Since: f.now, Last: "Shall I go on?"}
	start = f.now
	w := waitJSON(t, c.call("wait", map[string]any{"since": got.NextSince}, true).text())
	if len(w.Agents) != 1 || w.Agents[0].Name != "a" || f.now.Sub(start) > time.Second {
		t.Fatalf("from the task's next_since: %+v", w)
	}

	if h := c.call("wait", map[string]any{"since": w.NextSince, "timeout": 7200}, true); !isHandle(h) || string(h.TTLMs) != "7150000" {
		t.Fatalf("past an hour: %s", h.line)
	}
}

// A wait task given a timeout returns a change that comes before it as one
// with none does (#363, story 9).
func TestAWaitTaskGivenATimeoutReturnsAChangeBeforeItAsWithoutOne(t *testing.T) {
	f, c, since := taskSession(t)
	start := f.now
	h := c.call("wait", map[string]any{"names": []string{"b"}, "since": since, "timeout": 300}, true)
	f.now = start.Add(100 * time.Second)
	f.states["id-b"] = journaltest.HookState{State: state.Done, Since: f.now, Last: "PR #58 opened"}
	g := c.get(h.TaskID)
	if w := completedWith(t, g); len(w.Agents) != 1 || w.Agents[0].Name != "b" || w.Agents[0].State != state.Done {
		t.Fatalf("%s", g.line)
	}
}

// A wait given a timeout whose hold ends at or past it, a look having taken
// long, returns no agents in its call, as at a timeout: a task would be
// late before it began.
func TestAWaitWhoseHoldEndsPastItsTimeoutReturnsNoAgentsInTheCall(t *testing.T) {
	f, c, since := taskSession(t)
	start := f.now
	f.after(49*time.Second, func() { f.now = f.now.Add(5 * time.Second) })
	r := c.call("wait", map[string]any{"since": since, "timeout": 51}, true)
	if w := waitJSON(t, r.text()); isHandle(r) || r.IsError || w.Agents == nil || len(w.Agents) != 0 || w.NextSince == "" || f.now.Before(start.Add(51*time.Second)) {
		t.Fatalf("after %v: %s", f.now.Sub(start), r.line)
	}
	if f.taskKey != nil {
		t.Fatalf("a key made for no task: %x", f.taskKey)
	}
}

// Agents that change together come back in one result of a wait task, as
// in one call (#363, story 7): once a poll's look finds an agent, it takes
// one look more and returns those of both, with the cursor of the last,
// from which neither comes back again.
func TestAWaitTaskReturnsAgentsThatChangeTogetherInOneResult(t *testing.T) {
	f, c, since := taskSession(t)
	h := c.call("wait", map[string]any{"since": since}, true)
	f.now = f.now.Add(time.Minute)
	f.states["id-b"] = journaltest.HookState{State: state.Done, Since: f.now, Last: "PR #58 opened"}
	f.after(0, func() {
		f.states["id-a"] = journaltest.HookState{State: state.Question, Since: f.now, Last: "Shall I go on?"}
	})
	g := c.get(h.TaskID)
	w := completedWith(t, g)
	if len(w.Agents) != 2 || w.Agents[0].Name+w.Agents[1].Name != "ab" {
		t.Fatalf("%s", g.line)
	}
	f.onSleep = nil
	if again := waitJSON(t, c.call("wait", map[string]any{"since": w.NextSince, "timeout": 1}, true).text()); len(again.Agents) != 0 {
		t.Fatalf("from its next_since: %+v", again.Agents)
	}
}

// An agent the wait covered when its call ended, killed between two polls,
// comes back from the wait task ended (#363, story 12), as from a call: its
// id carries each covered agent's id and name, and the trace of kills
// gives the repository and sandbox of one hq killed. An agent the wait did
// not cover is not returned.
func TestAWaitTaskReturnsAnAgentKilledBetweenTwoPollsAsEnded(t *testing.T) {
	f, c, since := taskSession(t)
	f.tmux.windows = append(f.tmux.windows, agentWindow("@3", "c", "/w/lib", f.now.Add(-time.Hour), false))
	f.states["id-c"] = journaltest.HookState{State: state.Working, Since: f.now.Add(-time.Minute), Last: "on it"}
	h := c.call("wait", map[string]any{"names": []string{"b"}, "since": since}, true)
	if g := c.get(h.TaskID); g.Status != "working" {
		t.Fatalf("before the kills: %s", g.line)
	}
	f.now = f.now.Add(time.Minute)
	f.tmux.windows = slices.DeleteFunc(f.tmux.windows, func(w tmux.Window) bool { return w.ID == "@3" })
	if g := c.get(h.TaskID); g.Status != "working" {
		t.Fatalf("c, not covered, killed: %s", g.line)
	}
	f.kills = append(f.kills, kills.Kill{At: f.now, Name: "old", ID: "id-old", Repo: "/w/other", Sandbox: "claude-other"},
		kills.Kill{At: f.now, Name: "b", ID: "id-b", Repo: "/w/app", Sandbox: "claude-x"})
	f.tmux.windows = slices.DeleteFunc(f.tmux.windows, func(w tmux.Window) bool { return w.ID == "@2" })
	f.now = f.now.Add(5 * time.Second)
	at := f.now
	g := c.get(h.TaskID)
	w := completedWith(t, g)
	if len(w.Agents) != 1 {
		t.Fatalf("b killed: %s", g.line)
	}
	if b := w.Agents[0]; b.Name != "b" || b.State != state.Ended || b.StateSeq != nil || !b.Since.Equal(at) || b.Last != agent.EndedLast ||
		b.Repo != "app" || b.RepoPath != "/w/app" || b.Sandbox != "claude-x" || b.Setup != "default" {
		t.Fatalf("b killed: %+v", b)
	}
}

// A wait task on every agent returns each covered agent that is killed, also
// one the trace of kills does not hold (a window closed by hand), with no
// repository then, the first in the trace with its own.
func TestAWaitTaskOnEveryAgentReturnsEachKilledAgentAsEnded(t *testing.T) {
	f, c, since := taskSession(t)
	h := c.call("wait", map[string]any{"since": since}, true)
	f.kills = append(f.kills, kills.Kill{At: f.now, Name: "a", ID: "id-a", Repo: "/w/app", Sandbox: "claude-x"})
	f.tmux.windows = f.tmux.windows[:1]
	g := c.get(h.TaskID)
	w := completedWith(t, g)
	if len(w.Agents) != 2 {
		t.Fatalf("%s", g.line)
	}
	got := map[string]lsRow{}
	for _, r := range w.Agents {
		got[r.Name] = r
	}
	if a, b := got["a"], got["b"]; a.State != state.Ended || a.RepoPath != "/w/app" || b.State != state.Ended || b.RepoPath != "" || b.Sandbox != "" || b.Last != agent.EndedLast {
		t.Fatalf("a %+v, b %+v", a, b)
	}
}

// A wait task on every agent with no agent at all covers none, and returns
// an agent started after its handoff once it changes, as a call does.
func TestAWaitTaskThatCoversNoAgentReturnsOneStartedLater(t *testing.T) {
	f, c, since := taskSession(t)
	windows := f.tmux.windows
	f.tmux.windows = windows[:1]
	h := c.call("wait", map[string]any{"since": since}, true)
	if g := c.get(h.TaskID); g.Status != "working" {
		t.Fatalf("no agents: %s", g.line)
	}
	f.now = f.now.Add(time.Minute)
	f.tmux.windows = windows
	f.states["id-b"] = journaltest.HookState{State: state.Done, Since: f.now, Last: "PR #58 opened"}
	if w := completedWith(t, c.get(h.TaskID)); len(w.Agents) != 1 || w.Agents[0].Name != "b" {
		t.Fatalf("b started and done: %+v", w.Agents)
	}
}

// An agent that had ended with no number when the wait's call ended (its
// journal gone, its pane dead) is not returned by the wait task while it
// stays so, as a call does not return it again.
func TestAWaitTaskDoesNotReturnAnAgentAlreadyEndedWithNoNumber(t *testing.T) {
	f, c, since := taskSession(t)
	f.unreadable = map[string]bool{"id-b": true}
	f.tmux.windows[2].PaneDead = true
	h := c.call("wait", map[string]any{"since": since}, true)
	if !isHandle(h) {
		t.Fatalf("returned in the call: %s", h.line)
	}
	f.now = f.now.Add(time.Minute)
	if g := c.get(h.TaskID); g.Status != "working" {
		t.Fatalf("%s", g.line)
	}
}

// A wait that fails within its hold fails in its call, though its request
// declares tasks, as it always has: a since hq does not take, a name no
// agent has.
func TestAWaitThatFailsWithinItsHoldFailsInTheCall(t *testing.T) {
	for want, args := range map[string]func(since string) map[string]any{
		"hq: --since 'soon' is neither": func(string) map[string]any { return map[string]any{"since": "soon"} },
		"hq: no agent 'nobody'":         func(since string) map[string]any { return map[string]any{"names": []string{"nobody"}, "since": since} },
	} {
		_, c, since := taskSession(t)
		if r := c.call("wait", args(since), true); isHandle(r) || !r.IsError || !strings.HasPrefix(r.text(), want) {
			t.Errorf("%s: %s", want, r.line)
		}
	}
}

// A wait task works until a look sees a change after its cursor, and then
// completes with what wait would return, the text alone (#361, stories 4,
// 5, 8): with no limit it still works hours later. The result's next_since
// goes on as a wait's: a change after it comes back, and the task's agent
// does not come back again (story 6).
func TestAWaitTaskWorksUntilALookSeesAChangeThenCompletesWithTheWaitsResult(t *testing.T) {
	f, c, since := taskSession(t)
	h := c.call("wait", map[string]any{"since": since}, true)
	f.now = f.now.Add(5 * time.Hour)
	g := c.get(h.TaskID)
	if g.ResultType != "complete" || g.Status != "working" || g.TaskID != h.TaskID || g.CreatedAt != h.CreatedAt || g.LastUpdatedAt != h.CreatedAt ||
		string(g.TTLMs) != "null" || g.PollIntervalMs != waitTaskPoll.Milliseconds() || g.Result != nil {
		t.Fatalf("working: %s", g.line)
	}

	f.now = f.now.Add(time.Second)
	f.states["id-b"] = journaltest.HookState{State: state.Done, Since: f.now, Last: "PR #58 opened"}
	g = c.get(h.TaskID)
	if g.ResultType != "complete" || g.Status != "completed" || g.TaskID != h.TaskID || g.CreatedAt != h.CreatedAt || g.LastUpdatedAt != stampOf(f.now) ||
		g.Result == nil || g.Result.IsError || len(g.Result.Content) != 1 || g.Result.Content[0].Type != "text" || strings.Contains(g.line, "structuredContent") {
		t.Fatalf("completed: %s", g.line)
	}
	got := waitJSON(t, g.Result.text())
	if call := theCallReturns(t, f, "--since", since, "--timeout", "1s"); len(got.Agents) != 1 || got.Agents[0].Name+" "+got.Agents[0].State+" "+got.Agents[0].Last != call || got.NextSince == "" {
		t.Fatalf("the task gives %s, a wait %q", g.Result.text(), call)
	}

	f.now = f.now.Add(time.Second)
	f.states["id-a"] = journaltest.HookState{State: state.Question, Since: f.now, Last: "Shall I go on?"}
	start := f.now
	r := c.call("wait", map[string]any{"since": got.NextSince}, true)
	if w := waitJSON(t, r.text()); isHandle(r) || len(w.Agents) != 1 || w.Agents[0].Name != "a" || f.now.Sub(start) > time.Second {
		t.Fatalf("from the task's next_since: %s", r.line)
	}
}

// Any hq mcp answers for a wait task, also another than the one that took
// the call (#361, story 36): none holds the wait.
func TestAnotherHqMcpAnswersForAWaitTask(t *testing.T) {
	f, c, since := taskSession(t)
	h := c.call("wait", map[string]any{"names": []string{"b"}, "since": since}, true)
	other := rawConnect(t, newMCPServer(f.deps(), claudeCode))
	other.ok("server/discover", nil, true)
	if g := other.get(h.TaskID); g.Status != "working" || g.CreatedAt != h.CreatedAt {
		t.Fatalf("working: %s", g.line)
	}
	f.now = f.now.Add(time.Minute)
	f.states["id-a"] = journaltest.HookState{State: state.Done, Since: f.now, Last: "not waited for"}
	if g := other.get(h.TaskID); g.Status != "working" {
		t.Fatalf("a, not named: %s", g.line)
	}
	f.states["id-b"] = journaltest.HookState{State: state.Done, Since: f.now, Last: "PR #58 opened"}
	g := other.get(h.TaskID)
	if w := waitJSON(t, g.Result.text()); g.Status != "completed" || len(w.Agents) != 1 || w.Agents[0].Name != "b" {
		t.Fatalf("completed: %s", g.line)
	}
}

// Claude Code cancels a task twice at once when it is stopped: both are
// acknowledged, and nothing changes at the agents (#361, story 14): no look
// is taken, so not even a message waiting is typed in, which a look does;
// the task still answers.
func TestCancellingAWaitTaskTwiceIsAcknowledgedAndChangesNothing(t *testing.T) {
	f := sendFakes(state.Done)
	f.tty = false
	c := rawConnect(t, newMCPServer(f.deps(), claudeCode))
	since := waitJSON(t, c.call("list", nil, true).text()).NextSince
	h := c.call("wait", map[string]any{"names": []string{"a"}, "since": since}, true)
	f.inbox["id-a"] = []string{"any news?"}
	f.oldest["id-a"] = f.now.Add(-time.Minute)
	events := journalEvents(t, f, "id-a")
	first := c.write("tasks/cancel", map[string]any{"taskId": h.TaskID}, true)
	second := c.write("tasks/cancel", map[string]any{"taskId": h.TaskID}, true)
	for _, id := range []int{first, second} {
		if r, err := c.read(id); err != nil || !strings.Contains(r.line, `"result":{`) || r.ResultType != "complete" || r.TaskID != "" || r.Status != "" {
			t.Fatalf("cancel %d: %s %+v", id, r.line, err)
		}
	}
	if len(f.tmux.pasted) != 0 || len(f.inbox["id-a"]) != 1 || journalEvents(t, f, "id-a") != events {
		t.Fatalf("pasted %q, inbox %v, events %d of %d", f.tmux.pasted, f.inbox, journalEvents(t, f, "id-a"), events)
	}
	if g := c.get(h.TaskID); g.Status != "working" || strings.Join(f.tmux.pasted, "|") != "%4 any news?" {
		t.Fatalf("after: %s, pasted %q", g.line, f.tmux.pasted)
	}
}

// journalEvents is how many events agent id's journal holds, failing the
// test when it holds none: the fakes' journal keeps it under journalID.
func journalEvents(t *testing.T, f *fakes, id string) int {
	t.Helper()
	j, err := f.journal.Read(journalID(id))
	if err != nil || len(j.Events) == 0 {
		t.Fatalf("no events in %s's journal: %v", id, err)
	}
	return len(j.Events)
}

// Two waits are two wait tasks, though they wait for the same: each has an
// id of its own, the second does not end the first, and each completes
// with the change (#361, story 15).
func TestTwoWaitsAreTwoWaitTasksThatEachComplete(t *testing.T) {
	f, c, since := taskSession(t)
	first := c.call("wait", map[string]any{"since": since}, true)
	// The second is handed off at the same moment as the first.
	f.now = f.now.Add(-waitHold)
	second := c.call("wait", map[string]any{"since": since}, true)
	if first.CreatedAt != second.CreatedAt {
		t.Fatalf("not at the same moment: %s\n%s", first.line, second.line)
	}
	if !isHandle(first) || !isHandle(second) || first.TaskID == second.TaskID {
		t.Fatalf("%s\n%s", first.line, second.line)
	}
	if g := c.get(first.TaskID); g.Status != "working" {
		t.Fatalf("the first after the second: %s", g.line)
	}
	f.states["id-b"] = journaltest.HookState{State: state.Done, Since: f.now, Last: "PR #58 opened"}
	for _, h := range []rawResult{first, second} {
		if g := c.get(h.TaskID); g.Status != "completed" || waitJSON(t, g.Result.text()).Agents[0].Name != "b" {
			t.Fatalf("%s", g.line)
		}
	}
}

// An id hq gave no wait task is an unknown task: tasks/get and
// tasks/cancel refuse it with -32602, as the extension requires (#361).
func TestAnUnknownWaitTaskIsRefusedAsInvalidParams(t *testing.T) {
	for _, id := range []string{"", "nope", "w1"} {
		for _, method := range []string{"tasks/get", "tasks/cancel"} {
			_, c, _ := taskSession(t)
			if err := c.refused(method, map[string]any{"taskId": id}, true); err.Code != -32602 || err.Message != "Failed to retrieve task: Task not found" {
				t.Errorf("%s %q: %+v", method, id, err)
			}
		}
	}
}

// unknownTask fails the test unless tasks/get and tasks/cancel, each asked
// on a session of its own that session makes, refuse id as an unknown
// task.
func unknownTask(t *testing.T, session func() *rawClient, id string) {
	t.Helper()
	for _, method := range []string{"tasks/get", "tasks/cancel"} {
		if err := session().refused(method, map[string]any{"taskId": id}, true); err.Code != -32602 || err.Message != "Failed to retrieve task: Task not found" {
			t.Errorf("%s %q: %+v", method, id, err)
		}
	}
}

// A wait task's id is known only as hq gave it (#362, story 35): with any
// one character changed, in the terms it waits on or in what signs them,
// with one added or one cut, tasks/get and tasks/cancel refuse it as an
// unknown task, as they refuse an id hq never gave; as given it still
// answers. The id holds no key.
func TestAWaitTaskIdChangedInAnyWayIsAnUnknownTask(t *testing.T) {
	f, c, since := taskSession(t)
	h := c.call("wait", map[string]any{"names": []string{"b"}, "since": since}, true)
	id := h.TaskID
	changed := []string{id + "x", id[:len(id)-1], "x" + id, id[1:]}
	for i := range len(id) {
		other := byte('A')
		if id[i] == other {
			other = 'B'
		}
		changed = append(changed, id[:i]+string(other)+id[i+1:])
	}
	for _, ch := range changed {
		unknownTask(t, func() *rawClient { return keyed(t, f.taskKey) }, ch)
	}
	if g := c.get(id); g.Status != "working" || g.TaskID != id {
		t.Fatalf("as given: %s", g.line)
	}
	for _, key := range []string{hex.EncodeToString(f.taskKey), base64.StdEncoding.EncodeToString(f.taskKey), base64.RawURLEncoding.EncodeToString(f.taskKey), string(f.taskKey)} {
		if len(f.taskKey) == 0 || strings.Contains(id, key) {
			t.Fatalf("the id %q holds the key %q", id, key)
		}
	}
}

// The key that signs a wait task's id is made at the first wait that goes
// on as a task, and kept: no list, no wait that returns within its hold
// makes it, and a later task's id is signed with the same (#362). An id
// signed with the key of another state directory is an unknown task, both
// ways; so is one hq cannot check, the key gone with its state directory,
// and asking for it makes no key: the next wait that goes on as a task makes
// a new one, its id known and the old id unknown still.
func TestAWaitTaskIdSignedWithAnotherKeyIsAnUnknownTask(t *testing.T) {
	f, c, since := taskSession(t)
	f.after(10*time.Second, func() {
		f.states["id-b"] = journaltest.HookState{State: state.Done, Since: f.now, Last: "PR #58 opened"}
	})
	since = waitJSON(t, c.call("wait", map[string]any{"since": since}, true).text()).NextSince
	if f.taskKey != nil {
		t.Fatalf("a key before any task: %x", f.taskKey)
	}
	first := c.call("wait", map[string]any{"since": since}, true)
	key := f.taskKey
	second := c.call("wait", map[string]any{"names": []string{"a"}, "since": since}, true)
	if key == nil || string(f.taskKey) != string(key) || c.get(first.TaskID).Status != "working" || c.get(second.TaskID).Status != "working" {
		t.Fatalf("key %x, then %x", key, f.taskKey)
	}

	elsewhere := newWaitFakes()
	elsewhere.tty = false
	o := rawConnect(t, newMCPServer(elsewhere.deps(), claudeCode))
	o.ok("server/discover", nil, true)
	theirs := o.call("wait", map[string]any{"since": waitJSON(t, o.call("list", nil, true).text()).NextSince}, true)
	if !isHandle(theirs) || o.get(theirs.TaskID).Status != "working" {
		t.Fatalf("elsewhere: %s", theirs.line)
	}
	unknownTask(t, func() *rawClient { return sessionOn(t, elsewhere) }, first.TaskID)
	unknownTask(t, func() *rawClient { return sessionOn(t, f) }, theirs.TaskID)

	f.taskKey = nil
	unknownTask(t, func() *rawClient { return sessionOn(t, f) }, first.TaskID)
	if f.taskKey != nil {
		t.Fatalf("asking for a task made a key: %x", f.taskKey)
	}
	again := c.call("wait", map[string]any{"since": since}, true)
	if g := c.get(again.TaskID); f.taskKey == nil || string(f.taskKey) == string(key) || g.Status != "working" {
		t.Fatalf("after the key went: key %x, %s", f.taskKey, g.line)
	}
	unknownTask(t, func() *rawClient { return sessionOn(t, f) }, first.TaskID)
}

// A wait whose hold passes with no change when hq cannot make the key that
// signs a task's id fails in its call with the error, as a wait that cannot
// look does: it cannot go on as a task no hq mcp could check (#362).
func TestAWaitThatCannotSignItsTaskFailsInTheCall(t *testing.T) {
	f, c, since := taskSession(t)
	f.taskKeyErr = errors.New("mkdir /s/hq: permission denied")
	r := c.call("wait", map[string]any{"since": since}, true)
	if isHandle(r) || !r.IsError || r.text() != "hq: the wait cannot go on as a task: mkdir /s/hq: permission denied\n" {
		t.Fatalf("%s", r.line)
	}
}

// The task methods refuse a request that does not declare the extension,
// naming it (-32021, as the extension requires), also on a session begun
// with the older initialize, whose requests carry no capabilities.
func TestTheTaskMethodsRefuseARequestThatDoesNotDeclareTasks(t *testing.T) {
	f, c, since := taskSession(t)
	h := c.call("wait", map[string]any{"since": since}, true)
	for _, method := range []string{"tasks/get", "tasks/cancel"} {
		err := keyed(t, f.taskKey).refused(method, map[string]any{"taskId": h.TaskID}, false)
		if err.Code != -32021 || string(err.Data) != `{"requiredCapabilities":{"extensions":{"io.modelcontextprotocol/tasks":{}}}}` {
			t.Errorf("%s: %+v", method, err)
		}
	}
	for _, method := range []string{"tasks/get", "tasks/cancel"} {
		f := newWaitFakes()
		f.tty = false
		old := rawConnect(t, newMCPServer(f.deps(), claudeCode))
		old.writeLine(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"old","version":"1"}}}`)
		if _, err := old.read(1); err != nil {
			t.Fatalf("initialize: %+v", *err)
		}
		old.writeLine(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
		old.writeLine(fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":%q}`, method))
		if _, err := old.read(2); err == nil || err.Code != -32021 {
			t.Errorf("%s with no params: %+v", method, err)
		}
	}
}

// writeLine sends a line as it is.
func (c *rawClient) writeLine(l string) {
	c.t.Helper()
	writeBounded(c.t, c.w, []byte(l+"\n"))
}

// The experimental tasks of the protocol's revision 2025-11-25 are not the
// declaration of the MCP Tasks extension (#357, Implementation Decisions):
// a wait that asks to be a task as that revision has it (params.task), on
// a session begun with initialize that declares them in its capabilities,
// or with the 2026-07-28 protocol's capabilities naming them outside
// extensions, goes on in its call past the hold and returns the change
// there; the task methods refuse it.
func TestTheExperimentalTasksOf20251125AreNotTheDeclaration(t *testing.T) {
	const experimental = `{"list":{},"cancel":{},"requests":{"tools":{"call":{}},"sampling":{"createMessage":{}}}}`
	for name, meta := range map[string]string{
		"initialize": "",
		"2026-07-28": `,"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"claude-code","version":"2.1.332"},"io.modelcontextprotocol/clientCapabilities":{"tasks":` + experimental + `}}`,
	} {
		f := newWaitFakes()
		f.tty = false
		c := rawConnect(t, newMCPServer(f.deps(), claudeCode))
		if meta == "" {
			c.writeLine(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{"tasks":` + experimental + `},"clientInfo":{"name":"claude-code","version":"2.1.332"}}}`)
			if _, err := c.read(1); err != nil {
				t.Fatalf("%s: initialize: %+v", name, *err)
			}
			c.writeLine(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
		} else {
			c.writeLine(`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{` + meta[1:] + `}}`)
			if _, err := c.read(1); err != nil {
				t.Fatalf("%s: discover: %+v", name, *err)
			}
		}
		c.writeLine(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list","arguments":{}` + meta + `}}`)
		l, err := c.read(2)
		if err != nil {
			t.Fatalf("%s: list: %+v", name, *err)
		}
		start := f.now
		f.after(80*time.Second, func() {
			f.states["id-b"] = journaltest.HookState{State: state.Done, Since: f.now, Last: "PR #58 opened"}
		})
		c.writeLine(fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"wait","arguments":{"since":%q},"task":{"ttl":600000}%s}}`, waitJSON(t, l.text()).NextSince, meta))
		r, err := c.read(3)
		if err != nil || isHandle(r) || r.IsError {
			t.Fatalf("%s: %s", name, r.line)
		}
		if w := waitJSON(t, r.text()); len(w.Agents) != 1 || w.Agents[0].Name != "b" || f.now.Sub(start) < 80*time.Second {
			t.Fatalf("%s: after %v: %s", name, f.now.Sub(start), r.line)
		}
		c.writeLine(`{"jsonrpc":"2.0","id":4,"method":"tasks/get","params":{"taskId":"w1"` + meta + `}}`)
		if _, err := c.read(4); err == nil || err.Code != -32021 {
			t.Errorf("%s: tasks/get: %+v", name, err)
		}
	}
}

// Claude Desktop's and the gateway's waits never become tasks, whatever the
// request declares: they end within their client's limit, and the task
// methods are not there.
func TestClaudeDesktopsAndTheGatewaysWaitsNeverBecomeTasks(t *testing.T) {
	for _, client := range []mcpClient{claudeDesktop, sbxGateway} {
		f := newWaitFakes()
		f.tty = false
		c := rawConnect(t, newMCPServer(f.deps(), client))
		c.ok("server/discover", nil, true)
		since := waitJSON(t, c.call("list", nil, true).text()).NextSince
		start := f.now
		r := c.call("wait", map[string]any{"since": since}, true)
		if isHandle(r) || len(waitJSON(t, r.text()).Agents) != 0 || f.now.Sub(start) != client.maxWait {
			t.Errorf("%s: after %v: %s", client.name, f.now.Sub(start), r.line)
		}
		if err := c.refused("tasks/get", map[string]any{"taskId": "nope"}, true); err.Code != -32601 {
			t.Errorf("%s: tasks/get: %+v", client.name, err)
		}
	}
}

// A poll that fails as a wait's call would end, tmux gone or a look that
// fails, completes the wait task with that error as a tool error, the text
// a call would end with (#363, story 13): Claude Code tells the supervisor
// "Task failed" at once.
func TestAPollThatFailsCompletesTheWaitTaskWithTheCallsError(t *testing.T) {
	for name, fail := range map[string]func(f *fakes){
		"tmux gone":  func(f *fakes) { f.tmux.missing = true },
		"look fails": func(f *fakes) { f.tmux.windowsErr = errors.New("no server running") },
	} {
		f, c, since := taskSession(t)
		h := c.call("wait", map[string]any{"since": since}, true)
		fail(f)
		g := c.get(h.TaskID)
		call := c.call("wait", map[string]any{"since": since}, true)
		if g.Status != "completed" || g.Result == nil || !g.Result.IsError || !strings.HasPrefix(g.Result.text(), "hq: ") || g.Result.text() != call.text() || !call.IsError {
			t.Errorf("%s: the task %s, the call %s", name, g.line, call.line)
		}
	}
}

// Once the hq on disk is of another generation, hq mcp answers a question
// for a wait task with the task completed with the line every tool call
// fails with, as a tool error, never a JSON-RPC error, which carries no
// message to the supervisor (#363, story 16); it takes no look, so not even
// a message waiting is typed in; a cancel is still acknowledged.
func TestAfterAnUpdateToAnotherGenerationAWaitTaskCompletesWithTheUpdatesLine(t *testing.T) {
	f := sendFakes(state.Done)
	f.tty = false
	c := rawConnect(t, newMCPServer(f.deps(), claudeCode))
	since := waitJSON(t, c.call("list", nil, true).text()).NextSince
	h := c.call("wait", map[string]any{"names": []string{"a"}, "since": since}, true)
	f.inbox["id-a"] = []string{"any news?"}
	f.oldest["id-a"] = f.now.Add(-time.Minute)
	f.disk.ino++
	f.diskGeneration = version.Generation + 1
	f.now = f.now.Add(time.Minute)
	g := c.get(h.TaskID)
	if g.Status != "completed" || g.Result == nil || !g.Result.IsError || g.Result.text() != "hq: "+hqUpdated+"\n" || g.LastUpdatedAt != stampOf(f.now) {
		t.Fatalf("%s", g.line)
	}
	if len(f.tmux.pasted) != 0 || len(f.inbox["id-a"]) != 1 {
		t.Fatalf("a look was taken: pasted %q, inbox %v", f.tmux.pasted, f.inbox)
	}
	if r := c.ok("tasks/cancel", map[string]any{"taskId": h.TaskID}, true); r.ResultType != "complete" {
		t.Fatalf("cancel: %s", r.line)
	}
	if r := c.call("list", nil, true); !r.IsError || r.text() != "hq: "+hqUpdated+"\n" {
		t.Fatalf("a tool call: %s", r.line)
	}
	if f.generationAsks != 1 {
		t.Errorf("the hq on disk asked %d times, want once", f.generationAsks)
	}
}
