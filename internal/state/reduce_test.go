package state

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"
)

// JournalFixture is a fixture journal (testdata/journals): an agent's
// journal in steps, each what one run of the hook, or what hq did, left in
// it with what the look then saw, and what the old look (agent.Look) gave
// after it. The
// steps' hook, payload, inline, raw, pad, messages, in, hq, screen, pane,
// sbx and asked say how the integration test rebuilds the journal
// (TestTheFixtureJournalsAreWhatTheHookAndTheIngestMakeOfTheirPayloads);
// the rest it writes. A journal reported from an agent can be added with
// its steps' events and what the state should have been.
type JournalFixture struct {
	About string        `json:"about"`
	Steps []JournalStep `json:"steps"`
}

// JournalStep is one step of a fixture journal, a second after the one
// before: what hq does to the agent (HQ), then a run of the hook (Hook),
// then a look at the agent, with what its pane shows (Screen), its pane
// (Pane) and sbx's answer (Sbx) as they are then.
type JournalStep struct {
	// HQ is what hq does to the agent first: launch it (as hq new) or
	// restore it, relaunch it (as hq sandbox restart), take it down (as a
	// restart, before its sandbox stops).
	HQ string `json:"hq,omitempty"`
	// Hook is the hook's kind it runs as (refuse, prompt, stop, answer,
	// dialog, input, result, start, resume, end), none when empty.
	Hook string `json:"hook,omitempty"`
	// Payload names the payload of testdata it is given; Inline is one
	// written here (given compact, as Claude writes it); Raw is one hq
	// cannot read.
	Payload string          `json:"payload,omitempty"`
	Inline  json.RawMessage `json:"inline,omitempty"`
	Raw     string          `json:"raw,omitempty"`
	// Pad adds to the payload a field of that many bytes, after its
	// others.
	Pad int `json:"pad,omitempty"`
	// Messages are left in the agent's inbox before the hook runs.
	Messages int `json:"messages,omitempty"`
	// In names the branch of a worktree the hook runs in, the repository
	// when empty.
	In string `json:"in,omitempty"`
	// Screen names the screen of testdata the agent's pane shows to the
	// look from this step on (screen-NAME.txt); none until the first.
	Screen string `json:"screen,omitempty"`
	// Pane is the agent's pane from this step on, until a relaunch: dead,
	// as tmux dates it now, or dead-undated, a death tmux gives no time.
	Pane string `json:"pane,omitempty"`
	// Sbx is sbx's answer from this step on: stopped, the agent's sandbox
	// not listed, or running; asked Asked seconds after the step (before
	// it, when negative).
	Sbx   string `json:"sbx,omitempty"`
	Asked int    `json:"asked,omitempty"`
	// Events are what the ingest appended to the journal after the run,
	// their times as the test dated the files, their names with the
	// process id 1 and the names of messages counted from 1.
	Events []Event `json:"events,omitempty"`
	// Reply is the journal's reply when the run changed it.
	Reply *Reply `json:"reply,omitempty"`
	// Look is what the old look gave after the step.
	Look *OldLook `json:"look,omitempty"`
}

// OldLook is what the old look (agent.Look) gave of the agent after a
// step: its state, the moment it counts from, its last message, its own
// tasks and its dialog verdict, whose dialog it is at (hooks, subagent,
// own) since when.
type OldLook struct {
	State       string    `json:"state"`
	Since       time.Time `json:"since"`
	Last        string    `json:"last,omitempty"`
	OwnTasks    []Task    `json:"own_tasks,omitempty"`
	Dialog      string    `json:"dialog,omitempty"`
	DialogSince time.Time `json:"dialog_since,omitzero"`
}

// Encode is the fixture as its file holds it.
func (fx JournalFixture) Encode() ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	err := enc.Encode(fx)
	return b.Bytes(), err
}

// fixtureJournals are the fixture journals, by file name.
func fixtureJournals(t *testing.T) map[string]JournalFixture {
	t.Helper()
	paths, err := filepath.Glob("testdata/journals/*.json")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no fixture journals: %v", err)
	}
	fxs := map[string]JournalFixture{}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var fx JournalFixture
		if err := json.Unmarshal(data, &fx); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		fxs[filepath.Base(p)] = fx
	}
	return fxs
}

// jsonOf is v as JSON, to compare and to show.
func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// asLook is what the old look would have to give for the reducer's result
// r.
func asLook(r Reduced) *OldLook {
	l := &OldLook{State: r.State, Since: r.Since.UTC(), Last: r.Last, OwnTasks: r.OwnTasks}
	if r.Dialog.Open {
		l.Dialog, l.DialogSince = r.Dialog.Whose, r.Dialog.Opened.Time.UTC()
	}
	return l
}

// The reducer gives, after every step of every fixture journal, the state,
// the moment it counts from, the last message, the own tasks and the
// dialog verdict the old look gave for the same payloads, screens, pane
// and sbx answer, its age counted to the moment passed in.
func TestTheReducerGivesEveryFixtureJournalAsTheOldLookDid(t *testing.T) {
	now := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	for name, fx := range fixtureJournals(t) {
		var events []Event
		reply := Reply{}
		for i, step := range fx.Steps {
			events = append(events, step.Events...)
			if step.Reply != nil {
				reply = *step.Reply
			}
			r := Reduce(events, reply, now)
			if step.Look == nil {
				t.Fatalf("%s, step %d: no look", name, i)
			}
			if got, want := jsonOf(t, asLook(r)), jsonOf(t, step.Look); got != want {
				t.Errorf("%s, step %d (%s%s %s%s%s):\n got %s\nwant %s", name, i, step.HQ, step.Hook, step.Payload, step.Raw, step.Screen, got, want)
			}
			if !r.Since.IsZero() && r.Age != now.Sub(r.Since) {
				t.Errorf("%s, step %d: age %v since %v", name, i, r.Age, r.Since)
			}
		}
	}
}

// Folding a journal on from a kept position gives what folding it from
// the start gives, at every event, and leaves the kept position as it was.
func TestFoldingOnFromAKeptPositionIsFoldingFromTheStart(t *testing.T) {
	now := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	for name, fx := range fixtureJournals(t) {
		var events []Event
		var kept Fold
		reply := Reply{}
		for _, step := range fx.Steps {
			if step.Reply != nil {
				reply = *step.Reply
			}
			for _, e := range step.Events {
				before := jsonOf(t, kept.Result(reply, now))
				next := kept.With(e)
				if after := jsonOf(t, kept.Result(reply, now)); after != before {
					t.Fatalf("%s, event %d: the kept position changed:\n%s\n%s", name, e.Number, before, after)
				}
				events = append(events, e)
				if got, want := jsonOf(t, next.Result(reply, now)), jsonOf(t, Reduce(events, reply, now)); got != want {
					t.Fatalf("%s, event %d:\nfolded on %s\nfrom start %s", name, e.Number, got, want)
				}
				kept = next
			}
		}
	}
}

// The time passed in counts the age and changes nothing else.
func TestTheTimePassedInChangesOnlyTheAge(t *testing.T) {
	now := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	for name, fx := range fixtureJournals(t) {
		var events []Event
		reply := Reply{}
		for _, step := range fx.Steps {
			events = append(events, step.Events...)
			if step.Reply != nil {
				reply = *step.Reply
			}
		}
		one, other := Reduce(events, reply, now), Reduce(events, reply, now.Add(time.Hour))
		if other.Age-one.Age != time.Hour || one.Age <= 0 {
			t.Errorf("%s: ages %v and %v", name, one.Age, other.Age)
		}
		one.Age, other.Age = 0, 0
		if !reflect.DeepEqual(one, other) {
			t.Errorf("%s:\n%+v\n%+v", name, one, other)
		}
	}
	if r := Reduce(nil, Reply{}, now); r.Reported || r.State != Starting || r.Age != 0 || r.Entered != (Mark{}) {
		t.Errorf("an empty journal: %+v", r)
	}
}

// atSecond is the time of event n of the journals below.
func atSecond(n int64) time.Time { return time.Date(2026, 1, 1, 0, 0, int(n), 0, time.UTC) }

// hookEvent is event n of the hook, of kind, as the hook's case at wrote
// it, with facts.
func hookEvent(n int64, hookCase, kind string, f Facts) Event {
	f.At = hookCase
	return Event{Number: n, Source: SourceHook, Kind: kind, Time: atSecond(n), Facts: f}
}

// A journal of an agent's turns, as unit tests write one.
func userPrompt(n int64, turn string) Event {
	return hookEvent(n, "prompt", "UserPromptSubmit", Facts{Turn: turn, Prompt: "say hi"})
}

func endOfTurn(n int64, turn string, question bool, tasks ...EventTask) Event {
	return hookEvent(n, "stop", KindTurnEnd, Facts{Turn: turn, Question: question, Tasks: tasks})
}

func subagentRunning(id string) EventTask {
	return EventTask{ID: id, Type: "subagent", Status: "running"}
}

// reducedAt is the state of journal after its event n.
func reducedAt(journal []Event, n int64) Reduced {
	i := slices.IndexFunc(journal, func(e Event) bool { return e.Number == n })
	return Reduce(journal[:i+1], Reply{}, atSecond(100))
}

// The result names the event at which the agent last entered its state,
// by today's rule (the report its state comes from, which a queued prompt
// renews), and apart from it the event at which the state or the dialog
// last changed.
func TestTheResultNamesWhereTheStateWasEnteredAndWhereItChanged(t *testing.T) {
	journal := []Event{
		userPrompt(1, "t1"),
		hookEvent(2, "answer", "PostToolUse", Facts{Turn: "t1", Tool: "Bash"}), // Claude has acted on the turn
		userPrompt(3, "t1"), // queued while it works: its report, the same state
		hookEvent(4, "dialog", "PermissionRequest", Facts{Turn: "t1", Tool: "Bash", Text: "Bash: ls"}),
		hookEvent(5, "dialog", "PermissionRequest", Facts{Turn: "t1", Tool: "Read", Text: "Read: a"}), // another dialog
		hookEvent(6, "answer", "PostToolUse", Facts{Turn: "t1", Tool: "Read"}),
		endOfTurn(7, "t1", false),
		hookEvent(8, "input", "Notification", Facts{Text: "Claude needs your permission"}),
	}
	for _, tc := range []struct {
		after            int64
		state            string
		entered, changed int64
	}{
		{1, Working, 1, 1},
		{2, Working, 1, 1},
		{3, Working, 3, 1},
		{4, NeedsInput, 4, 4},
		{5, NeedsInput, 5, 5},
		{6, Working, 6, 6},
		{7, Done, 7, 7},
		{8, NeedsInput, 8, 8},
	} {
		r := reducedAt(journal, tc.after)
		if r.State != tc.state || r.Entered != (Mark{tc.entered, atSecond(tc.entered)}) || r.Changed != (Mark{tc.changed, atSecond(tc.changed)}) || !r.Since.Equal(atSecond(tc.entered)) {
			t.Errorf("after %d: %s, entered %+v, changed %+v, since %v", tc.after, r.State, r.Entered, r.Changed, r.Since)
		}
	}
	// A later end of the same tool, once the dialog is closed, is no
	// report: the agent works since the answer.
	later := append(journal[:6:6], hookEvent(7, "answer", "PostToolUse", Facts{Turn: "t1", Tool: "Read", Subagent: true, Started: "~b1"}))
	if r := Reduce(later, Reply{}, atSecond(9)); r.State != Working || r.Entered.Number != 6 {
		t.Errorf("a later tool's end: %s, entered %+v", r.State, r.Entered)
	}
	// A tool's end that leaves the agent starting changes nothing.
	if r := Reduce(journal[1:2], Reply{}, atSecond(9)); r.Reported || r.Changed != (Mark{}) {
		t.Errorf("before any report: %+v", r)
	}
}

// The result names the turn end of the last completed turn: one reported
// as done or a question, a closing turn Claude started itself included,
// but not a turn end that leaves the agent working (background work, a
// queued prompt Claude takes), a stop blocked to deliver a message, or a
// session's start.
func TestTheResultNamesTheTurnEndOfTheLastCompletedTurn(t *testing.T) {
	journal := []Event{
		hookEvent(1, "start", "SessionStart", Facts{Start: "startup"}),
		userPrompt(2, "t1"),
		endOfTurn(3, "t1", false),
		userPrompt(4, "t2"),
		hookEvent(5, "stop", KindDelivery, Facts{Messages: []string{"1"}}), // a stop blocked to deliver it
		endOfTurn(6, "t2", true),
		userPrompt(7, "t3"),
		hookEvent(8, "answer", "PostToolUse", Facts{Turn: "t3", Tool: "Agent", Started: "a1"}),
		endOfTurn(9, "t3", false, subagentRunning("a1")),
		hookEvent(10, "prompt", "UserPromptSubmit", Facts{Turn: "w1", Wake: true, Woke: "a1"}),
		endOfTurn(11, "w1", false),
		userPrompt(12, "t4"),
		userPrompt(13, "t4"), // queued
		endOfTurn(14, "t4", false),
		hookEvent(15, "start", "SessionStart", Facts{Start: "clear"}),
		hookEvent(16, "resume", "SessionStart", Facts{Start: "resume"}),
	}
	for _, tc := range []struct {
		after, turnEnded int64
		state            string
	}{
		{1, 0, Done}, {2, 0, Working}, {3, 3, Done}, {5, 3, Working}, {6, 6, Question},
		{9, 6, Working}, {10, 6, Working}, {11, 11, Done}, {14, 11, Working}, {15, 11, Done}, {16, 11, Done},
	} {
		if r := reducedAt(journal, tc.after); r.TurnEnded != tc.turnEnded || r.State != tc.state {
			t.Errorf("after %d: %s, turn ended at %d", tc.after, r.State, r.TurnEnded)
		}
	}
}

// The result names the events the state and the dialog verdict rest on:
// for an agent working on background work, the prompt, the start of the
// task and the turn end that left it working.
func TestTheResultNamesTheEventsTheStateRestsOn(t *testing.T) {
	journal := []Event{
		userPrompt(1, "t1"),
		hookEvent(2, "answer", "PostToolUse", Facts{Turn: "t1", Tool: "Agent", Started: "a1"}),
		hookEvent(3, "answer", "PostToolUse", Facts{Turn: "t1", Tool: "Bash", Subagent: true, Started: "~b1"}),
		endOfTurn(4, "t1", false, subagentRunning("a1"), EventTask{ID: "b1", Type: ShellTask, Status: "running"}),
		hookEvent(5, "dialog", "Elicitation", Facts{Server: "elicit", Text: "Which colour?"}),
		hookEvent(6, "result", "ElicitationResult", Facts{Server: "elicit"}),
		hookEvent(7, "prompt", "UserPromptSubmit", Facts{Turn: "w1", Wake: true, Woke: "a1"}),
		endOfTurn(8, "w1", true),
	}
	for _, tc := range []struct {
		after         int64
		state         string
		rests, dialog []int64
	}{
		{1, Working, []int64{1}, nil},
		{2, Working, []int64{1, 2}, nil},                    // acted on
		{4, Working, []int64{1, 2, 3, 4}, nil},              // background work
		{5, NeedsInput, []int64{4, 5}, []int64{4, 5}},       // a subagent's form
		{6, Working, []int64{2, 3, 4, 6}, []int64{4, 5, 6}}, // answered: the background work again; the form closed
		{7, Working, []int64{7}, []int64{4, 5, 6}},          // Claude's wake-up, written over the answer
		{8, Question, []int64{8}, []int64{4, 5, 6}},
	} {
		r := reducedAt(journal, tc.after)
		if r.State != tc.state || !slices.Equal(r.RestsOn, tc.rests) || !slices.Equal(r.Dialog.RestsOn, tc.dialog) {
			t.Errorf("after %d: %s, rests on %v, the dialog on %v", tc.after, r.State, r.RestsOn, r.Dialog.RestsOn)
		}
	}
	if r := reducedAt(journal, 5); !r.Dialog.Open || r.Dialog.Whose != DialogSubagent || r.Dialog.Opened != (Mark{5, atSecond(5)}) {
		t.Errorf("the subagent's form: %+v", r.Dialog)
	}
}

// unreadableEvent is event n of the hook's case hookCase that hq could not
// read, of size bytes.
func unreadableEvent(n int64, hookCase string, size int64) Event {
	return hookEvent(n, hookCase, KindUnreadable, Facts{Size: size})
}

// A report too large to read is a turn end with a reply too long to keep:
// done, saying so, and the last message after it; one hq cannot read for
// another reason is starting, with the last message kept.
func TestAReportHqCannotReadIsStartingUnlessItIsTooLarge(t *testing.T) {
	big := TooLarge(maxFile + 1)
	for _, tc := range []struct {
		journal     []Event
		state, last string
	}{
		{[]Event{userPrompt(1, "t1"), unreadableEvent(2, "stop", maxFile+1)}, Done, big},
		{[]Event{userPrompt(1, "t1"), unreadableEvent(2, "stop", maxFile+1), userPrompt(3, "t2")}, Working, big},
		{[]Event{userPrompt(1, "t1"), unreadableEvent(2, "stop", maxFile)}, Starting, ""},
		{[]Event{userPrompt(1, "t1"), unreadableEvent(2, "stop", maxFile), userPrompt(3, "t2")}, Working, ""},
		{[]Event{userPrompt(1, "t1"), unreadableEvent(2, "prompt", maxFile)}, Starting, ""},
	} {
		r := Reduce(tc.journal, Reply{}, atSecond(9))
		if r.State != tc.state || r.Last != tc.last || !r.Reported {
			t.Errorf("%+v: %s %q", tc.journal[len(tc.journal)-1], r.State, r.Last)
		}
	}
}

// An own task's text is the reply's, which the journal keeps for its
// latest turn end only.
func TestOwnTasksTakeTheirTextsFromTheReply(t *testing.T) {
	shell := EventTask{ID: "b1", Type: ShellTask, Status: "running"}
	journal := []Event{userPrompt(1, "t1"), endOfTurn(2, "t1", false, shell)}
	for _, tc := range []struct {
		reply Reply
		want  string
	}{
		{Reply{Number: 2, Tasks: []TaskTexts{{ID: "b1", Command: "npm run dev"}}}, "npm run dev"},
		{Reply{Number: 2}, ""},
		{Reply{Number: 1, Tasks: []TaskTexts{{ID: "b1", Command: "npm run dev"}}}, ""},
	} {
		r := Reduce(journal, tc.reply, atSecond(9))
		if len(r.OwnTasks) != 1 || r.OwnTasks[0] != (Task{Type: ShellTask, Text: tc.want}) {
			t.Errorf("%+v: %+v", tc.reply, r.OwnTasks)
		}
	}
}
