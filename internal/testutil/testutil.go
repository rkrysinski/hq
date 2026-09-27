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
	"sync/atomic"
	"testing"
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
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", name, "kill-server").Run() })
	return name
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

// WSLStubs puts the stub sbx on PATH as sbx.exe and the stub wslpath as
// wslpath, as hq finds them on WSL, and returns the stub sbx's state
// directory, set in SBX_STUB_DIR for this test.
func WSLStubs(t *testing.T) string {
	t.Helper()
	sbx, dir := SbxStub(t)
	bin := t.TempDir()
	for name, target := range map[string]string{"sbx.exe": sbx, "wslpath": filepath.Join(filepath.Dir(sbx), "wslpath-stub")} {
		if err := os.Symlink(target, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

// GhStub returns the path of the stub gh and its release directory, set in
// GH_STUB_DIR for this test.
func GhStub(t *testing.T) (bin, dir string) {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	bin = filepath.Join(filepath.Dir(file), "gh-stub")
	dir = t.TempDir()
	t.Setenv("GH_STUB_DIR", dir)
	return bin, dir
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
