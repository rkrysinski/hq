package cli

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/journal/journaltest"
	"github.com/rkrysinski/hq/internal/kills"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
	"github.com/rkrysinski/hq/internal/version"
)

// sendTaskSession is a raw client of hq's MCP server for Claude Code on the
// send fakes, agent a in state st, that has said server/discover.
func sendTaskSession(t *testing.T, st string) (*fakes, *rawClient) {
	t.Helper()
	f := sendFakes(st)
	f.tty = false
	c := rawConnect(t, newMCPServer(f.deps(), claudeCode))
	c.ok("server/discover", nil, true)
	return f, c
}

// sendWaitOf is what a send with wait returned in text.
func sendWaitOf(t *testing.T, text string) sendWaitOutput {
	t.Helper()
	var got sendWaitOutput
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, text)
	}
	return got
}

// keysOf are the keys of the JSON object text, sorted.
func keysOf(t *testing.T, text string) []string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		t.Fatalf("json: %v\n%s", err, text)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// sendCompleted is what a send's wait task completed with, task answer g:
// a send with wait's result, not a tool error.
func sendCompleted(t *testing.T, g rawResult) sendWaitOutput {
	t.Helper()
	if g.Status != "completed" || g.Result == nil || g.Result.IsError {
		t.Fatalf("not completed with a result: %s", g.line)
	}
	return sendWaitOf(t, g.Result.text())
}

// sendTask has the declaring client c send a message with wait to agent a,
// which works on and on, and returns the handle it gets after the hold.
func sendTask(t *testing.T, f *fakes, c *rawClient, args map[string]any) rawResult {
	t.Helper()
	turnFor(f, 1<<30, "")
	call := map[string]any{"name": "a", "text": "Any news?", "wait": true}
	for k, v := range args {
		call[k] = v
	}
	start := f.now
	h := c.call("send", call, true)
	if !isHandle(h) || h.Status != "working" || len(h.Content) != 0 || !handedOffAfterTheHold(f.now.Sub(start)) {
		t.Fatalf("after %v: %s", f.now.Sub(start), h.line)
	}
	f.onSleep = nil
	return h
}

// handedOffAfterTheHold reports whether a send with wait that took took
// went on as a task at the first look past its hold: a look every
// agent.LoopEvery, after the delivery's own steps.
func handedOffAfterTheHold(took time.Duration) bool {
	return took >= waitHold && took < waitHold+agent.LoopEvery
}

// A send with wait whose answer comes within its hold returns it in the
// same call, though its request declares tasks (#364): within 50 s nothing
// changes.
func TestASendWithWaitAnsweredWithinItsHoldReturnsItInTheCall(t *testing.T) {
	f, c := sendTaskSession(t, state.Done)
	onSubmit(f, func() { turnFor(f, 80, "Done: the diff") }) // 20 s of looks
	start := f.now
	r := c.call("send", map[string]any{"name": "a", "text": "summarise the diff", "wait": true}, true)
	got := sendWaitOf(t, r.text())
	if isHandle(r) || r.IsError || got.Outcome != endTurn || got.Delivery != "delivered: typed into a as its next prompt" || len(got.Agents) != 1 || got.Agents[0].Last != "Done: the diff" {
		t.Fatalf("%s", r.line)
	}
	if took := f.now.Sub(start); took < 20*time.Second || took > 30*time.Second {
		t.Fatalf("returned after %v", took)
	}
}

// After the hold with no answer, a send with wait whose request declares
// tasks is answered with a wait task's handle (#364, story 17): working,
// with no time to live, at the poll interval hq names. One whose request
// does not declare them goes on in its call, as before, and returns the
// answer when it comes; one given a timeout within the hold ends at it in
// its call, whatever its request declares.
func TestASendWithWaitGoesOnAsAWaitTaskAfterItsHoldOnlyWhenItsRequestDeclaresTasks(t *testing.T) {
	f, c := sendTaskSession(t, state.Done)
	h := sendTask(t, f, c, nil)
	if h.ResultType != "task" || h.TaskID == "" || string(h.TTLMs) != "null" || h.PollIntervalMs != waitTaskPoll.Milliseconds() || h.CreatedAt != stampOf(f.now) || h.LastUpdatedAt != h.CreatedAt {
		t.Fatalf("handle %s", h.line)
	}

	f, c = sendTaskSession(t, state.Done)
	onSubmit(f, func() { turnFor(f, 320, "Done: late") }) // 80 s of looks
	start := f.now
	r := c.call("send", map[string]any{"name": "a", "text": "Take your time", "wait": true}, false)
	if got := sendWaitOf(t, r.text()); isHandle(r) || got.Outcome != endTurn || got.Agents[0].Last != "Done: late" {
		t.Fatalf("not declaring tasks: %s", r.line)
	}
	if took := f.now.Sub(start); took < 80*time.Second || took > 90*time.Second {
		t.Fatalf("not declaring tasks: returned after %v", took)
	}

	for _, timeout := range []int{50, 30} {
		f, c := sendTaskSession(t, state.Done)
		onSubmit(f, func() { turnFor(f, 1<<30, "") })
		start := f.now
		r := c.call("send", map[string]any{"name": "a", "text": "hi", "wait": true, "timeout": timeout}, true)
		if got := sendWaitOf(t, r.text()); isHandle(r) || r.IsError || got.Outcome != endTimeout || got.Agents == nil || len(got.Agents) != 0 || f.now.Sub(start) < time.Duration(timeout)*time.Second {
			t.Errorf("timeout %d: after %v: %s", timeout, f.now.Sub(start), r.line)
		}
	}
}

// A send's wait task works until the turn that took the message ends, and
// then completes with what the send with wait returns, as a call would
// return it (#364, stories 17, 18): turn_ended with the agent's row, the
// delivery the call made, and the keys of the call's result; a wait from its
// next_since does not return that change again. A turn end before the
// message was taken is not its answer.
func TestASendsWaitTaskCompletesWithTheTurnThatTookTheMessage(t *testing.T) {
	f, c := sendTaskSession(t, state.Done)
	h := sendTask(t, f, c, nil)
	f.now = f.now.Add(10 * time.Minute)
	if g := c.get(h.TaskID); g.Status != "working" || g.Result != nil || g.CreatedAt != h.CreatedAt {
		t.Fatalf("working: %s", g.line)
	}
	// The hook takes the message as the earlier turn would stop: that turn
	// goes on with it, and its end is the answer.
	hookTakes(f, "Any news?", state.CaseStop)
	f.now = f.now.Add(time.Minute)
	if g := c.get(h.TaskID); g.Status != "working" {
		t.Fatalf("taken, still working: %s", g.line)
	}
	f.now = f.now.Add(time.Minute)
	f.states["id-a"] = journaltest.HookState{State: state.Done, Since: f.now, Last: "No news."}
	f.tmux.screens["%4"] = emptyBox
	g := c.get(h.TaskID)
	got := sendCompleted(t, g)
	if g.LastUpdatedAt != stampOf(f.now) || got.Outcome != endTurn || got.Message != "" || got.Delivery != "queued: a is working, delivered when it stops" ||
		len(got.Agents) != 1 || got.Agents[0].Name != "a" || got.Agents[0].State != state.Done || got.Agents[0].Last != "No news." || got.NextSince == "" {
		t.Fatalf("%s", g.line)
	}
	if keys := strings.Join(keysOf(t, g.Result.text()), " "); keys != "agents delivery next_since outcome" {
		t.Fatalf("keys %s", keys)
	}
	if w := waitJSON(t, c.call("wait", map[string]any{"names": []string{"a"}, "since": got.NextSince, "timeout": 1}, true).text()); len(w.Agents) != 0 {
		t.Fatalf("from its next_since: %+v", w.Agents)
	}

	// A message typed in within the hold, whose turn ends after it.
	f, c = sendTaskSession(t, state.Done)
	onSubmit(f, func() { turnFor(f, 1<<30, "") })
	start := f.now
	h = c.call("send", map[string]any{"name": "a", "text": "Take your time", "wait": true}, true)
	if !isHandle(h) || !handedOffAfterTheHold(f.now.Sub(start)) {
		t.Fatalf("typed in, after %v: %s", f.now.Sub(start), h.line)
	}
	f.onSleep = nil
	f.now = f.now.Add(time.Minute)
	f.states["id-a"] = journaltest.HookState{State: state.Done, Since: f.now, Last: "Done: took my time"}
	f.tmux.screens["%4"] = emptyBox
	g = c.get(h.TaskID)
	got = sendCompleted(t, g)
	if got.Outcome != endTurn || got.Delivery != "delivered: typed into a as its next prompt" || got.Agents[0].Last != "Done: took my time" {
		t.Fatalf("typed in: %+v", got)
	}
	// A call's result, here at its timeout, has the same keys.
	f.onSleep = nil
	call := c.call("send", map[string]any{"name": "a", "text": "and now?", "wait": true, "timeout": 1}, true)
	if keys := strings.Join(keysOf(t, call.text()), " "); isHandle(call) || keys != strings.Join(keysOf(t, g.Result.text()), " ") {
		t.Fatalf("the call's keys %s: %s", keys, call.line)
	}
}

// removeJournal removes agent id's journal, as hq kill does.
func removeJournal(f *fakes, id string) {
	_ = hookedJournal{f.journal, f}.Remove("/w/app", id)
}

// A send's wait task whose agent needs input or ends before it took the
// message completes with not_taken and where the message is, as the call
// does (#364, story 18): waiting while it needs input, kept for a relaunch
// once it has ended, gone with an agent killed, which comes back ended with
// what the trace of kills says of it.
func TestASendsWaitTaskCompletesNotTakenWhenTheAgentStopsBeforeTakingIt(t *testing.T) {
	for name, tc := range map[string]struct {
		stop    func(f *fakes)
		message string
		st      string
	}{
		"needs input": {func(f *fakes) {
			f.states["id-a"] = journaltest.HookState{State: state.NeedsInput, Since: f.now}
			f.tmux.screens["%4"] = "Do you want to proceed?\n❯ 1. Yes\n  2. No\nEsc to cancel\n"
		}, msgWaits, state.NeedsInput},
		"ended": {func(f *fakes) {
			f.states["id-a"] = journaltest.HookState{State: state.Ended, Since: f.now}
		}, msgKept, state.Ended},
		"killed": {func(f *fakes) {
			f.kills = append(f.kills, kills.Kill{At: f.now, Name: "a", ID: "id-a", Repo: "/w/app", Sandbox: "claude-x"})
			f.tmux.windows = f.tmux.windows[:1]
			delete(f.inbox, "id-a")
			removeJournal(f, "id-a")
		}, msgGone, state.Ended},
	} {
		f, c := sendTaskSession(t, state.Done)
		h := sendTask(t, f, c, nil)
		f.now = f.now.Add(time.Minute)
		tc.stop(f)
		got := sendCompleted(t, c.get(h.TaskID))
		if got.Outcome != endNotTaken || got.Message != tc.message || len(got.Agents) != 1 || got.Agents[0].Name != "a" || got.Agents[0].State != tc.st {
			t.Errorf("%s: %+v", name, got)
		}
		if a := got.Agents[0]; name == "killed" && (a.RepoPath != "/w/app" || a.Sandbox != "claude-x" || a.Last != agent.EndedLast || !a.Since.Equal(f.now)) {
			t.Errorf("killed: %+v", a)
		}
	}
}

// An agent killed after it took the message comes back from the send's wait
// task as the end of the turn that took it, as from the call (#364): also
// once hq kill has removed its journal, when the call saw it take the
// message, which the task's id carries.
func TestASendsWaitTaskReturnsAnAgentKilledAfterTakingTheMessageAsItsTurnsEnd(t *testing.T) {
	for _, removed := range []bool{false, true} {
		f, c := sendTaskSession(t, state.Done)
		f.states["id-a"] = journaltest.HookState{State: state.Working, Since: f.now, Prompt: "p"}
		f.tmux.screens["%4"] = workingScreen
		f.after(10*time.Second, func() { hookTakes(f, "Any news?", state.CaseStop) })
		start := f.now
		h := c.call("send", map[string]any{"name": "a", "text": "Any news?", "wait": true}, true)
		if !isHandle(h) || !handedOffAfterTheHold(f.now.Sub(start)) {
			t.Fatalf("removed %v: %s", removed, h.line)
		}
		f.onSleep = nil
		f.now = f.now.Add(time.Minute)
		f.tmux.windows = f.tmux.windows[:1]
		if removed {
			removeJournal(f, "id-a")
		}
		got := sendCompleted(t, c.get(h.TaskID))
		if got.Outcome != endTurn || got.Message != "" || len(got.Agents) != 1 || got.Agents[0].State != state.Ended {
			t.Errorf("removed %v: %+v", removed, got)
		}
	}
}

// A send with wait whose hold ends where its timeout does, to the
// millisecond the task's deadline keeps, returns as at a timeout in its
// call: a task would be due before it began.
func TestASendWithWaitWhoseHoldEndsAtItsTimeoutReturnsInTheCall(t *testing.T) {
	f, c := sendTaskSession(t, state.Done)
	f.now = f.now.Add(700 * time.Microsecond)
	turnFor(f, 1<<30, "")
	start := f.now
	f.after(49*time.Second, func() { f.now = start.Add(51*time.Second - 500*time.Microsecond) })
	r := c.call("send", map[string]any{"name": "a", "text": "hi", "wait": true, "timeout": 51}, true)
	if got := sendWaitOf(t, r.text()); isHandle(r) || r.IsError || got.Outcome != endTimeout || got.Agents == nil || len(got.Agents) != 0 || got.NextSince == "" {
		t.Fatalf("after %v: %s", f.now.Sub(start), r.line)
	}
	if f.taskKey != nil {
		t.Fatalf("a key made for no task: %x", f.taskKey)
	}
}

// A send's wait task given a timeout completes at it with no agents, the
// message where it is (#364, story 18): its id carries the deadline, and the
// time to live hq gives the client is the time from the handoff to it, also
// past the hour a call may last. Once the deadline has passed, a poll types
// the message in no more, as the call does not at its timeout; before it, a
// poll types it in as any look does, once the agent is back at its prompt.
func TestASendsWaitTaskGivenATimeoutCompletesAtItWithNoAgents(t *testing.T) {
	f, c := sendTaskSession(t, state.Done)
	start := f.now
	h := sendTask(t, f, c, map[string]any{"timeout": 120})
	if string(h.TTLMs) != "70000" {
		t.Fatalf("handle: %s", h.line)
	}
	f.now = start.Add(120*time.Second - time.Millisecond)
	if g := c.get(h.TaskID); g.Status != "working" || string(g.TTLMs) != "70000" {
		t.Fatalf("a moment before the deadline: %s", g.line)
	}
	// Back at its prompt, its box empty, as the deadline passes.
	f.now = start.Add(123 * time.Second)
	f.states["id-a"] = journaltest.HookState{State: state.Done, Since: f.now.Add(-2 * time.Second), Last: "Done: the earlier turn"}
	f.tmux.screens["%4"] = emptyBox
	got := sendCompleted(t, c.get(h.TaskID))
	if got.Outcome != endTimeout || got.Agents == nil || len(got.Agents) != 0 || got.NextSince == "" || got.Delivery != "queued: a is working, delivered when it stops" {
		t.Fatalf("after the deadline: %+v", got)
	}
	if len(f.tmux.pasted) != 0 || len(f.inbox["id-a"]) != 1 {
		t.Fatalf("typed in past the deadline: pasted %v, inbox %v", f.tmux.pasted, f.inbox)
	}

	f, c = sendTaskSession(t, state.Done)
	start = f.now
	h = sendTask(t, f, c, map[string]any{"timeout": 7200})
	if string(h.TTLMs) != "7150000" {
		t.Fatalf("past an hour: %s", h.line)
	}
	f.now = start.Add(time.Hour)
	f.states["id-a"] = journaltest.HookState{State: state.Done, Since: f.now.Add(-time.Minute), Last: "Done: the earlier turn"}
	f.tmux.screens["%4"] = emptyBox
	onSubmit(f, func() { turnFor(f, 1<<30, "") })
	if g := c.get(h.TaskID); g.Status != "working" || strings.Join(f.tmux.pasted, "|") != "%4 Any news?" {
		t.Fatalf("before the deadline: %s, pasted %q", g.line, f.tmux.pasted)
	}
	f.onSleep = nil
	// Claude reports the prompt typed in: the turn it starts is the answer.
	if g := c.get(h.TaskID); g.Status != "working" {
		t.Fatalf("typed in: %s", g.line)
	}
	f.now = f.now.Add(time.Minute)
	f.states["id-a"] = journaltest.HookState{State: state.Done, Since: f.now, Last: "Plenty."}
	f.tmux.screens["%4"] = emptyBox
	if got := sendCompleted(t, c.get(h.TaskID)); got.Outcome != endTurn || got.Agents[0].Last != "Plenty." {
		t.Fatalf("typed in by a poll: %+v", got)
	}
}

// A stalled message and a slash command that starts no turn stay the call's
// error and outcome, though its request declares tasks (#364, story 19):
// each is told within 5 s, also when those 5 s run past the hold, which the
// call then holds on for; it goes on as a task only with nothing to tell.
func TestAStallAndNoTurnStayInTheCall(t *testing.T) {
	typing := strings.Replace(emptyBox, "❯ \n", "❯ half a thought\n", 1)
	f, c := sendTaskSession(t, state.Done)
	f.tmux.screens["%4"] = typing
	start := f.now
	r := c.call("send", map[string]any{"name": "a", "text": "hi", "wait": true}, true)
	if isHandle(r) || !r.IsError || r.text() != "hq: stalled: a did not take the message: it still waits in a's inbox, and nothing was typed in; see hq read a before sending again\n" || f.now.Sub(start) > stallBound+time.Second {
		t.Fatalf("stalled after %v: %s", f.now.Sub(start), r.line)
	}

	f, c = sendTaskSession(t, state.Done)
	start = f.now
	r = c.call("send", map[string]any{"name": "a", "text": "/effort low", "wait": true}, true)
	if got := sendWaitOf(t, r.text()); isHandle(r) || got.Outcome != endNoTurn || f.now.Sub(start) > stallBound+2*time.Second {
		t.Fatalf("no turn after %v: %s", f.now.Sub(start), r.line)
	}

	// The agent's turn ends 48 s in without taking the message, the user
	// typing in its box: stalled 5 s later, in the call.
	f, c = sendTaskSession(t, state.Done)
	turnFor(f, 1<<30, "")
	start = f.now
	f.after(48*time.Second, func() {
		f.states["id-a"] = journaltest.HookState{State: state.Done, Since: f.now, Last: "Done: earlier"}
		f.tmux.screens["%4"] = typing
	})
	r = c.call("send", map[string]any{"name": "a", "text": "hi", "wait": true}, true)
	if took := f.now.Sub(start); isHandle(r) || !r.IsError || !strings.Contains(r.text(), "it still waits in a's inbox") || took < 53*time.Second || took > 54*time.Second {
		t.Fatalf("stalled past the hold after %v: %s", took, r.line)
	}

	// Its turn ends 47 s in, the call types the message in, and Claude leaves
	// it in the box: stalled 5 s after the typing, in the call.
	f, c = sendTaskSession(t, state.Done)
	turnFor(f, 1<<30, "")
	start = f.now
	f.after(47*time.Second, func() {
		f.states["id-a"] = journaltest.HookState{State: state.Done, Since: f.now, Last: "Done: earlier"}
		f.tmux.screens["%4"] = emptyBox
		onSubmit(f, func() { f.tmux.screens["%4"] = strings.Replace(emptyBox, "❯ \n", "❯ hi\n", 1) })
	})
	r = c.call("send", map[string]any{"name": "a", "text": "hi", "wait": true}, true)
	if took := f.now.Sub(start); isHandle(r) || !r.IsError || !strings.Contains(r.text(), "it is typed into a's prompt box and not sent") || took < 52*time.Second {
		t.Fatalf("typed in and left past the hold after %v: %s", took, r.line)
	}
}

// Any hq mcp answers for a send's wait task, also another than the one that
// took the call (#364, story 36), and an id changed in what it waits for is
// an unknown task.
func TestAnotherHqMcpAnswersForASendsWaitTask(t *testing.T) {
	f, c := sendTaskSession(t, state.Done)
	h := sendTask(t, f, c, nil)
	other := rawConnect(t, newMCPServer(f.deps(), claudeCode))
	other.ok("server/discover", nil, true)
	if g := other.get(h.TaskID); g.Status != "working" || g.CreatedAt != h.CreatedAt {
		t.Fatalf("working: %s", g.line)
	}
	f.now = f.now.Add(time.Minute)
	hookTakes(f, "Any news?", state.CaseStop)
	f.states["id-a"] = journaltest.HookState{State: state.Done, Since: f.now, Last: "Here."}
	f.tmux.screens["%4"] = emptyBox
	if got := sendCompleted(t, other.get(h.TaskID)); got.Outcome != endTurn || got.Agents[0].Last != "Here." {
		t.Fatalf("completed: %+v", got)
	}
	before, after, _ := strings.Cut(h.TaskID, ":a=")
	c.unknownTask(before + ":b=" + after)
	c.unknownTask(strings.Replace(h.TaskID, "s1:", "w1:", 1))
}

// A poll that fails as the call would, tmux gone or a look that fails,
// completes the send's wait task with that error as a tool error (#364);
// so does an update to another generation, with the line every tool call
// fails with, taking no look.
func TestASendsWaitTaskFailsAsTheCallWould(t *testing.T) {
	for name, fail := range map[string]func(f *fakes){
		"tmux gone":  func(f *fakes) { f.tmux.missing = true },
		"look fails": func(f *fakes) { f.tmux.windowsErr = errors.New("no server running") },
		"updated": func(f *fakes) {
			f.disk.ino++
			f.diskGeneration = version.Generation + 1
		},
	} {
		f, c := sendTaskSession(t, state.Done)
		h := sendTask(t, f, c, nil)
		fail(f)
		g := c.get(h.TaskID)
		call := c.call("send", map[string]any{"name": "a", "text": "and now?", "wait": true}, true)
		if g.Status != "completed" || g.Result == nil || !g.Result.IsError || !strings.HasPrefix(g.Result.text(), "hq: ") || g.Result.text() != call.text() || !call.IsError {
			t.Errorf("%s: the task %s, the call %s", name, g.line, call.line)
		}
	}
}

// A send with wait whose hold passes with no answer when hq cannot make the
// key that signs a task's id fails in its call with the error, the message
// left as a send leaves it (#364, as #362 for wait).
func TestASendWithWaitThatCannotSignItsTaskFailsInTheCall(t *testing.T) {
	f, c := sendTaskSession(t, state.Done)
	f.taskKeyErr = errors.New("mkdir /s/hq: permission denied")
	onSubmit(f, func() { turnFor(f, 1<<30, "") })
	r := c.call("send", map[string]any{"name": "a", "text": "hi", "wait": true}, true)
	if isHandle(r) || !r.IsError || r.text() != "hq: the message is left, but the wait for its answer cannot go on as a task: mkdir /s/hq: permission denied\n" {
		t.Fatalf("%s", r.line)
	}
}

// A send's wait task whose agent was killed and whose name another agent
// has taken since does not take the new one for it: it knows its agent by
// id.
func TestASendsWaitTaskKnowsItsAgentById(t *testing.T) {
	f, c := sendTaskSession(t, state.Done)
	h := sendTask(t, f, c, nil)
	f.now = f.now.Add(time.Minute)
	w := agentWindow("@5", "a", "/w/app", f.now, false)
	w.Pane, w.Options["id"] = "%5", "id-a2"
	f.tmux.windows = []tmux.Window{f.tmux.windows[0], w}
	f.states["id-a2"] = journaltest.HookState{State: state.Done, Since: f.now, Last: "Hello from the new a"}
	got := sendCompleted(t, c.get(h.TaskID))
	if got.Outcome != endNotTaken || got.Message != msgGone || got.Agents[0].State != state.Ended {
		t.Fatalf("%+v", got)
	}
}

// A waited send reads its message's taking off the events numbered above
// the cursor it took before leaving the message, in its call and in its
// task alike: the event that cursor names came before the message.
func TestAWaitedSendReadsTheEventsAboveItsCursor(t *testing.T) {
	got := above([]state.Event{{Number: 6}, {Number: 7}, {Number: 8}, {Number: 9}}, 7)
	if len(got) != 2 || got[0].Number != 8 || got[1].Number != 9 {
		t.Fatalf("%+v", got)
	}
}
