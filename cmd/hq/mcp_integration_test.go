//go:build integration

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
}

// row is an agent as the list, read and wait tools give it.
type row struct {
	Name    string          `json:"name"`
	State   string          `json:"state"`
	Last    string          `json:"last"`
	Pending int             `json:"pending"`
	Reply   string          `json:"reply"`
	Asks    json.RawMessage `json:"asks"`
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
	s := &supervisor{t: t, socket: testutil.TmuxSocket(t)}
	cmd := exec.Command(buildHQ(t), "mcp")
	cmd.Env = append(os.Environ(), "PATH="+path+string(os.PathListSeparator)+os.Getenv("PATH"), "HQ_TMUX_SOCKET="+s.socket, "HOME="+t.TempDir(), "TMUX=")
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
	for i := 0; i < 8; i++ {
		var w waited
		out := s.ok("wait", map[string]any{"since": since, "timeout": 5})
		if err := json.Unmarshal([]byte(out), &w); err != nil {
			s.t.Fatalf("wait: %v\n%s", err, out)
		}
		since = w.NextSince
		for _, r := range w.Agents {
			if r.Name == name && r.State == st {
				return r, since
			}
		}
	}
	out, _ := exec.Command("tmux", "-L", s.socket, "capture-pane", "-p", "-a", "-t", "hq:").Output()
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
	out, _ := exec.Command("tmux", "-L", s.socket, "list-windows", "-a", "-F", "#{window_id} #{@hq_name}").Output()
	for _, w := range strings.Split(string(out), "\n") {
		if id, n, _ := strings.Cut(w, " "); n == name {
			if out, err := exec.Command("tmux", "-L", s.socket, "send-keys", "-t", id, line, "Enter").CombinedOutput(); err != nil {
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
	r, since := s.until("1m", "a", "done")
	if r.Last != "Done: hello" {
		t.Fatalf("a %+v", r)
	}
	s.readUntil("b", "working")

	// Ask b, which is at work: the message waits for its stop.
	how, sent := s.send(map[string]any{"name": "b", "text": "check the logs"})
	if how != "queued: b is working, delivered when it stops" {
		t.Fatalf("send: %q", how)
	}
	if r := s.read("b"); r.State != "working" || r.Pending != 1 {
		t.Fatalf("b %+v", r)
	}
	// b answers before wait is called: wait from send's next_since still
	// returns the answer.
	s.typeIn("b", "go on")
	s.readUntil("b", "done")
	r, since = s.until(sent, "b", "done")
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
	if out := s.ok("kill", map[string]any{"name": "a"}); out != "killed a; the sandbox stays\n" {
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
}
