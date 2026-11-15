package cli

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/agent"
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
	w.Pane, w.Options["inbox"] = "%4", generation
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
	d.takeMessages = func(string, string) ([]state.Message, error) { return nil, nil }
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
	// An outdated agent: nothing is left for it (#194).
	f = sendFakes(state.Done)
	delete(f.tmux.windows[1].Options, genOption)
	code, _, errOut = f.run("send", "a", "hi")
	if code != ExitUsage || errOut != "hq: a was started by an older hq: kill it and start it again\n" || len(f.inbox["id-a"]) != 0 {
		t.Fatalf("exit %d %q, inbox %v", code, errOut, f.inbox)
	}
	// An agent of a setup's sandbox is relaunched with its setup (#136).
	f = sendFakes(state.Done)
	f.tmux.windows[1].PaneDead = true
	f.tmux.windows[1].Options["sandbox"] = "hq-local-app"
	code, _, errOut = f.run("send", "a", "hi")
	if code != ExitUsage || errOut != "hq: a has ended and cannot receive messages; relaunch it with hq sandbox restart app --setup local, or kill it and start it again\n" {
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
		{[]string{"send"}, ExitUsage, "hq: usage: hq send NAME TEXT [--now] [--json]; quote the text\n"},
		{[]string{"send", "a"}, ExitUsage, "hq: usage: hq send NAME TEXT [--now] [--json]; quote the text\n"},
		{[]string{"send", "a", "run", "tests"}, ExitUsage, "hq: usage: hq send NAME TEXT [--now] [--json]; quote the text\n"},
		{[]string{"send", "a", "hi", "-f"}, ExitUsage, "hq: unknown option '-f' (usage: hq send NAME TEXT [--now] [--json])\n"},
		{[]string{"send", "a", " \x1b[31m \n"}, ExitUsage, "hq: empty message (usage: hq send NAME TEXT [--now] [--json])\n"},
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
	as, _ := lookNow(f.deps())
	a := as[0]
	a.State = state.Ended
	if got := queued(a, false); got != "queued: a has ended; delivered once it is relaunched" {
		t.Fatal(got)
	}
}

func TestSendTypesIntoABoxShowingOnlyClaudesFaintHint(t *testing.T) {
	// A session just started shows a placeholder in its empty box.
	f := sendFakes(state.Done)
	f.tmux.screens["%4"] = strings.Replace(emptyBox, "❯ \n", "\x1b[39m❯ \x1b[2mTry \"fix lint errors\"\x1b[0m\n", 1)
	if code, out, _ := f.run("send", "a", "hi"); code != 0 || out != "delivered: typed into a as its next prompt\n" || len(f.tmux.pasted) != 1 {
		t.Fatalf("exit %d %q", code, out)
	}
}

func TestSendToAnAgentWaitingForItsSubagentsTypesItInAsItsNextPrompt(t *testing.T) {
	f := sendFakes("")
	f.states["id-a"] = state.Report{State: state.Working, Since: f.now.Add(-time.Minute), Latest: f.now.Add(-time.Second), Background: true}
	code, out, errOut := f.run("send", "a", "also run the linter")
	if code != 0 || out != "delivered: typed into a as its next prompt\n" {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	if got := strings.Join(f.tmux.pasted, "|"); got != "%4 also run the linter" || strings.Join(f.tmux.submitted, " ") != "%4" {
		t.Fatalf("pasted %q submitted %v", got, f.tmux.submitted)
	}
	// With the user typing there it waits for the next prompt, as for done.
	f = sendFakes("")
	f.states["id-a"] = state.Report{State: state.Working, Since: f.now.Add(-time.Minute), Latest: f.now.Add(-time.Second), Background: true}
	f.tmux.screens["%4"] = strings.Replace(emptyBox, "❯ ", "❯ a draft", 1)
	if code, out, _ := f.run("send", "a", "hi"); code != 0 || out != "queued: a has something in its prompt box, delivered with its next prompt\n" || len(f.tmux.pasted) != 0 {
		t.Fatalf("typing: exit %d %q", code, out)
	}
}

// slashScreens are Claude Code 2.1.304 right after hq sent /model (#125):
// the command still in the box, its panel in place of the box, then the
// box empty and at rest.
func slashScreens(t *testing.T) (inBox, panel, settled string) {
	return readFixture(t, "screen-styled-model-in-box.txt"), readFixture(t, "screen-styled-model-panel.txt"), readFixture(t, "screen-styled-model-settled.txt")
}

func TestSendRightAfterASlashCommandWaitsForTheBoxToSettle(t *testing.T) {
	inBox, panel, settled := slashScreens(t)
	f := sendFakes(state.Done)
	f.tmux.screens["%4"] = inBox
	start := f.now
	var settledAt, submittedAt time.Time
	f.onSleep = func() {
		switch since := f.now.Sub(start); {
		case since >= 2500*time.Millisecond:
			if settledAt.IsZero() {
				settledAt = f.now
			}
			f.tmux.screens["%4"] = settled
		case since >= 400*time.Millisecond:
			f.tmux.screens["%4"] = panel
		}
	}
	took := f.tmux.onSubmit
	f.tmux.onSubmit = func() { submittedAt = f.now; took() }
	code, out, errOut := f.run("send", "a", "/effort medium")
	if code != 0 || out != "delivered: typed into a as its next prompt\n" {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	if got := strings.Join(f.tmux.pasted, "|"); got != "%4 /effort medium" || len(f.inbox["id-a"]) != 0 {
		t.Fatalf("pasted %q inbox %v", got, f.inbox)
	}
	if settledAt.IsZero() || submittedAt.Before(settledAt) {
		t.Fatalf("typed at %v, the box settled at %v", submittedAt.Sub(start), settledAt.Sub(start))
	}
	// A box that never settles keeps the message for the next prompt, once
	// hq send has waited for it a while.
	f = sendFakes(state.Done)
	f.tmux.screens["%4"] = inBox
	start = f.now
	code, out, _ = f.run("send", "a", "/effort medium")
	if code != 0 || out != "queued: a has something in its prompt box, delivered with its next prompt; if its hooks deliver it, the slash command arrives as text and does not run\n" || len(f.tmux.pasted) != 0 {
		t.Fatalf("exit %d %q pasted %v", code, out, f.tmux.pasted)
	}
	if waited := f.now.Sub(start); waited < boxWait || waited > boxWait+time.Second {
		t.Fatalf("waited %v", waited)
	}
}

func TestSendReturnsOnceClaudeTookWhatItTypedIn(t *testing.T) {
	inBox, panel, _ := slashScreens(t)
	f := sendFakes(state.Done)
	var takenAt time.Time
	f.tmux.onSubmit = func() { f.tmux.screens["%4"] = inBox }
	f.onSleep = func() {
		if len(f.tmux.submitted) > 0 && f.tmux.screens["%4"] == inBox {
			if takenAt.IsZero() {
				takenAt = f.now.Add(400 * time.Millisecond)
			} else if !f.now.Before(takenAt) {
				f.tmux.screens["%4"] = panel
			}
		}
	}
	code, out, _ := f.run("send", "a", "/model claude-opus-5-5")
	if code != 0 || out != "delivered: typed into a as its next prompt\n" {
		t.Fatalf("exit %d %q", code, out)
	}
	// So the next send meets the command's panel, not the command.
	if f.tmux.screens["%4"] != panel || f.now.Sub(takenAt) > time.Second {
		t.Fatalf("returned %v after Claude took it", f.now.Sub(takenAt))
	}
	// Not taken within a while: delivered all the same.
	f = sendFakes(state.Done)
	f.tmux.onSubmit = func() { f.tmux.screens["%4"] = inBox }
	start := f.now
	if code, out, _ := f.run("send", "a", "/model claude-opus-5-5"); code != 0 || out != "delivered: typed into a as its next prompt\n" {
		t.Fatalf("exit %d %q", code, out)
	}
	if waited := f.now.Sub(start); waited < leaveWait || waited > leaveWait+boxWait {
		t.Fatalf("waited %v", waited)
	}
}

// waitingFakes has agent a done at its empty prompt, with a message that has
// waited a minute in its inbox.
func waitingFakes() *fakes {
	f := sendFakes(state.Done)
	f.inbox["id-a"] = []string{"/effort medium"}
	f.oldest["id-a"] = f.now.Add(-time.Minute)
	return f
}

func TestAMessageLeftWaitingForAnAgentAtItsPromptIsTypedInWhenHqLooks(t *testing.T) {
	for _, args := range [][]string{{"ls"}, {"ls", "--json"}, {"read", "a"}, {"wait", "a", "--timeout", "1s"}} {
		f := waitingFakes()
		if code, _, errOut := f.run(args...); code != 0 {
			t.Fatalf("%v: exit %d %q", args, code, errOut)
		}
		if got := strings.Join(f.tmux.pasted, "|"); got != "%4 /effort medium" || strings.Join(f.tmux.submitted, " ") != "%4" || len(f.inbox["id-a"]) != 0 {
			t.Fatalf("%v: pasted %q submitted %v inbox %v", args, got, f.tmux.submitted, f.inbox)
		}
	}
	// So does the list's.
	f := waitingFakes()
	src := listSource(f.deps(), "%1")
	as, err := src.Agents(agent.Sandboxes{})
	if err != nil {
		t.Fatal(err)
	}
	src.Deliver(as)
	if got := strings.Join(f.tmux.pasted, "|"); got != "%4 /effort medium" || len(f.inbox["id-a"]) != 0 {
		t.Fatalf("list: pasted %q inbox %v", got, f.inbox)
	}
}

func TestAMessageLeftWaitingIsTypedInOnlyAtAnEmptyBoxAtRest(t *testing.T) {
	inBox, panel, _ := slashScreens(t)
	for name, change := range map[string]func(f *fakes){
		// hq send is still on it.
		"just sent": func(f *fakes) { f.oldest["id-a"] = f.now.Add(-time.Second) },
		"working":   func(f *fakes) { f.states["id-a"] = state.Report{State: state.Working, Since: f.now.Add(-time.Second)} },
		"typing":    func(f *fakes) { f.tmux.screens["%4"] = strings.Replace(emptyBox, "❯ \n", "❯ half a thought\n", 1) },
		"in box":    func(f *fakes) { f.tmux.screens["%4"] = inBox },
		"panel":     func(f *fakes) { f.tmux.screens["%4"] = panel },
		"outdated":  func(f *fakes) { f.tmux.windows[1].Options[genOption] = "" },
	} {
		f := waitingFakes()
		change(f)
		start := f.now
		if code, _, errOut := f.run("ls"); code != 0 {
			t.Fatalf("%s: exit %d %q", name, code, errOut)
		}
		if len(f.tmux.pasted) != 0 || len(f.inbox["id-a"]) != 1 {
			t.Fatalf("%s: pasted %v inbox %v", name, f.tmux.pasted, f.inbox)
		}
		// One look, not hq send's wait.
		if waited := f.now.Sub(start); waited >= boxWait {
			t.Fatalf("%s: waited %v", name, waited)
		}
	}
}

func TestSendDoesNotTypeIntoABoxClaudeIsAboutToRedraw(t *testing.T) {
	// A prompt just taken: the box empty, no spinner yet, for one look;
	// then Claude at work on it, its hooks not yet reported.
	f := sendFakes(state.Done)
	f.onSleep = func() {
		f.tmux.screens["%4"] = strings.Replace(emptyBox, "● Tests pass.\n", "● Tests pass.\n\n✶ Brewing… (1s)\n", 1)
	}
	code, out, _ := f.run("send", "a", "hi")
	if code != 0 || out != "queued: a has something in its prompt box, delivered with its next prompt\n" || len(f.tmux.pasted) != 0 {
		t.Fatalf("exit %d %q pasted %v", code, out, f.tmux.pasted)
	}
}

// #145: a done agent whose box holds only Claude's faint suggestion, under
// a footer of four lines (a notice above the status line, Claude Code
// 2.1.317 in the sbx template), takes the message as its next prompt.
func TestSendTypesIntoABoxWithASuggestionAboveAFooterWithANotice(t *testing.T) {
	f := sendFakes(state.Done)
	f.tmux.screens["%4"] = readFixture(t, "screen-styled-agent-notice-suggestion.txt")
	if code, out, _ := f.run("send", "a", "hi"); code != 0 || out != "delivered: typed into a as its next prompt\n" || len(f.tmux.pasted) != 1 {
		t.Fatalf("exit %d %q pasted %v", code, out, f.tmux.pasted)
	}
}

// A message that starts with a slash command and that Claude Code would
// collapse into a placeholder, so that it is no command (state.Collapses),
// is typed as its command word and the rest pasted after it: the command
// runs with the rest as its arguments, whatever the length (#173).
func TestSendTypesACommandOverTheLimitsAsItsWordAndPastesTheRest(t *testing.T) {
	long := strings.Repeat("x", 900)
	for name, tc := range map[string]struct{ text, typed, pasted string }{
		"more than 3 lines":           {"/probe one\ntwo\nthree\nfour", "/probe", " one\ntwo\nthree\nfour"},
		"over 800 characters":         {"/probe " + long, "/probe", " " + long},
		"a first line over the limit": {"/probe " + long + "\nmore", "/probe", " " + long + "\nmore"},
		"the command on a line alone": {"/probe\none\ntwo\nthree", "/probe", "\none\ntwo\nthree"},
	} {
		f := sendFakes(state.Done)
		code, out, errOut := f.run("send", "a", tc.text)
		if code != 0 || out != "delivered: typed into a as its next prompt\n" {
			t.Fatalf("%s: exit %d %q %q", name, code, out, errOut)
		}
		want := []string{"type %4 " + tc.typed, "paste %4 " + tc.pasted, "enter %4"}
		if strings.Join(f.tmux.input, "|") != strings.Join(want, "|") {
			t.Errorf("%s: input %q, want %q", name, f.tmux.input, want)
		}
	}
}

// A command word over the limit itself is typed too: Type keeps each part
// under it.
func TestSendTypesACommandWithNothingAfterItWhole(t *testing.T) {
	f := sendFakes(state.Done)
	word := "/" + strings.Repeat("x", 900)
	if code, _, errOut := f.run("send", "a", word); code != 0 {
		t.Fatalf("exit %d %q", code, errOut)
	}
	if got := strings.Join(f.tmux.input, "|"); got != "type %4 "+word+"|enter %4" {
		t.Fatalf("input %q", got)
	}
}

// A short command and a plain message of any length are pasted as before:
// Claude Code runs the one and gives the model the other whole.
func TestSendPastesAShortCommandAndAPlainMessageWhole(t *testing.T) {
	for _, text := range []string{
		"/model claude-opus-5-5",
		"/probe one\ntwo\nthree",
		"plain\nwith\nfour\nlines",
		strings.Repeat("plain ", 200),
		"a path /like/this\nand\nmore\nlines",
	} {
		f := sendFakes(state.Done)
		if code, out, _ := f.run("send", "a", text); code != 0 || out != "delivered: typed into a as its next prompt\n" {
			t.Fatalf("%.20q: exit %d %q", text, code, out)
		}
		if got := strings.Join(f.tmux.input, "|"); got != "paste %4 "+strings.TrimSpace(text)+"|enter %4" {
			t.Errorf("%.20q: input %.60q", text, got)
		}
	}
}

// With several messages waiting, a slash command is a prompt of its own,
// never joined behind another message's text: hq types the first prompt
// in and holds the others, out of the hooks' reach, for the agent's next
// prompts, one at a time and in the order sent (#173).
func TestSendTypesAWaitingCommandAsAPromptOfItsOwnInTheOrderSent(t *testing.T) {
	f := sendFakes(state.Done)
	f.inbox["id-a"] = []string{"first", "second", "/probe third", "/clear"}
	code, out, _ := f.run("send", "a", "fourth")
	if code != 0 || out != "queued: typed an earlier message into a as its next prompt; this one follows as a prompt of its own once a is back at its prompt\n" {
		t.Fatalf("exit %d %q", code, out)
	}
	if got := strings.Join(f.tmux.input, "|"); got != "paste %4 first\n\nsecond|enter %4" {
		t.Fatalf("input %q", got)
	}
	if got := strings.Join(f.held["id-a"], "|"); got != "/probe third|/clear|fourth" || len(f.inbox["id-a"]) != 0 {
		t.Fatalf("held %q inbox %q", got, f.inbox["id-a"])
	}
	// The next looks at the agent back at its prompt type in the next
	// prompt each.
	for i, want := range []string{"paste %4 /probe third", "paste %4 /clear", "paste %4 fourth"} {
		f.tmux.input = nil
		f.now = f.now.Add(time.Minute)
		if code, _, errOut := f.run("ls"); code != 0 {
			t.Fatalf("ls: exit %d %s", code, errOut)
		}
		if got := strings.Join(f.tmux.input, "|"); got != want+"|enter %4" {
			t.Fatalf("look %d: input %q, want %q", i, got, want)
		}
	}
	if len(f.held["id-a"]) != 0 {
		t.Fatalf("held %q", f.held["id-a"])
	}
	// The message just sent is typed in when it is the first prompt, and
	// those after it are held.
	f = sendFakes(state.Done)
	f.inbox["id-a"] = []string{"/probe first"}
	if code, out, _ := f.run("send", "a", "second"); code != 0 || !strings.HasPrefix(out, "queued: typed an earlier message into a") {
		t.Fatalf("exit %d %q", code, out)
	}
	if got := strings.Join(f.tmux.input, "|"); got != "paste %4 /probe first|enter %4" || strings.Join(f.held["id-a"], "|") != "second" {
		t.Fatalf("input %q held %q", got, f.held["id-a"])
	}
}

func TestSendLeavesTheOthersToTheHooksWhenHoldingFails(t *testing.T) {
	f := sendFakes(state.Done)
	f.inbox["id-a"] = []string{"first"}
	f.holdErr = errors.New("disk full")
	code, _, errOut := f.run("send", "a", "/probe second")
	if code == 0 || !strings.Contains(errOut, "typed an earlier message into a, but cannot hold the others for its next prompts (disk full): they are left to its hooks") {
		t.Fatalf("exit %d %q", code, errOut)
	}
	if got := strings.Join(f.tmux.input, "|"); got != "paste %4 first|enter %4" || strings.Join(f.inbox["id-a"], "|") != "/probe second" {
		t.Fatalf("input %q inbox %q", got, f.inbox["id-a"])
	}
	// With nothing to hold, holding is not tried.
	f = sendFakes(state.Done)
	f.holdErr = errors.New("disk full")
	if code, out, errOut := f.run("send", "a", "/probe alone"); code != 0 || out != "delivered: typed into a as its next prompt\n" {
		t.Fatalf("nothing held: exit %d %q %q", code, out, errOut)
	}
}

// A failure to type the first prompt in puts every message back for the
// hooks, in the order sent, none of them held (#173).
func TestSendPutsEveryMessageBackInOrderWhenTypingFails(t *testing.T) {
	for name, fail := range map[string]func(*fakeTmux){
		"paste": func(t *fakeTmux) { t.pasteErr = errors.New("no pane") },
		"type":  func(t *fakeTmux) { t.typeErr = errors.New("no pane") },
	} {
		f := sendFakes(state.Done)
		f.inbox["id-a"] = []string{"/probe one\ntwo\nthree\nfour", "second"}
		fail(f.tmux)
		if code, _, _ := f.run("send", "a", "/clear"); code == 0 {
			t.Fatalf("%s: exit 0", name)
		}
		if got := strings.Join(f.inbox["id-a"], "|"); got != "/probe one\ntwo\nthree\nfour|second|/clear" || len(f.held["id-a"]) != 0 {
			t.Fatalf("%s: inbox %q held %q", name, got, f.held["id-a"])
		}
	}
}

// A paste that fails after the command word was typed takes the word back
// out of the box, so the box is empty again for the user and the next send.
func TestSendTakesTheCommandWordBackWhenThePasteAfterItFails(t *testing.T) {
	f := sendFakes(state.Done)
	f.tmux.pasteErr = errors.New("no pane")
	if code, _, _ := f.run("send", "a", "/probe one\ntwo\nthree\nfour"); code == 0 {
		t.Fatal("exit 0")
	}
	if got := strings.Join(f.tmux.input, "|"); got != "type %4 /probe|press %4 C-u" {
		t.Fatalf("input %q", got)
	}
}

// With messages held for an agent that is not at its prompt, a new message
// its hooks take at once is still said to be taken: the held ones are not
// the hooks' (#173).
func TestSendSaysTheHooksTookItWhileOthersAreHeld(t *testing.T) {
	f := sendFakes(state.Working)
	f.held["id-a"] = []string{"/probe held"}
	d := f.deps()
	d.postMessage = func(string, string, state.Message, bool) error { return nil }
	var out strings.Builder
	if err := runSend(Env{Stdout: &out}, d, []string{"a", "/probe go"}); err != nil || out.String() != "delivered: a took it with its hooks, as text, so the slash command did not run\n" {
		t.Fatalf("%v %q", err, out.String())
	}
	// One left to the hooks beside the held: queued.
	out.Reset()
	if err := runSend(Env{Stdout: &out}, f.deps(), []string{"a", "go"}); err != nil || out.String() != "queued: a is working, delivered when it stops\n" {
		t.Fatalf("%v %q", err, out.String())
	}
}

// A message that starts with a slash command and that the agent's hooks
// deliver reaches Claude as text, so the command does not run: hq says so
// wherever it leaves one to them (#173), and not where it types it in.
func TestSendSaysAnyCommandItLeavesToTheHooksArrivesAsText(t *testing.T) {
	const asText = "; if its hooks deliver it, the slash command arrives as text and does not run\n"
	for name, tc := range map[string]struct {
		state  string
		screen string
		args   []string
		want   string
	}{
		"working":     {state.Working, emptyBox, nil, "queued: a is working, delivered when it stops" + asText},
		"working now": {state.Working, emptyBox, []string{"--now"}, "queued: a is working, delivered after its next tool call, or when it stops" + asText},
		"dialog":      {state.NeedsInput, "Do you want to proceed?\n❯ 1. Yes\n  2. No\nEsc to cancel\n", nil, "queued: a needs input, delivered once its dialog is closed, when it stops" + asText},
		"box":         {state.Done, strings.Replace(emptyBox, "❯ \n", "❯ half a thought\n", 1), nil, "queued: a has something in its prompt box, delivered with its next prompt" + asText},
	} {
		f := sendFakes(tc.state)
		f.tmux.screens["%4"] = tc.screen
		code, out, _ := f.run(append([]string{"send", "a", "/probe go"}, tc.args...)...)
		if code != 0 || out != tc.want {
			t.Errorf("%s: exit %d %q, want %q", name, code, out, tc.want)
		}
		// A plain message: the line as before.
		f = sendFakes(tc.state)
		f.tmux.screens["%4"] = tc.screen
		if _, out, _ := f.run(append([]string{"send", "a", "go"}, tc.args...)...); out != strings.TrimSuffix(tc.want, asText)+"\n" {
			t.Errorf("%s, plain: %q", name, out)
		}
	}
	// --json carries the same line.
	f := sendFakes(state.Working)
	if _, out, _ := f.run("send", "a", "/probe go", "--json"); !strings.Contains(out, `"delivery": "queued: a is working, delivered when it stops`+strings.TrimSuffix(asText, "\n")+`"`) {
		t.Errorf("json: %q", out)
	}
	// Starting.
	f = sendFakes("")
	if _, out, _ := f.run("send", "a", "/probe go"); out != "queued: a is starting, delivered with its first prompt, or when it stops"+asText {
		t.Errorf("starting: %q", out)
	}
	// Taken by the hooks at once, while it works or as a turn begins: it
	// did not run.
	const taken = "delivered: a took it with its hooks, as text, so the slash command did not run\n"
	d := sendFakes(state.Working).deps()
	d.pending = func(string, string) int { return 0 }
	var out strings.Builder
	if err := runSend(Env{Stdout: &out}, d, []string{"a", "/probe go"}); err != nil || out.String() != taken {
		t.Errorf("taken: %v %q", err, out.String())
	}
	d = sendFakes(state.Done).deps()
	d.takeMessages = func(string, string) ([]state.Message, error) { return nil, nil }
	out.Reset()
	if err := runSend(Env{Stdout: &out}, d, []string{"a", "/probe go"}); err != nil || out.String() != taken {
		t.Errorf("taken at rest: %v %q", err, out.String())
	}
}
