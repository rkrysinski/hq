//go:build integration

package state

import (
	"fmt"
	"testing"

	"github.com/rkrysinski/hq/internal/testutil"
)

// hooked is an agent whose hooks a test fires as Claude does, with a
// notification sequence on the events that have one, to read what each of
// them notifies (spec §5, #46).
type hooked struct {
	t        *testing.T
	root, id string
}

// agents gives each case of a test an agent of its own in one repository.
func agents(t *testing.T) func() hooked {
	root := testutil.GitRepo(t, "app")
	n := 0
	return func() hooked {
		n++
		return hooked{t, root, fmt.Sprintf("0a1b2c3d4e%02x", n)}
	}
}

// on fires the hook of kind and returns what it told Claude.
func (h hooked) on(kind string, payload []byte) hookOutput {
	h.t.Helper()
	notify := ""
	switch kind {
	case "stop", "dialog", "input":
		notify = "[%s]"
	}
	return decodeOutput(h.t, run(h.t, h.root, h.root, h.id, hook(kind, notify), payload))
}

// event fires the hook of kind with the fixture named payload.
func (h hooked) event(kind, payload string) hookOutput {
	h.t.Helper()
	return h.on(kind, fixture(h.t, payload))
}

// notified is the notification the hook of kind sends for the fixture.
func (h hooked) notified(kind, payload string) string {
	h.t.Helper()
	return h.event(kind, payload).TerminalSequence
}

// supervisorPrompt is a prompt hq mcp gave the agent: announced, then
// submitted.
func (h hooked) supervisorPrompt() {
	h.t.Helper()
	if err := Announce(h.root, h.id); err != nil {
		h.t.Fatal(err)
	}
	h.event("prompt", "prompt")
}

// message leaves a message for the agent, the supervisor's when supervisor.
func (h hooked) message(supervisor, now bool, text string) {
	h.t.Helper()
	postFrom(h.t, h.root, h.id, supervisor, now, text)
}

func (h hooked) state() string {
	h.t.Helper()
	r, _ := Read(h.root, h.id)
	return r.State
}

const (
	doneNotice     = "[Done: main]"
	questionNotice = "[Question: main]"
	inputNotice    = "[Needs input: main]"
)

func TestASupervisedTurnEndsWithoutANotificationButItsDialogNotifies(t *testing.T) {
	h := agents(t)()

	// The supervisor's prompt: the turn's end changes the state and
	// notifies nobody, done or question.
	h.supervisorPrompt()
	if got := h.notified("stop", "stop-done"); got != "" || h.state() != Done {
		t.Fatalf("done on a supervised turn: notified %q, %s", got, h.state())
	}
	h.supervisorPrompt()
	if got := h.notified("stop", "stop-question"); got != "" || h.state() != Question {
		t.Fatalf("question on a supervised turn: notified %q, %s", got, h.state())
	}

	// Dialogs stay the user's: one notification when it opens, and the
	// user's answer does not make the turn theirs.
	h.supervisorPrompt()
	if got := h.notified("dialog", "dialog-ask"); got != inputNotice || h.state() != NeedsInput {
		t.Fatalf("a dialog on a supervised turn: notified %q, %s", got, h.state())
	}
	if got := h.notified("input", "notification"); got != "" {
		t.Fatalf("Claude's late notification of the same dialog: %q", got)
	}
	h.event("answer", "answer-ask")
	if got := h.notified("stop", "stop-done"); got != "" || h.state() != Done {
		t.Fatalf("done after the user answered: notified %q, %s", got, h.state())
	}
	h.supervisorPrompt()
	if got := h.notified("input", "notification"); got != inputNotice {
		t.Fatalf("needs input with no dialog on a supervised turn: %q", got)
	}
	if got := h.notified("stop", "stop-done"); got != "" {
		t.Fatalf("done after it: %q", got)
	}

	// Ownership is the turn's, not the agent's: the user's prompt on the
	// same agent notifies as ever, and the supervisor's next one does not.
	h.event("prompt", "prompt")
	if got := h.notified("stop", "stop-done"); got != doneNotice {
		t.Fatalf("the user's turn on the same agent: %q", got)
	}
	h.event("prompt", "prompt")
	if got := h.notified("stop", "stop-question"); got != questionNotice {
		t.Fatalf("the user's question: %q", got)
	}
	h.supervisorPrompt()
	if got := h.notified("stop", "stop-done"); got != "" {
		t.Fatalf("the supervisor's turn after the user's: %q", got)
	}
}

func TestAnyPromptOrMessageOfTheUsersMakesTheTurnNotify(t *testing.T) {
	agent := agents(t)
	const supervisor, user, now = true, false, true
	blocked := func(h hooked) {
		t.Helper()
		if o := h.event("stop", "stop-to-block"); o.Decision != "block" || o.TerminalSequence != "" {
			t.Fatalf("the stop with a message waiting: %+v", o)
		}
	}
	for name, tc := range map[string]struct {
		turn func(h hooked)
		want string
	}{
		"the supervisor's message reaches a turn the user started": {func(h hooked) {
			h.event("prompt", "prompt")
			h.message(supervisor, !now, "also run the linter")
			blocked(h)
		}, doneNotice},
		"with now too": {func(h hooked) {
			h.event("prompt", "prompt")
			h.message(supervisor, now, "use the v2 API")
			h.event("answer", "post-tool")
		}, doneNotice},
		"the supervisor's message reaches its own turn": {func(h hooked) {
			h.supervisorPrompt()
			h.message(supervisor, !now, "also run the linter")
			blocked(h)
		}, ""},
		"with now, within its own turn": {func(h hooked) {
			h.supervisorPrompt()
			h.message(supervisor, now, "use the v2 API")
			if o := h.event("answer", "post-tool"); o.HookSpecificOutput.AdditionalContext == "" {
				t.Fatal("not delivered after the tool")
			}
		}, ""},
		"the user sends a message during a supervised turn": {func(h hooked) {
			h.supervisorPrompt()
			h.message(user, !now, "and the docs")
			blocked(h)
		}, doneNotice},
		"the user sends one with now": {func(h hooked) {
			h.supervisorPrompt()
			h.message(user, now, "stop editing main.go")
			h.event("answer", "post-tool")
		}, doneNotice},
		"messages of both wait at the turn's end": {func(h hooked) {
			h.supervisorPrompt()
			h.message(supervisor, !now, "also run the linter")
			h.message(user, !now, "and the docs")
			blocked(h)
		}, doneNotice},
		"the user types a prompt during a supervised turn": {func(h hooked) {
			h.supervisorPrompt()
			h.event("prompt", "prompt-pasted")
		}, doneNotice},
		"the supervisor's message goes along with a prompt the user was typing": {func(h hooked) {
			h.message(supervisor, !now, "also run the linter")
			if o := h.event("prompt", "prompt"); o.HookSpecificOutput.AdditionalContext == "" {
				t.Fatal("not delivered with the prompt")
			}
		}, doneNotice},
		"a message of the user's goes along with the supervisor's prompt": {func(h hooked) {
			h.message(user, !now, "and the docs")
			h.supervisorPrompt()
		}, doneNotice},
	} {
		h := agent()
		tc.turn(h)
		if got := h.notified("stop", "stop-after-block"); got != tc.want {
			t.Errorf("%s: the turn's end notified %q, want %q", name, got, tc.want)
		}
		// One notification at the turn's final end, and the next turn is
		// judged afresh.
		h.supervisorPrompt()
		if got := h.notified("stop", "stop-done"); got != "" {
			t.Errorf("%s: the supervised turn after it notified %q", name, got)
		}
	}
}

func TestClosingTurnsInheritTheOwnerOfTheTurnThatStartedTheBackgroundWork(t *testing.T) {
	agent := agents(t)
	const task = "a58b43841609db047" // the subagent of the fixtures

	// A supervised turn leaves a subagent running: working, and done after
	// its closing turn, all without a notification.
	h := agent()
	h.supervisorPrompt()
	h.on("answer", toolEnd("Agent", "", launched(task)))
	if got := h.notified("stop", "stop-background"); got != "" || h.state() != Working {
		t.Fatalf("the turn end with a subagent running: notified %q, %s", got, h.state())
	}
	h.event("prompt", "prompt-wake")
	if got := h.notified("stop", "stop-done"); got != "" || h.state() != Done {
		t.Fatalf("the closing turn of a supervised turn: notified %q, %s", got, h.state())
	}

	// Its question with the subagent still running notifies nobody either,
	// nor does the closing turn that follows it.
	h = agent()
	h.supervisorPrompt()
	if got := h.notified("stop", "stop-background-question"); got != "" || h.state() != Question {
		t.Fatalf("a supervised question with a subagent running: notified %q, %s", got, h.state())
	}
	h.event("prompt", "prompt-wake")
	if got := h.notified("stop", "stop-done"); got != "" || h.state() != Done {
		t.Fatalf("the closing turn after the question: notified %q, %s", got, h.state())
	}

	// A supervised turn the user interrupted fires no hook; the closing
	// turn of the work it started is still the supervisor's.
	h = agent()
	h.supervisorPrompt()
	h.on("answer", toolEnd("Agent", "", launched(task)))
	h.event("prompt", "prompt-wake")
	if got := h.notified("stop", "stop-done"); got != "" {
		t.Fatalf("the closing turn of an interrupted supervised turn: %q", got)
	}

	// The user's turn: its closing turn notifies as ever.
	h = agent()
	h.event("prompt", "prompt")
	h.notified("stop", "stop-background")
	h.event("prompt", "prompt-wake")
	if got := h.notified("stop", "stop-done"); got != doneNotice {
		t.Fatalf("the closing turn of the user's turn: %q", got)
	}

	// Work of both is owed: the user's turn left a subagent running, and
	// the supervisor's prompt came while it ran. Its end notifies.
	h = agent()
	h.event("prompt", "prompt")
	h.notified("stop", "stop-background")
	h.supervisorPrompt()
	if got := h.notified("stop", "stop-background"); got != "" || h.state() != Working {
		t.Fatalf("the supervisor's turn while the user's subagent runs: notified %q, %s", got, h.state())
	}
	h.event("prompt", "prompt-wake")
	if got := h.notified("stop", "stop-done"); got != doneNotice {
		t.Fatalf("the closing turn with work of both owed: %q", got)
	}
	// With nothing owed any more, the supervisor's next turn is its own.
	h.supervisorPrompt()
	if got := h.notified("stop", "stop-done"); got != "" {
		t.Fatalf("the supervised turn after it: %q", got)
	}

	// The other way round: the user prompts while the supervisor's
	// subagent runs, and the end notifies.
	h = agent()
	h.supervisorPrompt()
	h.notified("stop", "stop-background")
	h.event("prompt", "prompt-pasted")
	h.notified("stop", "stop-background")
	h.event("prompt", "prompt-wake")
	if got := h.notified("stop", "stop-done"); got != doneNotice {
		t.Fatalf("the closing turn after the user's prompt: %q", got)
	}
}

func TestOwnershipIsForgottenWithTheSession(t *testing.T) {
	agent := agents(t)
	// Background work of a supervised turn outlives the session and wakes
	// the new one: done, with a notification.
	for name, restart := range map[string]func(h hooked){
		"/clear":   func(h hooked) { h.event("end", "session-end"); h.event("start", "session-start-clear") },
		"a resume": func(h hooked) { h.event("resume", "session-start-resume") },
		"an end":   func(h hooked) { h.event("end", "session-end") },
	} {
		h := agent()
		h.supervisorPrompt()
		if got := h.notified("stop", "stop-background"); got != "" {
			t.Fatalf("%s: the turn end with a subagent running: %q", name, got)
		}
		restart(h)
		h.event("prompt", "prompt-wake")
		if got := h.notified("stop", "stop-done"); got != doneNotice {
			t.Errorf("%s: the turn that woke the new session notified %q", name, got)
		}
	}

	// A new session reports its start before its first prompt: the prompt
	// hq mcp started it with is still the supervisor's.
	h := agent()
	if err := Announce(h.root, h.id); err != nil {
		t.Fatal(err)
	}
	h.event("start", "session-start")
	h.event("prompt", "prompt")
	if got := h.notified("stop", "stop-done"); got != "" {
		t.Fatalf("the first prompt of an agent the supervisor started: %q", got)
	}
}

func TestAnAnnouncementIsForOnePromptAndCanBeWithdrawn(t *testing.T) {
	agent := agents(t)

	// Taken back before the prompt came: the prompt is the user's.
	h := agent()
	if Announced(h.root, h.id) {
		t.Fatal("announced before anything")
	}
	if err := Announce(h.root, h.id); err != nil || !Announced(h.root, h.id) {
		t.Fatalf("announce: %v", err)
	}
	if err := Withdraw(h.root, h.id); err != nil || Announced(h.root, h.id) {
		t.Fatalf("withdraw: %v", err)
	}
	if err := Withdraw(h.root, h.id); err != nil {
		t.Fatalf("nothing to withdraw: %v", err)
	}
	h.event("prompt", "prompt")
	if got := h.notified("stop", "stop-done"); got != doneNotice {
		t.Fatalf("a prompt after the announcement was withdrawn: %q", got)
	}

	// The prompt takes it: hq sees it gone, and the next prompt is the
	// user's.
	h.supervisorPrompt()
	if Announced(h.root, h.id) {
		t.Fatal("the prompt left the announcement")
	}
	h.notified("stop", "stop-done")
	h.event("prompt", "prompt")
	if got := h.notified("stop", "stop-done"); got != doneNotice {
		t.Fatalf("the user's prompt after the supervisor's: %q", got)
	}

	// Claude's own prompt, for finished background work, is not the one
	// announced: the supervisor's prompt after it still is.
	h = agent()
	h.event("prompt", "prompt")
	h.notified("stop", "stop-done")
	if err := Announce(h.root, h.id); err != nil {
		t.Fatal(err)
	}
	h.event("prompt", "prompt-wake")
	if !Announced(h.root, h.id) {
		t.Fatal("Claude's wake-up took the announcement")
	}
	if got := h.notified("stop", "stop-done"); got != doneNotice {
		t.Fatalf("the closing turn of the user's work: %q", got)
	}
	h.event("prompt", "prompt")
	if got := h.notified("stop", "stop-done"); got != "" || Announced(h.root, h.id) {
		t.Fatalf("the announced prompt after a wake-up: notified %q", got)
	}

	// Nothing is left of either mark once the agent is removed.
	h.supervisorPrompt()
	if err := Announce(h.root, h.id); err != nil {
		t.Fatal(err)
	}
	if err := Remove(h.root, h.id); err != nil {
		t.Fatal(err)
	}
	if f := files(t, h.root, h.id); len(f) != 2 { // the other agent's report and its last stop
		t.Fatalf("files left: %v", f)
	}
}
