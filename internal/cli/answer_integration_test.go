//go:build integration

package cli

import (
	"path/filepath"
	"strings"
	"testing"

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
