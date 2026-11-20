//go:build integration

package state_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/journal"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/testutil"
	"github.com/rkrysinski/hq/internal/tmux"
	"github.com/rkrysinski/hq/internal/version"
)

var update = flag.Bool("update", false, "write the fixture journals as they are rebuilt")

// The fixture journals are what the hook and the ingest make of their
// payloads (the operator's decision 8 of #165): each is rebuilt by running
// the hook on its steps' payloads, as Claude runs it, and ingesting what
// it left after each run, with what the look then gave. A rebuilt journal
// that differs from the committed file fails; -update writes it.
func TestTheFixtureJournalsAreWhatTheHookAndTheIngestMakeOfTheirPayloads(t *testing.T) {
	paths, err := filepath.Glob("testdata/journals/*.json")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no fixture journals: %v", err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			committed, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var fx state.JournalFixture
			if err := json.Unmarshal(committed, &fx); err != nil {
				t.Fatal(err)
			}
			rebuilt, err := rebuild(t, fx).Encode()
			if err != nil {
				t.Fatal(err)
			}
			if *update {
				if err := os.WriteFile(path, rebuilt, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			if !bytes.Equal(rebuilt, committed) {
				t.Errorf("rebuilt journal differs from %s (go test -tags integration ./internal/state -run %s -update writes it):\n%s",
					path, t.Name(), firstDifference(rebuilt, committed))
			}
		})
	}
}

// The first second of a fixture journal's steps: one second a step after
// it, so each run of the hook dates what it wrote apart.
var firstStep = time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC)

// rebuild runs fx's steps through hq, the hook, the ingest and the look
// in a repository and a state directory of their own.
func rebuild(t *testing.T, fx state.JournalFixture) state.JournalFixture {
	t.Helper()
	root := testutil.GitRepo(t, "app")
	const id = "0a1b2c3d4e5f"
	store := &lookStore{Files: journal.Files{Dir: t.TempDir()}}
	script := hookScript(t)
	worktrees := map[string]string{}
	var seen int
	var reply int64
	w := tmux.Window{ID: "@1", Pane: "%1", Name: "agent-17", Options: map[string]string{
		"id": id, "name": "agent-17", "repo": root, "sandbox": "claude-app", agent.GenerationOption: strconv.Itoa(version.Generation),
	}}
	var sandboxes agent.Sandboxes
	out := state.JournalFixture{About: fx.About}
	for i, step := range fx.Steps {
		when := firstStep.Add(time.Duration(i) * time.Second)
		act(t, store, &w, id, step.HQ, when)
		for m := range step.Messages {
			if _, err := state.Post(root, id, state.Message{Text: fmt.Sprintf("message %d", m+1)}, false, when); err != nil {
				t.Fatal(err)
			}
		}
		if step.Hook != "" {
			cwd := root
			if step.In != "" {
				if worktrees[step.In] == "" {
					wt := filepath.Join(filepath.Dir(root), strings.ReplaceAll(step.In, "/", "-"))
					testutil.Git(t, root, "worktree", "add", "-q", "-b", step.In, wt)
					worktrees[step.In] = wt
				}
				cwd = worktrees[step.In]
			}
			runHook(t, script, cwd, root, id, step.Hook, payloadOf(t, step))
			dateFiles(t, when, state.EventsDir(root, id))
		}
		if err := store.Ingest(root, id); err != nil {
			t.Fatal(err)
		}
		switch step.Pane {
		case "dead":
			w.PaneDead, w.DeadAt = true, when
		case "dead-undated":
			w.PaneDead, w.DeadAt = true, time.Time{}
		}
		switch step.Sbx {
		case "stopped":
			sandboxes = agent.Sandboxes{Running: map[string]bool{}, Asked: when.Add(time.Duration(step.Asked) * time.Second)}
		case "running":
			sandboxes = agent.Sandboxes{Running: map[string]bool{"claude-app": true}, Asked: when.Add(time.Duration(step.Asked) * time.Second)}
		}
		if step.Screen != "" {
			data, err := os.ReadFile(filepath.Join("testdata", "screen-"+step.Screen+".txt"))
			if err != nil {
				t.Fatal(err)
			}
			// A capture with its styles (-e) is read as the look captures
			// a pane, without them.
			store.screen = styles.ReplaceAllString(string(data), "")
		}
		as := agent.Look([]tmux.Window{w}, sandboxes, when, store, nil)
		j, err := store.Read(id)
		if err != nil {
			t.Fatal(err)
		}
		s := step
		s.Events, s.Reply = nil, nil
		for _, e := range j.Events[seen:] {
			s.Events = append(s.Events, normalized(e))
		}
		seen = len(j.Events)
		if j.Reply.Number != reply {
			r := j.Reply
			s.Reply, reply = &r, r.Number
		}
		s.Look = fixtureLookOf(as[0])
		out.Steps = append(out.Steps, s)
	}
	return out
}

// lookStore is where the look of a fixture journal reads the agent's
// screen and its journal: the screen of the step, and the journal's
// store.
type lookStore struct {
	journal.Files
	screen string
}

// Stands says the agent's home window stands: the fixture's look is at it.
func (s *lookStore) Stands(string) bool { return true }

func (s *lookStore) Screens(panes []string) (map[string]string, error) {
	out := map[string]string{}
	if s.screen != "" {
		for _, p := range panes {
			out[p] = s.screen
		}
	}
	return out, nil
}

// act does to the agent of window w what hq does as how at when: appends
// its event to the agent's journal, as hq does before it acts, and sets
// what hq sets on the window.
func act(t *testing.T, store *lookStore, w *tmux.Window, id, how string, when time.Time) {
	t.Helper()
	if how == "" {
		return
	}
	e := state.Event{Source: state.SourceHQ, Time: when}
	set := map[string]string{}
	switch how {
	case "launch", "restore":
		// In a new home window.
		e.Kind, e.Facts.Start = state.KindLaunched, "new"
		set["started"], set["new"] = agent.Stamp(when), "1"
		if how == "restore" {
			e.Facts.Start, set["new"] = "restore", ""
		}
		delete(w.Options, "ending")
		w.PaneDead, w.DeadAt = false, time.Time{}
	case "relaunch":
		e.Kind = state.KindRelaunched
		set["ending"], set["started"], set["new"] = "", agent.Stamp(when), ""
		w.PaneDead, w.DeadAt = false, time.Time{}
	case "take down":
		e.Kind = state.KindTakenDown
		set["ending"] = "1"
	default:
		t.Fatalf("hq %q", how)
	}
	j, err := store.Read(id)
	if err != nil {
		t.Fatal(err)
	}
	e.Basis = journal.Basis(j.Events)
	if _, err := store.Append(id, e); err != nil {
		t.Fatal(err)
	}
	for k, v := range set {
		w.Options[k] = v
	}
}

// fixtureLookOf is what a fixture journal holds of agent a as the look
// gives it.
func fixtureLookOf(a agent.Agent) *state.FixtureLook {
	l := &state.FixtureLook{State: a.State, Since: a.Since.UTC(), Last: a.Last, OwnTasks: a.OwnTasks}
	if d := a.Dialog(); d.Open() {
		l.Dialog, l.DialogSince = d.Whose, d.Since.UTC()
	}
	return l
}

// hookScript is the hook's script as hq injects it (state.Settings).
func hookScript(t *testing.T) string {
	t.Helper()
	var s struct{ Env map[string]string }
	if err := json.Unmarshal([]byte(state.Settings("agent-17", "0a1b2c3d4e5f", "")), &s); err != nil || s.Env["HQ_HOOK"] == "" {
		t.Fatalf("settings: %v", err)
	}
	return s.Env["HQ_HOOK"]
}

// runHook runs the hook of kind as Claude does, in cwd, with the project
// directory root and payload on stdin.
func runHook(t *testing.T, script, cwd, root, id, kind string, payload []byte) {
	t.Helper()
	cmd := exec.Command("sh", "-c", `eval "$HQ_HOOK"`, "hq-hook", kind)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "HQ_ID="+id, "CLAUDE_PROJECT_DIR="+root, "HQ_HOOK="+script)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil || stderr.Len() != 0 {
		t.Fatalf("hook %s: %v %q", kind, err, stderr.String())
	}
}

// payloadOf is the payload step gives the hook.
func payloadOf(t *testing.T, step state.JournalStep) []byte {
	t.Helper()
	var p []byte
	switch {
	case step.Payload != "":
		data, err := os.ReadFile(filepath.Join("testdata", step.Payload+".json"))
		if err != nil {
			t.Fatal(err)
		}
		p = data
	case step.Inline != nil:
		var b bytes.Buffer
		if err := json.Compact(&b, step.Inline); err != nil {
			t.Fatal(err)
		}
		p = b.Bytes()
	default:
		p = []byte(step.Raw)
	}
	if step.Pad > 0 {
		end := bytes.LastIndexByte(p, '}')
		if end < 0 {
			t.Fatal("no payload to pad")
		}
		p = append(append(p[:end:end], `,"pad":"`+strings.Repeat("x", step.Pad)+`"`...), p[end:]...)
	}
	return p
}

// dateFiles dates what the hook wrote in dirs during the step at when:
// every file newer than the fixture journals' first day.
func dateFiles(t *testing.T, when time.Time, dirs ...string) {
	t.Helper()
	for _, dir := range dirs {
		_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if info, err := d.Info(); err == nil && info.ModTime().After(firstStep.Add(24*time.Hour)) {
				if err := os.Chtimes(path, when, when); err != nil {
					t.Fatal(err)
				}
			}
			return nil
		})
	}
}

// styles are the escapes of a capture taken with its styles.
var styles = regexp.MustCompile("\x1b\\[[0-9;:]*m")

// pid is the process id in the name of an event file.
var pid = regexp.MustCompile(`\.[0-9]+$`)

// normalized is e as a fixture journal holds it: its time in UTC, its
// file named with the process id 1, the messages of a delivery named by
// their place in it.
func normalized(e state.Event) state.Event {
	e.Time = e.Time.UTC()
	if !e.Facts.From.IsZero() {
		e.Facts.From = e.Facts.From.UTC()
	}
	if !e.Facts.Asked.IsZero() {
		e.Facts.Asked = e.Facts.Asked.UTC()
	}
	e.File = pid.ReplaceAllString(e.File, ".1")
	for i := range e.Facts.Messages {
		e.Facts.Messages[i] = fmt.Sprint(i + 1)
	}
	return e
}

// firstDifference shows the first line where a and b differ.
func firstDifference(a, b []byte) string {
	al, bl := strings.Split(string(a), "\n"), strings.Split(string(b), "\n")
	for i := range min(len(al), len(bl)) {
		if al[i] != bl[i] {
			return fmt.Sprintf("line %d:\n rebuilt   %s\n committed %s", i+1, al[i], bl[i])
		}
	}
	return fmt.Sprintf("rebuilt %d lines, committed %d", len(al), len(bl))
}
