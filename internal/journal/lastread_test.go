package journal

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/state"
)

// An append reads on from what the append before kept beside the journal
// (ID.read) when it reaches exactly to the journal's end, so evidence it
// kept stands though no event of the journal holds it; what was kept
// without evidence is read anew from the journal's start.
func TestAnAppendReadsOnFromWhatWasKeptUpToTheJournalsEnd(t *testing.T) {
	const id = "0123456789abcdef"
	at := time.Unix(1000, 0)
	atWork := state.Event{Source: state.SourceScreen, Kind: state.KindAtWork, Time: at}
	for kept, want := range map[string]string{
		`{"offset":%d,"basis":0,"evidence":{"at work":"0"}}`: state.AsideDuplicate,
		`{"offset":%d,"basis":0}`:                            "",
	} {
		// A journal of its own each, holding a turn end and no work.
		store := Files{Dir: t.TempDir()}
		if _, err := store.Append(id, state.Event{Source: state.SourceScreen, Kind: state.KindTurnEnded, Time: at}); err != nil {
			t.Fatal(err)
		}
		path := store.journalPath(id)
		if err := os.WriteFile(path+readSuffix, fmt.Appendf(nil, kept, size(path)), 0o600); err != nil {
			t.Fatal(err)
		}
		out, err := store.Append(id, atWork)
		if err != nil || len(out) != 1 || out[0].Aside != want {
			t.Fatalf("kept %s: %+v %v, want aside %q", kept, out, err, want)
		}
	}
}

// An ingest whose append a dead process cut off is undone and appended
// anew by the next ingest, by the journal's size its record names: the
// event file it was appending is neither lost nor appended twice.
func TestTheNextIngestUndoesAnAppendCutOff(t *testing.T) {
	const id = "0123456789abcdef"
	root, store := t.TempDir(), Files{Dir: t.TempDir()}
	if _, err := store.Append(id, state.Event{Source: state.SourceHQ, Kind: state.KindLaunched, Time: time.Unix(1000, 0)}); err != nil {
		t.Fatal(err)
	}
	dir := state.EventsDir(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "0001-stop"), []byte("not an event\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	afterAppend = func() { panic("died") }
	func() {
		defer func() { _ = recover() }()
		_ = store.Ingest(root, id)
	}()
	afterAppend = nil
	// The append cut off before its last bytes were written.
	path := store.journalPath(id)
	if err := os.Truncate(path, size(path)-3); err != nil {
		t.Fatal(err)
	}
	if err := store.Ingest(root, id); err != nil {
		t.Fatal(err)
	}
	j, err := store.Read(id)
	if err != nil || len(j.Events) != 2 || j.Events[1].File != "0001-stop" {
		t.Fatalf("journal %+v %v, want the event file appended once", j.Events, err)
	}
}
