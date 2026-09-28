package cli

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
)

// emptyBox is Claude waiting at its prompt box with nothing in it.
var emptyBox = "● Tests pass.\n" + strings.Repeat("─", 30) + "\n❯ \n" + strings.Repeat("─", 30) + "\n  ⏵⏵ bypass permissions on (shift+tab to cycle)\n"

// sendFakes has agent a, started by this hq, in the running sandbox
// claude-x, in pane %4, in state st (none: starting).
func sendFakes(st string) *fakes {
	f := newLsFakes()
	w := agentWindow("@4", "a", "/w/app", f.now.Add(-time.Minute), false)
	w.Pane, w.Options["inbox"] = "%4", inboxHooks
	f.tmux.windows = []tmux.Window{{ID: "@0", Name: "hq", Options: map[string]string{}}, w}
	if st != "" {
		f.states["id-a"] = state.Report{State: st, Since: f.now.Add(-time.Second)}
	}
	f.tmux.screens = map[string]string{"%4": emptyBox}
	return f
}

func TestSendToAWorkingAgentLeavesItForItsStop(t *testing.T) {
	f := sendFakes(state.Working)
	code, out, errOut := f.run("send", "a", "also run the linter")
	if code != 0 || out != "queued: a is working, delivered when it stops\n" {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	if got := strings.Join(f.inbox["id-a"], "|"); got != "also run the linter" {
		t.Fatalf("inbox %q", got)
	}
	if len(f.tmux.pasted) != 0 || len(f.tmux.submitted) != 0 {
		t.Fatal("typed into a working agent")
	}
}

func TestSendNowToAWorkingAgentGoesAfterItsNextToolCall(t *testing.T) {
	f := sendFakes(state.Working)
	code, out, _ := f.run("send", "--now", "a", "stop editing main.go")
	if code != 0 || out != "queued: a is working, delivered after its next tool call, or when it stops\n" {
		t.Fatalf("exit %d %q", code, out)
	}
	if got := strings.Join(f.inbox["id-a"], "|"); got != "stop editing main.go (now)" {
		t.Fatalf("inbox %q", got)
	}
}

func TestSendToAnAgentInADialogWaitsUntilTheDialogCloses(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"a", "hi"}, "queued: a needs input, delivered once its dialog is closed, when it stops\n"},
		{[]string{"a", "hi", "--now"}, "queued: a needs input, delivered once its dialog is closed, after its next tool call\n"},
	} {
		f := sendFakes(state.NeedsInput)
		// The dialog on its screen is never touched.
		f.tmux.screens["%4"] = "Do you want to proceed?\n❯ 1. Yes\n  2. No\nEsc to cancel\n"
		code, out, _ := f.run(append([]string{"send"}, tc.args...)...)
		if code != 0 || out != tc.want || len(f.inbox["id-a"]) != 1 || len(f.tmux.pasted) != 0 {
			t.Fatalf("%v: exit %d %q inbox %v pasted %v", tc.args, code, out, f.inbox, f.tmux.pasted)
		}
	}
}

func TestSendToAnAgentAtItsEmptyPromptTypesItInAsItsNextPrompt(t *testing.T) {
	for _, st := range []string{state.Done, state.Question} {
		f := sendFakes(st)
		f.inbox["id-a"] = []string{"sent earlier"}
		code, out, errOut := f.run("send", "a", "then open a PR\nwith the label bug")
		if code != 0 || out != "delivered: typed into a as its next prompt\n" {
			t.Fatalf("%s: exit %d %q %q", st, code, out, errOut)
		}
		// Oldest first, in one prompt, and gone from the inbox.
		if got := strings.Join(f.tmux.pasted, "|"); got != "%4 sent earlier\n\nthen open a PR\nwith the label bug" {
			t.Fatalf("%s: pasted %q", st, got)
		}
		if strings.Join(f.tmux.submitted, " ") != "%4" || len(f.inbox["id-a"]) != 0 {
			t.Fatalf("%s: submitted %v inbox %v", st, f.tmux.submitted, f.inbox)
		}
	}
}

func TestSendWhileTheUserTypesRidesAlongWithTheNextPrompt(t *testing.T) {
	for name, screen := range map[string]string{
		"typing":     strings.Replace(emptyBox, "❯ \n", "❯ half a thought\n", 1),
		"menu":       strings.Replace(emptyBox, "❯ \n", "❯ /\n", 1) + "  /help  Show help\n",
		"no box":     "anything",
		"not loaded": "",
	} {
		f := sendFakes(state.Done)
		f.tmux.screens["%4"] = screen
		if name == "not loaded" {
			delete(f.tmux.screens, "%4")
		}
		code, out, _ := f.run("send", "a", "hi")
		if code != 0 || out != "queued: a has something in its prompt box, delivered with its next prompt\n" {
			t.Fatalf("%s: exit %d %q", name, code, out)
		}
		if len(f.tmux.pasted) != 0 || len(f.inbox["id-a"]) != 1 {
			t.Fatalf("%s: pasted %v inbox %v", name, f.tmux.pasted, f.inbox)
		}
	}
	f := sendFakes(state.Done)
	f.tmux.screenErr = errors.New("no server")
	if code, out, _ := f.run("send", "a", "hi"); code != 0 || !strings.HasPrefix(out, "queued:") || len(f.inbox["id-a"]) != 1 {
		t.Fatalf("screen error: exit %d %q", code, out)
	}
}

func TestSendToAStartingAgentWaitsForItsFirstReport(t *testing.T) {
	f := sendFakes("")
	sleeps := 0
	f.onSleep = func() {
		if sleeps++; sleeps == 3 {
			f.states["id-a"] = state.Report{State: state.Done, Since: f.now}
		}
	}
	code, out, _ := f.run("send", "a", "hi")
	if code != 0 || out != "delivered: typed into a as its next prompt\n" || sleeps < 3 {
		t.Fatalf("exit %d %q after %d sleeps", code, out, sleeps)
	}
	// One that does not report in time leaves it to its hooks.
	f = sendFakes("")
	start := f.now
	code, out, _ = f.run("send", "a", "hi")
	if code != 0 || out != "queued: a is starting, delivered with its first prompt, or when it stops\n" || f.now.Sub(start) < startWait {
		t.Fatalf("exit %d %q after %v", code, out, f.now.Sub(start))
	}
	// Its first prompt was typed in the meantime: working, it stops later.
	f = sendFakes("")
	f.onSleep = func() { f.states["id-a"] = state.Report{State: state.Working, Since: f.now} }
	if _, out, _ = f.run("send", "a", "hi"); out != "queued: a is working, delivered when it stops\n" {
		t.Fatalf("%q", out)
	}
}

func TestSendSaysWhenTheHooksAlreadyTookIt(t *testing.T) {
	// The hook takes it between hq's post and its look at the inbox.
	f := sendFakes(state.Working)
	d := f.deps()
	d.pending = func(string, string) int { return 0 }
	var out strings.Builder
	if err := runSend(Env{Stdout: &out}, d, []string{"a", "hi"}); err != nil || out.String() != "delivered: a took it with its hooks\n" {
		t.Fatalf("%v %q", err, out.String())
	}
	// At rest, but the hooks of a turn just begun took it first.
	f = sendFakes(state.Done)
	d = f.deps()
	d.takeMessages = func(string, string) ([]string, error) { return nil, nil }
	out.Reset()
	if err := runSend(Env{Stdout: &out}, d, []string{"a", "hi"}); err != nil || out.String() != "delivered: a took it with its hooks\n" || len(f.tmux.pasted) != 0 {
		t.Fatalf("%v %q", err, out.String())
	}
}

func TestSendKeepsTheMessagesWhenTypingThemInFails(t *testing.T) {
	f := sendFakes(state.Done)
	f.tmux.pasteErr = errors.New("no pane %4")
	code, _, errOut := f.run("send", "a", "hi")
	if code != ExitEnvironment || errOut != "hq: no pane %4\n" {
		t.Fatalf("exit %d %q", code, errOut)
	}
	if got := strings.Join(f.inbox["id-a"], "|"); got != "hi" || len(f.tmux.submitted) != 0 {
		t.Fatalf("inbox %q submitted %v", got, f.tmux.submitted)
	}
	f = sendFakes(state.Done)
	f.takeErr = errors.New("permission denied")
	if code, _, errOut := f.run("send", "a", "hi"); code != ExitEnvironment || !strings.Contains(errOut, "cannot take the messages for a: permission denied") {
		t.Fatalf("exit %d %q", code, errOut)
	}
	f = sendFakes(state.Working)
	f.postErr = errors.New("read-only file system")
	if code, _, errOut := f.run("send", "a", "hi"); code != ExitEnvironment || errOut != "hq: cannot leave the message for a: read-only file system\n" {
		t.Fatalf("exit %d %q", code, errOut)
	}
}

func TestSendRefusesAnAgentThatCannotReceive(t *testing.T) {
	f := sendFakes(state.Done)
	f.tmux.windows[1].PaneDead = true
	code, _, errOut := f.run("send", "a", "hi")
	if code != ExitUsage || errOut != "hq: a has ended and cannot receive messages; relaunch it with hq sandbox restart app, or kill it and start it again\n" {
		t.Fatalf("exit %d %q", code, errOut)
	}
	// Hooks from an older hq ignore the inbox.
	f = sendFakes(state.Done)
	delete(f.tmux.windows[1].Options, "inbox")
	code, _, errOut = f.run("send", "a", "hi")
	if code != ExitUsage || errOut != "hq: a was started by an older hq and cannot receive messages; relaunch it with hq sandbox restart app, or kill it and start it again\n" {
		t.Fatalf("exit %d %q", code, errOut)
	}
	if len(f.inbox) != 0 {
		t.Fatalf("left a message it cannot deliver: %v", f.inbox)
	}
}

func TestSendUsage(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
		err  string
	}{
		{[]string{"send"}, ExitUsage, "hq: usage: hq send NAME TEXT [--now]; quote the text\n"},
		{[]string{"send", "a"}, ExitUsage, "hq: usage: hq send NAME TEXT [--now]; quote the text\n"},
		{[]string{"send", "a", "run", "tests"}, ExitUsage, "hq: usage: hq send NAME TEXT [--now]; quote the text\n"},
		{[]string{"send", "a", "hi", "-f"}, ExitUsage, "hq: unknown option '-f' (usage: hq send NAME TEXT [--now])\n"},
		{[]string{"send", "a", " \x1b[31m \n"}, ExitUsage, "hq: empty message (usage: hq send NAME TEXT [--now])\n"},
		{[]string{"send", "a", strings.Repeat("x", state.MaxMessage+1)}, ExitUsage, "hq: message too long: at most 8192 bytes\n"},
		{[]string{"send", "nobody", "hi"}, ExitNotFound, "hq: no agent 'nobody' (see hq ls)\n"},
	} {
		f := sendFakes(state.Working)
		code, _, errOut := f.run(tc.args...)
		if code != tc.code || errOut != tc.err {
			t.Errorf("%q: exit %d %q", tc.args, code, errOut)
		}
		if len(f.inbox) != 0 {
			t.Errorf("%q: inbox %v", tc.args, f.inbox)
		}
	}
	// A message that starts with a dash is the text, not an option.
	f := sendFakes(state.Working)
	if code, _, errOut := f.run("send", "a", "-v is not a flag here"); code != 0 || f.inbox["id-a"][0] != "-v is not a flag here" {
		t.Fatalf("exit %d %q %v", code, errOut, f.inbox)
	}
}

func TestSendNeedsTmux(t *testing.T) {
	f := sendFakes(state.Working)
	f.tmux.version = "2.9"
	if code, _, _ := f.run("send", "a", "hi"); code != ExitEnvironment || len(f.inbox) != 0 {
		t.Fatalf("exit %d", code)
	}
}

func TestKillRemovesTheMessagesWaitingForIt(t *testing.T) {
	f := sendFakes(state.Working)
	f.inbox["id-a"] = []string{"never delivered"}
	if code, _, _ := f.run("kill", "a", "-y"); code != 0 || len(f.inbox) != 0 || strings.Join(f.removed, ",") != "/w/app id-a" {
		t.Fatalf("exit %d inbox %v removed %v", code, f.inbox, f.removed)
	}
}

func TestQueuedSaysWhenAnEndedAgentsMessagesGo(t *testing.T) {
	f := sendFakes(state.Done)
	as, _ := collect(f.deps())
	a := as[0]
	a.State = state.Ended
	if got := queued(a, false); got != "queued: a has ended; delivered once it is relaunched" {
		t.Fatal(got)
	}
}
