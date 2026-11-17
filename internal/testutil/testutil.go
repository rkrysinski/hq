//go:build integration || e2e

// Package testutil sets up real tmux, a stub sbx and git repositories for
// integration and end-to-end tests, never touching the user's own tmux
// server or sandboxes (docs/hq/testing.md).
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

// platformEnv is platform.OverrideEnv; named here because the packages
// platform is built on use testutil in their own tests.
const platformEnv = "HQ_PLATFORM"

var sockets atomic.Int64

// TmuxSocket returns a tmux socket name private to this test and ends that
// server when the test finishes.
func TmuxSocket(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	name := fmt.Sprintf("hq-test-%d-%d", os.Getpid(), sockets.Add(1))
	t.Cleanup(func() { killServer(name, t.Logf) })
	return name
}

// killServer ends a test's tmux server, waits for what runs in its panes
// to exit, and removes its socket file. tmux returns before the programs
// exit, and one still starting (the stub sbx right after hq new) would
// write into a directory the test's cleanup is removing. A pane's program
// can exit before what it started: the server's hangup ends hq's session
// command at once, while the fake Claude it runs fires its SessionEnd hook,
// which writes beside the repository (#191). The fake can even exit before
// a hook of its own, one it started after the hangup, which then writes on
// with no parent (#191): tmux makes each pane's program lead a process
// group, which what it starts keeps, so killServer waits until no process
// of those groups is left (running, waitGone), and names through logf what
// still runs when it stops waiting. tmux 3.6 leaves the socket file behind
// however its server ends (#74), and gives its path only while the server
// runs.
func killServer(socket string, logf func(format string, args ...any)) {
	path := socketPath(socket)
	out, _ := exec.Command("tmux", "-u", "-L", socket, "list-panes", "-a", "-F", "#{pane_pid}").Output()
	var panes []int
	for _, f := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(f); err == nil {
			panes = append(panes, pid)
		}
	}
	all := append([]int{}, panes...)
	for _, p := range running(panes) {
		all = append(all, p.pid)
	}
	_ = exec.Command("tmux", "-u", "-L", socket, "kill-server").Run()
	waitGone(all, running, time.Sleep, logf)
	_ = os.Remove(path)
}

// running is what runs of pids, of the processes below them and of the
// process groups they lead, as ps lists the processes now (within); what
// runs of pids alone when ps cannot list them, with no command. A process
// that has exited runs nothing, though until its parent reaps it a signal
// 0 still finds it: what a test asks of a process the cleanup waited for,
// it asks here, so the two agree on when the process is gone (#191).
func running(pids []int) []process {
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=,pgid=,stat=,args=").Output()
	if err != nil {
		var alive []process
		for _, pid := range pids {
			if syscall.Kill(pid, 0) == nil {
				alive = append(alive, process{pid: pid})
			}
		}
		return alive
	}
	return within(string(out), pids)
}

// socketPath is the socket file of the server on socket: as tmux gives it
// while the server runs, else where tmux puts it for a server it starts,
// under $TMUX_TMPDIR or /tmp.
func socketPath(socket string) string {
	if out, err := exec.Command("tmux", "-u", "-L", socket, "display-message", "-p", "#{socket_path}").Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	dir := os.Getenv("TMUX_TMPDIR")
	if dir == "" {
		dir = "/tmp"
	}
	return filepath.Join(dir, fmt.Sprintf("tmux-%d", os.Getuid()), socket)
}

// SbxStub returns the path of the stub sbx and its state directory, set in
// SBX_STUB_DIR for this test. An hq the test starts runs as on macOS, where
// sbx is `sbx` and paths are hq's own, whatever the machine: on WSL it would
// otherwise look for sbx.exe, which is the user's real one or none (#13).
func SbxStub(t *testing.T) (bin, dir string) {
	t.Helper()
	t.Setenv(platformEnv, "native")
	_, file, _, _ := runtime.Caller(0)
	bin = filepath.Join(filepath.Dir(file), "sbx-stub")
	dir = t.TempDir()
	t.Setenv("SBX_STUB_DIR", dir)
	return bin, dir
}

// WSLStubs puts the stub sbx on PATH as sbx.exe and the stubs of the
// Windows side listed in wsl-stubs (wslpath; cmd.exe, which knows only the
// folders hq asks for; powershell.exe, which lists the claude.exe processes
// in WSL_STUB_PROCESSES, none by default) under their own names, as hq finds
// them on WSL, has an hq the test starts run as on WSL whatever the machine,
// and returns the stub sbx's state directory, set in SBX_STUB_DIR for this
// test.
func WSLStubs(t *testing.T) string {
	t.Helper()
	sbx, dir := SbxStub(t)
	links, err := WindowsStubs(filepath.Dir(sbx))
	if err != nil {
		t.Fatal(err)
	}
	links["sbx.exe"] = sbx
	bin := t.TempDir()
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(platformEnv, "wsl")
	return dir
}

// WindowsStubs reads the list wsl-stubs in dir: the stub in dir of each
// command it names, skipping a stub not there.
func WindowsStubs(dir string) (map[string]string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "wsl-stubs"))
	if err != nil {
		return nil, err
	}
	stubs := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || strings.HasPrefix(f[0], "#") {
			continue
		}
		if len(f) != 2 {
			return nil, fmt.Errorf("wsl-stubs: want a command and its stub: %q", line)
		}
		stub := filepath.Join(dir, f[1])
		if _, err := os.Stat(stub); err != nil {
			continue
		}
		stubs[f[0]] = stub
	}
	return stubs, nil
}

var (
	mainRuns       bool
	fakeClaudeOnce sync.Once
	fakeClaudeDir  string
	fakeClaudeBin  string
	fakeClaudeErr  error
)

// Main runs a package's tests, then removes the fake claude FakeClaude
// built for them. A package whose tests call FakeClaude runs them from its
// TestMain with os.Exit(testutil.Main(m)); without it, every test run would
// leave a copy of the fake in the temporary directory (#226).
func Main(m *testing.M) int {
	mainRuns = true
	code := m.Run()
	if fakeClaudeDir != "" {
		_ = os.RemoveAll(fakeClaudeDir)
	}
	return code
}

// FakeClaude builds the fake claude (once per test binary, which removes
// it in Main) and has the stub sbx run it for this test's agents, so they
// fire the injected hooks.
func FakeClaude(t *testing.T) {
	t.Helper()
	if !mainRuns {
		t.Fatal("testutil.FakeClaude: run this package's tests with testutil.Main from its TestMain, which removes the fake claude when they end (#226)")
	}
	fakeClaudeOnce.Do(func() {
		dir, err := os.MkdirTemp("", "hq-fakeclaude-")
		if err != nil {
			fakeClaudeErr = err
			return
		}
		fakeClaudeDir = dir
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
