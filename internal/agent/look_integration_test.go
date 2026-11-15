//go:build integration

package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/journal"
	"github.com/rkrysinski/hq/internal/journal/journaltest"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/testutil"
	"github.com/rkrysinski/hq/internal/tmux"
)

// filesStore is the journals' real store, with a screen for every pane.
type filesStore struct {
	journal.Files
	screen string
}

func (s filesStore) Screens(panes []string) (map[string]string, error) {
	out := map[string]string{}
	for _, p := range panes {
		out[p] = s.screen
	}
	return out, nil
}

// The look wired to the real journal: with a prompt queued behind the turn
// at work, a screen showing Claude at work below the Interrupted line
// appends that Claude took it, once, which hands the state over to it; the
// next look reads the agent working on it since then, and the same screen
// is no turn end (#88). The hook's working files it leaves as they are:
// hq on the host reads and writes none of them (#199).
func TestTheLookHandsOverOnTheRealJournalOnce(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	const id = "0a1b2c3d4e5f"
	payload, err := os.ReadFile("../state/testdata/queued-while-working.json")
	if err != nil {
		t.Fatal(err)
	}
	queued := append([]byte("branch main\n"), payload...)
	events := state.EventsDir(root, id)
	if err := os.MkdirAll(events, 0o755); err != nil {
		t.Fatal(err)
	}
	asked := time.Now().Add(-time.Minute)
	for i, name := range []string{"000000001.prompt.1", "000000002.prompt.1"} {
		path := filepath.Join(events, name)
		if err := os.WriteFile(path, queued, 0o644); err != nil {
			t.Fatal(err)
		}
		at := asked.Add(time.Duration(i) * time.Second)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	// The hook's working files: the queued prompt, and its mark that Claude
	// acted on the turn at work (its prompt_id).
	dir := state.Dir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".queued"), queued, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".acted"), []byte("00000019-0000-4000-8000-000000000019\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := tmux.Window{ID: "@1", Pane: "%1", Name: "a", Options: map[string]string{GenerationOption: gen, "id": id, "name": "a", "repo": root, "started": "100"}}
	s := filesStore{journal.Files{Dir: t.TempDir()}, capture(t, "fs-queued-taken-thinking")}

	before := time.Now()
	a := Look([]tmux.Window{w}, Sandboxes{}, time.Now(), s, nil)[0]
	if a.State != state.Working || a.Since.Before(before) {
		t.Fatalf("handed over: %+v", a)
	}
	for suffix, want := range map[string]string{".queued": string(queued), ".acted": "00000019-0000-4000-8000-000000000019\n"} {
		if got, err := os.ReadFile(filepath.Join(dir, id+suffix)); err != nil || string(got) != want {
			t.Errorf("%s: %q %v", suffix, got, err)
		}
	}
	later := Look([]tmux.Window{w}, Sandboxes{}, time.Now().Add(time.Second), s, NewFolds())[0]
	if later.State != state.Working || !later.Since.Equal(a.Since) {
		t.Fatalf("the look after: %+v", later)
	}
	j, err := s.Read(id)
	if err != nil {
		t.Fatal(err)
	}
	if got := journaltest.Kinds(j.Events); got != "UserPromptSubmit UserPromptSubmit "+state.KindTookQueued {
		t.Fatalf("journal: %s", got)
	}
}
