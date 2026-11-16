package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/state"
)

// workingScreen is Claude at work: its spinner above the box, which no
// screen rule takes for a screen at rest.
var workingScreen = "✻ Working…\n" + strings.Repeat("─", 30) + "\n❯ \n" + strings.Repeat("─", 30) + "\n  ⏵⏵ bypass permissions on · esc to interrupt\n"

// onSubmit has the fakes' Claude do then as hq presses Enter on what it
// typed in, after the hook of the prompt has taken hq's word (newFakes).
func onSubmit(f *fakes, then func()) {
	prev := f.tmux.onSubmit
	f.tmux.onSubmit = func() { prev(); then() }
}

// turnFor has agent a take a turn from now: working, with Claude's spinner
// on its screen, until sleeps more sleeps have passed, then done with
// reply, back at its empty box.
func turnFor(f *fakes, sleeps int, reply string) {
	f.states["id-a"] = state.Report{State: state.Working, Since: f.now, Prompt: "p"}
	f.tmux.screens["%4"] = workingScreen
	n := 0
	f.onSleep = func() {
		if n++; n == sleeps {
			f.states["id-a"] = state.Report{State: state.Done, Since: f.now, Last: reply}
			f.tmux.screens["%4"] = emptyBox
		}
	}
}

// sendWaitJSON runs hq send --wait --json with args, failing unless it
// exits 0; what it printed.
func sendWaitJSON(t *testing.T, f *fakes, args ...string) sendWaitOutput {
	t.Helper()
	out := mustRun(t, f, append([]string{"send", "--wait", "--json"}, args...)...)
	var got sendWaitOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %q", err, out)
	}
	return got
}

// hookTakes leaves in agent a's journal, as its hook would, the delivery of
// the message text at the hook's case hook, and takes it out of the inbox.
func hookTakes(f *fakes, text, hook string) {
	f.seen("/w/app", "id-a", state.SourceHook, state.KindDelivery, state.Facts{Messages: []string{fakeName(text)}, Hook: hook}, f.now)
	delete(f.inbox, "id-a")
}

func TestSendWaitReturnsWhenTheTurnItsMessageStartedEnds(t *testing.T) {
	f := sendFakes(state.Done)
	onSubmit(f, func() { turnFor(f, 6, "Done: the diff") })
	code, out, errOut := f.run("send", "--wait", "a", "summarise the diff")
	if code != 0 || !strings.HasPrefix(out, "delivered: typed into a as its next prompt\n") || !strings.Contains(out, "Done: the diff") || !strings.Contains(out, "next: hq wait a --since c") {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}

	// --json: the delivery, the row as hq wait gives it, and a cursor from
	// which hq wait does not return that change again.
	f = sendFakes(state.Done)
	onSubmit(f, func() { turnFor(f, 6, "Done: again") })
	got := sendWaitJSON(t, f, "a", "summarise it again")
	if got.Delivery != "delivered: typed into a as its next prompt" || got.Outcome != endTurn || got.Message != "" || len(got.Agents) != 1 || got.Agents[0].State != state.Done || got.Agents[0].Last != "Done: again" {
		t.Fatalf("%+v", got)
	}
	if out := mustRun(t, f, "wait", "a", "--since", got.NextSince, "--timeout", "1s"); !strings.HasPrefix(out, "nothing yet\n") {
		t.Fatalf("the change came back: %q", out)
	}
}

func TestSendWaitOnAWorkingAgentReturnsTheEndOfTheTurnThatTookIt(t *testing.T) {
	for _, tc := range []struct {
		now  bool
		hook string
	}{{false, state.CaseStop}, {true, state.CaseAnswer}} {
		f := sendFakes(state.Done)
		turnFor(f, 1000, "")
		args := []string{"a", "check the logs"}
		if tc.now {
			args = append(args, "--now")
		}
		// The hook takes it a few looks in; the turn goes on with it far
		// longer than the bound, which never runs while the agent works.
		n := 0
		f.onSleep = func() {
			switch n++; n {
			case 8:
				text := "check the logs"
				if tc.now {
					text += " (now)"
				}
				hookTakes(f, text, tc.hook)
			case 80:
				f.states["id-a"] = state.Report{State: state.Done, Since: f.now, Last: "Checked the logs"}
				f.tmux.screens["%4"] = emptyBox
			}
		}
		got := sendWaitJSON(t, f, args...)
		if got.Outcome != endTurn || len(got.Agents) != 1 || got.Agents[0].Last != "Checked the logs" || n < 80 {
			t.Fatalf("now %v: %+v after %d sleeps", tc.now, got, n)
		}
		if len(f.tmux.pasted) != 0 {
			t.Fatalf("now %v: typed into a working agent: %v", tc.now, f.tmux.pasted)
		}
	}
}

func TestSendWaitNeverReturnsATurnEndBeforeItsMessageWasTaken(t *testing.T) {
	// The turn the message was left in ends without taking it; the message
	// is typed in then, and the end of the turn it starts is returned.
	f := sendFakes(state.Done)
	turnFor(f, 4, "Done: the earlier turn")
	onSubmit(f, func() { turnFor(f, 6, "Done: the logs") })
	got := sendWaitJSON(t, f, "a", "check the logs")
	if got.Outcome != endTurn || got.Agents[0].Last != "Done: the logs" || strings.Join(f.tmux.pasted, "|") != "%4 check the logs" {
		t.Fatalf("%+v pasted %v", got, f.tmux.pasted)
	}
	if got.Delivery != "queued: a is working, delivered when it stops" {
		t.Fatalf("delivery %q", got.Delivery)
	}
}

func TestSendWaitReturnsAnAgentThatNeedsTheUserBeforeItTookTheMessage(t *testing.T) {
	// At a dialog already: at once, the message still waiting.
	f := sendFakes(state.NeedsInput)
	f.tmux.screens["%4"] = "Do you want to proceed?\n❯ 1. Yes\n  2. No\nEsc to cancel\n"
	code, out, errOut := f.run("send", "--wait", "a", "then commit")
	if code != 0 || !strings.Contains(out, "\nnot taken: a needs input before it took the message, which still waits for it\n") || len(f.inbox["id-a"]) != 1 {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	// A dialog that opens before it took the message.
	f = sendFakes(state.Done)
	turnFor(f, 1000, "")
	n := 0
	f.onSleep = func() {
		if n++; n == 5 {
			f.states["id-a"] = state.Report{State: state.NeedsInput, Since: f.now}
			f.tmux.screens["%4"] = "Do you want to proceed?\n❯ 1. Yes\n  2. No\nEsc to cancel\n"
		}
	}
	got := sendWaitJSON(t, f, "a", "then commit")
	if got.Outcome != endNotTaken || got.Message != msgWaits || got.Agents[0].State != state.NeedsInput || len(f.inbox["id-a"]) != 1 || n > 7 {
		t.Fatalf("%+v inbox %v after %d sleeps", got, f.inbox, n)
	}
}

func TestSendWaitReturnsAnAgentThatEndedBeforeItTookTheMessage(t *testing.T) {
	// Ended: the message is kept for its relaunch.
	f := sendFakes(state.Done)
	turnFor(f, 1000, "")
	n := 0
	f.onSleep = func() {
		if n++; n == 5 {
			f.states["id-a"] = state.Report{State: state.Ended, Since: f.now}
		}
	}
	code, out, _ := f.run("send", "--wait", "a", "then commit")
	if code != 0 || !strings.Contains(out, "\nnot taken: a has ended before it took the message, which is kept for its relaunch\n") {
		t.Fatalf("exit %d %q", code, out)
	}
	// Killed: the message went with it.
	f = sendFakes(state.Done)
	turnFor(f, 1000, "")
	n = 0
	f.onSleep = func() {
		if n++; n == 5 {
			f.tmux.windows = f.tmux.windows[:1]
			delete(f.inbox, "id-a")
		}
	}
	got := sendWaitJSON(t, f, "a", "then commit")
	if got.Outcome != endNotTaken || got.Message != msgGone || got.Agents[0].State != state.Ended {
		t.Fatalf("%+v", got)
	}
	// Killed after it took it: the end of the turn that took it.
	f = sendFakes(state.Done)
	turnFor(f, 1000, "")
	n = 0
	f.onSleep = func() {
		switch n++; n {
		case 3:
			hookTakes(f, "then commit", state.CaseStop)
		case 5:
			f.tmux.windows = f.tmux.windows[:1]
		}
	}
	if got := sendWaitJSON(t, f, "a", "then commit"); got.Outcome != endTurn || got.Message != "" || got.Agents[0].State != state.Ended {
		t.Fatalf("%+v", got)
	}
}

func TestSendWaitFailsAsStalledWhenTheBoxHoldsTheUsersTyping(t *testing.T) {
	f := sendFakes(state.Done)
	f.tmux.screens["%4"] = strings.Replace(emptyBox, "❯ \n", "❯ half a thought\n", 1)
	start := f.now
	code, out, errOut := f.run("send", "--wait", "a", "hi")
	if code != ExitStalled || out != "queued: a has something in its prompt box, delivered with its next prompt\n" ||
		errOut != "hq: stalled: a did not take the message: it still waits in a's inbox, and nothing was typed in; see hq read a before sending again\n" {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	if took := f.now.Sub(start); took < stallBound || took > stallBound+time.Second {
		t.Fatalf("stalled after %v", took)
	}
	if len(f.tmux.pasted) != 0 || len(f.inbox["id-a"]) != 1 {
		t.Fatalf("pasted %v inbox %v", f.tmux.pasted, f.inbox)
	}
}

func TestSendWaitTypesItInAsSoonAsTheBoxIsFree(t *testing.T) {
	// The box comes free after hq send has stopped waiting for it, before
	// the bound: the waiting call types it in itself.
	f := sendFakes(state.Done)
	f.tmux.screens["%4"] = strings.Replace(emptyBox, "❯ \n", "❯ half a thought\n", 1)
	n := 0
	f.onSleep = func() {
		if n++; f.now.Sub(time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)) >= boxWait+500*time.Millisecond && f.tmux.screens["%4"] != emptyBox && len(f.tmux.pasted) == 0 {
			f.tmux.screens["%4"] = emptyBox
		}
	}
	onSubmit(f, func() { turnFor(f, 4, "Done: hi") })
	if got := sendWaitJSON(t, f, "a", "hi"); got.Outcome != endTurn || len(f.tmux.pasted) != 1 {
		t.Fatalf("%+v pasted %v", got, f.tmux.pasted)
	}
}

func TestSendWaitFailsAsStalledWhenClaudeDoesNotTakeWhatWasTypedIn(t *testing.T) {
	for _, tc := range []struct {
		screen, want string
	}{
		{strings.Replace(emptyBox, "❯ \n", "❯ hi\n", 1), "it is typed into a's prompt box and not sent"},
		{emptyBox, "it left a's prompt box and started no turn"},
	} {
		f := sendFakes(state.Done)
		onSubmit(f, func() { f.tmux.screens["%4"] = tc.screen })
		code, out, errOut := f.run("send", "--wait", "a", "hi")
		if code != ExitStalled || out != "delivered: typed into a as its next prompt\n" || errOut != "hq: stalled: a did not take the message: "+tc.want+"; see hq read a before sending again\n" {
			t.Fatalf("exit %d %q %q", code, out, errOut)
		}
		// Typed in once, and nothing put back for the hooks.
		if len(f.tmux.pasted) != 1 || len(f.tmux.submitted) != 1 || len(f.inbox["id-a"]) != 0 {
			t.Fatalf("pasted %v submitted %v inbox %v", f.tmux.pasted, f.tmux.submitted, f.inbox)
		}
	}
}

func TestSendWaitOnACommandReturnsAsTheCommandGoes(t *testing.T) {
	// A command of Claude Code's own that starts no turn: normally, once
	// the bound has passed.
	f := sendFakes(state.Done)
	start := f.now
	code, out, errOut := f.run("send", "--wait", "a", "/effort low")
	if code != 0 || !strings.HasPrefix(out, "delivered: typed into a as its next prompt\nno turn: the slash command left a's prompt box and started no turn\n") || f.now.Sub(start) < stallBound {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	// One that opens a dialog of Claude Code's own: needs input.
	f = sendFakes(state.Done)
	onSubmit(f, func() {
		f.states["id-a"] = state.Report{State: state.NeedsInput, Since: f.now}
		f.tmux.screens["%4"] = "Change effort level?\n❯ 1. Yes\n  2. No\n"
	})
	if got := sendWaitJSON(t, f, "a", "/effort"); got.Outcome != endTurn || got.Agents[0].State != state.NeedsInput {
		t.Fatalf("%+v", got)
	}
	// One that starts a turn: the end of that turn.
	f = sendFakes(state.Done)
	onSubmit(f, func() { turnFor(f, 6, "Done: reviewed") })
	if got := sendWaitJSON(t, f, "a", "/review"); got.Outcome != endTurn || got.Agents[0].Last != "Done: reviewed" {
		t.Fatalf("%+v", got)
	}
}

func TestSendWaitTimesOutWithTheMessageWhereItWas(t *testing.T) {
	f := sendFakes(state.Done)
	turnFor(f, 1000, "")
	start := f.now
	code, out, _ := f.run("send", "--wait", "--timeout", "2", "a", "hi")
	if code != 0 || !strings.HasPrefix(out, "queued: a is working, delivered when it stops\nnothing yet\nnext: hq wait a --since c") || len(f.inbox["id-a"]) != 1 {
		t.Fatalf("exit %d %q inbox %v", code, out, f.inbox)
	}
	if took := f.now.Sub(start); took < 2*time.Second || took > 3*time.Second {
		t.Fatalf("timed out after %v", took)
	}
	// hq wait from its next_since returns that turn's end, once.
	next := strings.TrimSuffix(out[strings.Index(out, "--since ")+len("--since "):], "\n")
	f.onSleep = nil
	f.states["id-a"] = state.Report{State: state.Done, Since: f.now, Last: "Done: hi"}
	f.tmux.screens["%4"] = emptyBox
	var w waitOutput
	if err := json.Unmarshal([]byte(mustRun(t, f, "wait", "a", "--since", next, "--json")), &w); err != nil || len(w.Agents) != 1 || w.Agents[0].Last != "Done: hi" {
		t.Fatalf("%+v %v", w, err)
	}
	if out := mustRun(t, f, "wait", "a", "--since", w.NextSince, "--timeout", "1s"); !strings.HasPrefix(out, "nothing yet") {
		t.Fatalf("returned twice: %q", out)
	}
	// --json at the timeout: no agents.
	got := sendWaitJSON(t, f, "--timeout", "1s", "a", "more")
	if got.Outcome != endTimeout || got.Agents == nil || len(got.Agents) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestSendWaitCountsTheDeliveryInItsTimeout(t *testing.T) {
	// A starting agent that never reports: hq send would wait 30 s for it.
	f := sendFakes("")
	start := f.now
	if got := sendWaitJSON(t, f, "--timeout", "1s", "a", "hi"); got.Outcome != endTimeout {
		t.Fatalf("%+v", got)
	}
	if took := f.now.Sub(start); took > 2*time.Second {
		t.Fatalf("took %v", took)
	}
	// An agent at its prompt whose box does not come free: not the 4 s of
	// waiting for the box either.
	f = sendFakes(state.Done)
	f.tmux.screens["%4"] = strings.Replace(emptyBox, "❯ \n", "❯ typing\n", 1)
	start = f.now
	if got := sendWaitJSON(t, f, "--timeout", "1s", "a", "hi"); got.Outcome != endTimeout {
		t.Fatalf("%+v", got)
	}
	if took := f.now.Sub(start); took > 2*time.Second {
		t.Fatalf("took %v", took)
	}
}

func TestTheTakingOfAMessageIsReadOffTheAgentsEvents(t *testing.T) {
	at := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	const msg = "0000000000000000005-ab12cd34.now"
	ev := func(n int64, source, kind string, facts state.Facts) state.Event {
		return state.Event{Number: n, Source: source, Kind: kind, Time: at.Add(time.Duration(n) * time.Second), Facts: facts}
	}
	typed := func(n int64, names ...string) state.Event {
		return ev(n, state.SourceHQ, state.KindDelivery, state.Facts{Messages: names})
	}
	prompt := func(n int64, wake bool) state.Event {
		return ev(n, state.SourceHook, state.KindPrompt, state.Facts{Wake: wake})
	}
	withdrawn := ev(4, state.SourceHQ, state.KindWithdrawn, state.Facts{Withdrawn: state.KindDelivery, Messages: []string{msg}})
	aside := typed(3, msg)
	aside.Aside = "duplicate"
	for name, tc := range map[string]struct {
		events []state.Event
		want   taking
	}{
		"none":            {nil, taking{}},
		"another message": {[]state.Event{typed(3, "0000000000000000006-ffffffff")}, taking{}},
		"typed, no prompt": {[]state.Event{prompt(2, false), typed(3, "x", msg)},
			taking{taken: true, byHQ: true, at: at.Add(3 * time.Second), delivery: 3}},
		// A wake-up is no turn the message started; a prompt before the
		// delivery neither.
		"typed, then a wake-up, then its prompt": {[]state.Event{typed(3, msg), prompt(4, true), prompt(6, false), prompt(7, false)},
			taking{taken: true, byHQ: true, at: at.Add(3 * time.Second), delivery: 3, prompt: 6}},
		// Put back after a failed typing, without .now, and typed again.
		"withdrawn": {[]state.Event{typed(3, msg), withdrawn, prompt(5, false)}, taking{}},
		"withdrawn, then typed under its name less .now": {[]state.Event{typed(3, msg), withdrawn, typed(6, strings.TrimSuffix(msg, ".now")), prompt(7, false)},
			taking{taken: true, byHQ: true, at: at.Add(6 * time.Second), delivery: 6, prompt: 7}},
		"withdrawn before it was taken": {[]state.Event{withdrawn, typed(6, msg)},
			taking{taken: true, byHQ: true, at: at.Add(6 * time.Second), delivery: 6}},
		"set aside": {[]state.Event{aside}, taking{}},
		"by the hook": {[]state.Event{ev(3, state.SourceHook, state.KindDelivery, state.Facts{Messages: []string{msg}, Hook: state.CaseStop})},
			taking{taken: true, at: at.Add(3 * time.Second), delivery: 3}},
	} {
		if got := takingOf(tc.events, msg); got != tc.want {
			t.Errorf("%s: %+v, want %+v", name, got, tc.want)
		}
	}
}

func TestTheStallClockRunsOnlyWhileItsConditionHolds(t *testing.T) {
	at := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	var c stallClock
	steps := []struct {
		key    string
		origin time.Time
		now    time.Time
		want   time.Duration
	}{
		{"waiting", at, at.Add(2 * time.Second), 2 * time.Second}, // from the origin at first
		{"waiting", at.Add(time.Hour), at.Add(3 * time.Second), 3 * time.Second},
		{"typed 7", at.Add(4 * time.Second), at.Add(5 * time.Second), time.Second}, // a new key: its origin
		{"", at, at.Add(6 * time.Second), 0},                                       // paused
		{"typed 7", at.Add(4 * time.Second), at.Add(8 * time.Second), 0},           // after a pause: from now
		{"typed 7", at, at.Add(9 * time.Second), time.Second},
		{"typed 9", at.Add(8 * time.Second), at.Add(10 * time.Second), 2 * time.Second},
	}
	for i, s := range steps {
		if got := c.held(s.key, s.origin, s.now); got != s.want {
			t.Fatalf("step %d: %v, want %v", i, got, s.want)
		}
	}
}

func TestSendWaitWithNoTimeoutWaitsForAStartingAgentAsSendDoes(t *testing.T) {
	f := sendFakes("")
	n := 0
	f.onSleep = func() {
		if n++; n == 3 {
			f.states["id-a"] = state.Report{State: state.Done, Since: f.now}
		}
	}
	onSubmit(f, func() { turnFor(f, 4, "Done: hi") })
	if got := sendWaitJSON(t, f, "--timeout", "0", "a", "hi"); got.Delivery != "delivered: typed into a as its next prompt" || got.Outcome != endTurn {
		t.Fatalf("%+v", got)
	}
}

func TestSendWaitEndsAtItsTimeoutWhenThereIsNothingToTypeIn(t *testing.T) {
	// The box comes free, but another process has taken the message, and
	// its delivery is not in the journal: the call still times out.
	f := sendFakes(state.Done)
	f.tmux.screens["%4"] = strings.Replace(emptyBox, "❯ \n", "❯ typing\n", 1)
	f.onSleep = func() {
		if f.now.Sub(time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)) > boxWait {
			delete(f.inbox, "id-a")
			f.tmux.screens["%4"] = emptyBox
		}
	}
	start := f.now
	if got := sendWaitJSON(t, f, "--timeout", "4500ms", "a", "hi"); got.Outcome != endTimeout || len(f.tmux.pasted) != 0 {
		t.Fatalf("%+v pasted %v", got, f.tmux.pasted)
	}
	if took := f.now.Sub(start); took > 5*time.Second {
		t.Fatalf("took %v", took)
	}
}

func TestSendWaitStallsWithNoTimeoutWhenThereIsNothingToTypeIn(t *testing.T) {
	// As above with no timeout: the bound still runs, so the call ends,
	// saying the message is no longer in the inbox, not that it waits there
	// untyped, which hq cannot know.
	f := sendFakes(state.Done)
	f.tmux.screens["%4"] = strings.Replace(emptyBox, "❯ \n", "❯ typing\n", 1)
	start := f.now
	f.onSleep = func() {
		if f.now.Sub(start) > boxWait {
			delete(f.inbox, "id-a")
			f.tmux.screens["%4"] = emptyBox
		}
		if f.now.Sub(start) > 4*stallBound {
			t.Fatal("still waiting long after the bound")
		}
	}
	if code, _, errOut := f.run("send", "--wait", "--timeout", "0", "a", "hi"); code != ExitStalled || len(f.tmux.pasted) != 0 ||
		errOut != "hq: stalled: a did not take the message: it is no longer in a's inbox, and no delivery of it is in a's journal; see hq read a before sending again\n" {
		t.Fatalf("exit %d %q pasted %v", code, errOut, f.tmux.pasted)
	}
}

func TestWaitWithNoTimeoutWaitsPastTheDefault(t *testing.T) {
	f := sendFakes(state.Done)
	turnFor(f, 300, "Done: late") // 75 s of looks, past the default 50 s
	if out := mustRun(t, f, "wait", "a", "--timeout", "0"); !strings.Contains(out, "Done: late") {
		t.Fatalf("%q", out)
	}
}

func TestSendWaitStallsWhenTheBoundHasHeldForExactly5s(t *testing.T) {
	// The agent works when the message is left (no wait for the box), and
	// ends its turn without taking it, its box holding the user's typing;
	// from that look on, every look is 250 ms of the fakes' clock after the
	// last, so the condition holds for exactly the bound at one of them.
	f := sendFakes(state.Done)
	turnFor(f, 1000, "")
	var atPrompt time.Time
	n := 0
	f.onSleep = func() {
		if n++; n == 4 {
			f.states["id-a"] = state.Report{State: state.Done, Since: f.now, Last: "Done: earlier"}
			f.tmux.screens["%4"] = strings.Replace(emptyBox, "❯ \n", "❯ typing\n", 1)
			atPrompt = f.now
		}
	}
	code, _, errOut := f.run("send", "--wait", "a", "hi")
	if code != ExitStalled || !strings.Contains(errOut, "it still waits in a's inbox") {
		t.Fatalf("exit %d %q", code, errOut)
	}
	// At exactly 5 s the bound has passed: the stall comes at that look,
	// not the one after it.
	if held := f.now.Sub(atPrompt); held != stallBound {
		t.Fatalf("stalled after %v at its prompt, want %v", held, stallBound)
	}
}

// At the look where --timeout has passed, a waiting send types nothing in,
// although the agent is back at its prompt by then with an empty box and
// the message has waited waitingAfter: the message stays where it is
// (#170, decision 6), and deliverWaiting is handed only the other agents.
func TestSendWaitTypesNothingOnceItsTimeoutHasPassed(t *testing.T) {
	f := sendFakes(state.Done)
	turnFor(f, 1000, "")
	start := f.now
	f.onSleep = func() {
		if f.now.Sub(start) >= 7*time.Second && f.states["id-a"].State == state.Working {
			f.states["id-a"] = state.Report{State: state.Done, Since: f.now, Last: "Done: the earlier turn"}
			f.tmux.screens["%4"] = emptyBox
		}
	}
	code, out, errOut := f.run("send", "--wait", "--timeout", "7", "a", "hi")
	if code != 0 || !strings.Contains(out, "nothing yet\n") {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	if len(f.tmux.pasted) != 0 || len(f.inbox["id-a"]) != 1 {
		t.Fatalf("typed in at the timeout: pasted %v, inbox %v", f.tmux.pasted, f.inbox)
	}
}
