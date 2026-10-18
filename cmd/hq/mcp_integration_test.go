//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/testutil"
)

// buildHQ builds hq into a temporary directory.
func buildHQ(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "hq")
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
	// hq from a shell beside it, as the user does.
	bin string
	env []string
}

// row is an agent as the list, read and wait tools give it.
type row struct {
	Name    string          `json:"name"`
	State   string          `json:"state"`
	Last    string          `json:"last"`
	Pending int             `json:"pending"`
	Reply   string          `json:"reply"`
	Asks    json.RawMessage `json:"asks"`
	Dialog  string          `json:"dialog"`
}

type waited struct {
	Agents    []row  `json:"agents"`
	NextSince string `json:"next_since"`
}

func newSupervisor(t *testing.T) *supervisor {
	t.Helper()
	testutil.FakeClaude(t)
	stub, _ := testutil.SbxStub(t)
	path := t.TempDir()
	if err := os.Symlink(stub, filepath.Join(path, "sbx")); err != nil {
		t.Fatal(err)
	}
	s := &supervisor{t: t, socket: testutil.TmuxSocket(t), bin: buildHQ(t)}
	s.env = append(os.Environ(), "PATH="+path+string(os.PathListSeparator)+os.Getenv("PATH"), "HQ_TMUX_SOCKET="+s.socket, "HOME="+t.TempDir(), "TMUX=")
	cmd := exec.Command(s.bin, "mcp")
	cmd.Env = s.env
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "supervisor", Version: "v0"}, nil).Connect(context.Background(), &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.cs = cs
	t.Cleanup(func() { _ = cs.Close() })
	return s
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

// The desktop notifications of issue #46's acceptance, on the fake Claude,
// which records each one a hook sends: work the supervisor sent ends
// without one, a dialog and anything of the user's still notify.
func TestWorkTheSupervisorSentEndsWithoutANotification(t *testing.T) {
	record := filepath.Join(t.TempDir(), "notified")
	t.Setenv("FAKE_CLAUDE_NOTIFIED", record)
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
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
	} `json:"mcpServers"`
	Preferences map[string]any `json:"preferences"`
}

func install(t *testing.T, bin string, env ...string) (string, string, error) {
	t.Helper()
	cmd := exec.Command(bin, "mcp", "install")
	cmd.Env = append(os.Environ(), env...)
	var out, errOut strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	return out.String(), errOut.String(), err
}

func TestMCPInstallAddsHqToClaudeDesktopOnceAndKeepsTheRest(t *testing.T) {
	bin, _ := filepath.EvalSymlinks(buildHQ(t))
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
	bin, _ := filepath.EvalSymlinks(buildHQ(t))
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
}
