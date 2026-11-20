//go:build integration

package agent

import (
	"os"
	"path/filepath"
	"strings"
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

// Stands says every home window stands.
func (s filesStore) Stands(string) bool { return true }

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

// killingFiles is the real store with hq kill removing the agent at the
// look's read of its journal, or at its first append of evidence after
// that read (atAppend): its home window goes, then its files and journal.
type killingFiles struct {
	filesStore
	root     string
	atAppend bool
	killed   *bool
}

func (s killingFiles) kill(id string) error {
	if *s.killed {
		return nil
	}
	*s.killed = true
	return s.Remove(s.root, id)
}

func (s killingFiles) Stands(string) bool { return !*s.killed }

func (s killingFiles) ReadFrom(id string, from int64) (journal.Journal, error) {
	if !s.atAppend {
		if err := s.kill(id); err != nil {
			return journal.Journal{}, err
		}
	}
	return s.Files.ReadFrom(id, from)
}

func (s killingFiles) AppendEvidence(id string, stands func() bool, events ...state.Event) ([]state.Event, error) {
	if err := s.kill(id); err != nil {
		return nil, err
	}
	return s.Files.AppendEvidence(id, stands, events...)
}

// A look that falls between the steps of hq kill, at the read of the
// agent's journal or between that read and its append of the pane's
// death, leaves no file of the agent in the journals' directory (#263).
func TestALookBetweenTheStepsOfAKillLeavesNoFileOfTheAgentInTheRealJournals(t *testing.T) {
	for _, atAppend := range []bool{false, true} {
		root := testutil.GitRepo(t, "app")
		const id = "0a1b2c3d4e5f"
		dir := t.TempDir()
		files := journal.Files{Dir: dir}
		if _, err := files.Append(id, state.Event{Source: state.SourceHQ, Kind: state.KindLaunched, Time: time.Now(), Facts: state.Facts{Repo: root}}); err != nil {
			t.Fatal(err)
		}
		events := state.EventsDir(root, id)
		if err := os.MkdirAll(events, 0o755); err != nil {
			t.Fatal(err)
		}
		stop := journaltest.Hook("main", map[string]any{"hook_event_name": "Stop", "prompt_id": "p1", "last_assistant_message": "Done."})
		if err := os.WriteFile(filepath.Join(events, "000000001.stop.1"), stop, 0o644); err != nil {
			t.Fatal(err)
		}
		of := func() []string {
			entries, err := os.ReadDir(filepath.Join(dir, "journals"))
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), id) {
					names = append(names, e.Name())
				}
			}
			return names
		}
		w := tmux.Window{ID: "@1", Pane: "%1", Name: "a", PaneDead: true, DeadAt: time.Now(), Options: map[string]string{GenerationOption: gen, "id": id, "name": "a", "repo": root, "started": "100"}}
		killed := false
		s := killingFiles{filesStore{files, ""}, root, atAppend, &killed}
		if err := files.Ingest(root, id); err != nil {
			t.Fatal(err)
		}
		if names := strings.Join(of(), " "); names != id+" "+id+".read "+id+".reply" {
			t.Fatalf("before the kill: %s", names)
		}
		Look([]tmux.Window{w}, Sandboxes{}, time.Now(), s, nil)
		if names := of(); !killed || len(names) != 0 {
			t.Fatalf("killed at the append %v: killed %v, left %v", atAppend, killed, names)
		}
	}
}
