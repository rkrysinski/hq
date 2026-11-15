//go:build integration

package state

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/testutil"
)

// leftEvents are the events the hook of agent id left in the repository at
// root, in the order of their names, as ParseEvent reads them; the files
// stay. It fails on a file that is not an event file but the hook's count.
func leftEvents(t *testing.T, root, id string) []Event {
	t.Helper()
	dir := EventsDir(root, id)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var events []Event
	for _, e := range entries {
		if e.Name() == ".n" {
			continue
		}
		if !regexp.MustCompile(`^[0-9]{9}\.[a-z]+\.[0-9]+$`).MatchString(e.Name()) {
			t.Fatalf("a file %q among the event files", e.Name())
		}
		data, info, err := ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		ev, _ := ParseEvent(e.Name(), data, info.ModTime())
		events = append(events, ev)
	}
	return events
}

// eventKinds are the kinds of events, a delivery with the hook's kind it
// came with, joined by spaces.
func eventKinds(events []Event) string {
	var ks []string
	for _, e := range events {
		k := e.Kind
		if k == KindDelivery {
			k += "@" + e.Facts.At
		}
		ks = append(ks, k)
	}
	return strings.Join(ks, " ")
}

// clearEvents removes the event files the hook left, as an ingest does.
func clearEvents(t *testing.T, root, id string) {
	t.Helper()
	dir := EventsDir(root, id)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if IsEventFile(e.Name()) {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

func TestHookLeavesAnEventFileForEveryReportARuleReads(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	wt := filepath.Join(filepath.Dir(root), "feat-42")
	testutil.Git(t, root, "worktree", "add", "-q", "-b", "feat-42", wt)
	const id = "0a1b2c3d4e5f"
	before := time.Now().Add(-time.Second)

	fire(t, root, root, id, "start", fixture(t, "session-start"))
	fire(t, root, root, id, "prompt", fixture(t, "prompt"))
	// A turn of 200 tool calls: the first one's end tells that Claude has
	// acted on the turn; the others tell nothing new.
	for range 200 {
		fire(t, wt, root, id, "answer", fixture(t, "post-tool"))
	}
	fire(t, wt, root, id, "dialog", fixture(t, "dialog-bash"))
	fire(t, wt, root, id, "input", fixture(t, "notification")) // the dialog, reported once
	fire(t, wt, root, id, "answer", fixture(t, "answer-bash")) // closes it
	fire(t, wt, root, id, "stop", fixture(t, "stop-done"))
	fire(t, wt, root, id, "end", fixture(t, "session-end"))

	events := leftEvents(t, root, id)
	if got := eventKinds(events); got != "SessionStart UserPromptSubmit PostToolUse PermissionRequest PostToolUse Stop SessionEnd" {
		t.Fatalf("events %s", got)
	}
	for i, e := range events {
		if e.Source != SourceHook || e.Time.Before(before) || time.Since(e.Time) > time.Minute {
			t.Errorf("event %d: %+v", i, e)
		}
		if i > 0 && e.File <= events[i-1].File {
			t.Errorf("event %d named %s after %s", i, e.File, events[i-1].File)
		}
	}
	if events[1].Facts.Branch != "main" || events[2].Facts.Branch != "feat-42" || events[2].Facts.Tool != "Bash" {
		t.Errorf("facts: %+v %+v", events[1].Facts, events[2].Facts)
	}
	// The old files are still written, and still decide the state.
	if r, ok := Read(root, id); !ok || r.State != Ended {
		t.Errorf("the state file: %+v %v", r, ok)
	}
}

func TestHookLeavesEventsForTurnsTheStateFileDoesNotShow(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	const id = "0a1b2c3d4e5f"
	fire(t, root, root, id, "prompt", fixture(t, "prompt"))
	// A turn that ends with background work running: kept beside the state
	// file, and an event all the same; so is Claude's wake-up for it.
	fire(t, root, root, id, "answer", []byte(`{"hook_event_name":"PostToolUse","prompt_id":"`+promptID+`","tool_name":"Agent","tool_response":{"status":"async_launched","agentId":"a58b43841609db047"}}`))
	fire(t, root, root, id, "stop", fixture(t, "stop-background"))
	fire(t, root, root, id, "prompt", fixture(t, "prompt-wake"))
	// A subagent's tool tells nothing, unless it starts a shell.
	fire(t, root, root, id, "answer", fixture(t, "post-tool-subagent"))
	fire(t, root, root, id, "answer", fixture(t, "post-tool-subagent-monitor"))
	events := leftEvents(t, root, id)
	if got := eventKinds(events); got != "UserPromptSubmit PostToolUse Stop UserPromptSubmit PostToolUse" {
		t.Fatalf("events %s", got)
	}
	if events[1].Facts.Started != "a58b43841609db047" || events[3].Facts.Woke != "a58b43841609db047" || events[4].Facts.Started != "~b3ukv3517" {
		t.Errorf("facts: %+v", events)
	}
	if r, _ := Read(root, id); r.State != Working {
		t.Errorf("the state file: %+v", r)
	}
	// A prompt queued while the agent works, and its hand-over at the
	// turn's stop.
	clearEvents(t, root, id)
	root2 := testutil.GitRepo(t, "queued")
	fire(t, root2, root2, id, "prompt", fixture(t, "queued-first"))
	fire(t, root2, root2, id, "prompt", fixture(t, "queued-while-working"))
	fire(t, root2, root2, id, "stop", fixture(t, "queued-stop"))
	if got := eventKinds(leftEvents(t, root2, id)); got != "UserPromptSubmit UserPromptSubmit Stop" {
		t.Fatalf("queued: %s", got)
	}
	if r, _ := Read(root2, id); r.State != Working {
		t.Errorf("the queued prompt's state file: %+v", r)
	}
}

// promptID is the prompt_id of the prompt fixture.
const promptID = "0000001b-0000-4000-8000-00000000001b"

func TestHookDeliveriesAreEventsNamingEachMessage(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	const id = "0a1b2c3d4e5f"
	fire(t, root, root, id, "prompt", fixture(t, "prompt"))
	inbox := func() []string {
		entries, _ := os.ReadDir(InboxDir(root, id))
		var names []string
		for _, e := range entries {
			if isMessage(e.Name()) && !e.IsDir() {
				names = append(names, e.Name())
			}
		}
		return names
	}

	// A stop the hook blocks: the delivery, then the turn end after it.
	postFrom(t, root, id, true, false, "one")
	post(t, root, id, false, "two")
	// A held message is hq's alone: no hook delivers it.
	taken, _ := Take(root, id)
	if err := Hold(root, id, taken[1:]); err != nil {
		t.Fatal(err)
	}
	post(t, root, id, false, "two")
	names := inbox()
	if len(names) != 1 {
		t.Fatalf("inbox %v", names)
	}
	if o := decodeOutput(t, run(t, root, root, id, hook("stop", ""), fixture(t, "stop-to-block"))); o.Decision != "block" {
		t.Fatalf("stop: %+v", o)
	}
	fire(t, root, root, id, "stop", fixture(t, "stop-after-block"))
	events := leftEvents(t, root, id)
	if got := eventKinds(events); got != "UserPromptSubmit delivery@stop Stop" {
		t.Fatalf("events %s", got)
	}
	if strings.Join(events[1].Facts.Messages, " ") != names[0] {
		t.Errorf("delivered %v, inbox %v", events[1].Facts.Messages, names)
	}
	clearEvents(t, root, id)

	// With the next prompt: the delivery, then the prompt, naming each
	// message, the supervisor's too.
	postFrom(t, root, id, true, false, "three")
	post(t, root, id, false, "four")
	names = inbox()
	if out := run(t, root, root, id, hook("prompt", ""), fixture(t, "prompt")); !strings.Contains(out, "four") {
		t.Fatalf("prompt: %q", out)
	}
	events = leftEvents(t, root, id)
	if got := eventKinds(events); got != "delivery@prompt UserPromptSubmit" || strings.Join(events[0].Facts.Messages, " ") != strings.Join(names, " ") || len(names) != 2 {
		t.Fatalf("events %s, %v, inbox %v", got, events[0].Facts.Messages, names)
	}
	clearEvents(t, root, id)

	// After a tool's end (--now): the tool's end, then the delivery, then
	// the turn end.
	post(t, root, id, true, "five")
	names = inbox()
	tool := []byte(strings.Replace(string(fixture(t, "post-tool")), "00000007-0000-4000-8000-000000000007", promptID, 1))
	if out := run(t, root, root, id, hook("answer", ""), tool); !strings.Contains(out, "five") {
		t.Fatalf("tool: %q", out)
	}
	fire(t, root, root, id, "stop", fixture(t, "stop-done"))
	events = leftEvents(t, root, id)
	if got := eventKinds(events); got != "PostToolUse delivery@answer Stop" || strings.Join(events[1].Facts.Messages, " ") != names[0] {
		t.Fatalf("events %s, %v, inbox %v", got, events[1].Facts.Messages, names)
	}
	if n := Held(root, id); n != 1 {
		t.Errorf("held %d", n)
	}
}

// A question dialog answered at the terminal before the hook that holds
// on it first saw its report in the state file: the state file, newer
// than the hold's copy and not the dialog's, ends the hold at once, not
// after the 10 s it waits for a report that never comes (#193: the event
// files made the dialog's report come a moment later).
func TestTheHoldEndsWhenItsDialogWasAnsweredBeforeItSawIt(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	const id = "0a1b2c3d4e5f"
	fire(t, root, root, id, "prompt", fixture(t, "prompt"))
	h := holding(hook("hold", ""))
	cmd := exec.Command(h.Command, h.Args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "HQ_ID="+id, "CLAUDE_PROJECT_DIR="+root, hookEnv+"="+hookScript)
	cmd.Stdin = bytes.NewReader(fixture(t, "dialog-ask"))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Past the second of the hold's copy, for a shell that dates files
	// by the second.
	time.Sleep(1100 * time.Millisecond)
	fire(t, root, root, id, "dialog", fixture(t, "dialog-ask"))
	fire(t, root, root, id, "answer", fixture(t, "answer-ask"))
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		cmd.Process.Kill()
		t.Fatal("the hold still waits for its dialog's report")
	}
	if left, _ := filepath.Glob(filepath.Join(Dir(root), id+".[0-9]*")); len(left) != 0 {
		t.Errorf("left %v", left)
	}
}
