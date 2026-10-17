//go:build integration

package state

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/testutil"
)

// held is the question dialog's hook, started as Claude starts it, beside
// the dialog's own hook, and left to hold.
type held struct {
	cmd  *exec.Cmd
	out  bytes.Buffer
	done chan struct{}
}

// hold starts the hook of kind hold on payload, as run does, without
// waiting for it.
func hold(t *testing.T, cwd, project, id string, payload []byte) *held {
	t.Helper()
	h := hook("hold", "")
	k := &held{cmd: exec.Command(h.Command, h.Args...), done: make(chan struct{})}
	k.cmd.Dir = cwd
	k.cmd.Env = append(os.Environ(), "HQ_ID="+id, "CLAUDE_PROJECT_DIR="+project, hookEnv+"="+hookScript)
	k.cmd.Stdin = bytes.NewReader(payload)
	k.cmd.Stdout = &k.out
	if err := k.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = k.cmd.Wait(); close(k.done) }()
	t.Cleanup(func() { _ = k.cmd.Process.Kill(); <-k.done })
	return k
}

// ended waits up to within for the hook to end, and returns what it
// printed; ok is false while it still holds.
func (k *held) ended(within time.Duration) (out string, ok bool) {
	select {
	case <-k.done:
		return k.out.String(), true
	case <-time.After(within):
		return "", false
	}
}

// openDialog reports the question dialog of payload for id, as its dialog
// hook does, and returns it as hq reads it.
func openDialog(t *testing.T, root, id string, payload []byte) Questions {
	t.Helper()
	fire(t, root, root, id, "dialog", payload)
	q, err := ReadQuestions(root, id)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

// leave leaves decision as the answer to q.
func leave(t *testing.T, root, id string, q Questions, decision []byte) {
	t.Helper()
	if err := LeaveAnswer(root, id, q, decision); err != nil {
		t.Fatal(err)
	}
}

// The question dialog's hook holds while its dialog is open and prints the
// answer hq leaves for it as its decision, taking it out of the inbox: an
// allow carrying the answers, or a deny that interrupts the turn (#90).
func TestTheQuestionDialogsHookPrintsTheAnswerHqLeaves(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	const id = "0a1b2c3d4e5f"
	payload := fixture(t, "dialog-ask-several")
	q := openDialog(t, root, id, payload)

	// Left before the hook looks: taken at its first look.
	decision, sent, err := q.Decision([]Answer{{Options: []int{2}}, {Options: []int{1, 3}}, {Text: "Gamma, typed by hq"}})
	if err != nil {
		t.Fatal(err)
	}
	leave(t, root, id, q, decision)
	k := hold(t, root, root, id, payload)
	out, ok := k.ended(3 * time.Second)
	if !ok || out != string(decision)+"\n" {
		t.Fatalf("answered: %v %q", ok, out)
	}
	if !AnswerTaken(root, id, q) || len(inboxLeft(root, id)) != 0 {
		t.Fatalf("left in the inbox: %v", inboxLeft(root, id))
	}
	if left, _ := filepath.Glob(filepath.Join(Dir(root), id+".*")); len(left) != 0 {
		t.Fatalf("left beside the state file: %v", left)
	}
	// Claude reports the answers as #89 recorded them: what hq sent, as
	// strings, the list joined.
	fire(t, root, root, id, "answer", fixture(t, "answer-ask-hook"))
	if got, ok := ReadAnswers(root, id); !ok || !reflect.DeepEqual(got, sent) {
		t.Fatalf("Claude got %v, hq sent %v", got, sent)
	}
	if r, _ := Read(root, id); r.State != Working {
		t.Fatalf("after the answer: %+v", r)
	}
	q = openDialog(t, root, id, payload)

	// Left while it holds: taken within a second, the cancel too.
	k = hold(t, root, root, id, payload)
	if _, ok := k.ended(time.Second); ok {
		t.Fatal("did not hold")
	}
	leave(t, root, id, q, q.Cancel())
	if out, ok := k.ended(2 * time.Second); !ok || out != string(q.Cancel())+"\n" {
		t.Fatalf("cancelled: %v %q", ok, out)
	}
}

// Claude runs the dialog's two hooks side by side: the question dialog's
// may look before the dialog's own has reported it, and holds all the same.
func TestTheQuestionDialogsHookWaitsForItsDialogsReport(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	const id = "0a1b2c3d4e5f"
	payload := fixture(t, "dialog-ask-several")
	fire(t, root, root, id, "prompt", fixture(t, "prompt"))
	k := hold(t, root, root, id, payload)
	time.Sleep(500 * time.Millisecond)
	q := openDialog(t, root, id, payload)
	if _, ok := k.ended(time.Second); ok {
		t.Fatal("did not hold")
	}
	leave(t, root, id, q, q.Cancel())
	if out, ok := k.ended(2 * time.Second); !ok || out != string(q.Cancel())+"\n" {
		t.Fatalf("%v %q", ok, out)
	}
}

// Once the dialog closes otherwise (answered at the terminal), the hook
// ends by itself with no decision, Claude not stopping it, and takes no
// answer left after that (#89, case 2d).
func TestTheQuestionDialogsHookEndsWhenItsDialogCloses(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	const id = "0a1b2c3d4e5f"
	payload := fixture(t, "dialog-ask-several")
	q := openDialog(t, root, id, payload)
	k := hold(t, root, root, id, payload)
	if _, ok := k.ended(500 * time.Millisecond); ok {
		t.Fatal("did not hold")
	}
	fire(t, root, root, id, "answer", fixture(t, "answer-ask-several"))
	if out, ok := k.ended(2 * time.Second); !ok || out != "" {
		t.Fatalf("after the dialog closed: %v %q", ok, out)
	}
	leave(t, root, id, q, q.Cancel())
	time.Sleep(500 * time.Millisecond)
	if AnswerTaken(root, id, q) {
		t.Fatal("an answer taken after the dialog closed")
	}
	if !WithdrawAnswer(root, id, q) || len(inboxLeft(root, id)) != 0 {
		t.Fatalf("not withdrawn: %v", inboxLeft(root, id))
	}
}

// An answer is for one dialog's report: the hook of another, the same
// questions asked in another turn included, never takes it, and the hook
// of a subagent's dialog, which hq does not answer, holds nothing.
func TestTheQuestionDialogsHookTakesOnlyTheAnswerForItsDialog(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	const id = "0a1b2c3d4e5f"
	first := fixture(t, "dialog-ask-several")
	q := openDialog(t, root, id, first)
	fire(t, root, root, id, "answer", fixture(t, "answer-ask-several"))
	leave(t, root, id, q, q.Cancel()) // too late for the first dialog

	again := bytes.Replace(first, []byte(`"prompt_id":"292b`), []byte(`"prompt_id":"392b`), 1)
	openDialog(t, root, id, again)
	k := hold(t, root, root, id, again)
	if out, ok := k.ended(time.Second); ok {
		t.Fatalf("took the first dialog's answer: %q", out)
	}
	if AnswerTaken(root, id, q) {
		t.Fatal("the first dialog's answer is gone")
	}

	sub := []byte(strings.Replace(string(first), `"hook_event_name"`, `"agent_id":"a1b2c3","hook_event_name"`, 1))
	fire(t, root, root, id, "dialog", sub)
	if out, ok := hold(t, root, root, id, sub).ended(time.Second); !ok || out != "" {
		t.Fatalf("a subagent's dialog: %v %q", ok, out)
	}
}

// At its timeout Claude ends the hook with SIGTERM: it ends with no
// decision and leaves nothing behind.
func TestTheQuestionDialogsHookEndsQuietlyAtItsTimeout(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	const id = "0a1b2c3d4e5f"
	payload := fixture(t, "dialog-ask-several")
	openDialog(t, root, id, payload)
	k := hold(t, root, root, id, payload)
	time.Sleep(500 * time.Millisecond)
	_ = k.cmd.Process.Signal(syscall.SIGTERM)
	if out, ok := k.ended(2 * time.Second); !ok || out != "" || k.cmd.ProcessState.ExitCode() != 0 {
		t.Fatalf("%v %q %v", ok, out, k.cmd.ProcessState)
	}
	if left, _ := filepath.Glob(filepath.Join(Dir(root), id+".*")); len(left) != 0 {
		t.Fatalf("left beside the state file: %v", left)
	}
}

// One answer waits for a dialog at a time: another for the same dialog is
// refused while it waits (two hq processes answering at once), and one left
// for an earlier dialog, which no hook takes, goes when a new one is left.
// What Claude reports it got is read off the state file once the dialog's
// tool ended.
func TestAnAnswerWaitsAloneForItsDialog(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	const id = "0a1b2c3d4e5f"
	if _, err := ReadQuestions(root, id); err == nil {
		t.Fatal("questions before any report")
	}
	first := openDialog(t, root, id, fixture(t, "dialog-ask"))
	leave(t, root, id, first, first.Cancel())
	if err := LeaveAnswer(root, id, first, first.Cancel()); err != ErrAnswerWaiting {
		t.Fatalf("a second answer: %v", err)
	}
	second := openDialog(t, root, id, fixture(t, "dialog-ask-several"))
	leave(t, root, id, second, second.Cancel())
	if left := inboxLeft(root, id); len(left) != 1 || left[0] != second.name {
		t.Fatalf("left %v", left)
	}
	if !WithdrawAnswer(root, id, second) || WithdrawAnswer(root, id, second) || !AnswerTaken(root, id, second) {
		t.Fatal("withdrawn twice")
	}

	if _, ok := ReadAnswers(root, id); ok {
		t.Fatal("answers while the dialog is open")
	}
	fire(t, root, root, id, "answer", fixture(t, "answer-ask-several"))
	if got, ok := ReadAnswers(root, id); !ok || got["Which name?"] != "Gamma typed" {
		t.Fatalf("answers %v %v", got, ok)
	}

	for _, bad := range []string{"", "../x", "XYZ"} {
		if _, err := ReadQuestions(root, bad); err == nil {
			t.Errorf("%q: questions", bad)
		}
		if LeaveAnswer(root, bad, second, nil) == nil || AnswerTaken(root, bad, second) || WithdrawAnswer(root, bad, second) {
			t.Errorf("%q: an answer", bad)
		}
		if _, ok := ReadAnswers(root, bad); ok {
			t.Errorf("%q: answers", bad)
		}
	}
	if LeaveAnswer(root, id, Questions{}, nil) == nil || AnswerTaken(root, id, Questions{}) || WithdrawAnswer(root, id, Questions{}) {
		t.Error("an answer to no dialog")
	}
}
