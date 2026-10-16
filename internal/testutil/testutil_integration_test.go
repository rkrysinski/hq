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

// socketAfter runs a test that asks TmuxSocket for a socket and does start
// with it, and returns the server's socket path, the pid of its pane's
// program and whether the test passed, once its cleanup has run.
func socketAfter(t *testing.T, start func(t *testing.T, socket string)) (path string, pid int, passed bool) {
	t.Helper()
	passed = t.Run("server", func(t *testing.T) {
		socket := TmuxSocket(t)
		start(t, socket)
		out, err := exec.Command("tmux", "-u", "-L", socket, "display-message", "-p", "#{socket_path}").Output()
		if err != nil {
			t.Fatalf("socket path: %v", err)
		}
		path = strings.TrimSpace(string(out))
		if out, err = exec.Command("tmux", "-u", "-L", socket, "list-panes", "-a", "-F", "#{pane_pid}").Output(); err != nil {
			t.Fatalf("pane pid: %v", err)
		}
		pid, _ = strconv.Atoi(strings.TrimSpace(string(out)))
	})
	return path, pid, passed
}

func newSession(t *testing.T, socket, program string) {
	t.Helper()
	if out, err := exec.Command("tmux", "-u", "-L", socket, "new-session", "-d", "-s", "t", program).CombinedOutput(); err != nil {
		t.Fatalf("new-session: %v %s", err, out)
	}
}

func gone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("socket file %s left behind: %v", path, err)
	}
}

func TestATestsTmuxServerAndItsSocketFileAreGoneWhenItEnds(t *testing.T) {
	path, pid, passed := socketAfter(t, func(t *testing.T, socket string) { newSession(t, socket, "sleep 60") })
	if !passed {
		t.Fatal("the test failed")
	}
	gone(t, path)
	if syscall.Kill(pid, 0) == nil {
		t.Fatalf("pane program %d still running", pid)
	}
}

func TestATestsSocketFileIsGoneWithTMUX_TMPDIRSet(t *testing.T) {
	// Short, as a socket's path must fit a socket address (#50).
	tmp, err := os.MkdirTemp("/tmp", "hq")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmp) })
	t.Setenv("TMUX_TMPDIR", tmp)
	path, _, passed := socketAfter(t, func(t *testing.T, socket string) { newSession(t, socket, "sleep 60") })
	if !passed {
		t.Fatal("the test failed")
	}
	if real, _ := filepath.EvalSymlinks(tmp); !strings.HasPrefix(path, real+string(filepath.Separator)) {
		t.Fatalf("socket %s not under TMUX_TMPDIR %s", path, real)
	}
	gone(t, path)
}

func TestATestsSocketFileIsGoneWhenItsServerAlreadyExited(t *testing.T) {
	// The server ends on its own once its last session does, and tmux
	// leaves the socket file behind.
	path, _, passed := socketAfter(t, func(t *testing.T, socket string) {
		newSession(t, socket, "sleep 0.5")
		t.Cleanup(func() {
			for i := 0; i < 50 && exec.Command("tmux", "-u", "-L", socket, "has-session").Run() == nil; i++ {
				time.Sleep(100 * time.Millisecond)
			}
		})
	})
	if !passed {
		t.Fatal("the test failed")
	}
	gone(t, path)
}

func TestATestThatNeverStartsAServerCleansUpWithoutError(t *testing.T) {
	var socket string
	if !t.Run("no server", func(t *testing.T) { socket = TmuxSocket(t) }) {
		t.Fatal("the test failed")
	}
	if err := exec.Command("tmux", "-u", "-L", socket, "has-session").Run(); err == nil {
		t.Fatal("a server is running")
	}
}
