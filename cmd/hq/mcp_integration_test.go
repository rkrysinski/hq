//go:build integration

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/testutil"
)

// buildHQ builds hq into a temporary directory and returns its path with
// symlinks resolved, as hq gives its own path: on macOS t.TempDir() is under
// /var, a symlink to /private/var, so the unresolved path never matches what
// hq prints or writes (#158).
func buildHQ(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "hq")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

// supervisor is an MCP client that has started hq mcp as Claude Desktop
// does: a child process spoken to over its stdin and stdout.
type supervisor struct {
	t      *testing.T
	cs     *mcp.ClientSession
	socket string
	// bin is the hq it started and env that hq's environment, for running
	// hq from a shell beside it, as the user does: the test's own, with
	// overrides over it.
	bin       string
	env       []string
	overrides []string
}

// row is an agent as the list, read and wait tools give it.
type row struct {
	Name          string          `json:"name"`
	State         string          `json:"state"`
	Last          string          `json:"last"`
	Pending       int             `json:"pending"`
	Reply         string          `json:"reply"`
	Asks          json.RawMessage `json:"asks"`
	Dialog        string          `json:"dialog"`
	CompletionSeq *int64          `json:"completion_seq"`
}

type waited struct {
	Agents    []row  `json:"agents"`
	NextSince string `json:"next_since"`
}

func newSupervisor(t *testing.T) *supervisor {
	t.Helper()
	s := supervisorEnv(t)
	cmd := exec.Command(s.bin, "mcp")
	cmd.Env = s.env
	s.connect(cmd)
	return s
}

// TestMain runs the tests through testutil.Main, which removes the fake
// claude supervisorEnv has built for them (#226).
func TestMain(m *testing.M) {
	os.Exit(testutil.Main(m))
}

// supervisorEnv is a supervisor not yet connected: hq built, and the
// environment it runs in, on the fake Claude and the stub sbx.
func supervisorEnv(t *testing.T) *supervisor {
	t.Helper()
	testutil.FakeClaude(t)
	stub, _ := testutil.SbxStub(t)
	path := t.TempDir()
	if err := os.Symlink(stub, filepath.Join(path, "sbx")); err != nil {
		t.Fatal(err)
	}
	s := &supervisor{t: t, socket: testutil.TmuxSocket(t), bin: buildHQ(t)}
	// Its own preferences file too: CI runners set XDG_CONFIG_HOME, which
	// would otherwise share the runner's file between tests (#141).
	s.overrides = []string{"PATH=" + path + string(os.PathListSeparator) + os.Getenv("PATH"), "HQ_TMUX_SOCKET=" + s.socket, "HOME=" + t.TempDir(), "XDG_CONFIG_HOME=" + t.TempDir(), "XDG_STATE_HOME=" + t.TempDir(), "TMUX="}
	s.env = append(os.Environ(), s.overrides...)
	return s
}

// connect starts hq mcp with cmd and speaks to it over its stdin and
// stdout.
func (s *supervisor) connect(cmd *exec.Cmd) {
	s.t.Helper()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "supervisor", Version: "v0"}, nil).Connect(context.Background(), &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		s.t.Fatal(err)
	}
	s.cs = cs
	s.t.Cleanup(func() { _ = cs.Close() })
}

// call calls a tool; it fails the test when the tool fails, unless fail
// is expected.
func (s *supervisor) call(tool string, args map[string]any) (string, bool) {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	r, err := s.cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		s.t.Fatalf("%s: %v", tool, err)
	}
	var text strings.Builder
	for _, c := range r.Content {
		text.WriteString(c.(*mcp.TextContent).Text)
	}
	return text.String(), r.IsError
}

func (s *supervisor) ok(tool string, args map[string]any) string {
	s.t.Helper()
	out, isErr := s.call(tool, args)
	if isErr {
		s.t.Fatalf("%s %v: %s", tool, args, out)
	}
	return out
}

// until calls wait in a loop from since, as the tools teach, until it
// returns name in state, and returns that row and the next since.
func (s *supervisor) until(since, name, st string) (row, string) {
	s.t.Helper()
	return s.untilRow(since, name, st, func(row) bool { return true })
}

// untilRow is until for a row that is as wanted: an agent just started is
// done before its first prompt, with no last message.
func (s *supervisor) untilRow(since, name, st string, wanted func(row) bool) (row, string) {
	s.t.Helper()
	for i := 0; i < 8; i++ {
		var w waited
		out := s.ok("wait", map[string]any{"since": since, "timeout": 5})
		if err := json.Unmarshal([]byte(out), &w); err != nil {
			s.t.Fatalf("wait: %v\n%s", err, out)
		}
		since = w.NextSince
		for _, r := range w.Agents {
			if r.Name == name && r.State == st && wanted(r) {
				return r, since
			}
		}
	}
	out, _ := exec.Command("tmux", "-u", "-L", s.socket, "capture-pane", "-p", "-a", "-t", "hq:").Output()
	s.t.Fatalf("%s never became %s: %+v\n%s", name, st, s.read(name), out)
	return row{}, ""
}

// send sends a message and returns how it goes and send's next_since.
func (s *supervisor) send(args map[string]any) (string, string) {
	s.t.Helper()
	var out struct {
		Delivery  string `json:"delivery"`
		NextSince string `json:"next_since"`
	}
	if res := s.ok("send", args); json.Unmarshal([]byte(res), &out) != nil || out.NextSince == "" {
		s.t.Fatalf("send: %s", res)
	}
	return out.Delivery, out.NextSince
}

func (s *supervisor) read(name string) row {
	s.t.Helper()
	var r row
	if out := s.ok("read", map[string]any{"name": name}); json.Unmarshal([]byte(out), &r) != nil {
		s.t.Fatalf("read: %s", out)
	}
	return r
}

// readUntil reads an agent until it is in state st: wait returns only the
// states that want the user.
func (s *supervisor) readUntil(name, st string) {
	s.t.Helper()
	for i := 0; i < 100; i++ {
		if s.read(name).State == st {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	s.t.Fatalf("%s never became %s", name, st)
}

// typeIn types a line into an agent's session, as the user does there.
func (s *supervisor) typeIn(name, line string) {
	s.t.Helper()
	out, _ := exec.Command("tmux", "-u", "-L", s.socket, "list-windows", "-a", "-F", "#{window_id} #{@hq_name}").Output()
	for _, w := range strings.Split(string(out), "\n") {
		if id, n, _ := strings.Cut(w, " "); n == name {
			if out, err := exec.Command("tmux", "-u", "-L", s.socket, "send-keys", "-t", id, line, "Enter").CombinedOutput(); err != nil {
				s.t.Fatalf("send-keys: %v %s", err, out)
			}
			return
		}
	}
	s.t.Fatalf("no window for %s in %q", name, out)
}

// hq mcp over stdio starts, watches, asks and ends agents with the CLI's
// own code: the supervisor's journey of PRD #132 on the fake Claude.
func TestMCPServerSupervisesAgentsOverStdio(t *testing.T) {
	s := newSupervisor(t)
	app, lib := testutil.GitRepo(t, "app"), testutil.GitRepo(t, "lib")
	if out := s.ok("new", map[string]any{"name": "a", "dir": app, "prompt": "hello"}); !strings.HasPrefix(out, "creating a sandbox for app") || !strings.Contains(out, "started a in app") {
		t.Fatalf("new a: %q", out)
	}
	s.ok("new", map[string]any{"name": "b", "dir": lib, "prompt": "slow job"})

	// Watch: a's turn ends; b is at work.
	_, since := s.untilRow("1m", "a", "done", func(r row) bool { return r.Last == "Done: hello" })
	s.readUntil("b", "working")

	// Ask b, which is at work: the message waits for its stop.
	how, _ := s.send(map[string]any{"name": "b", "text": "check the logs"})
	if how != "queued: b is working, delivered when it stops" {
		t.Fatalf("send: %q", how)
	}
	if r := s.read("b"); r.State != "working" || r.Pending != 1 {
		t.Fatalf("b %+v", r)
	}
	// b answers before wait is called: wait from the watch's since, the
	// wait's before send, still returns the answer.
	s.typeIn("b", "go on")
	s.readUntil("b", "done")
	r, since := s.until(since, "b", "done")
	if r.Last != "Answered: "+state.MessageLabel+"check the logs" || r.Pending != 0 {
		t.Fatalf("b answered %+v", r)
	}
	if r := s.read("b"); !strings.Contains(r.Reply, "check the logs") || r.Pending != 0 {
		t.Fatalf("b's answer %+v", r)
	}

	// A course correction within the turn: after its next tool call.
	s.typeIn("b", "slow tool job")
	s.readUntil("b", "working")
	if how, _ := s.send(map[string]any{"name": "b", "text": "use the staging db", "now": true}); !strings.HasPrefix(how, "queued: b is working, delivered after its next tool call") {
		t.Fatalf("send now: %q", how)
	}
	s.typeIn("b", "go on")
	if r, _ = s.until(since, "b", "done"); r.Last != "Done: slow tool job | "+state.MessageLabel+"use the staging db" {
		t.Fatalf("b corrected %+v", r)
	}

	// Shown to the user: docked, and no terminal shows the dashboard.
	if out := s.ok("go", map[string]any{"name": "a"}); out != "docked a; no terminal shows the dashboard: run hq in a terminal to see it\n" {
		t.Fatalf("go: %q", out)
	}
	if out := s.ok("kill", map[string]any{"name": "a", "confirmed": true}); out != "killed a; the sandbox stays\n" {
		t.Fatalf("kill: %q", out)
	}
	var ls waited
	if out := s.ok("list", nil); json.Unmarshal([]byte(out), &ls) != nil || len(ls.Agents) != 1 || ls.Agents[0].Name != "b" || ls.NextSince == "" {
		t.Fatalf("list: %s", out)
	}
	if out, isErr := s.call("read", map[string]any{"name": "a"}); !isErr || out != "hq: no agent 'a' (see hq ls)\n" {
		t.Fatalf("read a killed agent: %v %q", isErr, out)
	}
}

// Two agents whose turns end within one look of each other, watched as
// the tools teach: wait in a loop, each from the next_since of the wait
// before, with a read of what it returned in between. Both are returned,
// each once; the owner's supervisor got one of two such agents and, taking
// since from its read, never the other (#116).
func TestASupervisorWatchingWithWaitGetsBothAgentsThatFinishTogether(t *testing.T) {
	s := newSupervisor(t)
	app, lib := testutil.GitRepo(t, "app"), testutil.GitRepo(t, "lib")
	s.ok("new", map[string]any{"name": "a", "dir": app, "prompt": "slow job"})
	s.ok("new", map[string]any{"name": "b", "dir": lib, "prompt": "slow job"})
	s.readUntil("a", "working")
	s.readUntil("b", "working")
	var ls waited
	if out := s.ok("list", nil); json.Unmarshal([]byte(out), &ls) != nil || ls.NextSince == "" {
		t.Fatalf("list: %s", out)
	}

	// wait is running when both turns end, within one look of each other.
	calls := make(chan string, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		r, err := s.cs.CallTool(ctx, &mcp.CallToolParams{Name: "wait", Arguments: map[string]any{"since": ls.NextSince, "names": []string{"a", "b"}, "timeout": 30}})
		if err != nil || r.IsError {
			calls <- fmt.Sprintf("wait failed: %v %+v", err, r)
			return
		}
		calls <- r.Content[0].(*mcp.TextContent).Text
	}()
	time.Sleep(time.Second)
	s.typeIn("a", "go on")
	time.Sleep(150 * time.Millisecond)
	s.typeIn("b", "go on")

	returned := map[string]int{}
	since, out := "", ""
	select {
	case out = <-calls:
	case <-time.After(45 * time.Second):
		t.Fatal("wait did not return")
	}
	for i := 0; ; i++ {
		var w waited
		if err := json.Unmarshal([]byte(out), &w); err != nil {
			t.Fatalf("wait: %v\n%s", err, out)
		}
		since = w.NextSince
		for _, r := range w.Agents {
			if r.State != "done" || r.Last != "Done: slow job" {
				t.Fatalf("returned %+v", r)
			}
			returned[r.Name]++
			// Read it, as after any wait: read's next_since is that
			// agent's, not the watch's.
			s.read(r.Name)
		}
		if len(returned) == 2 || i == 8 {
			break
		}
		out = s.ok("wait", map[string]any{"since": since, "names": []string{"a", "b"}, "timeout": 5})
	}
	if returned["a"] != 1 || returned["b"] != 1 {
		t.Fatalf("returned %v", returned)
	}
	// Each once: the loop goes on with nothing.
	if out := s.ok("wait", map[string]any{"since": since, "names": []string{"a", "b"}, "timeout": 2}); !strings.Contains(out, `"agents": []`) {
		t.Fatalf("again: %s", out)
	}
}

// shell runs hq as the user does from a shell, beside the supervisor's.
func (s *supervisor) shell(args ...string) string {
	s.t.Helper()
	cmd := exec.Command(s.bin, args...)
	cmd.Env = s.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		s.t.Fatalf("hq %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// Agents the supervisor starts with a first prompt on the fake Claude,
// each prompt taken 3 s after its session's start (#220). /model NAME is a
// slash command of Claude Code's own that starts no turn and that it
// reports nowhere (2.1.329): the agent is done once its screen shows it,
// with no completed turn, and wait from before the start, of hq mcp or of
// a shell, returns it once. The supervisor's word on that first prompt is
// taken back, so a prompt the user types next, or a message they left
// with hq send --wait while the agent was starting, is theirs and
// notifies at its turn's end; send --wait returns. A plain first prompt,
// only late, keeps its agent starting: wait returns it only at its turn's
// end.
func TestAFirstPromptClaudeTookAsASlashCommandOfItsOwnEndsTheStart(t *testing.T) {
	record := filepath.Join(t.TempDir(), "notified")
	t.Setenv("FAKE_CLAUDE_NOTIFIED", record)
	t.Setenv("FAKE_CLAUDE_FIRST_PROMPT_DELAY", "3s")
	s := newSupervisor(t)
	var ls waited
	if out := s.ok("list", nil); json.Unmarshal([]byte(out), &ls) != nil || ls.NextSince == "" {
		t.Fatalf("list: %s", out)
	}
	s.ok("new", map[string]any{"name": "a", "dir": testutil.GitRepo(t, "app"), "prompt": "/model opus"})
	launched := time.Now()
	s.ok("new", map[string]any{"name": "b", "dir": testutil.GitRepo(t, "lib"), "prompt": "hello"})
	s.ok("new", map[string]any{"name": "c", "dir": testutil.GitRepo(t, "web"), "prompt": "/model opus"})
	if r := s.read("a"); r.State != "starting" {
		t.Fatalf("a right after its start: %+v", r)
	}
	// The user leaves c a message while it is starting.
	sent := make(chan string, 1)
	go func() {
		cmd := exec.Command(s.bin, "send", "--wait", "--timeout", "40s", "c", "note while starting")
		cmd.Env = s.env
		out, err := cmd.CombinedOutput()
		sent <- fmt.Sprintf("%v %s", err, out)
	}()
	// b is starting throughout the 3 s before its prompt is taken, then at
	// work on it or done with it.
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		r := s.read("b")
		if r.State == "starting" {
			if time.Now().After(deadline) {
				t.Fatalf("b still starting after 20 s: %+v", r)
			}
			continue
		}
		if r.State != "working" && (r.State != "done" || r.Last != "Done: hello") || time.Since(launched) < 2500*time.Millisecond {
			t.Fatalf("b after %v: %+v", time.Since(launched), r)
		}
		break
	}

	returned := map[string][]row{}
	since := ls.NextSince
	for i := 0; i < 12 && (len(returned["a"]) == 0 || len(returned["b"]) == 0); i++ {
		var w waited
		if out := s.ok("wait", map[string]any{"since": since, "names": []string{"a", "b"}, "timeout": 5}); json.Unmarshal([]byte(out), &w) != nil {
			t.Fatalf("wait: %s", out)
		}
		since = w.NextSince
		for _, r := range w.Agents {
			returned[r.Name] = append(returned[r.Name], r)
		}
	}
	if a := returned["a"]; len(a) != 1 || a[0].State != "done" || a[0].CompletionSeq != nil || a[0].Last != "" {
		t.Fatalf("a returned %+v", a)
	}
	if b := returned["b"]; len(b) != 1 || b[0].State != "done" || b[0].Last != "Done: hello" || b[0].CompletionSeq == nil {
		t.Fatalf("b returned %+v", b)
	}
	if out := s.ok("wait", map[string]any{"since": since, "names": []string{"a", "b"}, "timeout": 1}); !strings.Contains(out, `"agents": []`) {
		t.Fatalf("a and b again: %s", out)
	}
	// hq wait --since from before the launch, from a shell, returns a once.
	var w waited
	if out := s.shell("wait", "a", "--since", ls.NextSince, "--timeout", "5s", "--json"); json.Unmarshal([]byte(out), &w) != nil || len(w.Agents) != 1 || w.Agents[0].State != "done" || w.Agents[0].CompletionSeq != nil {
		t.Fatalf("hq wait a --since: %s", out)
	}
	select {
	case out := <-sent:
		if !strings.HasPrefix(out, "<nil> delivered: typed into c as its next prompt") || !strings.Contains(out, "Done: note while starting") {
			t.Fatalf("send --wait to c: %s", out)
		}
	case <-time.After(45 * time.Second):
		t.Fatal("send --wait to c did not return")
	}

	// The user prompts a: theirs, so it notifies.
	s.typeIn("a", "thanks")
	if r, _ := s.until(since, "a", "done"); r.Last != "Done: thanks" || r.CompletionSeq == nil {
		t.Fatalf("a after the user's prompt: %+v", r)
	}
	b, _ := os.ReadFile(record)
	var notified []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		name, _, _ := strings.Cut(l, " ")
		notified = append(notified, name)
	}
	if slices.Sort(notified); !slices.Equal(notified, []string{"a", "c"}) {
		t.Fatalf("notified %q", b)
	}
}

// The desktop notifications of issue #46's acceptance, on the fake Claude,
// which records each one a hook sends: work the supervisor sent ends
// without one, a dialog and anything of the user's still notify.
func TestWorkTheSupervisorSentEndsWithoutANotification(t *testing.T) {
	record := filepath.Join(t.TempDir(), "notified")
	t.Setenv("FAKE_CLAUDE_NOTIFIED", record)
	// An agent started with a prompt is starting, not done, until it
	// reports it, however long its session waits for it (#191).
	t.Setenv("FAKE_CLAUDE_FIRST_PROMPT_DELAY", "400ms")
	s := newSupervisor(t)
	app := testutil.GitRepo(t, "app")
	// notified reads the notifications sent so far, as "agent text".
	notified := func() string {
		b, _ := os.ReadFile(record)
		var lines []string
		for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			if name, seq, ok := strings.Cut(l, " "); ok {
				text, _ := strconv.Unquote(seq)
				lines = append(lines, name+" "+strings.TrimSuffix(strings.TrimPrefix(text, "\x1b]9;"), "\a"))
			}
		}
		return strings.Join(lines, "|")
	}
	expect := func(when, want string) {
		t.Helper()
		if got := notified(); got != want {
			t.Fatalf("%s: notified %q, want %q", when, got, want)
		}
	}
	typed := func(text string) string {
		t.Helper()
		how, sent := s.send(map[string]any{"name": "a", "text": text})
		if how != "delivered: typed into a as its next prompt" {
			t.Fatalf("send %q: %s", text, how)
		}
		return sent
	}

	// An agent the supervisor starts with a prompt reaches done, takes a
	// message as its next prompt, and ends a turn with a question: its row
	// changes as ever, and nobody is notified.
	s.ok("new", map[string]any{"name": "a", "dir": app, "prompt": "hello"})
	s.untilRow("1m", "a", "done", func(r row) bool { return r.Last == "Done: hello" })
	if r, _ := s.until(typed("one more thing"), "a", "done"); r.Last != "Done: one more thing" {
		t.Fatalf("a %+v", r)
	}
	if r, _ := s.until(typed("a question for you"), "a", "question"); r.Last != "Shall I go on?" {
		t.Fatalf("a %+v", r)
	}
	expect("done and question on supervised turns", "")

	// It opens a dialog on such a turn: one notification, the user's. Once
	// they have answered, the turn is still the supervisor's.
	sent := typed("this needs input")
	s.until(sent, "a", "needs input")
	time.Sleep(500 * time.Millisecond) // Claude's own late notification of the dialog
	expect("a dialog on a supervised turn", "a Needs input: main")
	s.typeIn("a", "1")
	s.until(sent, "a", "done")
	expect("done after the user answered", "a Needs input: main")

	// The user prompts the agent, and the supervisor sends it a message
	// before it finishes: one notification, at the turn's final end.
	s.typeIn("a", "slow job")
	s.readUntil("a", "working")
	how, sent := s.send(map[string]any{"name": "a", "text": "check the logs"})
	if how != "queued: a is working, delivered when it stops" {
		t.Fatalf("send: %q", how)
	}
	s.typeIn("a", "go on")
	if r, _ := s.until(sent, "a", "done"); r.Last != "Answered: "+state.MessageLabel+"check the logs" {
		t.Fatalf("a %+v", r)
	}
	expect("the supervisor's message in the user's turn", "a Needs input: main|a Done: main")

	// The supervisor's turn leaves a subagent running: done after its
	// closing turn, with no notification.
	sent = typed("background check")
	s.readUntil("a", "working")
	s.typeIn("a", "wake")
	if r, _ := s.until(sent, "a", "done"); r.Last != "The background work is done, 0 still running." {
		t.Fatalf("a %+v", r)
	}
	expect("the closing turn of a supervised turn", "a Needs input: main|a Done: main")

	// The supervisor's turn runs, and the user types a prompt at the agent:
	// one notification, at the end of what they asked.
	sent = typed("slow work")
	s.readUntil("a", "working")
	s.typeIn("a", "go on")
	_, since := s.until(sent, "a", "done")
	s.typeIn("a", "and thanks")
	if r, _ := s.until(since, "a", "done"); r.Last != "Done: and thanks" {
		t.Fatalf("a %+v", r)
	}
	expect("the user's prompt at an agent the supervisor works with", "a Needs input: main|a Done: main|a Done: main")

	// hq new with a prompt and hq send from a shell are the user's: both
	// notify as ever.
	s.shell("new", "b", app, "hello")
	_, since = s.until(since, "b", "done")
	expect("hq new from a shell", "a Needs input: main|a Done: main|a Done: main|b Done: main")
	if out := s.shell("send", "b", "again"); out != "delivered: typed into b as its next prompt\n" {
		t.Fatalf("hq send: %q", out)
	}
	if r, _ := s.until(since, "b", "done"); r.Last != "Done: again" {
		t.Fatalf("b %+v", r)
	}
	expect("hq send from a shell", "a Needs input: main|a Done: main|a Done: main|b Done: main|b Done: main")
}

// The supervisor answers an agent's question dialog on the user's word,
// through the dialog's hook: the agent goes on, its turn still the
// supervisor's, and the dialog notified the user once, as ever (#90).
func TestTheSupervisorAnswersAQuestionDialogOnTheUsersWord(t *testing.T) {
	record := filepath.Join(t.TempDir(), "notified")
	t.Setenv("FAKE_CLAUDE_NOTIFIED", record)
	t.Setenv("FAKE_CLAUDE_DELAY", "600ms")
	s := newSupervisor(t)
	app := testutil.GitRepo(t, "app")
	s.ok("new", map[string]any{"name": "a", "dir": app, "prompt": "this needs input"})
	s.until("1m", "a", "needs input")
	r := s.read("a")
	if len(r.Dialog) != 6 || !strings.Contains(string(r.Asks), "Which colour do you pick?") {
		t.Fatalf("read %+v", r)
	}
	answers := []any{map[string]any{"options": []int{2}}}
	if out, isErr := s.call("answer", map[string]any{"name": "a", "dialog": r.Dialog, "answers": answers}); !isErr || !strings.Contains(out, "ask the user what to pick") {
		t.Fatalf("without the user's word: %v %q", isErr, out)
	}
	if got := s.read("a"); got.State != "needs input" || got.Dialog != r.Dialog {
		t.Fatalf("answered without the user's word: %+v", got)
	}
	var res struct {
		State     string `json:"state"`
		NextSince string `json:"next_since"`
	}
	if out := s.ok("answer", map[string]any{"name": "a", "dialog": r.Dialog, "answers": answers, "confirmed": true}); json.Unmarshal([]byte(out), &res) != nil || res.State != "working" || res.NextSince == "" {
		t.Fatalf("answer: %s", out)
	}
	if r, _ := s.until(res.NextSince, "a", "done"); r.Last != "Done: this needs input" {
		t.Fatalf("a %+v", r)
	}
	b, _ := os.ReadFile(record)
	if got := strings.Count(string(b), "\n"); got != 1 || !strings.Contains(string(b), "Needs input: main") {
		t.Fatalf("notified %q", b)
	}
}

// Claude Code starts hq mcp from the entry hq mcp install wrote, over a
// pipe, with its own environment and the entry's (#115): it supervises as
// Claude Desktop does, and its wait may last longer than Claude Desktop's.
func TestClaudeCodeSupervisesFromTheEntryHqMcpInstallWrote(t *testing.T) {
	record := filepath.Join(t.TempDir(), "notified")
	t.Setenv("FAKE_CLAUDE_NOTIFIED", record)
	s := supervisorEnv(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(`{"numStartups": 3}`), 0o600)
	cmd := exec.Command(s.bin, "mcp", "install", "claude-code")
	cmd.Env = append(s.env, "CLAUDE_CONFIG_DIR="+dir, "HQ_PLATFORM=native")
	if out, err := cmd.CombinedOutput(); err != nil || !strings.HasPrefix(string(out), "added hq to Claude Code") {
		t.Fatalf("install: %v %s", err, out)
	}
	var c config
	b, _ := os.ReadFile(filepath.Join(dir, ".claude.json"))
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatalf("%v %s", err, b)
	}
	entry := c.MCPServers["hq"]
	cmd = exec.Command(entry.Command, entry.Args...)
	cmd.Env = s.env
	for k, v := range entry.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	s.connect(cmd)

	tools, err := s.cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools.Tools {
		if tl.Name == "wait" && !strings.Contains(tl.Description, "After timeout seconds (at most 3600)") {
			t.Fatalf("wait: %s", tl.Description)
		}
	}
	// Its instructions are Claude Code's own: one call, no loop (#359).
	if got := s.cs.InitializeResult().Instructions; !strings.Contains(got, "- To watch agents, call wait once") || strings.Contains(got, "in a loop") {
		t.Fatalf("instructions: %s", got)
	}
	// A turn the supervisor started ends without a notification; a wait
	// longer than Claude Desktop's returns as soon as it ends (an agent
	// just started is done once before its first prompt).
	app := testutil.GitRepo(t, "app")
	s.ok("new", map[string]any{"name": "a", "dir": app, "prompt": "hello"})
	since := "1m"
	for done := false; !done; {
		start := time.Now()
		out := s.ok("wait", map[string]any{"since": since, "names": []string{"a"}, "timeout": 120})
		var w waited
		if json.Unmarshal([]byte(out), &w) != nil || len(w.Agents) != 1 || time.Since(start) > 30*time.Second {
			t.Fatalf("wait after %v: %s", time.Since(start), out)
		}
		since, done = w.NextSince, w.Agents[0].Last == "Done: hello"
	}
	if b, _ := os.ReadFile(record); len(b) != 0 {
		t.Fatalf("notified %q", b)
	}
	// What the user sends from a shell notifies them as ever.
	s.shell("send", "a", "again")
	s.untilRow(since, "a", "done", func(r row) bool { return r.Last == "Done: again" })
	for i := 0; i < 50; i++ {
		if b, _ := os.ReadFile(record); strings.Contains(string(b), "Done: main") {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	b, _ = os.ReadFile(record)
	t.Fatalf("hq send from a shell: notified %q", b)
}

// rawSupervisor is hq mcp claude-code spoken to over its standard input and
// output in lines of JSON, as Claude Code 2.1.332 speaks to it: protocol
// 2026-07-28, server/discover and no initialize, each request declaring the
// MCP Tasks extension in its _meta (#361). The go-sdk client cannot see a
// task handle.
type rawSupervisor struct {
	t     *testing.T
	in    io.WriteCloser
	lines chan string
	n     int
}

// rawResult is what a raw supervisor reads in a result: a tool's, a task's
// handle or a task.
type rawResult struct {
	ResultType string `json:"resultType"`
	TaskID     string `json:"taskId"`
	Status     string `json:"status"`
	Content    []struct {
		Text string `json:"text"`
	} `json:"content"`
	Result *struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	} `json:"result"`
}

// raw starts an hq mcp claude-code in the supervisor's environment, as a
// Claude Code session starts one, and says server/discover to it.
func (s *supervisor) raw() *rawSupervisor {
	s.t.Helper()
	cmd := exec.Command(s.bin, "mcp", "claude-code")
	cmd.Env = s.env
	in, err := cmd.StdinPipe()
	if err != nil {
		s.t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		s.t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		s.t.Fatal(err)
	}
	r := &rawSupervisor{t: s.t, in: in, lines: make(chan string, 64)}
	go func() {
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			r.lines <- sc.Text()
		}
		close(r.lines)
	}()
	s.t.Cleanup(func() {
		_ = in.Close()
		ended := make(chan error, 1)
		go func() { ended <- cmd.Wait() }()
		select {
		case <-ended:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			s.t.Errorf("hq mcp claude-code did not end within 10 s of its input's end")
		}
	})
	r.request("server/discover", nil, 10*time.Second)
	return r
}

// request sends method with params and returns its result, failing the
// test on an error or when no answer comes within bound.
func (r *rawSupervisor) request(method string, params map[string]any, bound time.Duration) rawResult {
	r.t.Helper()
	p := map[string]any{"_meta": map[string]any{
		"io.modelcontextprotocol/protocolVersion":    "2026-07-28",
		"io.modelcontextprotocol/clientInfo":         map[string]any{"name": "claude-code", "version": "2.1.332"},
		"io.modelcontextprotocol/clientCapabilities": map[string]any{"extensions": map[string]any{"io.modelcontextprotocol/tasks": map[string]any{}}},
	}}
	for k, v := range params {
		p[k] = v
	}
	r.n++
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": r.n, "method": method, "params": p})
	if _, err := r.in.Write(append(b, '\n')); err != nil {
		r.t.Fatalf("%s: %v", method, err)
	}
	timeout := time.After(bound)
	for {
		select {
		case l, ok := <-r.lines:
			if !ok {
				r.t.Fatalf("%s: hq mcp ended its output", method)
			}
			var m struct {
				ID     int             `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  json.RawMessage `json:"error"`
			}
			if json.Unmarshal([]byte(l), &m) != nil || m.ID != r.n {
				continue
			}
			var res rawResult
			if m.Error != nil || json.Unmarshal(m.Result, &res) != nil {
				r.t.Fatalf("%s: %s", method, l)
			}
			return res
		case <-timeout:
			r.t.Fatalf("%s: no answer within %v", method, bound)
		}
	}
}

// A wait that sees no change within its hold goes on as a wait task (#361,
// story 36): one hq mcp claude-code answers the call with the task's handle
// after 50 s, the agent it waits for ends its turn, and another, started
// afresh as a resumed Claude Code session starts one, answers for the task
// with the wait's result. Neither holds the wait in memory.
func TestAWaitTaskOneHqMcpHandedOffIsAnsweredByAnother(t *testing.T) {
	s := supervisorEnv(t)
	app := testutil.GitRepo(t, "app")
	s.shell("new", "a", app, "slow job")
	for deadline := time.Now().Add(20 * time.Second); !strings.Contains(s.shell("ls", "a"), "working"); time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("a not working: %s", s.shell("ls", "a"))
		}
	}
	first := s.raw()
	var ls waited
	if r := first.request("tools/call", map[string]any{"name": "list", "arguments": map[string]any{"names": []string{"a"}}}, 10*time.Second); len(r.Content) != 1 || json.Unmarshal([]byte(r.Content[0].Text), &ls) != nil {
		t.Fatalf("list: %+v", r)
	}
	start := time.Now()
	h := first.request("tools/call", map[string]any{"name": "wait", "arguments": map[string]any{"names": []string{"a"}, "since": ls.NextSince}}, 90*time.Second)
	if h.ResultType != "task" || h.Status != "working" || h.TaskID == "" || time.Since(start) < 50*time.Second {
		t.Fatalf("after %v: %+v", time.Since(start), h)
	}
	s.typeIn("a", "finish it")
	second := s.raw()
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		g := second.request("tasks/get", map[string]any{"taskId": h.TaskID}, 10*time.Second)
		if g.Status == "working" && time.Now().Before(deadline) {
			continue
		}
		var w waited
		if g.Status != "completed" || g.Result == nil || g.Result.IsError || len(g.Result.Content) != 1 || json.Unmarshal([]byte(g.Result.Content[0].Text), &w) != nil ||
			len(w.Agents) != 1 || w.Agents[0].Name != "a" || w.Agents[0].State != "done" || w.Agents[0].Last != "Done: slow job" || w.NextSince == "" {
			t.Fatalf("tasks/get: %+v", g)
		}
		return
	}
}

// desktopConfig is where Claude Desktop keeps its configuration under home
// on this machine.
func desktopConfig(home string) string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	}
	return filepath.Join(home, ".config", "Claude", "claude_desktop_config.json")
}

type config struct {
	MCPServers map[string]struct {
		Type    string            `json:"type"`
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
		Timeout int               `json:"timeout"`
	} `json:"mcpServers"`
	Preferences map[string]any `json:"preferences"`
	Startups    int            `json:"numStartups"`
}

func install(t *testing.T, bin string, env ...string) (string, string, error) {
	t.Helper()
	return installFor(t, bin, nil, env...)
}

// installFor runs hq mcp install with the client's name in args.
func installFor(t *testing.T, bin string, args []string, env ...string) (string, string, error) {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"mcp", "install"}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	var out, errOut strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	return out.String(), errOut.String(), err
}

func TestMCPInstallAddsHqToClaudeDesktopOnceAndKeepsTheRest(t *testing.T) {
	bin := buildHQ(t)
	home, _ := filepath.EvalSymlinks(t.TempDir())
	path := desktopConfig(home)
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	theirs := `{"mcpServers":{"github":{"command":"npx","env":{"GITHUB_TOKEN":"secret-123"}}},"preferences":{"zoom":1.25}}`
	_ = os.WriteFile(path, []byte(theirs), 0o600)
	// The native platform, on WSL too (#13).
	env := []string{"HOME=" + home, "PATH=/opt/tools/bin:/usr/bin:/bin", "HQ_TMUX_SOCKET=", "XDG_CONFIG_HOME=", "TMUX_TMPDIR=", "WSL_DISTRO_NAME=", "HQ_PLATFORM=native"}

	out, errOut, err := install(t, bin, env...)
	if err != nil || !strings.HasPrefix(out, "added hq to Claude Desktop ("+path+")\nthe file as it was: "+path+".hq-backup-") {
		t.Fatalf("%v %q %q", err, out, errOut)
	}
	var c config
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	hq := c.MCPServers["hq"]
	if hq.Command != bin || strings.Join(hq.Args, " ") != "mcp" || hq.Env["PATH"] != "/opt/tools/bin:/usr/bin:/bin" || len(hq.Env) != 1 {
		t.Fatalf("hq %+v", hq)
	}
	if c.MCPServers["github"].Env["GITHUB_TOKEN"] != "secret-123" || c.Preferences["zoom"] != 1.25 {
		t.Fatalf("the rest changed:\n%s", b)
	}

	out, _, err = install(t, bin, env...)
	if err != nil || out != "hq is already set up in Claude Desktop ("+path+")\n" {
		t.Fatalf("again: %v %q", err, out)
	}
	if after, _ := os.ReadFile(path); string(after) != string(b) {
		t.Fatal("the second install wrote")
	}
	backups, _ := filepath.Glob(path + ".hq-backup-*")
	if len(backups) != 1 {
		t.Fatalf("backups %q", backups)
	}
	if kept, _ := os.ReadFile(backups[0]); string(kept) != theirs {
		t.Fatalf("backup %q", kept)
	}

	// Without Claude Desktop's folder: nothing created, the entry printed.
	bare, _ := filepath.EvalSymlinks(t.TempDir())
	out, errOut, err = install(t, bin, append([]string{"HOME=" + bare}, env[1:]...)...)
	if err == nil || !strings.Contains(out, `"hq": {`) || !strings.HasPrefix(errOut, "hq: Claude Desktop not found: there is no "+filepath.Dir(desktopConfig(bare))+";") {
		t.Fatalf("no folder: %v %q %q", err, out, errOut)
	}
	if left, _ := os.ReadDir(bare); len(left) != 0 {
		t.Fatalf("no folder: install created %v", left)
	}

	// A file it cannot edit safely: left alone, the entry printed.
	_ = os.WriteFile(path, []byte("{broken"), 0o600)
	out, errOut, err = install(t, bin, env...)
	if err == nil || !strings.Contains(out, `"hq": {`) || !strings.HasPrefix(errOut, "hq: cannot edit "+path+" safely") {
		t.Fatalf("broken: %v %q %q", err, out, errOut)
	}
	if b, _ := os.ReadFile(path); string(b) != "{broken" {
		t.Fatalf("changed %q", b)
	}
}

// On WSL the configuration is in the Windows user's %APPDATA%, and Claude
// Desktop starts hq through wsl.exe (the WSL side is stubbed).
func TestMCPInstallOnWSLStartsHqThroughWslExe(t *testing.T) {
	bin := buildHQ(t)
	testutil.WSLStubs(t)
	root := t.TempDir()
	t.Setenv("WSL_STUB_ROOT", root)
	env := []string{"WSL_DISTRO_NAME=Stub", "HQ_TMUX_SOCKET=", "XDG_CONFIG_HOME=", "TMUX_TMPDIR="}
	path := root + "/mnt/c/Users/stub/AppData/Roaming/Claude/claude_desktop_config.json"

	// Windows without Claude Desktop has no %APPDATA%\Claude: nothing is
	// created there, the entry is printed (#10).
	out, errOut, err := install(t, bin, env...)
	if err == nil || !strings.Contains(out, `"command": "wsl.exe"`) || !strings.HasPrefix(errOut, "hq: Claude Desktop not found: there is no "+filepath.Dir(path)+";") {
		t.Fatalf("no folder: %v %q %q", err, out, errOut)
	}
	if left, _ := os.ReadDir(root); len(left) != 0 {
		t.Fatalf("no folder: install created %v", left)
	}

	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	out, errOut, err = install(t, bin, env...)
	if err != nil || !strings.HasPrefix(out, "added hq to Claude Desktop ("+path+")\n") {
		t.Fatalf("%v %q %q", err, out, errOut)
	}
	var c config
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatalf("%v %s", err, b)
	}
	hq := c.MCPServers["hq"]
	want := "-d Stub --exec /usr/bin/env PATH=" + os.Getenv("PATH") + " " + bin + " mcp"
	if hq.Command != "wsl.exe" || strings.Join(hq.Args, " ") != want || hq.Env != nil {
		t.Fatalf("hq %+v", hq)
	}

	// Installed from the Microsoft Store, Claude Desktop reads the
	// configuration in its package's folder: hq is set up there, and the one
	// in %APPDATA% stays as it is (#25).
	store := root + "/mnt/c/Users/stub/AppData/Local/Packages/Claude_pzs8sxrjxfjjc/LocalCache/Roaming/Claude/claude_desktop_config.json"
	_ = os.MkdirAll(filepath.Dir(store), 0o700)
	_ = os.WriteFile(store, []byte(`{"preferences":{"sidebarMode":"chat"}}`), 0o600)
	out, errOut, err = install(t, bin, env...)
	if err != nil || !strings.HasPrefix(out, "added hq to Claude Desktop ("+store+")\n") {
		t.Fatalf("store: %v %q %q", err, out, errOut)
	}
	var sc config
	sb, _ := os.ReadFile(store)
	if err := json.Unmarshal(sb, &sc); err != nil || sc.MCPServers["hq"].Command != "wsl.exe" || sc.Preferences["sidebarMode"] != "chat" {
		t.Fatalf("store: %v %s", err, sb)
	}
	if after, _ := os.ReadFile(path); string(after) != string(b) {
		t.Fatal("store: the configuration in %APPDATA% changed")
	}

	// Since late 2026 Claude Desktop from the Store reads Claude-Data and
	// leaves a marker in the package's folder, which it no longer reads:
	// hq is set up in Claude-Data, the package's folder stays as it is
	// (#154).
	data := root + "/mnt/c/Users/stub/AppData/Local/Claude-Data/claude_2f7a1c/claude_desktop_config.json"
	_ = os.MkdirAll(filepath.Dir(data), 0o700)
	_ = os.WriteFile(filepath.Join(filepath.Dir(store), ".user-data-left.1761040000"), nil, 0o600)
	before, _ := os.ReadFile(store)
	out, errOut, err = install(t, bin, env...)
	if err != nil || out != "added hq to Claude Desktop ("+data+")\nquit and reopen Claude Desktop to load it\n" {
		t.Fatalf("Claude-Data: %v %q %q", err, out, errOut)
	}
	if after, _ := os.ReadFile(store); string(after) != string(before) {
		t.Fatal("Claude-Data: the package's configuration changed")
	}
	// Running, Claude Desktop reads the folder its helpers are given: hq
	// is set up there, and says it was not the one it looked for first.
	other := root + "/mnt/c/Users/stub/AppData/Local/Claude-Data/claude_9b/claude_desktop_config.json"
	_ = os.MkdirAll(filepath.Dir(other), 0o700)
	t.Setenv("WSL_STUB_PROCESSES", `"C:\Program Files\WindowsApps\Claude_1.0.0_x64__pzs8sxrjxfjjc\app\claude.exe"`+"\r\n"+
		`"C:\Program Files\WindowsApps\Claude_1.0.0_x64__pzs8sxrjxfjjc\app\claude.exe" --type=renderer --user-data-dir="C:\Users\stub\AppData\Local\Claude-Data\claude_9b" --lang=en`+"\r\n")
	out, errOut, err = install(t, bin, env...)
	if err != nil || out != "Claude Desktop is running and reads "+other+", not "+data+", where hq looks first; hq is set up in the one it reads\n"+
		"added hq to Claude Desktop ("+other+")\nquit Claude Desktop from its icon in the system tray (closing its window is not enough) and open it again to load hq\n" {
		t.Fatalf("running: %v %q %q", err, out, errOut)
	}
}

func TestMCPInstallAddsHqToClaudeCodeOnceAndKeepsTheRest(t *testing.T) {
	bin := buildHQ(t)
	home, _ := filepath.EvalSymlinks(t.TempDir())
	path := filepath.Join(home, ".claude.json")
	code := []string{"claude-code"}
	env := []string{"HOME=" + home, "PATH=/opt/tools/bin:/usr/bin:/bin", "HQ_TMUX_SOCKET=hq-dev", "XDG_CONFIG_HOME=", "TMUX_TMPDIR=", "CLAUDE_CONFIG_DIR=", "WSL_DISTRO_NAME=", "HQ_PLATFORM=native"}

	// Claude Code never run: nothing created, the command printed.
	out, errOut, err := installFor(t, bin, code, env...)
	if err == nil || !strings.HasPrefix(out, "claude mcp add-json --scope user hq '{") || errOut != "hq: Claude Code not found: there is no "+path+"; run claude once, then hq mcp install claude-code again (or run the command above)\n" {
		t.Fatalf("no file: %v %q %q", err, out, errOut)
	}
	if left, _ := os.ReadDir(home); len(left) != 0 {
		t.Fatalf("no file: install created %v", left)
	}

	theirs := `{"numStartups": 7, "mcpServers": {"github": {"type": "stdio", "command": "npx", "env": {"GITHUB_TOKEN": "secret-123"}}}, "projects": {"/w/app": {"allowedTools": []}}}`
	_ = os.WriteFile(path, []byte(theirs), 0o600)
	out, errOut, err = installFor(t, bin, code, env...)
	if err != nil || !strings.HasPrefix(out, "added hq to Claude Code ("+path+")\nthe file as it was: "+path+".hq-backup-") || !strings.HasSuffix(out, "\nrestart Claude Code to load it\n") {
		t.Fatalf("%v %q %q", err, out, errOut)
	}
	var c config
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	hq := c.MCPServers["hq"]
	if hq.Type != "stdio" || hq.Command != bin || strings.Join(hq.Args, " ") != "mcp claude-code" || len(hq.Env) != 2 ||
		hq.Env["PATH"] != "/opt/tools/bin:/usr/bin:/bin" || hq.Env["HQ_TMUX_SOCKET"] != "hq-dev" || hq.Timeout != 3660000 {
		t.Fatalf("hq %+v", hq)
	}
	if c.MCPServers["github"].Env["GITHUB_TOKEN"] != "secret-123" || c.Startups != 7 || !strings.Contains(string(b), `"/w/app"`) {
		t.Fatalf("the rest changed:\n%s", b)
	}
	out, _, err = installFor(t, bin, code, env...)
	if err != nil || out != "hq is already set up in Claude Code ("+path+")\n" {
		t.Fatalf("again: %v %q", err, out)
	}
	if after, _ := os.ReadFile(path); string(after) != string(b) {
		t.Fatal("the second install wrote")
	}

	// Claude Code's configuration directory moved: the file there.
	moved, _ := filepath.EvalSymlinks(t.TempDir())
	_ = os.WriteFile(filepath.Join(moved, ".claude.json"), []byte("{}"), 0o600)
	out, _, err = installFor(t, bin, code, append(env, "CLAUDE_CONFIG_DIR="+moved)...)
	if err != nil || !strings.HasPrefix(out, "added hq to Claude Code ("+filepath.Join(moved, ".claude.json")+")\n") {
		t.Fatalf("CLAUDE_CONFIG_DIR: %v %q", err, out)
	}

	// A file it cannot edit safely: left alone, the command printed.
	_ = os.WriteFile(path, []byte("{broken"), 0o600)
	out, errOut, err = installFor(t, bin, code, env...)
	if err == nil || !strings.HasPrefix(out, "claude mcp add-json") || !strings.HasPrefix(errOut, "hq: cannot edit "+path+" safely") {
		t.Fatalf("broken: %v %q %q", err, out, errOut)
	}
	if b, _ := os.ReadFile(path); string(b) != "{broken" {
		t.Fatalf("changed %q", b)
	}
}

// On WSL hq sets itself up for Claude Code in WSL, with the entry it gets
// on macOS, and for Claude Code on Windows once that has run, with an entry
// that starts hq through wsl.exe as Claude Desktop's does (#115; the
// Windows side is stubbed).
func TestMCPInstallForClaudeCodeOnWSLAlsoSetsUpClaudeCodeOnWindows(t *testing.T) {
	bin := buildHQ(t)
	testutil.WSLStubs(t)
	root := t.TempDir()
	t.Setenv("WSL_STUB_ROOT", root)
	home := t.TempDir()
	path := filepath.Join(home, ".claude.json")
	_ = os.WriteFile(path, []byte("{}"), 0o600)
	env := []string{"WSL_DISTRO_NAME=Stub", "HOME=" + home, "CLAUDE_CONFIG_DIR=", "HQ_TMUX_SOCKET=", "XDG_CONFIG_HOME=", "TMUX_TMPDIR="}
	out, errOut, err := installFor(t, bin, []string{"claude-code"}, env...)
	if err != nil || out != "added hq to Claude Code ("+path+")\nthe file as it was: "+out[len("added hq to Claude Code ("+path+")\nthe file as it was: "):] {
		t.Fatalf("%v %q %q", err, out, errOut)
	}
	if left, _ := os.ReadDir(root); len(left) != 0 {
		t.Fatalf("Claude Code never ran on Windows: install created %v", left)
	}
	var c config
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatalf("%v %s", err, b)
	}
	if hq := c.MCPServers["hq"]; hq.Command != bin || strings.Join(hq.Args, " ") != "mcp claude-code" || hq.Env["PATH"] != os.Getenv("PATH") {
		t.Fatalf("hq %+v", hq)
	}

	windows := root + "/mnt/c/Users/stub/.claude.json"
	_ = os.MkdirAll(filepath.Dir(windows), 0o700)
	_ = os.WriteFile(windows, []byte(`{"numStartups": 4, "mcpServers": {"github": {"command": "cmd", "args": ["/c", "npx"]}}}`), 0o600)
	out, errOut, err = installFor(t, bin, []string{"claude-code"}, env...)
	if err != nil || !strings.HasPrefix(out, "hq is already set up in Claude Code ("+path+")\nadded hq to Claude Code on Windows ("+windows+")\nthe file as it was: ") || !strings.HasSuffix(out, "\nrestart Claude Code to load it\n") {
		t.Fatalf("%v %q %q", err, out, errOut)
	}
	b, _ = os.ReadFile(windows)
	c = config{}
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatalf("%v %s", err, b)
	}
	want := "-d Stub --exec /usr/bin/env PATH=" + os.Getenv("PATH") + " " + bin + " mcp claude-code"
	if hq := c.MCPServers["hq"]; hq.Type != "stdio" || hq.Command != "wsl.exe" || strings.Join(hq.Args, " ") != want || hq.Env != nil || hq.Timeout != 3660000 {
		t.Fatalf("hq on Windows %+v", hq)
	}
	if c.Startups != 4 || c.MCPServers["github"].Command != "cmd" {
		t.Fatalf("the rest changed:\n%s", b)
	}
	if _, err := os.Stat(windows + ".lock"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the lock is left: %v", err)
	}
}

// A Claude Code inside a sandbox reaches hq through sbx's gateway (#141):
// hq mcp install sbx registers hq with sbx once however often it runs, hq
// mcp load loads it into the repository's sandbox, and the command line sbx
// holds starts an hq that offers the eight tools, wait capped as for
// Claude Desktop, and lists the host's agents.
func TestASandboxsClaudeCodeSupervisesFromTheRegistrationHqMcpInstallSbxMade(t *testing.T) {
	s := supervisorEnv(t)
	dir := os.Getenv("SBX_STUB_DIR")
	hq := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(s.bin, args...)
		cmd.Env = s.env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("hq %q: %v %s", args, err, out)
		}
		return string(out)
	}
	if out := hq("mcp", "install", "sbx"); !strings.HasPrefix(out, "added hq to sbx") {
		t.Fatalf("install: %s", out)
	}
	if out := hq("mcp", "install", "sbx"); !strings.HasPrefix(out, "hq is already set up in sbx") {
		t.Fatalf("again: %s", out)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "mcp-adds.log")); string(b) != "hq\n" {
		t.Fatalf("registrations %q", b)
	}
	app := testutil.GitRepo(t, "app")
	hq("new", "a", app, "hello")
	if out := hq("mcp", "load", app); out != "loaded hq into the sandbox claude-app: every agent in it sees hq's tools\n" {
		t.Fatalf("load: %s", out)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "mcp-loads.log")); string(b) != "hq claude-app\n" {
		t.Fatalf("loads %q", b)
	}

	var registered struct {
		Command []string `json:"command"`
	}
	b, _ := os.ReadFile(filepath.Join(dir, "mcp", "hq"))
	if err := json.Unmarshal(b, &registered); err != nil || registered.Command[0] != "/usr/bin/env" {
		t.Fatalf("%v %s", err, b)
	}
	// The gateway starts it with an environment of its own, not the user's.
	cmd := exec.Command(registered.Command[0], registered.Command[1:]...)
	// The test's home, the last HOME in its environment.
	for _, kv := range s.env {
		if strings.HasPrefix(kv, "HOME=") {
			cmd.Env = []string{kv}
		}
	}
	s.connect(cmd)
	tools, err := s.cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range tools.Tools {
		names = append(names, tl.Name)
		if tl.Name == "wait" && !strings.Contains(tl.Description, "(default and at most 50)") {
			t.Fatalf("wait: %s", tl.Description)
		}
	}
	if slices.Sort(names); strings.Join(names, " ") != "answer go kill list new read send wait" {
		t.Fatalf("tools %q", names)
	}
	if out := s.ok("list", nil); !strings.Contains(out, `"name": "a"`) {
		t.Fatalf("list: %s", out)
	}
}
