//go:build integration

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/platform/platformtest"
	"github.com/rkrysinski/hq/internal/proc"
	"github.com/rkrysinski/hq/internal/repo"
	"github.com/rkrysinski/hq/internal/sbx"
	"github.com/rkrysinski/hq/internal/testutil"
	"github.com/rkrysinski/hq/internal/tmux"
	"github.com/rkrysinski/hq/internal/update"
	"github.com/rkrysinski/hq/internal/version"
)

type realHQ struct {
	t      *testing.T
	d      deps
	cwd    string
	socket string // the private tmux server
}

// newRealHQ wires hq to a private tmux server and the stub sbx.
func newRealHQ(t *testing.T) *realHQ {
	bin, _ := testutil.SbxStub(t)
	h := &realHQ{t: t, socket: testutil.TmuxSocket(t)}
	d := defaultDeps()
	d.tmux = tmux.Client{Run: proc.Exec{}, Socket: h.socket}
	d.sbx = sbx.Client{Run: proc.Exec{}, Platform: platformtest.Fake{Sbx: bin}}
	d.getwd = func() (string, error) { return h.cwd, nil }
	h.d = d
	return h
}

func (h *realHQ) run(args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := mainWith(args, Env{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errOut}, h.d)
	return code, out.String(), errOut.String()
}

func (h *realHQ) ls() []lsRow {
	h.t.Helper()
	code, out, errOut := h.run("ls", "--json")
	var rows []lsRow
	if code != 0 || json.Unmarshal([]byte(out), &rows) != nil {
		h.t.Fatalf("ls: exit %d %q %q", code, out, errOut)
	}
	return rows
}

// waitSessions waits until the stub sbx has recorded n sessions in sandbox,
// which it does a moment after tmux shows them running; a stop before that
// would miss them.
func (h *realHQ) waitSessions(sandbox string, n int) {
	h.t.Helper()
	for i := 0; i < 50; i++ {
		b, _ := os.ReadFile(os.Getenv("SBX_STUB_DIR") + "/pids-" + sandbox)
		if strings.Count(string(b), "\n") >= n {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	h.t.Fatalf("%s: the stub has not recorded %d sessions", sandbox, n)
}

func (h *realHQ) waitState(name, state string) lsRow {
	h.t.Helper()
	for i := 0; i < 50; i++ {
		for _, r := range h.ls() {
			if r.Name == name && r.State == state {
				return r
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	h.t.Fatalf("%s never became %s: %+v", name, state, h.ls())
	return lsRow{}
}

// waitReport waits until name shows state with last as its last message.
func (h *realHQ) waitReport(name, state, last string) {
	h.t.Helper()
	for i := 0; i < 50; i++ {
		if r := h.waitState(name, state); r.Last == last {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	h.t.Fatalf("%s never showed %s with %q: %+v", name, state, last, h.ls())
}

// typeIn types a line into the agent's session, as a person in hq go does.
func (h *realHQ) typeIn(name, line string) {
	h.t.Helper()
	ws, _ := h.d.tmux.Windows()
	a, ok := agent.Find(agent.FromWindows(ws), name)
	if !ok {
		h.t.Fatalf("no agent %s", name)
	}
	if out, err := exec.Command("tmux", "-L", h.socket, "send-keys", "-t", a.Window, line, "Enter").CombinedOutput(); err != nil {
		h.t.Fatalf("send-keys: %v %s", err, out)
	}
}

func TestNewStartsSessionInSandboxAndLsListsIt(t *testing.T) {
	h := newRealHQ(t)
	r := testutil.GitRepo(t, "app")
	h.cwd = r
	code, out, errOut := h.run("new", "a", "say hi")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "creating a sandbox for app") || !strings.Contains(out, "started a in app (sandbox claude-app)") {
		t.Fatalf("stdout %q", out)
	}
	h.waitState("a", "starting")
	// The window runs as soon as it is released; the stub logs its args a moment later.
	var log []byte
	for i := 0; i < 50 && !strings.Contains(string(log), "say hi"); i++ {
		time.Sleep(100 * time.Millisecond)
		log, _ = os.ReadFile(os.Getenv("SBX_STUB_DIR") + "/runs.log")
	}
	if !strings.Contains(string(log), `"HQ_AGENT":"a"`) || !strings.HasSuffix(strings.TrimSpace(string(log)), "say hi") {
		t.Fatalf("session args %q", log)
	}

	// A second agent from a worktree of the same repository shares the sandbox.
	wt := r + "/.claude/worktrees/x"
	testutil.Git(t, r, "worktree", "add", "-q", "-b", "x", wt)
	h.cwd = wt
	if code, out, errOut := h.run("new", "b"); code != 0 || !strings.Contains(out, "sandbox claude-app") || strings.Contains(out, "creating") {
		t.Fatalf("second agent: exit %d %q %q", code, out, errOut)
	}
	if rows := h.ls(); len(rows) != 2 || !repo.Same(rows[0].RepoPath, r) {
		t.Fatalf("rows %+v", rows)
	}
	if code, _, _ := h.run("new", "a"); code != ExitUsage {
		t.Fatalf("duplicate: exit %d", code)
	}
}

func TestStatesFollowTheSessionAndLeaveTheRepositoryClean(t *testing.T) {
	testutil.FakeClaude(t)
	t.Setenv("FAKE_CLAUDE_DELAY", "600ms")
	h := newRealHQ(t)
	r := testutil.GitRepo(t, "app")
	h.cwd = r
	if code, _, errOut := h.run("new", "a", "hello"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	h.waitState("a", "starting")
	h.waitState("a", "working")
	h.waitReport("a", "done", "Done: hello")

	h.typeIn("a", "a question")
	h.waitReport("a", "working", "Done: hello")
	h.waitReport("a", "question", "Shall I go on?")

	h.typeIn("a", "needs input")
	h.waitReport("a", "needs input", "Claude needs your permission")
	h.typeIn("a", "yes")
	h.waitReport("a", "done", "Done: needs input")

	// An agent in a worktree reports to the repository, like its siblings.
	wt := filepath.Join(t.TempDir(), "x")
	testutil.Git(t, r, "worktree", "add", "-q", "-b", "x", wt)
	h.cwd = wt
	if code, _, errOut := h.run("new", "b", "hi"); code != 0 {
		t.Fatalf("new b: exit %d: %s", code, errOut)
	}
	h.waitReport("b", "done", "Done: hi")

	// A session that ends keeps its last message.
	h.typeIn("a", "/exit")
	h.waitReport("a", "ended", "Done: needs input")

	for _, dir := range []string{r, wt} {
		cmd := exec.Command("git", "status", "--porcelain")
		cmd.Dir = dir
		if out, err := cmd.Output(); err != nil || len(out) != 0 {
			t.Fatalf("%s is not clean: %q", dir, out)
		}
	}
}

func TestAgentWhoseSessionExitsIsEndedAndStaysListed(t *testing.T) {
	h := newRealHQ(t)
	h.cwd = testutil.GitRepo(t, "app")
	t.Setenv("SBX_STUB_EXIT", "1")
	if code, _, errOut := h.run("new", "a"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	h.waitState("a", "ended")
	code, out, _ := h.run("ls")
	if code != 0 || !strings.Contains(out, "ended") {
		t.Fatalf("ls: %d %q", code, out)
	}
}

func TestKillEndsTheSessionAndFreesTheName(t *testing.T) {
	h := newRealHQ(t)
	h.cwd = testutil.GitRepo(t, "app")
	if code, _, errOut := h.run("new", "a"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	h.waitState("a", "starting")
	ws, _ := h.d.tmux.Windows()
	a, _ := agent.Find(agent.FromWindows(ws), "a")
	marker := `HQ_ID":"` + a.ID + `"`
	if exec.Command("pgrep", "-f", marker).Run() != nil {
		t.Fatal("the session is not running before the kill")
	}
	if code, out, errOut := h.run("kill", "a", "-y"); code != 0 || out != "killed a; the sandbox stays\n" {
		t.Fatalf("kill: exit %d %q %q", code, out, errOut)
	}
	if exec.Command("pgrep", "-f", marker).Run() == nil {
		t.Fatal("the session still runs after the kill")
	}
	if rows := h.ls(); len(rows) != 0 {
		t.Fatalf("rows %+v", rows)
	}
	if code, _, errOut := h.run("new", "a"); code != 0 {
		t.Fatalf("name not freed: exit %d %s", code, errOut)
	}
}

func TestSandboxRestartRelaunchesAgentsAndRmRemovesTheSandbox(t *testing.T) {
	h := newRealHQ(t)
	h.cwd = testutil.GitRepo(t, "app")
	for _, name := range []string{"a", "b"} {
		if code, _, errOut := h.run("new", name); code != 0 {
			t.Fatalf("new %s: exit %d %s", name, code, errOut)
		}
		h.waitState(name, "starting")
	}
	h.waitSessions("claude-app", 2)
	ids := func() map[string]string {
		ws, _ := h.d.tmux.Windows()
		m := map[string]string{}
		for _, a := range agent.FromWindows(ws) {
			m[a.Name] = a.ID
		}
		return m
	}
	before := ids()

	if code, out, errOut := h.run("sandbox", "restart", "app"); code != 0 || out != "restarted the sandbox claude-app; relaunched a, b\n" {
		t.Fatalf("restart: exit %d %q %q", code, out, errOut)
	}
	h.waitState("a", "starting")
	h.waitState("b", "starting")
	after := ids()
	for name, id := range before {
		if after[name] == "" || after[name] == id {
			t.Fatalf("%s: id before %s, after %s", name, id, after[name])
		}
		if exec.Command("pgrep", "-f", `HQ_ID":"`+id+`"`).Run() == nil {
			t.Fatalf("%s's old session still runs", name)
		}
	}

	if code, _, errOut := h.run("sandbox", "rm", "app", "-y"); code != ExitUsage || !strings.Contains(errOut, "(a, b)") {
		t.Fatalf("rm with agents: exit %d %q", code, errOut)
	}
	if code, _, _ := h.run("stop", "-y"); code != 0 {
		t.Fatal("stop")
	}
	if code, out, errOut := h.run("sandbox", "rm", h.cwd, "-y"); code != 0 || out != "removed the sandbox claude-app\n" {
		t.Fatalf("rm: exit %d %q %q", code, out, errOut)
	}
	if code, _, _ := h.run("sandbox", "rm", "app", "-y"); code != ExitNotFound {
		t.Fatalf("rm again: exit %d", code)
	}
}

func TestVersionHintKeepsItsCheckInThePreferencesFile(t *testing.T) {
	gh, ghDir := testutil.GhStub(t)
	if err := os.WriteFile(ghDir+"/latest", []byte("v0.2.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	old := version.Version
	version.Version = "v0.1.0"
	defer func() { version.Version = old }()
	d := defaultDeps()
	d.releases = update.Releases{Run: proc.Exec{}, Bin: gh}
	var out bytes.Buffer
	if code := mainWith([]string{"--version"}, Env{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &out}, d); code != 0 || out.String() != "hq v0.1.0\nv0.2.0 available - hq update\n" {
		t.Fatalf("exit %d %q", code, out.String())
	}
	data, err := os.ReadFile(cfg + "/hq/preferences.json")
	if err != nil || !strings.Contains(string(data), `"latest_release": "v0.2.0"`) {
		t.Fatalf("%v %s", err, data)
	}
	if exe, err := d.executable(); err != nil || !filepath.IsAbs(exe) {
		t.Fatalf("executable %q %v", exe, err)
	}
}
