package cli

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
)

// cursorNumber is the number a cursor hq printed stands for.
func cursorNumber(t *testing.T, c string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(strings.TrimPrefix(c, cursorLetter), 10, 64)
	if err != nil || !strings.HasPrefix(c, cursorLetter) {
		t.Errorf("cursor %q", c)
	}
	return n
}

func TestParseSinceTellsACursorFromAMomentAndADuration(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	for in, want := range map[string]since{
		"c0":                     {isCursor: true},
		"c1234":                  {cursor: 1234, isCursor: true},
		"2026-11-16T09:59:30.5Z": {moment: now.Add(-29500 * time.Millisecond)},
		"10m":                    {moment: now.Add(-10 * time.Minute)},
		"0":                      {moment: now},
	} {
		got, err := parseSince(in, now)
		if err != nil || got.isCursor != want.isCursor || got.cursor != want.cursor || !got.moment.Equal(want.moment) {
			t.Errorf("%q: %+v %v", in, got, err)
		}
	}
	for _, in := range []string{"", "c", "c-1", "c+1", "c1.5", "c99999999999999999999", "1234", "-5m", "yesterday"} {
		if _, err := parseSince(in, now); err == nil || !strings.Contains(err.Error(), "neither a next_since as hq printed it (c1234)") {
			t.Errorf("%q: %v", in, err)
		}
	}
	// What hq prints reads back as the same cursor, never as a moment or a
	// duration.
	if got, err := parseSince(cursorText(42), now); err != nil || !got.isCursor || got.cursor != 42 {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	if _, err := time.ParseDuration(cursorText(42)); err == nil {
		t.Fatal("a cursor reads as a duration")
	}
}

// Every row hq prints as JSON carries the agent's two numbers (ADR 0019):
// the change that put it in its state and the one that ended its last
// completed turn. An outdated agent has none, and no hq wait returns it.
func TestRowsCarryTheTwoNumbersAndAnOutdatedOneNone(t *testing.T) {
	f := newWaitFakes()
	f.tmux.windows[2].Options[agent.GenerationOption] = "2"
	f.states["id-a"] = state.Report{State: state.Done, Since: f.now.Add(-time.Second), Last: "Done: hello"}
	var ls struct {
		Agents []map[string]any `json:"agents"`
	}
	if err := json.Unmarshal([]byte(mustRun(t, f, "ls", "--json")), &ls); err != nil || len(ls.Agents) != 2 {
		t.Fatalf("ls: %v %+v", err, ls)
	}
	done := float64(f.cursorOf(t, "id-a"))
	for _, r := range ls.Agents {
		want := map[string]any{"a": done, "b": nil}[r["name"].(string)]
		if r["state_seq"] != want || r["completion_seq"] != want {
			t.Errorf("ls %s: state_seq %v completion_seq %v, want %v", r["name"], r["state_seq"], r["completion_seq"], want)
		}
	}
	var read map[string]any
	if err := json.Unmarshal([]byte(mustRun(t, f, "read", "a", "--json")), &read); err != nil || read["state_seq"] != done || read["completion_seq"] != done {
		t.Fatalf("read: %v %v %v", err, read["state_seq"], read["completion_seq"])
	}

	// a works again: its state moves, its completed turn stays; then it is
	// done again, and only a is returned, never the outdated b.
	f.states["id-a"] = state.Report{State: state.Working, Since: f.now, Last: "on it"}
	mustRun(t, f, "ls", "--json")
	working := f.cursorOf(t, "id-a")
	f.after(time.Second, func() {
		f.states["id-a"] = state.Report{State: state.Done, Since: f.now, Last: "Done again"}
		f.tmux.windows[2].PaneDead = true
	})
	w := waitJSON(t, mustRun(t, f, "wait", "--since", cursorText(working), "--json"))
	if len(w.Agents) != 1 || w.Agents[0].Name != "a" || w.Agents[0].StateSeq == nil || *w.Agents[0].StateSeq != f.cursorOf(t, "id-a") ||
		*w.Agents[0].CompletionSeq != *w.Agents[0].StateSeq || *w.Agents[0].StateSeq <= working {
		t.Fatalf("wait: %+v", w.Agents)
	}
}

// A cursor orders changes by their numbers, not by any clock: with the
// clock moved ten minutes back and then forward between calls, each
// change comes once, where a moment would lose the one dated back.
func TestWaitWithACursorMissesNothingWhenTheClockMovesBackAndForth(t *testing.T) {
	f := newWaitFakes()
	first := waitJSON(t, mustRun(t, f, "wait", "--timeout", "1s", "--json"))

	f.now = f.now.Add(-10 * time.Minute)
	f.states["id-a"] = state.Report{State: state.Done, Since: f.now, Last: "a done"}
	second := waitJSON(t, mustRun(t, f, "wait", "--since", first.NextSince, "--json"))
	if len(second.Agents) != 1 || second.Agents[0].Name != "a" {
		t.Fatalf("back: %+v", second.Agents)
	}

	f.now = f.now.Add(20 * time.Minute)
	f.states["id-b"] = state.Report{State: state.Question, Since: f.now, Last: "b asks?"}
	third := waitJSON(t, mustRun(t, f, "wait", "--since", second.NextSince, "--json"))
	if len(third.Agents) != 1 || third.Agents[0].Name != "b" {
		t.Fatalf("forward: %+v", third.Agents)
	}
	if fourth := waitJSON(t, mustRun(t, f, "wait", "--since", third.NextSince, "--timeout", "2s", "--json")); len(fourth.Agents) != 0 {
		t.Fatalf("again: %+v", fourth.Agents)
	}
}

// A cursor hq did not give, above the counter, as after a lost state
// directory, is refused with where to take a new one (#166, decision 11).
func TestWaitRefusesACursorAheadOfTheCounter(t *testing.T) {
	f := newWaitFakes()
	code, out, errOut := f.run("wait", "--since", "c999", "--timeout", "1s")
	if code != ExitUsage || out != "" || errOut != "hq: --since 'c999' is ahead of every change hq has numbered on this host (was its state directory lost?): take a new one from hq ls --json\n" {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	// The counter itself is a cursor hq may have given.
	n, _ := f.journal.Through()
	if code, _, errOut := f.run("wait", "--since", cursorText(n), "--timeout", "1s"); code != 0 {
		t.Fatalf("the counter: exit %d %q", code, errOut)
	}
}

// An agent whose journal cannot be read and whose pane is dead is ended
// with no event behind it: the wait that saw it before returns it, as it
// returns a killed one (#166, decision 3A); a later call does not.
func TestWaitReturnsAnEndedAgentWhoseJournalCannotBeReadAsAKilledOne(t *testing.T) {
	f := newWaitFakes()
	f.unreadable = map[string]bool{}
	f.after(time.Second, func() {
		f.unreadable["id-b"] = true
		f.tmux.windows[2].PaneDead = true
	})
	w := waitJSON(t, mustRun(t, f, "wait", "--json"))
	if len(w.Agents) != 1 || w.Agents[0].Name != "b" || w.Agents[0].State != state.Ended || w.Agents[0].StateSeq != nil || w.Agents[0].CompletionSeq != nil {
		t.Fatalf("first: %+v", w.Agents)
	}
	if again := waitJSON(t, mustRun(t, f, "wait", "--since", w.NextSince, "--timeout", "2s", "--json")); len(again.Agents) != 0 {
		t.Fatalf("again: %+v", again.Agents)
	}
	// A killed agent's row has no numbers either.
	f = newWaitFakes()
	f.after(time.Second, func() { f.tmux.windows = f.tmux.windows[:2] })
	if w := waitJSON(t, mustRun(t, f, "wait", "b", "--json")); len(w.Agents) != 1 || w.Agents[0].State != state.Ended || w.Agents[0].StateSeq != nil {
		t.Fatalf("killed: %+v", w.Agents)
	}
}

// A look vouches only for what it could see: an agent whose window
// appeared while it looked had changes it did not read.
func TestAnAgentAppearedWhenItsWindowDid(t *testing.T) {
	a := tmux.Window{Options: map[string]string{"id": "a1"}}
	b := tmux.Window{Options: map[string]string{"id": "b2"}}
	dash := tmux.Window{Options: map[string]string{}}
	for _, tc := range []struct {
		before, after []tmux.Window
		want          bool
	}{
		{[]tmux.Window{a}, []tmux.Window{a}, false},
		{[]tmux.Window{a, b}, []tmux.Window{a}, false},
		{[]tmux.Window{a}, []tmux.Window{dash, a}, false},
		{[]tmux.Window{a}, []tmux.Window{a, b}, true},
		{nil, []tmux.Window{b}, true},
	} {
		if got := agentAppeared(tc.before, tc.after); got != tc.want {
			t.Errorf("%v -> %v: %v", tc.before, tc.after, got)
		}
	}
}

// While a look reads b's journal, another process numbers a turn of a's
// that the look, having read a already, does not see. The look's cursor
// stands for no more than it saw (the lowest counter of its reads, not the
// highest number), so the next hq wait returns that turn (ADR 0019).
func TestALooksCursorLeavesWhatAnotherProcessNumberedMeanwhileToTheNextWait(t *testing.T) {
	f := newWaitFakes()
	turns := 0
	f.onRead = func(id string) {
		if id != "id-b" {
			return
		}
		turns++
		a := journalID("id-a")
		f.journal.Report("/w/app", a, state.Report{State: state.Working, Since: f.now, Prompt: "go on"}, state.Detail{})
		f.journal.Report("/w/app", a, state.Report{State: state.Done, Since: f.now, Last: fmt.Sprintf("turn %d", turns)}, state.Detail{})
		_ = f.journal.Ingest("/w/app", a)
	}
	var ls waitOutput
	if err := json.Unmarshal([]byte(mustRun(t, f, "ls", "--json")), &ls); err != nil {
		t.Fatal(err)
	}
	f.onRead = nil
	last := f.cursorOf(t, "id-a")
	if cursorNumber(t, ls.NextSince) >= last {
		t.Fatalf("the list's cursor %s stands for a's turn %d, which it did not see: %+v", ls.NextSince, last, ls.Agents)
	}
	w := waitJSON(t, mustRun(t, f, "wait", "--since", ls.NextSince, "--json"))
	if len(w.Agents) != 1 || w.Agents[0].Name != "a" || *w.Agents[0].StateSeq != last || w.Agents[0].Last != fmt.Sprintf("turn %d", turns) {
		t.Fatalf("wait: %+v (a's last turn %d)", w.Agents, last)
	}
}

// Without --since a call starts from its first look: an agent that look
// shows in such a state is not returned, though the look itself appended
// the event that put it there, numbered above the counter of its read of
// an agent it read before (here a dialog of Claude's own on b's screen).
func TestWaitWithoutSinceNeverReturnsWhatItsFirstLookShowed(t *testing.T) {
	f := newWaitFakes()
	f.tmux.windows[1].Pane, f.tmux.windows[2].Pane = "%1", "%2"
	f.states["id-a"] = state.Report{State: state.Done, Since: f.now.Add(-time.Hour), Last: "hi"}
	f.states["id-b"] = state.Report{State: state.Done, Since: f.now.Add(-time.Hour), Last: "hi"}
	rule := strings.Repeat("─", 40)
	f.tmux.screens = map[string]string{
		"%1": "● hi\n" + rule + "\n❯ \n" + rule + "\n  footer\n",
		"%2": "● hi\n" + strings.Repeat("▔", 40) + "\n   Change effort level?\n\n   ❯ 1. Yes, switch to max\n     2. No, go back\n",
	}
	code, out, _ := f.run("wait", "--timeout", "2s", "--json")
	w := waitJSON(t, out)
	if code != 0 || len(w.Agents) != 0 {
		t.Fatalf("exit %d: %+v", code, w.Agents)
	}
	// Nor by a call from its cursor: it is no change after it.
	if again := waitJSON(t, mustRun(t, f, "wait", "--since", w.NextSince, "--timeout", "2s", "--json")); len(again.Agents) != 0 {
		t.Fatalf("again: %+v", again.Agents)
	}
}

// hq send's cursor is the counter as it leaves the message, not that of
// the look before it: a change another process numbers in between is
// before the message, and a wait from the cursor does not take it for the
// agent's answer.
func TestSendsCursorIsTheCounterAsItLeavesTheMessage(t *testing.T) {
	f := sendFakes(state.Done)
	b := journalID("id-b")
	f.onThrough = func() {
		_, _ = f.journal.Append(b, state.Event{Source: state.SourceHQ, Kind: state.KindLaunched, Time: f.now})
	}
	var sent sendOutput
	if err := json.Unmarshal([]byte(mustRun(t, f, "send", "a", "No", "--json")), &sent); err != nil {
		t.Fatal(err)
	}
	if want := f.cursorBefore(t, "id-a", state.KindDelivery); sent.NextSince != want {
		t.Fatalf("next_since %s, want %s, the counter just before the delivery", sent.NextSince, want)
	}
}

// dialogOnB has two agents done at their prompts, b's screen showing a
// dialog of Claude's own, which a look appends for b after it has read a:
// the look shows b at a change numbered above its cursor (the lowest
// counter of its reads). With alternate, b's dialog changes at every look,
// so every look appends anew. looks counts the looks (each reads the
// counter once, before it lists the windows).
func dialogOnB(alternate bool, looks *int) *fakes {
	f := newWaitFakes()
	f.tmux.windows[1].Pane, f.tmux.windows[2].Pane = "%1", "%2"
	f.states["id-a"] = state.Report{State: state.Done, Since: f.now.Add(-time.Hour), Last: "hi"}
	f.states["id-b"] = state.Report{State: state.Done, Since: f.now.Add(-time.Hour), Last: "hi"}
	rule := strings.Repeat("─", 40)
	dialog := func(n int) string {
		return "● hi\n" + strings.Repeat("▔", 40) + fmt.Sprintf("\n   Change effort level %d?\n\n   ❯ 1. Yes, switch to max\n     2. No, go back\n", n)
	}
	f.tmux.screens = map[string]string{"%1": "● hi\n" + rule + "\n❯ \n" + rule + "\n  footer\n", "%2": dialog(0)}
	f.onThrough = func() {
		*looks++
		if alternate {
			f.tmux.screens["%2"] = dialog(*looks)
		}
	}
	return f
}

// hq ls --json shows no row its cursor does not stand for: a look that
// shows b at a change numbered above its cursor is followed by another,
// whose cursor covers it, so a wait from the list's next_since does not
// return the row the list already showed (#166).
func TestTheListsCursorStandsForEveryRowItShows(t *testing.T) {
	looks := 0
	f := dialogOnB(false, &looks)
	var ls waitOutput
	if err := json.Unmarshal([]byte(mustRun(t, f, "ls", "--json")), &ls); err != nil {
		t.Fatal(err)
	}
	f.onThrough = nil
	if looks != 2 {
		t.Fatalf("%d looks, want a second one", looks)
	}
	for _, r := range ls.Agents {
		if r.StateSeq == nil || *r.StateSeq > cursorNumber(t, ls.NextSince) {
			t.Fatalf("row %s at %v, the cursor %s", r.Name, r.StateSeq, ls.NextSince)
		}
	}
	if w := waitJSON(t, mustRun(t, f, "wait", "--since", ls.NextSince, "--timeout", "1s", "--json")); len(w.Agents) != 0 {
		t.Fatalf("wait returned what the list showed: %+v", w.Agents)
	}
	// The plain table has no cursor, and looks once.
	looks = 0
	f = dialogOnB(false, &looks)
	mustRun(t, f, "ls")
	if looks != 1 {
		t.Fatalf("the table took %d looks", looks)
	}
}

// Agents that change at every look still get a list: hq ls --json looks
// coverLooks times at most, then prints the last look.
func TestTheListLooksAgainOnlyAFewTimes(t *testing.T) {
	looks := 0
	f := dialogOnB(true, &looks)
	var ls waitOutput
	if err := json.Unmarshal([]byte(mustRun(t, f, "ls", "--json")), &ls); err != nil {
		t.Fatal(err)
	}
	if looks != coverLooks || len(ls.Agents) != 2 {
		t.Fatalf("%d looks, want %d; rows %+v", looks, coverLooks, ls.Agents)
	}
}
