//go:build integration

// Package scripts_test tests scripts/local-dev.sh --stub, which QA of
// Windows/WSL changes runs on any machine (#159).
package scripts_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
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
	if ws := d.workspace(); ws != d.dir()+"/repo" {
		t.Fatalf("workspace %q", ws)
	}
	if out := d.must("--stub", "native", "ls"); !strings.Contains(out, "a ") {
		t.Fatalf("ls: %s", out)
	}
}

func TestStubWSLExeGivesTheStubSbxExeAWindowsPath(t *testing.T) {
	d := newLocalDev(t)
	d.must("--stub", "wsl-exe", "new", "a", d.dir()+"/repo", "hello")
	if ws, want := d.workspace(), `\\wsl.localhost\Stub`+strings.ReplaceAll(d.dir(), "/", `\`)+`\repo`; ws != want {
		t.Fatalf("workspace %q, want %q", ws, want)
	}
}

func TestStubWSLLinuxPutsTheStubSbxAsTheLinuxOneBesideTheWindowsSide(t *testing.T) {
	d := newLocalDev(t)
	d.must("--stub", "wsl-linux", "--version")
	stubs := d.stubs()
	for _, name := range []string{"sbx", "wslpath", "cmd.exe"} {
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
	if ok || !strings.Contains(out, "sbx not found") {
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
