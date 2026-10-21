//go:build integration

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/testutil"
)

// atDialog starts agent a on a prompt that opens the fake Claude's question
// dialog and returns the dialog's id once the agent needs input.
func (h *realHQ) atDialog(prompt string) string {
	h.t.Helper()
	if code, _, errOut := h.run("new", "a", prompt); code != 0 {
		h.t.Fatalf("exit %d: %s", code, errOut)
	}
	return h.dialogOf("a")
}

// dialogOf waits until name needs input and returns its dialog's id.
func (h *realHQ) dialogOf(name string) string {
	h.t.Helper()
	r := h.waitState(name, "needs input")
	if len(r.Dialog) != 6 {
		h.t.Fatalf("no dialog id: %+v", r)
	}
	return r.Dialog
}

// An agent at a question dialog answered with hq answer goes on as if the
// option had been picked at the terminal: the dialog closes through its
// hook, nothing is typed, and a message queued meanwhile goes after the
// answer, as hq send promised (#90).
func TestAnswerFromTheCLIClosesTheDialogThroughItsHook(t *testing.T) {
	testutil.FakeClaude(t)
	t.Setenv("FAKE_CLAUDE_DELAY", "600ms")
	h := newRealHQ(t)
	h.cwd = testutil.GitRepo(t, "app")
	id := h.atDialog("needs input")
	h.send("queued: a needs input, delivered once its dialog is closed, when it stops", "a", "then commit")
	if code, out, errOut := h.run("answer", "a", "--dialog", id, "2"); code != 0 || out != "answered: a is working\n" {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	h.waitPane("a", "· Which colour do you pick? → Blue\n  ⎿  Allowed by PermissionRequest hook")
	h.waitReport("a", "done", "Answered: "+state.MessageLabel+"then commit")
	// The hook ended with its decision: nothing of it is left.
	if left, _ := filepath.Glob(filepath.Join(state.Dir(h.cwd), "*.[0-9]*")); len(left) != 0 {
		t.Fatalf("left beside the state file: %v", left)
	}
	if left, _ := filepath.Glob(filepath.Join(h.cwd, ".git", "hq", "inbox", "*", "*")); len(left) != 0 {
		t.Fatalf("left in the inbox: %v", left)
	}
}

// In a sandbox whose template answers AskUserQuestion by itself, hq
// refuses before it leaves an answer, naming the hook; the agent stays at
// its dialog, for the user to answer there, with nothing left in its inbox
// (#128). (The fake Claude does not run the template's hooks: that Claude
// then ignores any later answer was seen with the real one.)
func TestAnswerRefusedWhileTheTemplateAnswersAskUserQuestion(t *testing.T) {
	testutil.FakeClaude(t)
	h := newRealHQ(t)
	h.cwd = testutil.GitRepo(t, "app")
	etc := filepath.Join(os.Getenv("SBX_STUB_DIR"), "root", "etc", "claude-code")
	approve := filepath.Join(t.TempDir(), "claude-approve")
	if err := os.MkdirAll(etc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(etc, "managed-settings.json"), []byte(`{"hooks":{"PermissionRequest":[{"hooks":[{"type":"command","command":"`+approve+`"}]}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// The template's hook before it left AskUserQuestion alone: a bare
	// allow for every request.
	if err := os.WriteFile(approve, []byte("#!/bin/sh\ncat >/dev/null\necho '{\"hookSpecificOutput\":{\"hookEventName\":\"PermissionRequest\",\"decision\":{\"behavior\":\"allow\"}}}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	id := h.atDialog("needs input")
	code, out, errOut := h.run("answer", "a", "--dialog", id, "2")
	if code != ExitUsage || out != "" || !strings.Contains(errOut, approve) {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	h.waitReport("a", "needs input", "Which colour do you pick?")
	if left, _ := filepath.Glob(filepath.Join(h.cwd, ".git", "hq", "inbox", "*", "*")); len(left) != 0 {
		t.Fatalf("left in the inbox: %v", left)
	}
}

// Cancelled from hq, the turn ends as Esc ends it.
func TestAnswerCancelFromTheCLIEndsTheTurn(t *testing.T) {
	testutil.FakeClaude(t)
	h := newRealHQ(t)
	h.cwd = testutil.GitRepo(t, "app")
	id := h.atDialog("needs input")
	if code, out, errOut := h.run("answer", "a", "--dialog", id, "--cancel"); code != 0 || out != "cancelled: a is done\n" {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	h.waitReport("a", "done", "User declined to answer questions")
}

// Answered at the terminal first, the dialog is gone: hq refuses the
// answer with the agent's state, and the hook that held on it has ended
// by itself.
func TestAnswerAfterTheTerminalAnsweredIsRefused(t *testing.T) {
	testutil.FakeClaude(t)
	h := newRealHQ(t)
	h.cwd = testutil.GitRepo(t, "app")
	id := h.atDialog("needs input")
	h.typeIn("a", "1")
	h.waitReport("a", "done", "Done: needs input")
	if code, _, errOut := h.run("answer", "a", "--dialog", id, "2"); code != ExitUsage || errOut != "hq: a is at no dialog: it is done\n" {
		t.Fatalf("exit %d %q", code, errOut)
	}
	if !strings.Contains(h.pane("a"), "· Which colour do you pick? → Red") || strings.Contains(h.pane("a"), "Allowed by PermissionRequest hook") {
		t.Fatalf("screen:\n%s", h.pane("a"))
	}
	if left, _ := filepath.Glob(filepath.Join(state.Dir(h.cwd), "*.[0-9]*")); len(left) != 0 {
		t.Fatalf("the hook still holds: %v", left)
	}
	// A new dialog has an id of its own: the old one names it no more.
	h.typeIn("a", "more input")
	if again := h.dialogOf("a"); again == id {
		t.Fatalf("the same id %s for another dialog", id)
	}
	if code, _, errOut := h.run("answer", "a", "--dialog", id, "2"); code != ExitUsage || !strings.HasPrefix(errOut, "hq: dialog "+id+" is not open: a is at dialog ") {
		t.Fatalf("old id: exit %d %q", code, errOut)
	}
}

// A dialog of Claude's own fires no hook: hq answer picks its option with
// keys in the agent's pane (#120), the ❯ moved to it, then Enter, and the
// agent is back at its prompt as before the dialog.
func TestAnswerFromTheCLIPicksAnOptionOfADialogOfClaudesOwn(t *testing.T) {
	testutil.FakeClaude(t)
	h := newRealHQ(t)
	h.cwd = testutil.GitRepo(t, "app")
	if code, _, errOut := h.run("new", "a", "hello"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	h.waitReport("a", "done", "Done: hello")
	for _, tc := range []struct{ option, said string }{
		{"2", "⎿  Kept effort level as high"},
		{"1", "⎿  Set effort level to max"},
	} {
		h.typeIn("a", "/effort max")
		id := h.dialogOf("a")
		if code, out, errOut := h.run("answer", "a", "--dialog", id, tc.option); code != 0 || out != "answered: a is done\n" {
			t.Fatalf("option %s: exit %d %q %q\n%s", tc.option, code, out, errOut, h.pane("a"))
		}
		h.waitPane("a", tc.said)
		h.waitReport("a", "done", "Done: hello")
		// The dialog is gone: its id answers nothing any more.
		if code, _, errOut := h.run("answer", "a", "--dialog", id, tc.option); code != ExitUsage || errOut != "hq: a is at no dialog: it is done\n" {
			t.Fatalf("option %s again: exit %d %q", tc.option, code, errOut)
		}
	}
}

// The supervisor changes an agent's effort with hq send after its turns
// (#120): each change sent after a turn opens Claude's dialog, answered
// with hq answer, round the levels and back; one sent right after another
// change switches at once, with no dialog, as Claude Code 2.1.304 does.
func TestEffortChangesSentAfterATurnAreAnsweredWithHqAnswer(t *testing.T) {
	testutil.FakeClaude(t)
	h := newRealHQ(t)
	h.cwd = testutil.GitRepo(t, "app")
	if code, _, errOut := h.run("new", "a", "hello"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	last := "Done: hello"
	h.waitReport("a", "done", last)
	for i, level := range []string{"medium", "high", "medium"} {
		if i > 0 {
			h.send("delivered: typed into a as its next prompt", "a", "go on "+level)
			last = "Done: go on " + level
			h.waitReport("a", "done", last)
		}
		h.send("delivered: typed into a as its next prompt", "a", "/effort "+level)
		id := h.dialogOf("a")
		if r := h.waitState("a", "needs input"); r.Last != "Change effort level?" {
			t.Fatalf("%s: %+v", level, r)
		}
		if code, out, errOut := h.run("answer", "a", "--dialog", id, "1"); code != 0 || out != "answered: a is done\n" {
			t.Fatalf("%s: exit %d %q %q\n%s", level, code, out, errOut, h.pane("a"))
		}
		h.waitReport("a", "done", last)
		if got := h.effortSet(); got != level {
			t.Fatalf("%s: set %q:\n%s", level, got, h.pane("a"))
		}
	}
	// Right after a change: no dialog, the level set at once.
	h.send("delivered: typed into a as its next prompt", "a", "/effort high")
	for i := 0; h.effortSet() != "high"; i++ {
		if i == 50 {
			t.Fatalf("high not set:\n%s", h.pane("a"))
		}
		time.Sleep(100 * time.Millisecond)
	}
	if r := h.waitState("a", "done"); r.Dialog != "" {
		t.Fatalf("a dialog after a change: %+v", r)
	}
}

// effortSet is the level the fake Claude of agent a said it set last.
func (h *realHQ) effortSet() string {
	p := h.pane("a")
	i := strings.LastIndex(p, "⎿  Set effort level to ")
	if i < 0 {
		return ""
	}
	return strings.Fields(p[i+len("⎿  Set effort level to "):])[0]
}
