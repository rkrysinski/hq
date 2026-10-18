//go:build integration

package mcpconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var at = time.Date(2026, 9, 28, 10, 25, 1, 0, time.UTC)

func TestInstallCreatesTheConfigurationWhenMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Library", "Application Support", "Claude", "claude_desktop_config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	r, err := Install(File{Path: path, Create: true}, hq, at)
	if err != nil || r.Outcome != Added || r.Backup != "" {
		t.Fatalf("%+v %v", r, err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), hq.Command) {
		t.Fatalf("%s", b)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode())
	}
}

func TestInstallCreatesNothingWithoutClaudeDesktopsFolder(t *testing.T) {
	support := filepath.Join(t.TempDir(), "Application Support")
	if err := os.Mkdir(support, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(support, "Claude", "claude_desktop_config.json")
	if r, err := Install(File{Path: path, Create: true}, hq, at); !errors.Is(err, ErrNoFolder) || r != (Result{}) {
		t.Fatalf("%+v %v", r, err)
	}
	// A file where the folder would be is no folder either.
	if err := os.WriteFile(filepath.Join(support, "Claude"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(File{Path: path, Create: true}, hq, at); !errors.Is(err, ErrNoFolder) {
		t.Fatalf("%v", err)
	}
	if left, _ := os.ReadDir(support); len(left) != 1 || left[0].IsDir() {
		t.Fatalf("install left %v", left)
	}
}

// Claude Code writes its configuration at its first start: without it,
// Claude Code was never run there, and nothing is created.
func TestInstallCreatesNoFileItMayNot(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude.json")
	if r, err := Install(File{Path: path}, hq, at); !errors.Is(err, ErrNoFile) || r != (Result{}) {
		t.Fatalf("%+v %v", r, err)
	}
	if left, _ := os.ReadDir(home); len(left) != 0 {
		t.Fatalf("install left %v", left)
	}
	// Its folder missing too, the lock cannot be taken: not there either.
	if _, err := Install(File{Path: filepath.Join(home, "Users", "dev", ".claude.json"), Lock: true}, hq, at); !errors.Is(err, ErrNoFile) {
		t.Fatalf("no folder: %v", err)
	}
	if _, err := Install(File{Path: path, Lock: true}, hq, at); !errors.Is(err, ErrNoFile) {
		t.Fatalf("locked: %v", err)
	}
	if left, _ := os.ReadDir(home); len(left) != 0 {
		t.Fatalf("install left %v", left)
	}
	_ = os.WriteFile(path, []byte(`{"numStartups": 3}`), 0o600)
	if r, err := Install(File{Path: path}, hq, at); err != nil || r.Outcome != Added || r.Backup == "" {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestInstallBacksUpThenReplacesAndTheSecondRunChangesNothing(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	path := filepath.Join(dir, "claude_desktop_config.json")
	if err := os.WriteFile(path, []byte(theirs), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := Install(File{Path: path, Create: true}, hq, at)
	if err != nil || r.Outcome != Added || r.Backup != path+".hq-backup-20260928-102501" {
		t.Fatalf("%+v %v", r, err)
	}
	if b, _ := os.ReadFile(r.Backup); string(b) != theirs {
		t.Errorf("backup %q", b)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `"hq"`) || !strings.Contains(string(b), "secret-123") {
		t.Errorf("%s", b)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o644 {
		t.Errorf("mode %v, want the file's own", fi.Mode())
	}
	r, err = Install(File{Path: path, Create: true}, hq, at.Add(time.Minute))
	if err != nil || r.Outcome != Unchanged || r.Backup != "" {
		t.Fatalf("second run %+v %v", r, err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(b) {
		t.Error("the second run wrote")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Errorf("files left: %v", entries)
	}
}

func TestInstallEditsTheFileALinkPointsTo(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles", "claude.json")
	_ = os.MkdirAll(filepath.Dir(target), 0o755)
	_ = os.WriteFile(target, []byte("{}"), 0o600)
	link := filepath.Join(dir, "claude_desktop_config.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(File{Path: link, Create: true}, hq, at); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the link was replaced by a file")
	}
	if b, _ := os.ReadFile(target); !strings.Contains(string(b), `"hq"`) {
		t.Errorf("%s", b)
	}
}

func TestInstallLeavesAFileItCannotEditAsItWas(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "claude_desktop_config.json")
	_ = os.WriteFile(path, []byte("{broken"), 0o600)
	if _, err := Install(File{Path: path, Create: true}, hq, at); err == nil {
		t.Fatal("no error")
	}
	if b, _ := os.ReadFile(path); string(b) != "{broken" {
		t.Errorf("changed: %s", b)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("files left: %v", entries)
	}
	// A directory where the file should be.
	if _, err := Install(File{Path: dir, Create: true}, hq, at); err == nil {
		t.Error("a directory is no error")
	}
	// A directory that cannot be written.
	ro := filepath.Join(dir, "ro")
	_ = os.Mkdir(ro, 0o500)
	_ = os.WriteFile(filepath.Join(ro, "x.json"), []byte("{}"), 0o600)
	if _, err := Install(File{Path: filepath.Join(ro, "x.json"), Create: true}, hq, at); err == nil && os.Geteuid() != 0 {
		t.Error("a read-only directory is no error")
	}
	if _, err := Install(File{Path: filepath.Join(ro, "sub", "x.json"), Create: true}, hq, at); err == nil && os.Geteuid() != 0 {
		t.Error("a directory that cannot be made is no error")
	}
}

// Claude Code saves its configuration holding FILE.lock (proper-lockfile):
// Install holds it too, waiting for a save of Claude Code's to end, so
// neither writes over the other (#115).
func TestInstallHoldsTheClientsLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude.json")
	_ = os.WriteFile(path, []byte(`{"numStartups": 1}`), 0o600)
	held := path + ".lock"
	if err := os.Mkdir(held, 0o777); err != nil {
		t.Fatal(err)
	}
	// Claude Code saves, then gives the lock up.
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = os.WriteFile(path, []byte(`{"numStartups": 2}`), 0o600)
		_ = os.Remove(held)
	}()
	start := time.Now()
	r, err := Install(File{Path: path, Lock: true}, hq, at)
	if err != nil || r.Outcome != Added || time.Since(start) < 300*time.Millisecond {
		t.Fatalf("%+v %v after %v", r, err, time.Since(start))
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), `"numStartups": 2`) || !strings.Contains(string(b), `"hq"`) {
		t.Fatalf("%s", b)
	}
	if _, err := os.Stat(held); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the lock is left: %v", err)
	}
}

func TestInstallWaitsForALiveLockOnlySoLong(t *testing.T) {
	old := lockWait
	lockWait = 200 * time.Millisecond
	t.Cleanup(func() { lockWait = old })
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude.json")
	_ = os.WriteFile(path, []byte("{}"), 0o600)
	_ = os.Mkdir(path+".lock", 0o777)
	if _, err := Install(File{Path: path, Lock: true}, hq, at); err == nil || !strings.Contains(err.Error(), ".claude.json.lock is held") {
		t.Fatalf("%v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "{}" {
		t.Fatalf("changed %s", b)
	}
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Fatalf("the holder's lock was taken: %v", err)
	}
	// One left by a Claude Code that died is not refreshed: taken after 10 s.
	gone := time.Now().Add(-11 * time.Second)
	_ = os.Chtimes(path+".lock", gone, gone)
	if r, err := Install(File{Path: path, Lock: true}, hq, at); err != nil || r.Outcome != Added {
		t.Fatalf("stale: %+v %v", r, err)
	}
}

// Claude Code locks the path it was given: through a link, the lock is
// beside the link, not the file it points to.
func TestInstallLocksBesideTheLink(t *testing.T) {
	old := lockWait
	lockWait = 100 * time.Millisecond
	t.Cleanup(func() { lockWait = old })
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles", "claude.json")
	_ = os.MkdirAll(filepath.Dir(target), 0o755)
	_ = os.WriteFile(target, []byte("{}"), 0o600)
	link := filepath.Join(dir, ".claude.json")
	_ = os.Symlink(target, link)
	_ = os.Mkdir(link+".lock", 0o777)
	if _, err := Install(File{Path: link, Lock: true}, hq, at); err == nil {
		t.Fatal("the lock beside the link was not held to")
	}
}

// A writer that does not lock (Claude Code when its lock does not come):
// its change between Install's read and move is merged, not lost.
func TestInstallMergesAgainWhenTheFileChangedMeanwhile(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	path := filepath.Join(dir, ".claude.json")
	_ = os.WriteFile(path, []byte(`{"numStartups": 1}`), 0o600)
	saves := 1
	beforeCommit = func() {
		if saves < 3 {
			saves++
			_ = os.WriteFile(path, []byte(fmt.Sprintf(`{"numStartups": %d}`, saves)), 0o600)
		}
	}
	t.Cleanup(func() { beforeCommit = func() {} })
	r, err := Install(File{Path: path}, hq, at)
	if err != nil || r.Outcome != Added {
		t.Fatalf("%+v %v", r, err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), `"numStartups": 3`) || !strings.Contains(string(b), `"hq"`) {
		t.Fatalf("%s", b)
	}
	if b, _ := os.ReadFile(r.Backup); string(b) != `{"numStartups": 3}` {
		t.Fatalf("backup %s", b)
	}
	if left, _ := os.ReadDir(dir); len(left) != 2 {
		t.Fatalf("files left: %v", left)
	}
	// A file that never stops changing is left to its writer.
	beforeCommit = func() { saves++; _ = os.WriteFile(path, []byte(fmt.Sprintf(`{"n": %d}`, saves)), 0o600) }
	_ = os.Remove(r.Backup)
	_ = os.WriteFile(path, []byte(`{"n": 0}`), 0o600)
	if _, err := Install(File{Path: path}, hq, at.Add(time.Hour)); err == nil || !strings.Contains(err.Error(), "kept changing") {
		t.Fatalf("%v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != fmt.Sprintf(`{"n": %d}`, saves) {
		t.Fatalf("%s", b)
	}
	if left, _ := os.ReadDir(dir); len(left) != 1 {
		t.Fatalf("files left: %v", left)
	}
}
