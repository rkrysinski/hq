//go:build integration

package state

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/testutil"
)

// fire runs the injected hook as Claude does: sh with the event's kind, the
// payload on stdin, the agent's environment, in dir.
func fire(t *testing.T, dir, id, kind string, payload []byte) {
	t.Helper()
	cmd := exec.Command("sh", hook(kind).Args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HQ_ID="+id, "CLAUDE_PROJECT_DIR="+dir)
	cmd.Stdin = bytes.NewReader(payload)
	if out, err := cmd.CombinedOutput(); err != nil || len(out) != 0 {
		t.Fatalf("hook %s: %v %q", kind, err, out)
	}
}

func TestHookReportsToTheMainRepositoryFromAWorktree(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	wt := filepath.Join(filepath.Dir(root), "feat-42")
	testutil.Git(t, root, "worktree", "add", "-q", "-b", "feat-42", wt)
	const id = "0a1b2c3d4e5f"

	if _, ok := Read(root, id); ok {
		t.Fatal("a report before the first event")
	}
	fire(t, wt, id, "prompt", fixture(t, "prompt"))
	if r, ok := Read(root, id); !ok || r.State != Working || time.Since(r.Since) > time.Minute {
		t.Fatalf("after the prompt: %+v %v", r, ok)
	}
	fire(t, wt, id, "stop", fixture(t, "stop-question"))
	fire(t, wt, id, "input", fixture(t, "notification"))
	if r, _ := Read(root, id); r.State != NeedsInput || r.Last != "Claude needs your permission" {
		t.Fatalf("after the notification: %+v", r)
	}
	fire(t, wt, id, "end", fixture(t, "session-end"))
	if r, _ := Read(root, id); r.State != Ended || r.Last != "Should I also update the README?" {
		t.Fatalf("after the end, the last message stays: %+v", r)
	}
	entries, _ := os.ReadDir(Dir(root))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, " ") != id+" "+id+".stop" {
		t.Fatalf("files %v", names)
	}
	out, _ := exec.Command("git", "-C", root, "status", "--porcelain").Output()
	if len(out) != 0 {
		t.Fatalf("the repository is not clean: %s", out)
	}
}

func TestHookNeverFailsAndWritesNothingWithoutAnAgentOrARepository(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	for _, id := range []string{"", "../x", "ABC", "a b"} {
		fire(t, root, id, "stop", fixture(t, "stop-done"))
		if _, err := os.Stat(filepath.Join(root, ".git", "hq")); !os.IsNotExist(err) {
			t.Fatalf("wrote for the invalid id %q: %v", id, err)
		}
	}
	fire(t, t.TempDir(), "abc", "stop", fixture(t, "stop-done")) // not a repository
}

func TestReadRefusesAnythingButASmallRegularFile(t *testing.T) {
	root := t.TempDir()
	dir := Dir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(root, "secret")
	os.WriteFile(secret, fixture(t, "stop-done"), 0o600)
	os.Symlink(secret, filepath.Join(dir, "aaa"))
	os.WriteFile(filepath.Join(dir, "bbb"), append(fixture(t, "stop-done"), bytes.Repeat([]byte(" "), maxFile)...), 0o644)
	syscall.Mkfifo(filepath.Join(dir, "ccc"), 0o644)
	os.Mkdir(filepath.Join(dir, "ddd"), 0o755)
	os.WriteFile(filepath.Join(dir, "eee"), fixture(t, "stop-done"), 0o644)
	os.Symlink(secret, filepath.Join(dir, "eee.stop"))
	for _, id := range []string{"aaa", "bbb", "ccc", "ddd", "../secret"} {
		if r, ok := Read(root, id); ok {
			t.Errorf("%s read: %+v", id, r)
		}
	}
	// A linked .stop file is ignored; the event file itself still counts.
	if r, ok := Read(root, "eee"); !ok || r.State != Done {
		t.Fatalf("eee: %+v %v", r, ok)
	}
}
