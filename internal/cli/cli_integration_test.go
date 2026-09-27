//go:build integration

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/proc"
	"github.com/rkrysinski/hq/internal/repo"
	"github.com/rkrysinski/hq/internal/sbx"
	"github.com/rkrysinski/hq/internal/testutil"
	"github.com/rkrysinski/hq/internal/tmux"
)

type realHQ struct {
	t   *testing.T
	d   deps
	cwd string
}

// newRealHQ wires hq to a private tmux server and the stub sbx.
func newRealHQ(t *testing.T) *realHQ {
	bin, _ := testutil.SbxStub(t)
	h := &realHQ{t: t}
	d := defaultDeps()
	d.tmux = tmux.Client{Run: proc.Exec{}, Socket: testutil.TmuxSocket(t)}
	d.sbx = sbx.Client{Run: proc.Exec{}, Bin: bin}
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

func (h *realHQ) waitState(name, state string) {
	h.t.Helper()
	for i := 0; i < 50; i++ {
		for _, r := range h.ls() {
			if r.Name == name && r.State == state {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	h.t.Fatalf("%s never became %s: %+v", name, state, h.ls())
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
	h.waitState("a", "running")
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
	h.waitState("a", "running")
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
