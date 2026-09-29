//go:build integration || e2e

// Package testutil sets up real tmux, a stub sbx and git repositories for
// integration and end-to-end tests, never touching the user's own tmux
// server or sandboxes (docs/agents/testing.md).
package testutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

var sockets atomic.Int64

// TmuxSocket returns a tmux socket name private to this test and ends that
// server when the test finishes.
func TmuxSocket(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	name := fmt.Sprintf("hq-test-%d-%d", os.Getpid(), sockets.Add(1))
	t.Cleanup(func() { killServer(name) })
	return name
}

// killServer ends a test's tmux server and waits for the programs in its
// panes to exit: tmux returns before they do, and one still starting (the
// stub sbx right after hq new) would write into a directory the test's
// cleanup is removing.
func killServer(socket string) {
	out, _ := exec.Command("tmux", "-L", socket, "list-panes", "-a", "-F", "#{pane_pid}").Output()
	_ = exec.Command("tmux", "-L", socket, "kill-server").Run()
	for _, f := range strings.Fields(string(out)) {
		pid, err := strconv.Atoi(f)
		if err != nil {
			continue
		}
		for i := 0; i < 50 && syscall.Kill(pid, 0) == nil; i++ {
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// SbxStub returns the path of the stub sbx and its state directory, set in
// SBX_STUB_DIR for this test.
func SbxStub(t *testing.T) (bin, dir string) {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	bin = filepath.Join(filepath.Dir(file), "sbx-stub")
	dir = t.TempDir()
	t.Setenv("SBX_STUB_DIR", dir)
	return bin, dir
}

// WSLStubs puts the stub sbx on PATH as sbx.exe, the stub wslpath as
// wslpath and the stub cmd.exe (it knows only %APPDATA%) as cmd.exe, as hq
// finds them on WSL, and returns the stub sbx's state
// directory, set in SBX_STUB_DIR for this test.
func WSLStubs(t *testing.T) string {
	t.Helper()
	sbx, dir := SbxStub(t)
	bin := t.TempDir()
	for name, target := range map[string]string{"sbx.exe": sbx, "wslpath": filepath.Join(filepath.Dir(sbx), "wslpath-stub"), "cmd.exe": filepath.Join(filepath.Dir(sbx), "cmd-stub")} {
		if err := os.Symlink(target, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

var (
	fakeClaudeOnce sync.Once
	fakeClaudeBin  string
	fakeClaudeErr  error
)

// FakeClaude builds the fake claude (once per test binary) and has the stub
// sbx run it for this test's agents, so they fire the injected hooks.
func FakeClaude(t *testing.T) {
	t.Helper()
	fakeClaudeOnce.Do(func() {
		dir, err := os.MkdirTemp("", "hq-fakeclaude-")
		if err != nil {
			fakeClaudeErr = err
			return
		}
		_, file, _, _ := runtime.Caller(0)
		fakeClaudeBin = filepath.Join(dir, "claude")
		cmd := exec.Command("go", "build", "-o", fakeClaudeBin, "github.com/rkrysinski/hq/tools/fakeclaude")
		cmd.Dir = filepath.Dir(file)
		if out, err := cmd.CombinedOutput(); err != nil {
			fakeClaudeErr = fmt.Errorf("build fake claude: %v\n%s", err, out)
		}
	})
	if fakeClaudeErr != nil {
		t.Fatal(fakeClaudeErr)
	}
	t.Setenv("SBX_STUB_CLAUDE", fakeClaudeBin)
}

// GitRepo creates a git repository with one commit and returns its path with
// symlinks resolved.
func GitRepo(t *testing.T, name string) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, name)
	Git(t, base, "init", "-q", "-b", "main", name)
	Git(t, dir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init")
	return dir
}

// Git runs git in dir.
func Git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
