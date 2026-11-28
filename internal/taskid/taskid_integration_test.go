//go:build integration

package taskid

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// entries are the names in dir.
func entries(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range es {
		names = append(names, e.Name())
	}
	return names
}

// The key is made at its first use, in a directory made for the user alone,
// readable by the user alone, and kept: Key gives the same ever after, and
// ReadKey gives it as it stands. Before it, ReadKey makes nothing, not even
// the directory.
func TestTheKeyIsMadeOnceForTheUserAlone(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state", "hq")
	if _, err := ReadKey(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("before: %v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("ReadKey made the directory: %v", err)
	}
	key, err := Key(dir)
	if err != nil || len(key) != keySize {
		t.Fatalf("%x %v", key, err)
	}
	for path, want := range map[string]fs.FileMode{dir: fs.ModeDir | 0o700, filepath.Join(dir, keyName): 0o600} {
		if fi, err := os.Stat(path); err != nil || fi.Mode() != want {
			t.Errorf("%s: %v %v, want %v", path, fi.Mode(), err, want)
		}
	}
	again, err := Key(dir)
	read, rerr := ReadKey(dir)
	if err != nil || rerr != nil || !bytes.Equal(again, key) || !bytes.Equal(read, key) {
		t.Fatalf("again %x %v, read %x %v, made %x", again, err, read, rerr, key)
	}
	if got := entries(t, dir); len(got) != 1 || got[0] != keyName {
		t.Fatalf("left %q", got)
	}
}

// Of processes that make the key at once, none overwrites another's: one
// that finds a key put in place by another after it looked keeps that one,
// and both sign with it; nothing else is left in the directory (#362).
func TestAKeyAnotherProcessPutInPlaceFirstIsKept(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "hq")
	var theirs []byte
	var theirErr error
	beforeLink = func() {
		beforeLink = nil
		theirs, theirErr = Key(dir)
	}
	t.Cleanup(func() { beforeLink = nil })
	mine, err := Key(dir)
	if err != nil || theirErr != nil || len(theirs) != keySize || !bytes.Equal(mine, theirs) {
		t.Fatalf("mine %x %v, theirs %x %v", mine, err, theirs, theirErr)
	}
	if got := entries(t, dir); len(got) != 1 || got[0] != keyName {
		t.Fatalf("left %q", got)
	}
}

// A file in the key's place that is no key hq made is refused, naming it,
// and kept: Key overwrites nothing. With no state directory known, or one
// that cannot be made (under a link to nowhere), there is no key, and
// nothing is made.
func TestNoKeyFromAFileHqDidNotMakeOrADirectoryItCannotMake(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, keyName)
	if err := os.WriteFile(path, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, get := range map[string]func(string) ([]byte, error){"Key": Key, "ReadKey": ReadKey} {
		if _, err := get(dir); err == nil || !strings.Contains(err.Error(), path+" is not a key hq made: it holds 5 bytes, not 32") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if b, _ := os.ReadFile(path); string(b) != "short" {
		t.Fatalf("overwritten: %q", b)
	}

	if _, err := Key(""); err == nil || err.Error() != "no state directory: set HOME or XDG_STATE_HOME" {
		t.Fatalf("no directory: %v", err)
	}
	nowhere := filepath.Join(t.TempDir(), "nowhere")
	link := filepath.Join(t.TempDir(), "state")
	if err := os.Symlink(nowhere, link); err != nil {
		t.Fatal(err)
	}
	if key, err := Key(filepath.Join(link, "hq")); err == nil || !errors.Is(err, fs.ErrExist) {
		t.Fatalf("under a link to nowhere: %x %v", key, err)
	}
	if _, err := os.Lstat(nowhere); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("made %s: %v", nowhere, err)
	}
}
