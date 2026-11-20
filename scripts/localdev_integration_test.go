//go:build integration

// Package scripts_test tests the scripts: here local-dev.sh --stub, which QA
// of Windows/WSL changes runs on any machine (#159).
package scripts_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var ids atomic.Int64

// localDev is one stub environment of local-dev.sh, apart from the
// checkout's own and from other tests', removed when the test ends.
type localDev struct {
	t   *testing.T
	id  string
	env []string
}

func newLocalDev(t *testing.T) *localDev {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	d := &localDev{t: t, id: fmt.Sprintf("test-%d-%d", os.Getpid(), ids.Add(1))}
	d.env = append(os.Environ(), "LOCAL_DEV_ID="+d.id)
	t.Cleanup(func() { d.run("--kill-server") })
	return d
}

func (d *localDev) dir() string { return "/tmp/hq-stub-" + d.id }

// realDir is dir with symlinks resolved, as hq names a repository in it
// (on macOS /tmp is /private/tmp).
func (d *localDev) realDir() string {
	d.t.Helper()
	dir, err := filepath.EvalSymlinks(d.dir())
	if err != nil {
		d.t.Fatal(err)
	}
	return dir
}

// run runs local-dev.sh with args and returns its output and whether it
// succeeded.
func (d *localDev) run(args ...string) (string, bool) {
	d.t.Helper()
	_, file, _, _ := runtime.Caller(0)
	cmd := exec.Command(filepath.Join(filepath.Dir(file), "local-dev.sh"), args...)
	cmd.Env = d.env
	cmd.Dir = os.TempDir()
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

// runApart runs local-dev.sh with args and returns its stdout and its
// stderr apart, and whether it succeeded.
func (d *localDev) runApart(args ...string) (string, string, bool) {
	d.t.Helper()
	_, file, _, _ := runtime.Caller(0)
	cmd := exec.Command(filepath.Join(filepath.Dir(file), "local-dev.sh"), args...)
	cmd.Env = d.env
	cmd.Dir = os.TempDir()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err == nil
}

func (d *localDev) must(args ...string) string {
	d.t.Helper()
	out, ok := d.run(args...)
	if !ok {
		d.t.Fatalf("local-dev.sh %v: %s", args, out)
	}
	return out
}

// workspace is what the stub sbx got as the sandbox's workspace.
func (d *localDev) workspace() string {
	d.t.Helper()
	b, err := os.ReadFile(filepath.Join(d.dir(), "sbx", "sandboxes", "claude-repo"))
	if err != nil {
		d.t.Fatal(err)
	}
	return strings.Split(string(b), "\n")[1]
}

// stubs are the commands the mode put on PATH.
func (d *localDev) stubs() []string {
	d.t.Helper()
	entries, err := os.ReadDir(filepath.Join(d.dir(), "stubs"))
	if err != nil {
		d.t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func has(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

func TestStubNativeGivesTheStubSbxHqsOwnPath(t *testing.T) {
	d := newLocalDev(t)
	d.must("--stub", "native", "new", "a", d.dir()+"/repo", "hello")
	if ws := d.workspace(); ws != d.realDir()+"/repo" {
		t.Fatalf("workspace %q", ws)
	}
	if out := d.must("--stub", "native", "ls"); !strings.Contains(out, "a ") {
		t.Fatalf("ls: %s", out)
	}
}

func TestStubGivesHqJournalsOfItsOwn(t *testing.T) {
	d := newLocalDev(t)
	d.must("--stub", "native", "new", "a", d.dir()+"/repo", "hello")
	journals := filepath.Join(d.dir(), "home", ".local", "state", "hq", "journals")
	for i := 0; ; i++ {
		d.must("--stub", "native", "ls")
		if entries, _ := os.ReadDir(journals); len(entries) > 0 {
			break
		}
		if i == 50 {
			t.Fatalf("no journal in %s", journals)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestStubWSLExeGivesTheStubSbxExeAWindowsPath(t *testing.T) {
	d := newLocalDev(t)
	d.must("--stub", "wsl-exe", "new", "a", d.dir()+"/repo", "hello")
	if ws, want := d.workspace(), `\\wsl.localhost\Stub`+strings.ReplaceAll(d.realDir(), "/", `\`)+`\repo`; ws != want {
		t.Fatalf("workspace %q, want %q", ws, want)
	}
	if out := d.must("--stub", "wsl-exe", "ls"); !strings.Contains(out, "a ") {
		t.Fatalf("ls: %s", out)
	}
}

func TestStubWSLLinuxGivesTheStubSbxAsTheLinuxOneHqsOwnPath(t *testing.T) {
	d := newLocalDev(t)
	d.must("--stub", "wsl-linux", "new", "a", d.dir()+"/repo", "hello")
	if ws := d.workspace(); ws != d.realDir()+"/repo" {
		t.Fatalf("workspace %q", ws)
	}
	if out := d.must("--stub", "wsl-linux", "ls"); !strings.Contains(out, "a ") {
		t.Fatalf("ls: %s", out)
	}
	stubs := d.stubs()
	for _, name := range []string{"sbx", "wslpath", "cmd.exe", "powershell.exe"} {
		if !has(stubs, name) {
			t.Errorf("no %s in %v", name, stubs)
		}
	}
	if has(stubs, "sbx.exe") {
		t.Errorf("sbx.exe in %v", stubs)
	}
}

func TestStubNoneHidesARealSbx(t *testing.T) {
	d := newLocalDev(t)
	real := t.TempDir()
	for _, name := range []string{"sbx", "sbx.exe"} {
		if err := os.WriteFile(filepath.Join(real, name), []byte("#!/bin/sh\necho real sbx\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	d.env = append(d.env, "PATH="+real+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, ok := d.run("--stub", "none", "new", "a", d.dir()+"/repo", "hello")
	// hq's own message names the missing sbx; the real one is never run.
	if ok || !strings.Contains(out, "found") || strings.Contains(out, "real sbx") {
		t.Fatalf("ok %v: %s", ok, out)
	}
}

func TestBeforeBuildsAtTheSamePathSoMcpInstallIsAlreadySetUp(t *testing.T) {
	d := newLocalDev(t)
	d.env = append(d.env, "LOCAL_DEV_BEFORE=HEAD")
	d.must("--stub", "wsl-exe", "--before", "mcp", "install", "sbx")
	if out := d.must("--stub", "wsl-exe", "mcp", "install", "sbx"); !strings.Contains(out, "already set up") {
		t.Fatalf("after: %s", out)
	}
}

func TestAServerOfOneModeRefusesAnother(t *testing.T) {
	d := newLocalDev(t)
	d.must("--stub", "native", "new", "a", d.dir()+"/repo", "hello")
	if out, ok := d.run("--stub", "wsl-exe", "ls"); ok || !strings.Contains(out, "--kill-server") {
		t.Fatalf("ok %v: %s", ok, out)
	}
}

func TestKillServerRemovesTheStubEnvironment(t *testing.T) {
	d := newLocalDev(t)
	d.must("--stub", "native", "new", "a", d.dir()+"/repo", "hello")
	d.must("--kill-server")
	if _, err := os.Stat(d.dir()); !os.IsNotExist(err) {
		t.Fatalf("%s left: %v", d.dir(), err)
	}
	if exec.Command("tmux", "-L", "hq-dev-"+d.id, "has-session").Run() == nil {
		t.Fatal("tmux server still runs")
	}
}

// A run keeps its temporary files in a directory of the checkout's own in
// the caller's TMPDIR, hq-dev-ID as its tmux server, which the server hq
// starts there has too, and --kill-server removes it (#226).
func TestARunKeepsItsTemporaryFilesInADirectoryOfTheCheckoutsOwnUntilKillServer(t *testing.T) {
	d := newLocalDev(t)
	d.must("--stub", "native", "new", "a", d.dir()+"/repo", "hello")
	want := filepath.Join(os.TempDir(), "hq-dev-"+d.id)
	out, err := exec.Command("tmux", "-u", "-L", "hq-dev-"+d.id, "show-environment", "-g", "TMPDIR").Output()
	if got := strings.TrimSpace(string(out)); err != nil || got != "TMPDIR="+want {
		t.Fatalf("the server's %q (%v), want TMPDIR=%s", got, err, want)
	}
	if info, err := os.Stat(want); err != nil || !info.IsDir() {
		t.Fatalf("%s is no directory: %v", want, err)
	}
	// From a shell that has that TMPDIR, as the server's own panes do,
	// --kill-server finds the same directory.
	d.env = append(d.env, "TMPDIR="+want)
	d.must("--kill-server")
	if _, err := os.Stat(want); !os.IsNotExist(err) {
		t.Fatalf("%s left: %v", want, err)
	}
}

// --stub-env prints the path of the checkout's stub environment and nothing
// else, creates nothing, and names the directory a --stub run then creates
// and --kill-server removes (#299).
func TestStubEnvPrintsThePathAloneAndCreatesNothing(t *testing.T) {
	d := newLocalDev(t)
	devTmp := filepath.Join(os.TempDir(), "hq-dev-"+d.id)
	want := "/tmp/hq-stub-" + d.id + "\n"
	stubEnv := func() {
		t.Helper()
		stdout, stderr, ok := d.runApart("--stub-env")
		if !ok || stdout != want || stderr != "" {
			t.Fatalf("ok %v, stdout %q, stderr %q, want stdout %q", ok, stdout, stderr, want)
		}
	}
	stubEnv()
	for _, p := range []string{d.dir(), devTmp} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s after --stub-env: %v", p, err)
		}
	}
	if exec.Command("tmux", "-L", "hq-dev-"+d.id, "has-session").Run() == nil {
		t.Fatal("a tmux server after --stub-env")
	}
	d.must("--stub", "native", "--version")
	stubEnv()
	if info, err := os.Stat(strings.TrimSuffix(want, "\n")); err != nil || !info.IsDir() {
		t.Fatalf("--stub created no %s: %v", want, err)
	}
	if out := d.must("--kill-server"); !strings.Contains(out, "stub environment "+strings.TrimSuffix(want, "\n")+" removed") {
		t.Fatalf("--kill-server: %s", out)
	}
	stubEnv()
}

// A --stub run that creates the stub environment says so on stderr, and
// only then; hq's output on stdout stays hq's alone (#299).
func TestStubSaysOnStderrWhenItCreatesTheStubEnvironment(t *testing.T) {
	d := newLocalDev(t)
	line := "local-dev: stub environment /tmp/hq-stub-" + d.id + " created\n"
	run := func() (string, string) {
		t.Helper()
		stdout, stderr, ok := d.runApart("--stub", "native", "--version")
		if !ok {
			t.Fatalf("--stub native --version: stdout %q, stderr %q", stdout, stderr)
		}
		return stdout, stderr
	}
	first, stderr := run()
	if !strings.Contains(stderr, line) {
		t.Fatalf("first run's stderr %q has no %q", stderr, line)
	}
	if !strings.HasPrefix(first, "hq ") || strings.Contains(first, "local-dev") {
		t.Fatalf("first run's stdout %q", first)
	}
	second, stderr := run()
	if strings.Contains(stderr, "stub environment") || second != first {
		t.Fatalf("second run: stdout %q (first %q), stderr %q", second, first, stderr)
	}
	d.must("--kill-server")
	if _, stderr := run(); !strings.Contains(stderr, line) {
		t.Fatalf("run after --kill-server: stderr %q has no %q", stderr, line)
	}
}
