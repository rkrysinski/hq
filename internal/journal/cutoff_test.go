package journal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/state"
)

// An ingest cut off between its record and a whole append, then an Append
// of another process before the next ingest: the next ingest neither takes
// the cut append for whole (losing the event file never appended) nor cuts
// the Append's event out (ADR 0018: the next process that takes the lock
// finishes it or undoes it).
func TestAnAppendAfterACutOffIngestLosesNoEvent(t *testing.T) {
	const id = "0123456789abcdef"
	for name, cut := range map[string]int64{"cut short of the append": 1, "cut far short of it": 1 << 20} {
		t.Run(name, func(t *testing.T) {
			root, store := t.TempDir(), Files{Dir: t.TempDir()}
			at := time.Unix(1000, 0)
			if _, err := store.Append(id, state.Event{Source: state.SourceHQ, Kind: state.KindLaunched, Time: at}); err != nil {
				t.Fatal(err)
			}
			dir := state.EventsDir(root, id)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			file := "0001-stop"
			if err := os.WriteFile(filepath.Join(dir, file), []byte("not an event\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			// The ingest recorded what it would append, then died.
			path := store.journalPath(id)
			from := size(path)
			p, _ := json.Marshal(pending{From: from, To: from + cut, Files: []string{file}})
			if err := os.WriteFile(path+pendingSuffix, p, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Append(id, state.Event{Source: state.SourceHQ, Kind: state.KindRelaunched, Time: at.Add(time.Second)}); err != nil {
				t.Fatal(err)
			}
			if err := store.Ingest(root, id); err != nil {
				t.Fatal(err)
			}
			j, err := store.Read(id)
			if err != nil {
				t.Fatal(err)
			}
			var kinds []string
			for _, e := range j.Events {
				kinds = append(kinds, e.Kind)
			}
			if len(kinds) != 3 || kinds[0] != state.KindLaunched || kinds[1] != state.KindRelaunched || kinds[2] != state.KindUnreadable {
				t.Fatalf("journal %v, want Launched, Relaunched and the file's event", kinds)
			}
			if _, err := os.Lstat(filepath.Join(dir, file)); err == nil {
				t.Fatal("the event file is still there")
			}
		})
	}
}

// An ingest whose append is whole when its process dies is left for the
// next ingest to finish, though an Append comes first: its events stay
// where they are, before the Append's, and are not appended again.
func TestAnAppendAfterAWholeIngestLeavesItToFinish(t *testing.T) {
	const id = "0123456789abcdef"
	root, store := t.TempDir(), Files{Dir: t.TempDir()}
	at := time.Unix(1000, 0)
	if _, err := store.Append(id, state.Event{Source: state.SourceHQ, Kind: state.KindLaunched, Time: at}); err != nil {
		t.Fatal(err)
	}
	dir := state.EventsDir(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "0001-stop")
	if err := os.WriteFile(file, []byte("not an event\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The ingest appends, then dies before it removes the file.
	afterAppend = func() { panic("died") }
	func() {
		defer func() { _ = recover() }()
		_ = store.Ingest(root, id)
	}()
	afterAppend = nil
	if _, err := os.Stat(store.journalPath(id) + pendingSuffix); err != nil {
		t.Fatalf("no record of the ingest under way: %v", err)
	}
	if _, err := store.Append(id, state.Event{Source: state.SourceHQ, Kind: state.KindRelaunched, Time: at.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	if err := store.Ingest(root, id); err != nil {
		t.Fatal(err)
	}
	j, err := store.Read(id)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range j.Events {
		kinds = append(kinds, e.Kind)
	}
	if len(kinds) != 3 || kinds[0] != state.KindLaunched || kinds[1] != state.KindUnreadable || kinds[2] != state.KindRelaunched {
		t.Fatalf("journal %v, want Launched, the file's event and Relaunched", kinds)
	}
	if _, err := os.Lstat(file); err == nil {
		t.Fatal("the event file is still there")
	}
}
