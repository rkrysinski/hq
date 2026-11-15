package journaltest

import (
	"encoding/json"
	"fmt"
	"strings"
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
// turn end's reply kept once, hq's own events appended, a removed journal
// gone, and ids hq did not issue refused.
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
			require(t, got == "PostToolUse UserPromptSubmit" && j.Events[1].Number > j.Events[0].Number, "%s: %+v", got, j.Events)
		}
	})

	t.Run("one counter for every agent", func(t *testing.T) {
		h := newHarness(t)
		one, two := h.Root(), h.Root()
		for i := range 3 {
			name := fmt.Sprintf("00000000%d.1", i+1)
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
			for i, e := range j.Events {
				expect(t, !seen[e.Number] && e.Number > 0 && !(i > 0 && e.Number <= j.Events[i-1].Number), "%s: number %d (%+v)", id, e.Number, j.Events)
				seen[e.Number] = true
			}
		}
		expect(t, len(seen) == 10 && seen[through], "numbers %v, through %d", seen, through)
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
	})

	t.Run("a delivery", func(t *testing.T) {
		h := newHarness(t)
		root := h.Root()
		h.Leave(root, "a1", "000000001.1", []byte("delivery stop\n0001-ab.sup\n0002-cd\n"), at)
		must(t, h.Store.Ingest(root, "a1"))
		e := read(t, h.Store, "a1").Events[0]
		expect(t, e.Kind == state.KindDelivery && e.Facts.At == "stop" && strings.Join(e.Facts.Messages, " ") == "0001-ab.sup 0002-cd", "%+v", e)
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
