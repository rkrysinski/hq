//go:build integration

package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/testutil"
)

type sendWaitResult struct {
	code     int
	out      sendWaitOutput
	raw, err string
}

// sendWait runs hq send --wait --json in the background, as a supervisor
// does while the agent goes on.
func (h *realHQ) sendWait(args ...string) chan sendWaitResult {
	ch := make(chan sendWaitResult, 1)
	go func() {
		code, out, errOut := h.run(append([]string{"send", "--wait", "--json"}, args...)...)
		r := sendWaitResult{code: code, raw: out, err: errOut}
		_ = json.Unmarshal([]byte(out), &r.out)
		ch <- r
	}()
	return ch
}

func (h *realHQ) sentWaited(ch chan sendWaitResult) sendWaitResult {
	h.t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(30 * time.Second):
		h.t.Fatal("hq send --wait did not return")
	}
	return sendWaitResult{}
}

// hq send --wait against the fake Claude: the end of the turn the message
// started, at a prompt and at work, and a cursor that does not return it
// again (#170).
func TestSendWaitReturnsTheTurnThatTookTheMessage(t *testing.T) {
	testutil.FakeClaude(t)
	t.Setenv("FAKE_CLAUDE_DELAY", "600ms")
	h := newRealHQ(t)
	h.cwd = testutil.GitRepo(t, "app")
	if code, _, errOut := h.run("new", "a", "hello"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	h.waitReport("a", "done", "Done: hello")

	// At its prompt: typed in, and the end of that turn.
	h.waitAtRest("a")
	r := h.sentWaited(h.sendWait("a", "summarise the diff"))
	if r.code != 0 || r.out.Outcome != endTurn || len(r.out.Agents) != 1 || r.out.Agents[0].Last != "Done: summarise the diff" {
		t.Fatalf("exit %d %s %s", r.code, r.raw, r.err)
	}
	if code, out, _ := h.run("wait", "a", "--since", r.out.NextSince, "--timeout", "2"); code != 0 || !strings.HasPrefix(out, "nothing yet") {
		t.Fatalf("the change came back: exit %d %q", code, out)
	}

	// At work: not the running turn's end until the hook took it; the turn
	// that went on with it.
	h.typeIn("a", "slow job")
	h.waitState("a", "working")
	ch := h.sendWait("a", "check the logs")
	h.waitRow("a", "working", func(row lsRow) bool { return row.Pending == 1 })
	h.typeIn("a", "go on")
	r = h.sentWaited(ch)
	if r.code != 0 || r.out.Outcome != endTurn || r.out.Agents[0].Last != "Answered: "+state.MessageLabel+"check the logs" {
		t.Fatalf("exit %d %s %s", r.code, r.raw, r.err)
	}
}

// What the fake Claude does not take: the user's typing in the box, a
// prompt it leaves there, and a command of its own that starts no turn.
func TestSendWaitSaysWhenTheAgentDoesNotTakeTheMessage(t *testing.T) {
	testutil.FakeClaude(t)
	h := newRealHQ(t)
	h.cwd = testutil.GitRepo(t, "app")
	if code, _, errOut := h.run("new", "a", "hello"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	h.waitReport("a", "done", "Done: hello")

	// The user typing: stalled, still waiting; it rides along with the
	// prompt they send.
	h.typeIn("a", "draft")
	h.waitPane("a", "❯ a draft")
	code, out, errOut := h.run("send", "--wait", "a", "ride along")
	if code != ExitStalled || errOut != "hq: stalled: a did not take the message: it still waits in a's inbox, and nothing was typed in; see hq read a before sending again\n" || h.ls()[0].Pending != 1 {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	h.typeIn("a", "sent")
	h.waitReport("a", "done", "Done: sent | "+state.MessageLabel+"ride along")

	// A prompt Claude leaves in the box: typed in once, not sent.
	h.waitAtRest("a")
	code, out, errOut = h.run("send", "--wait", "a", "unsent work")
	if code != ExitStalled || errOut != "hq: stalled: a did not take the message: it is typed into a's prompt box and not sent; see hq read a before sending again\n" {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	h.typeIn("a", "next")
	h.waitReport("a", "done", "Done: next")

	// A command of its own that opens a dialog of Claude Code's own (after
	// a turn, the fake's /effort asks first): the agent needing input.
	h.waitAtRest("a")
	r := h.sentWaited(h.sendWait("a", "/effort low"))
	if r.code != 0 || r.out.Outcome != endTurn || r.out.Agents[0].State != state.NeedsInput {
		t.Fatalf("exit %d %s %s", r.code, r.raw, r.err)
	}
	if code, out, errOut := h.run("answer", "a", "--dialog", r.out.Agents[0].Dialog, "1"); code != 0 {
		t.Fatalf("answer: exit %d %q %q", code, out, errOut)
	}
	// And one that starts no turn (no turn has run since the level
	// changed, so it asks nothing): normally, once the bound has passed.
	h.waitAtRest("a")
	start := time.Now()
	code, out, errOut = h.run("send", "--wait", "a", "/effort medium")
	if code != 0 || !strings.Contains(out, "no turn: the slash command left a's prompt box and started no turn\n") || time.Since(start) < stallBound {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
}
