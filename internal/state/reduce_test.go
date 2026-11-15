package state

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// JournalFixture is a fixture journal (testdata/journals): an agent's
// journal in steps, each what one run of the hook left, and what the old
// reading of the hook's working files (Read) gave after it. The steps'
// hook, payload, inline, raw, pad, messages and in say how the integration
// test rebuilds the journal (TestTheFixtureJournalsAreWhatTheHookAndTheIngestMakeOfTheirPayloads);
// the rest it writes. A journal reported from an agent can be added with
// its steps' events and what the state should have been.
type JournalFixture struct {
	About string        `json:"about"`
	Steps []JournalStep `json:"steps"`
}

// JournalStep is one run of the hook in a fixture journal.
type JournalStep struct {
	// Hook is the hook's kind it runs as (refuse, prompt, stop, answer,
	// dialog, input, result, start, resume, end).
	Hook string `json:"hook"`
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
	// Events are what the ingest appended to the journal after the run,
	// their times as the test dated the files, their names with the
	// process id 1 and the names of messages counted from 1.
	Events []Event `json:"events,omitempty"`
	// Reply is the journal's reply when the run changed it.
	Reply *Reply `json:"reply,omitempty"`
	// Old is what the old reading gave after the run.
	Old OldReading `json:"old"`
}

// OldReading is what Read gave of the hook's working files, and the
// dialog verdict it makes (Subagent, while it needs input), as a fixture
// journal holds it.
type OldReading struct {
	Reported   bool      `json:"reported"`
	State      string    `json:"state"`
	Since      time.Time `json:"since"`
	Latest     time.Time `json:"latest"`
	Last       string    `json:"last,omitempty"`
	LastStop   string    `json:"last_stop,omitempty"`
	Branch     string    `json:"branch,omitempty"`
	Worktree   string    `json:"worktree,omitempty"`
	Session    string    `json:"session,omitempty"`
	Prompt     string    `json:"prompt,omitempty"`
	Background bool      `json:"background,omitempty"`
	Owed       bool      `json:"owed,omitempty"`
	Woken      bool      `json:"woken,omitempty"`
	Acted      bool      `json:"acted,omitempty"`
	Queued     bool      `json:"queued,omitempty"`
	Subagent   bool      `json:"subagent,omitempty"`
	OwnTasks   []Task    `json:"own_tasks,omitempty"`
}

// OldReadingOf is what a fixture journal holds of r, the report Read gave
// (ok false: none yet).
func OldReadingOf(r Report, ok bool) OldReading {
	return OldReading{
		Reported: ok, State: r.State, Since: r.Since.UTC(), Latest: r.Latest.UTC(),
		Last: r.Last, LastStop: r.LastStop, Branch: r.Branch, Worktree: r.Cwd, Session: r.SessionID, Prompt: r.Prompt,
		Background: r.Background, Owed: r.Owed, Woken: r.Woken, Acted: r.Acted, Queued: r.Queued,
		Subagent: r.SubagentDialog, OwnTasks: r.OwnTasks,
	}
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

// asOld is what the old reading would have to give for the reducer's
// result r. The old reading gives the prompt of a wake-up as Claude sent
// it, which no rule reads and the journal does not keep.
func asOld(r Reduced, old OldReading) OldReading {
	o := OldReading{
		Reported: r.Reported, State: r.State, Since: r.Since, Latest: r.Latest,
		Last: r.Last, LastStop: r.LastStop, Branch: r.Branch, Worktree: r.Worktree, Session: r.Session, Prompt: r.Prompt,
		Background: r.Background, Owed: r.Owed, Woken: r.Woken, Acted: r.Acted, Queued: r.Queued,
		Subagent: r.Dialog.Subagent, OwnTasks: r.OwnTasks,
	}
	if strings.HasPrefix(old.Prompt, wakePrefix) && o.Prompt == "" {
		o.Prompt = old.Prompt
	}
	return o
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

// The reducer gives, after every report of every fixture journal, the
// state, last message, own tasks and dialog verdict the old reading gave
// for the same reports, and the rest of what it read too.
func TestTheReducerReadsEveryFixtureJournalAsTheOldReadingDid(t *testing.T) {
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
			if got, want := jsonOf(t, asOld(r, step.Old)), jsonOf(t, step.Old); got != want {
				t.Errorf("%s, step %d (%s %s%s):\n got %s\nwant %s", name, i, step.Hook, step.Payload, step.Raw, got, want)
			}
			if r.Reported && r.Age != now.Sub(r.Since) {
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
		{2, Working, []int64{1, 2}, nil},              // acted on
		{4, Working, []int64{1, 2, 3, 4}, nil},        // background work
		{5, NeedsInput, []int64{4, 5}, []int64{4, 5}}, // a subagent's form
		{6, Working, []int64{2, 3, 4, 6}, nil},        // answered: the background work again
		{7, Working, []int64{7}, nil},                 // Claude's wake-up, written over the answer
		{8, Question, []int64{8}, nil},
	} {
		r := reducedAt(journal, tc.after)
		if r.State != tc.state || !slices.Equal(r.RestsOn, tc.rests) || !slices.Equal(r.Dialog.RestsOn, tc.dialog) {
			t.Errorf("after %d: %s, rests on %v, the dialog on %v", tc.after, r.State, r.RestsOn, r.Dialog.RestsOn)
		}
	}
	if r := reducedAt(journal, 5); !r.Dialog.Open || !r.Dialog.Subagent || r.Dialog.Opened != (Mark{5, atSecond(5)}) {
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
