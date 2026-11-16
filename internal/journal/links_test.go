package journal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rkrysinski/hq/internal/state"
)

const linkedID = "0123456789abcdef"

// outsideFile is a file of the user's outside the repository, named as an
// event file is, in a directory of its own.
func outsideFile(t *testing.T) (dir, file string) {
	t.Helper()
	dir = t.TempDir()
	file = filepath.Join(dir, "2026-notes.txt")
	if err := os.WriteFile(file, []byte("the user's own file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !state.IsEventFile(filepath.Base(file)) {
		t.Fatalf("%s is not named as an event file", file)
	}
	return dir, file
}

// linkAt makes the path at, under root's .git, a link to target, its
// parents made as directories.
func linkAt(t *testing.T, at, target string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, at); err != nil {
		t.Fatal(err)
	}
}

// An events directory, or a parent of it under .git/hq, that is a link to
// another directory, as a sandbox can leave it: an ingest lists, reads and
// removes nothing there, and journals nothing (#193).
func TestIngestRefusesAnEventsDirectoryReachedThroughALink(t *testing.T) {
	for _, link := range []string{"hq", "hq/events", "hq/events/" + linkedID} {
		t.Run(link, func(t *testing.T) {
			root, store := t.TempDir(), Files{Dir: t.TempDir()}
			// The link leads to a tree that holds, where the agent's
			// directory would be, a directory of the user's own.
			target := t.TempDir()
			rest, _ := filepath.Rel(link, filepath.Join("hq/events", linkedID))
			mine := filepath.Join(target, rest)
			if err := os.MkdirAll(mine, 0o755); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(mine, "2026-notes.txt")
			if err := os.WriteFile(file, []byte("the user's own file\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			linkAt(t, filepath.Join(root, ".git", link), target)
			err := store.Ingest(root, linkedID)
			if err == nil || !strings.Contains(err.Error(), "is a link") {
				t.Fatalf("ingest: %v, want the link refused", err)
			}
			if _, err := os.Lstat(file); err != nil {
				t.Fatalf("the file outside the repository is gone: %v", err)
			}
			if j, _ := store.Read(linkedID); len(j.Events) != 0 {
				t.Fatalf("journaled through the link: %+v", j.Events)
			}
		})
	}
}

// The same events directory without a link is ingested: the guard refuses
// the link, not the directory.
func TestIngestTakesAnEventsDirectoryWithNoLink(t *testing.T) {
	root, store := t.TempDir(), Files{Dir: t.TempDir()}
	dir := state.EventsDir(root, linkedID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "2026-notes.txt")
	if err := os.WriteFile(file, []byte("not an event\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Ingest(root, linkedID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(file); err == nil {
		t.Fatal("the event file is still there after its ingest")
	}
	if j, _ := store.Read(linkedID); len(j.Events) != 1 {
		t.Fatalf("journal: %+v, want the file's one event", j.Events)
	}
}

// Removing an agent never deletes through a linked parent of its events
// directory; a linked events directory loses the link alone.
func TestRemoveDeletesNothingThroughALink(t *testing.T) {
	t.Run("a linked parent", func(t *testing.T) {
		root, store := t.TempDir(), Files{Dir: t.TempDir()}
		outside := t.TempDir()
		mine := filepath.Join(outside, linkedID)
		if err := os.MkdirAll(mine, 0o755); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(mine, "2026-notes.txt")
		if err := os.WriteFile(file, []byte("the user's own file\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		linkAt(t, filepath.Join(root, ".git", "hq", "events"), outside)
		if err := store.Remove(root, linkedID); err == nil || !strings.Contains(err.Error(), "is a link") {
			t.Fatalf("remove: %v, want the link refused", err)
		}
		if _, err := os.Lstat(file); err != nil {
			t.Fatalf("the file outside the repository is gone: %v", err)
		}
	})
	t.Run("a linked events directory", func(t *testing.T) {
		root, store := t.TempDir(), Files{Dir: t.TempDir()}
		outside, file := outsideFile(t)
		at := state.EventsDir(root, linkedID)
		linkAt(t, at, outside)
		if err := store.Remove(root, linkedID); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(at); err == nil {
			t.Fatal("the link is still there")
		}
		if _, err := os.Lstat(file); err != nil {
			t.Fatalf("the file outside the repository is gone: %v", err)
		}
	})
}
