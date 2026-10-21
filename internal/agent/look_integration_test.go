//go:build integration

package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/testutil"
	"github.com/rkrysinski/hq/internal/tmux"
)

// The look wired to the real state files: with the hook's copy of a queued
// prompt pending beside a report that says working, a screen showing Claude
// at work below the Interrupted line hands the state over to the prompt
// (state.TakeQueued), once; the next look reads the agent working on it
// since then, nothing queued any more (#88).
func TestTheLookHandsOverOnTheRealStateFilesOnce(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	const id = "0a1b2c3d4e5f"
	dir := state.Dir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile("../state/testdata/queued-while-working.json")
	if err != nil {
		t.Fatal(err)
	}
	queued := append([]byte("branch main\n"), payload...)
	for _, name := range []string{id, id + ".queued"} {
		if err := os.WriteFile(filepath.Join(dir, name), queued, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The hook's mark that Claude acted on the turn at work: its prompt_id.
	if err := os.WriteFile(filepath.Join(dir, id+".acted"), []byte("00000019-0000-4000-8000-000000000019\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	asked := time.Now().Add(-time.Minute)
	if err := os.Chtimes(filepath.Join(dir, id), asked, asked); err != nil {
		t.Fatal(err)
	}
	w := tmux.Window{ID: "@1", Pane: "%1", Name: "a", Options: map[string]string{"id": id, "name": "a", "repo": root, "started": "100"}}
	s := &fakeStore{screens: map[string]string{"%1": capture(t, "fs-queued-taken-thinking")}}

	before := time.Now()
	a := Look([]tmux.Window{w}, state.Read, state.TakeQueued, Sandboxes{}, time.Now(), s)[0]
	if a.State != state.Working || a.Since.Before(before) || a.acted || a.queued || len(s.options) != 0 {
		t.Fatalf("handed over: %+v %v", a, s.options)
	}
	for _, suffix := range []string{".queued", ".acted"} {
		if _, err := os.Lstat(filepath.Join(dir, id+suffix)); err == nil {
			t.Errorf("%s stayed", suffix)
		}
	}
	// The next look finds nothing to hand over, and the same screen is no
	// turn end: Claude is at work below the line.
	later := Look([]tmux.Window{w}, state.Read, state.TakeQueued, Sandboxes{}, time.Now().Add(time.Second), s)[0]
	if later.State != state.Working || !later.Since.Equal(a.Since) || len(s.options) != 0 {
		t.Fatalf("the look after: %+v %v", later, s.options)
	}
}
