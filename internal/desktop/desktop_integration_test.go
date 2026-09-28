//go:build integration

package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var at = time.Date(2026, 9, 28, 10, 25, 1, 0, time.UTC)

func TestInstallCreatesTheConfigurationWhenMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Library", "Application Support", "Claude", "claude_desktop_config.json")
	r, err := Install(path, hq, at)
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

func TestInstallBacksUpThenReplacesAndTheSecondRunChangesNothing(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	path := filepath.Join(dir, "claude_desktop_config.json")
	if err := os.WriteFile(path, []byte(theirs), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := Install(path, hq, at)
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
	r, err = Install(path, hq, at.Add(time.Minute))
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
	if _, err := Install(link, hq, at); err != nil {
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
	if _, err := Install(path, hq, at); err == nil {
		t.Fatal("no error")
	}
	if b, _ := os.ReadFile(path); string(b) != "{broken" {
		t.Errorf("changed: %s", b)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("files left: %v", entries)
	}
	// A directory where the file should be.
	if _, err := Install(dir, hq, at); err == nil {
		t.Error("a directory is no error")
	}
	// A directory that cannot be written.
	ro := filepath.Join(dir, "ro")
	_ = os.Mkdir(ro, 0o500)
	_ = os.WriteFile(filepath.Join(ro, "x.json"), []byte("{}"), 0o600)
	if _, err := Install(filepath.Join(ro, "x.json"), hq, at); err == nil && os.Geteuid() != 0 {
		t.Error("a read-only directory is no error")
	}
	if _, err := Install(filepath.Join(ro, "sub", "x.json"), hq, at); err == nil && os.Geteuid() != 0 {
		t.Error("a directory that cannot be made is no error")
	}
}
