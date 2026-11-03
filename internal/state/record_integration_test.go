//go:build integration

package state

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// hq's record of an agent is kept beside its state files, read back with
// its id, replaced when hq writes it again, and removed with the agent
// (#140).
func TestARecordIsKeptBesideTheStateFilesAndGoesWithTheAgent(t *testing.T) {
	root := t.TempDir()
	if got, err := Records(root); err != nil || len(got) != 0 {
		t.Fatalf("a repository without agents: %v %v", got, err)
	}
	if err := WriteRecord(root, "0a1b2c", Record{Name: "42", Sandbox: "claude-app", Server: "hq-dev-1"}); err != nil {
		t.Fatal(err)
	}
	if err := WriteRecord(root, "0a1b2c", Record{Name: "42", Sandbox: "hq-local-app"}); err != nil {
		t.Fatal(err)
	}
	if err := WriteRecord(root, "ffee00", Record{Name: "sup", Sandbox: "claude-app"}); err != nil {
		t.Fatal(err)
	}
	got, err := Records(root)
	want := []Record{{ID: "0a1b2c", Name: "42", Sandbox: "hq-local-app"}, {ID: "ffee00", Name: "sup", Sandbox: "claude-app"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("records %+v %v", got, err)
	}
	if err := Remove(root, "0a1b2c"); err != nil {
		t.Fatal(err)
	}
	if got, _ := Records(root); len(got) != 1 || got[0].ID != "ffee00" {
		t.Fatalf("after the agent was removed: %+v", got)
	}
	// No temporary file is left behind.
	entries, _ := os.ReadDir(Dir(root))
	if len(entries) != 1 || entries[0].Name() != "ffee00.hq" {
		t.Fatalf("files %v", entries)
	}
}

// The sandbox writes in the same directory, so only regular files of an
// id's name that hold a record are records: a link, a file of another
// name and one that holds something else are not (design §7.3).
func TestOnlyARegularFileOfAnIdsNameIsARecord(t *testing.T) {
	root := t.TempDir()
	dir := Dir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(secret, []byte(`{"name":"x","sandbox":"claude-app"}`), 0o600)
	os.Symlink(secret, filepath.Join(dir, "aaaaaa.hq"))
	os.WriteFile(filepath.Join(dir, "NOT-AN-ID.hq"), []byte(`{"name":"y","sandbox":"claude-app"}`), 0o600)
	os.WriteFile(filepath.Join(dir, "bbbbbb.hq"), []byte(`not json`), 0o600)
	os.WriteFile(filepath.Join(dir, "cccccc.hq"), []byte(`{"name":"","sandbox":"claude-app"}`), 0o600)
	os.WriteFile(filepath.Join(dir, "dddddd.hq"), []byte(`{"name":"z","sandbox":"claude-app","pad":"`+strings.Repeat("x", maxRecord)+`"}`), 0o600)
	os.WriteFile(filepath.Join(dir, "eeeeee"), []byte(`{"name":"w","sandbox":"claude-app"}`), 0o600)
	os.WriteFile(filepath.Join(dir, "ffffff.hq"), []byte(`{"name":"ok","sandbox":"claude-app"}`), 0o600)
	got, err := Records(root)
	if err != nil || len(got) != 1 || got[0] != (Record{ID: "ffffff", Name: "ok", Sandbox: "claude-app"}) {
		t.Fatalf("records %+v %v", got, err)
	}
}
