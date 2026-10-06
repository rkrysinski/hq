//go:build integration

package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/testutil"
)

type waitResult struct {
	code int
	out  waitOutput
	raw  string
	at   time.Time // when it returned
}

// wait runs hq wait in the background, as a supervisor does while the
// agents go on.
func (h *realHQ) wait(args ...string) chan waitResult {
	ch := make(chan waitResult, 1)
	go func() {
		code, out, errOut := h.run(append([]string{"wait", "--json"}, args...)...)
		r := waitResult{code: code, raw: out + errOut, at: time.Now()}
		_ = json.Unmarshal([]byte(out), &r.out)
		ch <- r
	}()
	return ch
}

func (h *realHQ) waited(ch chan waitResult) waitResult {
	h.t.Helper()
	select {
	case r := <-ch:
		if r.code != 0 {
			h.t.Fatalf("hq wait: exit %d %s", r.code, r.raw)
		}
		return r
	case <-time.After(30 * time.Second):
		h.t.Fatal("hq wait did not return")
	}
	return waitResult{}
}

// stillWaiting fails when hq wait has returned.
func (h *realHQ) stillWaiting(ch chan waitResult) {
	h.t.Helper()
	select {
	case r := <-ch:
		h.t.Fatalf("hq wait returned early: %s", r.raw)
	default:
	}
}

// only fails unless r returned exactly one agent, name in state with last.
func (h *realHQ) only(r waitResult, name, state, last string) lsRow {
	h.t.Helper()
	if len(r.out.Agents) != 1 || r.out.Agents[0].Name != name || r.out.Agents[0].State != state || r.out.Agents[0].Last != last {
		h.t.Fatalf("want %s %s %q, got %s", name, state, last, r.raw)
	}
	return r.out.Agents[0]
}

func since(r waitResult) string { return r.out.NextSince.Format(time.RFC3339Nano) }

func TestWaitReturnsAsAnAgentNeedsTheUserAndMissesNothingBetweenCalls(t *testing.T) {
	testutil.FakeClaude(t)
	t.Setenv("FAKE_CLAUDE_DELAY", "600ms")
	h := newRealHQ(t)
	h.cwd = testutil.GitRepo(t, "app")
	if code, _, errOut := h.run("new", "a", "hello"); code != 0 {
		t.Fatalf("new: %s", errOut)
	}
	h.waitReport("a", "done", "Done: hello")

	// Already done when the call starts: not returned. It waits on while
	// the agent works, and returns within a second of its question.
	ch := h.wait()
	time.Sleep(time.Second)
	h.stillWaiting(ch)
	h.typeIn("a", "a question")
	h.waitState("a", "working")
	h.stillWaiting(ch)
	first := h.waited(ch)
	row := h.only(first, "a", "question", "Shall I go on?")
	if took := first.at.Sub(row.Since); took > time.Second {
		t.Fatalf("returned %v after the question", took)
	}

	// A change between two calls is reported by the next one, at once.
	h.typeIn("a", "needs input")
	h.waitReport("a", "needs input", "Which colour do you pick?")
	start := time.Now()
	second := h.waited(h.wait("--since", since(first)))
	h.only(second, "a", "needs input", "Which colour do you pick?")
	if took := second.at.Sub(start); took > time.Second {
		t.Fatalf("the change between the calls took %v", took)
	}

	// The dialog answered, the turn goes on and ends: done.
	ch = h.wait("--since", since(second))
	h.typeIn("a", "yes")
	h.only(h.waited(ch), "a", "done", "Done: needs input")

	// Nothing more: "nothing yet" at the timeout, exit 0.
	code, out, _ := h.run("wait", "--timeout", "1s")
	if code != 0 || !strings.HasPrefix(out, "nothing yet\nnext: hq wait --since ") {
		t.Fatalf("timeout: exit %d %q", code, out)
	}
}

func TestWaitsInSeveralProcessesAgreeOnATurnTheUserEnded(t *testing.T) {
	testutil.FakeClaude(t)
	h := newRealHQ(t)
	h.cwd = testutil.GitRepo(t, "app")
	if code, _, errOut := h.run("new", "b", "hi"); code != 0 {
		t.Fatalf("new: %s", errOut)
	}
	h.waitReport("b", "done", "Done: hi")
	h.typeIn("b", "slow work")
	h.waitState("b", "working")
	// No hook reports an interrupted turn; the waits, like hq ls and the
	// list, see it on the agent's screen and keep the first record.
	one, two := h.wait("b"), h.wait()
	time.Sleep(time.Second)
	h.stillWaiting(one)
	h.typeIn("b", "esc")
	r1, r2 := h.only(h.waited(one), "b", "done", "Interrupted"), h.only(h.waited(two), "b", "done", "Interrupted")
	if !r1.Since.Equal(r2.Since) {
		t.Fatalf("the waits disagree: %v and %v", r1.Since, r2.Since)
	}
	if ls := h.waitState("b", "done"); !ls.Since.Equal(r1.Since) {
		t.Fatalf("hq ls says %v, hq wait %v", ls.Since, r1.Since)
	}
}

func TestWaitReturnsAnAgentKilledWhileWaitedOnAndRefusesAnUnknownOne(t *testing.T) {
	testutil.FakeClaude(t)
	h := newRealHQ(t)
	h.cwd = testutil.GitRepo(t, "app")
	for _, name := range []string{"a", "b"} {
		if code, _, errOut := h.run("new", name, "hi"); code != 0 {
			t.Fatalf("new %s: %s", name, errOut)
		}
		h.waitReport(name, "done", "Done: hi")
	}
	if code, _, errOut := h.run("wait", "a", "nobody"); code != ExitNotFound || errOut != "hq: no agent 'nobody' (see hq ls)\n" {
		t.Fatalf("unknown name: exit %d %q", code, errOut)
	}
	ch := h.wait("a")
	time.Sleep(500 * time.Millisecond)
	if code, _, errOut := h.run("kill", "a", "-y"); code != 0 {
		t.Fatalf("kill: %s", errOut)
	}
	h.only(h.waited(ch), "a", "ended", "Done: hi")
}

// hq new starts a sandbox that was not running; hq wait called at once
// asks sbx before the sandbox says it runs, and keeps the answer for a
// second, in which the agent's session starts and its turn ends (#48).
func TestWaitRightAfterNewNeverSeesTheAgentEndedOnAnOlderAnswerOfSbx(t *testing.T) {
	testutil.FakeClaude(t)
	h := newRealHQ(t)
	h.cwd = testutil.GitRepo(t, "app")
	if code, _, errOut := h.run("new", "a", "hello"); code != 0 {
		t.Fatalf("new: %s", errOut)
	}
	h.only(h.waited(h.wait("--since", "1m")), "a", "done", "Done: hello")
}
