package journaltest

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/journal"
	"github.com/rkrysinski/hq/internal/state"
)

// Harness is a store under test with the hooks' side of it.
type Harness struct {
	Store journal.Store
	// Leave leaves an event file named name with data, written at at, for
	// agent id of the repository at root, as its hook does.
	Leave func(root, id, name string, data []byte, at time.Time)
	// Left are the names of the event files waiting for agent id.
	Left func(root, id string) []string
	// Root is a new repository for agents.
	Root func() string
}

// Hook is an event file as the hook writes it for a payload of Claude's:
// the branch line, then the payload.
func Hook(branch string, payload map[string]any) []byte {
	data, _ := json.Marshal(payload)
	return append([]byte("branch "+branch+"\n"), data...)
}

// Contract checks what hq relies on from any journal store (ADR 0018):
// event files ingested in the order of their names and removed, numbers
// from one counter for every agent, rising in each journal, the latest
// turn end's reply kept once, hq's own events appended, evidence set aside
// as a duplicate or as overtaken (#195), evidence for a journal that is
// not there written only while the agent's home window stands (#263), one
// first append of writers racing, a journal read on from a position, what
// the latest dialog asks kept once, with a question dialog's report and
// the answers Claude got, a removed journal gone, and ids hq did not issue
// refused.
func Contract(t *testing.T, newHarness func(t *testing.T) Harness) {
	t.Helper()
	at := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	prompt := Hook("feat-42", map[string]any{"hook_event_name": "UserPromptSubmit", "prompt_id": "p1", "session_id": "s1", "prompt": "Run the tests"})
	tool := Hook("feat-42", map[string]any{"hook_event_name": "PostToolUse", "prompt_id": "p1", "tool_name": "Bash"})
	stop := func(reply string) []byte {
		return Hook("feat-42", map[string]any{"hook_event_name": "Stop", "prompt_id": "p1", "last_assistant_message": reply})
	}

	t.Run("in the order of the names", func(t *testing.T) {
		h := newHarness(t)
		root := h.Root()
		h.Leave(root, "a1", "000000003.7", stop("Done?"), at.Add(3*time.Second))
		h.Leave(root, "a1", "000000001.7", prompt, at.Add(time.Second))
		h.Leave(root, "a1", "000000002.7", tool, at.Add(2*time.Second))
		must(t, h.Store.Ingest(root, "a1"))
		j := read(t, h.Store, "a1")
		{
			got := Kinds(j.Events)
			require(t, got == "UserPromptSubmit PostToolUse Stop", "kinds %s", got)
		}
		for i, e := range j.Events {
			name := fmt.Sprintf("00000000%d.7", i+1)
			expect(t, e.Number == j.Events[0].Number+int64(i) && e.Source == state.SourceHook && e.File == name &&
				e.Time.Equal(at.Add(time.Duration(i+1)*time.Second)) && e.Facts.Branch == "feat-42" && e.Facts.Turn == "p1",
				"event %d: %+v", i, e)
		}
		expect(t, j.Events[2].Facts.Question && j.Events[0].Facts.Prompt == "Run the tests" && j.Events[1].Facts.Tool == "Bash", "facts: %+v", j.Events)
		expect(t, j.Through >= j.Events[2].Number, "through %d", j.Through)
		{
			left := h.Left(root, "a1")
			expect(t, len(left) == 0, "left %v", left)
		}
		// Nothing left: nothing more.
		{
			err := h.Store.Ingest(root, "a1")
			expect(t, err == nil && len(read(t, h.Store, "a1").Events) == 3, "a second ingest: %v", err)
		}
	})

	t.Run("a later file with an earlier name", func(t *testing.T) {
		h := newHarness(t)
		root := h.Root()
		h.Leave(root, "a1", "000000005.7", tool, at)
		must(t, h.Store.Ingest(root, "a1"))
		h.Leave(root, "a1", "000000004.7", prompt, at)
		must(t, h.Store.Ingest(root, "a1"))
		j := read(t, h.Store, "a1")
		{
			got := Kinds(j.Events)
			require(t, got == "PostToolUse UserPromptSubmit" && numberedInOrder(j.Events), "%s: %+v", got, j.Events)
		}
	})

	t.Run("one counter for every agent", func(t *testing.T) {
		h := newHarness(t)
		one, two := h.Root(), h.Root()
		for i := range 3 {
			name := fmt.Sprintf("%09d.1", i+1)
			h.Leave(one, "a1", name, prompt, at)
			must(t, h.Store.Ingest(one, "a1"))
			h.Leave(two, "b2", name, prompt, at)
			h.Leave(two, "b2", name+"0", tool, at)
			must(t, h.Store.Ingest(two, "b2"))
		}
		_, err := h.Store.Append("a1", state.Event{Source: state.SourceScreen, Kind: "rest", Time: at})
		must(t, err)
		seen := map[int64]bool{}
		var through int64
		for _, id := range []string{"a1", "b2"} {
			j := read(t, h.Store, id)
			through = max(through, j.Through)
			expect(t, numberedInOrder(j.Events), "%s: numbers not in order (%+v)", id, j.Events)
			for _, e := range j.Events {
				expect(t, !seen[e.Number], "%s: number %d twice (%+v)", id, e.Number, j.Events)
				seen[e.Number] = true
			}
		}
		expect(t, len(seen) == 10 && seen[through], "numbers %v, through %d", seen, through)
	})

	t.Run("the counter read on its own", func(t *testing.T) {
		h := newHarness(t)
		n, err := h.Store.Through()
		require(t, err == nil && n == 0, "before any event: %d %v", n, err)
		root := h.Root()
		h.Leave(root, "a1", "000000001.1", prompt, at)
		must(t, h.Store.Ingest(root, "a1"))
		_, err = h.Store.Append("b2", state.Event{Source: state.SourceHQ, Kind: state.KindLaunched, Time: at}, state.Event{Source: state.SourceScreen, Kind: "rest", Time: at})
		must(t, err)
		n, err = h.Store.Through()
		b := read(t, h.Store, "b2")
		expect(t, err == nil && n == 3 && n == b.Through && b.Events[1].Number == n, "after three events: %d %v (%+v)", n, err, b)
	})

	t.Run("the latest turn end keeps its reply", func(t *testing.T) {
		h := newHarness(t)
		root := h.Root()
		h.Leave(root, "a1", "000000001.1", stop("first reply"), at)
		must(t, h.Store.Ingest(root, "a1"))
		h.Leave(root, "a1", "000000002.1", stop("second reply"), at)
		h.Leave(root, "a1", "000000003.1", prompt, at)
		long := strings.Repeat("a reply of several megabytes ", 200_000)
		h.Leave(root, "a1", "000000004.1", stop(long), at)
		must(t, h.Store.Ingest(root, "a1"))
		j := read(t, h.Store, "a1")
		require(t, j.Reply.Text == long && j.Reply.Number == j.Events[3].Number, "reply %d of %d bytes, events %s", j.Reply.Number, len(j.Reply.Text), Kinds(j.Events))
		data, _ := json.Marshal(j.Events)
		expect(t, !strings.Contains(string(data), "reply"), "an event keeps a reply: %s", data)
		// A turn end hq cannot read is no reply.
		h.Leave(root, "a1", "000000005.1", []byte("branch x\n{"), at)
		must(t, h.Store.Ingest(root, "a1"))
		{
			j := read(t, h.Store, "a1")
			expect(t, j.Reply.Text == long && j.Events[4].Kind == state.KindUnreadable && j.Events[4].Facts.Size == 10, "after an unreadable file: %+v", j.Events[4])
		}
		// A turn an API error ended keeps Claude's error line as its reply.
		failed := Hook("feat-42", map[string]any{"hook_event_name": "StopFailure", "prompt_id": "p1", "error": "authentication_failed", "last_assistant_message": "Login expired · Please run /login"})
		h.Leave(root, "a1", "000000006.1", failed, at)
		must(t, h.Store.Ingest(root, "a1"))
		{
			j := read(t, h.Store, "a1")
			expect(t, j.Reply.Text == "Login expired · Please run /login" && j.Reply.Number == j.Events[5].Number, "after an API error: reply %d %q", j.Reply.Number, j.Reply.Text)
		}
	})

	t.Run("a delivery", func(t *testing.T) {
		h := newHarness(t)
		root := h.Root()
		h.Leave(root, "a1", "000000001.1", []byte("delivery stop\n0001-ab.sup\n0002-cd\n"), at)
		must(t, h.Store.Ingest(root, "a1"))
		e := read(t, h.Store, "a1").Events[0]
		expect(t, e.Kind == state.KindDelivery && e.Facts.Hook == state.CaseStop && strings.Join(e.Facts.Messages, " ") == "0001-ab.sup 0002-cd", "%+v", e)
	})

	t.Run("hq's own events", func(t *testing.T) {
		h := newHarness(t)
		root := h.Root()
		h.Leave(root, "a1", "000000001.1", prompt, at)
		must(t, h.Store.Ingest(root, "a1"))
		out, err := h.Store.Append("a1", state.Event{Source: state.SourceScreen, Kind: "rest", Time: at}, state.Event{Source: state.SourceHQ, Kind: "typed", Time: at})
		require(t, err == nil && len(out) == 2, "%v %+v", err, out)
		j := read(t, h.Store, "a1")
		expect(t, len(j.Events) == 3 && j.Events[1].Number == out[0].Number && j.Events[1].Kind == "rest" && j.Events[2].Number == j.Events[0].Number+2 && out[1].Source == state.SourceHQ, "%+v", j.Events)
		{
			none, err := h.Store.Append("a1")
			expect(t, err == nil && len(none) == 0, "nothing to append: %v %v", none, err)
		}
	})

	t.Run("evidence set aside", func(t *testing.T) {
		h := newHarness(t)
		root := h.Root()
		h.Leave(root, "a1", "000000001.1", prompt, at)
		must(t, h.Store.Ingest(root, "a1"))
		basis := read(t, h.Store, "a1").Events[0].Number
		rest := state.Event{Source: state.SourceScreen, Kind: state.KindAtRest, Time: at, Basis: basis, Facts: state.Facts{Screen: "f1"}}
		other := rest
		other.Facts.Screen = "f2"
		typed := state.Event{Source: state.SourceHQ, Kind: state.KindDelivery, Time: at, Basis: basis, Facts: state.Facts{Messages: []string{"0001-ab"}}}
		out, err := h.Store.Append("a1", rest, rest, other, rest, typed, typed)
		require(t, err == nil && len(out) == 6, "%v %+v", err, out)
		{
			got := asides(out)
			// The screen that came back after another is new evidence; what
			// hq does is never set aside.
			expect(t, got == "- duplicate - - - -", "appended at once: %s", got)
		}
		_, err = h.Store.Append("a1", rest)
		must(t, err)
		h.Leave(root, "a1", "000000002.1", tool, at)
		must(t, h.Store.Ingest(root, "a1"))
		late := rest
		late.Facts.Screen = "f3"
		gone := state.Event{Source: state.SourceSbx, Kind: state.KindNotRunning, Time: at, Basis: basis}
		died := state.Event{Source: state.SourceTmux, Kind: state.KindPaneDied, Time: at, Basis: basis}
		out, err = h.Store.Append("a1", late, gone, died, typed)
		must(t, err)
		{
			got := asides(out)
			// Read against the reports, the screen's and sbx's evidence is
			// overtaken; the pane's death and what hq does are not.
			expect(t, got == "overtaken overtaken - -", "after a later report: %s", got)
		}
		j := read(t, h.Store, "a1")
		{
			got := asides(j.Events[1:])
			expect(t, len(j.Events) == 13 && got == "- duplicate - - - - duplicate - overtaken overtaken - -", "the journal keeps what it set aside, marked: %s", got)
		}
		// A relaunch starts the evidence over.
		_, err = h.Store.Append("a1", state.Event{Source: state.SourceHQ, Kind: state.KindRelaunched, Time: at}, died)
		must(t, err)
		{
			j := read(t, h.Store, "a1")
			expect(t, j.Events[len(j.Events)-1].Aside == "", "the pane's death after a relaunch: %+v", j.Events[len(j.Events)-1])
		}
	})

	t.Run("evidence for a journal that is not there", func(t *testing.T) {
		h := newHarness(t)
		root := h.Root()
		asked := 0
		gone := func() bool { asked++; return false }
		h.Leave(root, "a1", "000000001.1", prompt, at)
		must(t, h.Store.Ingest(root, "a1"))
		died := state.Event{Source: state.SourceTmux, Kind: state.KindPaneDied, Time: at, Basis: read(t, h.Store, "a1").Events[0].Number}
		// To a journal that is there, evidence is appended as Append
		// appends it, set aside alike, and the window is not asked about.
		out, err := h.Store.AppendEvidence("a1", gone, died, died)
		require(t, err == nil && asides(out) == "- duplicate" && asked == 0, "to a journal there: %v %+v, asked %d", err, out, asked)
		expect(t, len(read(t, h.Store, "a1").Events) == 3, "appended: %+v", read(t, h.Store, "a1").Events)
		// To one removed, or never begun, nothing is written while the
		// agent's home window is gone.
		must(t, h.Store.Remove(root, "a1"))
		for _, id := range []string{"a1", "c3"} {
			asked = 0
			out, err := h.Store.AppendEvidence(id, gone, died)
			expect(t, errors.Is(err, journal.ErrGone) && len(out) == 0 && asked == 1, "%s, its window gone: %v %+v, asked %d", id, err, out, asked)
			expect(t, len(read(t, h.Store, id).Events) == 0, "%s written: %+v", id, read(t, h.Store, id).Events)
		}
		{
			ids, err := h.Store.IDs()
			expect(t, err == nil && len(ids) == 0, "journals after evidence of gone windows: %v %v", ids, err)
		}
		// While the window stands, the journal is begun with the evidence.
		out, err = h.Store.AppendEvidence("c3", func() bool { return true }, died)
		require(t, err == nil && len(out) == 1 && out[0].Number > 0, "its window standing: %v %+v", err, out)
		expect(t, Kinds(read(t, h.Store, "c3").Events) == state.KindPaneDied, "begun: %+v", read(t, h.Store, "c3").Events)
		// Nothing to append asks nothing.
		asked = 0
		none, err := h.Store.AppendEvidence("d4", gone)
		expect(t, err == nil && len(none) == 0 && asked == 0, "nothing to append: %v %v, asked %d", none, err, asked)
		// The window is asked about under the lock, with the append: a kill
		// that kills the window just after it was asked removes the journal
		// only after the append, and nothing of the agent is left.
		removed := make(chan error)
		_, err = h.Store.AppendEvidence("e5", func() bool {
			go func() { removed <- h.Store.Remove(root, "e5") }()
			return true
		}, died)
		must(t, err)
		select {
		case err := <-removed:
			must(t, err)
		case <-time.After(10 * time.Second):
			t.Fatal("the removal did not end within 10 s")
		}
		expect(t, len(read(t, h.Store, "e5").Events) == 0, "left after the kill: %+v", read(t, h.Store, "e5").Events)
		_, err = h.Store.AppendEvidence("../e5", func() bool { return true }, died)
		expect(t, err != nil, "an id hq did not issue taken")
	})

	t.Run("eight writers racing for one first append", func(t *testing.T) {
		h := newHarness(t)
		root := h.Root()
		h.Leave(root, "a1", "000000001.1", prompt, at)
		must(t, h.Store.Ingest(root, "a1"))
		ended := state.Event{Source: state.SourceScreen, Kind: state.KindTurnEnded, Time: at, Basis: read(t, h.Store, "a1").Events[0].Number}
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				_, err := h.Store.Append("a1", ended)
				expect(t, err == nil, "append: %v", err)
			})
		}
		wg.Wait()
		j := read(t, h.Store, "a1")
		got := asides(j.Events[1:])
		expect(t, got == "- duplicate duplicate duplicate duplicate duplicate duplicate duplicate", "%s", got)
	})

	t.Run("read on from a position", func(t *testing.T) {
		h := newHarness(t)
		root := h.Root()
		h.Leave(root, "a1", "000000001.1", prompt, at)
		must(t, h.Store.Ingest(root, "a1"))
		first := read(t, h.Store, "a1")
		require(t, first.From == 0 && len(first.Events) == 1, "first read: %+v", first)
		{
			again, err := h.Store.ReadFrom("a1", first.Next)
			must(t, err)
			expect(t, len(again.Events) == 0 && again.Next == first.Next && again.From == first.Next, "nothing new: %+v", again)
		}
		h.Leave(root, "a1", "000000002.1", tool, at)
		h.Leave(root, "a1", "000000003.1", stop("Done?"), at)
		must(t, h.Store.Ingest(root, "a1"))
		_, err := h.Store.Append("a1", state.Event{Source: state.SourceHQ, Kind: state.KindAnswer, Time: at})
		must(t, err)
		on, err := h.Store.ReadFrom("a1", first.Next)
		must(t, err)
		{
			got := Kinds(on.Events)
			expect(t, got == "PostToolUse Stop Answer" && on.From == first.Next && on.Reply.Text == "Done?" && on.Reply.Number == on.Events[1].Number, "the events since: %s, reply %+v", got, on.Reply)
		}
		{
			// The reply is given with its turn end only.
			after, err := h.Store.ReadFrom("a1", on.Next)
			must(t, err)
			expect(t, len(after.Events) == 0 && after.Reply.Number == 0, "after the turn end: %+v", after)
		}
		// A removed journal is read from the start again.
		must(t, h.Store.Remove(root, "a1"))
		h.Leave(root, "a1", "000000004.1", prompt, at)
		must(t, h.Store.Ingest(root, "a1"))
		{
			fresh, err := h.Store.ReadFrom("a1", on.Next)
			must(t, err)
			expect(t, fresh.From == 0 && len(fresh.Events) == 1 && fresh.Events[0].Kind == "UserPromptSubmit", "after the removal: %+v", fresh)
		}
	})

	t.Run("the ids are those of the agents with a journal", func(t *testing.T) {
		h := newHarness(t)
		root := h.Root()
		ids, err := h.Store.IDs()
		must(t, err)
		expect(t, len(ids) == 0, "ids before any journal: %v", ids)
		h.Leave(root, "b2", "000000001.1", prompt, at)
		must(t, h.Store.Ingest(root, "b2"))
		_, err = h.Store.Append("a1", state.Event{Source: state.SourceHQ, Kind: state.KindLaunched, Time: at})
		must(t, err)
		ids, err = h.Store.IDs()
		must(t, err)
		expect(t, strings.Join(ids, " ") == "a1 b2", "ids %v", ids)
		must(t, h.Store.Remove(root, "b2"))
		ids, err = h.Store.IDs()
		must(t, err)
		expect(t, strings.Join(ids, " ") == "a1", "ids after a removal %v", ids)
	})

	t.Run("the latest dialog keeps what it asks", func(t *testing.T) {
		h := newHarness(t)
		root := h.Root()
		dialog := Hook("feat-42", map[string]any{"hook_event_name": "PermissionRequest", "tool_name": "Bash", "tool_input": map[string]any{"command": "rm -rf build", "description": "Clean"}})
		h.Leave(root, "a1", "000000001.1", dialog, at)
		h.Leave(root, "a1", "000000002.1", tool, at)
		must(t, h.Store.Ingest(root, "a1"))
		j := read(t, h.Store, "a1")
		require(t, j.Ask.Number == j.Events[0].Number && j.Ask.Ask != nil && j.Ask.Ask.Command == "rm -rf build" && j.Ask.Ask.Tool == "Bash", "ask %+v", j.Ask)
		data, _ := json.Marshal(j.Events)
		expect(t, !strings.Contains(string(data), "rm -rf"), "an event keeps the command: %s", data)
		h.Leave(root, "a1", "000000003.1", Hook("feat-42", map[string]any{"hook_event_name": "Notification", "message": "Claude needs your permission"}), at)
		must(t, h.Store.Ingest(root, "a1"))
		{
			j := read(t, h.Store, "a1")
			expect(t, j.Ask.Number == j.Events[2].Number && j.Ask.Ask != nil && j.Ask.Ask.Message == "Claude needs your permission", "the next dialog: %+v", j.Ask)
		}
	})

	t.Run("a question dialog keeps its report and then the answers Claude got", func(t *testing.T) {
		h := newHarness(t)
		root := h.Root()
		ask := Hook("feat-42", map[string]any{"hook_event_name": "PermissionRequest", "prompt_id": "p1", "tool_name": state.AskTool, "tool_input": map[string]any{"questions": []any{map[string]any{"question": "Red or blue?", "options": []any{map[string]any{"label": "Red"}, map[string]any{"label": "Blue"}}}}}})
		answered := func(answers map[string]any) []byte {
			return Hook("feat-42", map[string]any{"hook_event_name": "PostToolUse", "prompt_id": "p1", "tool_name": state.AskTool, "tool_input": map[string]any{"answers": answers}})
		}
		h.Leave(root, "a1", "000000001.1", ask, at)
		must(t, h.Store.Ingest(root, "a1"))
		j := read(t, h.Store, "a1")
		require(t, j.Ask.Report == string(ask) && !j.Ask.Answered && j.Ask.Answers == nil, "the dialog: %+v", j.Ask)
		// Its tool's end in a later ingest, after a tool of another kind.
		h.Leave(root, "a1", "000000002.1", tool, at)
		h.Leave(root, "a1", "000000003.1", answered(map[string]any{"Red or blue?": "Blue"}), at)
		must(t, h.Store.Ingest(root, "a1"))
		{
			j := read(t, h.Store, "a1")
			expect(t, j.Ask.Number == j.Events[0].Number && j.Ask.Report == string(ask) && j.Ask.Answered && j.Ask.Answers["Red or blue?"] == "Blue", "answered: %+v", j.Ask)
		}
		// A dialog and its end in one ingest, the end with no answers; a
		// permission prompt keeps no report, and its tool's end no answers.
		h.Leave(root, "a1", "000000004.1", ask, at)
		h.Leave(root, "a1", "000000005.1", answered(nil), at)
		must(t, h.Store.Ingest(root, "a1"))
		{
			j := read(t, h.Store, "a1")
			expect(t, j.Ask.Number == j.Events[3].Number && j.Ask.Answered && len(j.Ask.Answers) == 0, "answered at once: %+v", j.Ask)
		}
		h.Leave(root, "a1", "000000006.1", Hook("feat-42", map[string]any{"hook_event_name": "PermissionRequest", "tool_name": "Bash", "tool_input": map[string]any{"command": "make"}}), at)
		must(t, h.Store.Ingest(root, "a1"))
		h.Leave(root, "a1", "000000007.1", answered(map[string]any{"Red or blue?": "Red"}), at)
		must(t, h.Store.Ingest(root, "a1"))
		{
			j := read(t, h.Store, "a1")
			expect(t, j.Ask.Number == j.Events[5].Number && j.Ask.Report == "" && !j.Ask.Answered && j.Ask.Answers == nil, "a permission prompt: %+v", j.Ask)
		}
	})

	t.Run("remove", func(t *testing.T) {
		h := newHarness(t)
		root := h.Root()
		for _, id := range []string{"a1", "b2"} {
			h.Leave(root, id, "000000001.1", stop("bye"), at)
			must(t, h.Store.Ingest(root, id))
		}
		h.Leave(root, "a1", "000000002.1", prompt, at)
		must(t, h.Store.Remove(root, "a1"))
		{
			j := read(t, h.Store, "a1")
			expect(t, len(j.Events) == 0 && j.Reply.Number == 0 && len(h.Left(root, "a1")) == 0, "left after the removal: %+v %v", j, h.Left(root, "a1"))
		}
		{
			j := read(t, h.Store, "b2")
			expect(t, len(j.Events) == 1 && j.Reply.Text == "bye", "another agent's journal: %+v", j)
		}
		must(t, h.Store.Remove(root, "c3")) // nothing to remove
	})

	t.Run("ids hq did not issue", func(t *testing.T) {
		h := newHarness(t)
		root := h.Root()
		for _, id := range []string{"", "../a1", "A1", "a1/b"} {
			_, aerr := h.Store.Append(id, state.Event{Kind: "x"})
			_, rerr := h.Store.Read(id)
			expect(t, h.Store.Ingest(root, id) != nil && aerr != nil && rerr != nil && h.Store.Remove(root, id) != nil, "%q taken", id)
		}
	})
}

// asides are the events' reasons to be set aside, "-" for one that stands,
// joined by spaces.
func asides(events []state.Event) string {
	var out []string
	for _, e := range events {
		out = append(out, cmp.Or(e.Aside, "-"))
	}
	return strings.Join(out, " ")
}

// numberedInOrder says every event of events has a number, each above the
// one before it, as a journal reads them.
func numberedInOrder(events []state.Event) bool {
	for i, e := range events {
		if e.Number <= 0 || i > 0 && e.Number <= events[i-1].Number {
			return false
		}
	}
	return true
}

// expect fails the test, going on, unless ok.
func expect(t *testing.T, ok bool, format string, args ...any) {
	t.Helper()
	if !ok {
		t.Errorf(format, args...)
	}
}

// require fails the test, stopping it, unless ok.
func require(t *testing.T, ok bool, format string, args ...any) {
	t.Helper()
	if !ok {
		t.Fatalf(format, args...)
	}
}

func read(t *testing.T, s journal.Store, id string) journal.Journal {
	t.Helper()
	j, err := s.Read(id)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
