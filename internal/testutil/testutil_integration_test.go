//go:build integration

package testutil

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// runServerTest runs a test that asks TmuxSocket for a socket and starts a
// server on it with a session running program, then calls before, and
// returns the server's socket path and the pid of its pane's program once
// the test's cleanup has run.
func runServerTest(t *testing.T, program string, before func(t *testing.T, socket string)) (path string, pid int) {
	t.Helper()
	passed := t.Run("server", func(t *testing.T) {
		socket := TmuxSocket(t)
		if out, err := exec.Command("tmux", "-u", "-L", socket, "new-session", "-d", "-s", "t", program).CombinedOutput(); err != nil {
			t.Fatalf("new-session: %v %s", err, out)
		}
		out, err := exec.Command("tmux", "-u", "-L", socket, "display-message", "-p", "#{socket_path}").Output()
		if err != nil {
			t.Fatalf("socket path: %v", err)
		}
		path = strings.TrimSpace(string(out))
		if out, err = exec.Command("tmux", "-u", "-L", socket, "list-panes", "-a", "-F", "#{pane_pid}").Output(); err != nil {
			t.Fatalf("pane pid: %v", err)
		}
		if pid, err = strconv.Atoi(strings.TrimSpace(string(out))); err != nil {
			t.Fatalf("pane pid %q: %v", out, err)
		}
		before(t, socket)
	})
	if !passed {
		t.FailNow()
	}
	return path, pid
}

// serverEnded waits for the server on socket to end on its own, as it does
// once its last session has: tmux leaves the socket file behind then too.
func serverEnded(t *testing.T, socket string) {
	t.Helper()
	running := func() bool { return exec.Command("tmux", "-u", "-L", socket, "display-message", "-p", "").Run() == nil }
	for i := 0; i < 50 && running(); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if running() {
		t.Fatal("the server is still running")
	}
}

func socketFileGone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("socket file %s left behind: %v", path, err)
	}
}

// tmuxTmpdir points TMUX_TMPDIR at a new directory for this test, short as
// a socket's path must fit a socket address (#50), and returns it with
// symlinks resolved.
func tmuxTmpdir(t *testing.T) string {
	t.Helper()
	tmp, err := os.MkdirTemp("/tmp", "hq")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmp) })
	t.Setenv("TMUX_TMPDIR", tmp)
	real, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func under(t *testing.T, path, dir string) {
	t.Helper()
	if !strings.HasPrefix(path, dir+string(filepath.Separator)) {
		t.Fatalf("socket %s not under TMUX_TMPDIR %s", path, dir)
	}
}

func TestATestsTmuxServerAndItsSocketFileAreGoneWhenItEnds(t *testing.T) {
	path, pid := runServerTest(t, "sleep 60", func(*testing.T, string) {})
	socketFileGone(t, path)
	if syscall.Kill(pid, 0) == nil {
		t.Fatalf("pane program %d still running", pid)
	}
}

func TestATestsSocketFileIsGoneWhenItsServerAlreadyEnded(t *testing.T) {
	path, _ := runServerTest(t, "sleep 0.3", serverEnded)
	socketFileGone(t, path)
}

func TestATestsSocketFileIsGoneUnderTmuxTmpdir(t *testing.T) {
	dir := tmuxTmpdir(t)
	path, _ := runServerTest(t, "sleep 60", func(*testing.T, string) {})
	under(t, path, dir)
	socketFileGone(t, path)
	path, _ = runServerTest(t, "sleep 0.3", serverEnded)
	under(t, path, dir)
	socketFileGone(t, path)
}

func TestATestThatNeverStartsAServerCleansUpWithoutError(t *testing.T) {
	var socket string
	if !t.Run("no server", func(t *testing.T) { socket = TmuxSocket(t) }) {
		t.FailNow()
	}
	if err := exec.Command("tmux", "-u", "-L", socket, "has-session").Run(); err == nil {
		t.Fatal("a server is running")
	}
}
