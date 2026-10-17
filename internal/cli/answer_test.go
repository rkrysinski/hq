package cli

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/state"
)

// stateFixture is a hook payload of state's testdata, as the hook writes it.
func stateFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("../state/testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	return append([]byte("branch main\n"), b...)
}

// askFakes has agent a, started by this hq, at the question dialog of the
// payload fixture (dialog-ask asks one question, dialog-ask-several three,
// the second multi-select), reported a minute ago; it returns the
// dialog's id. No hook holds on it (see holding).
func askFakes(t *testing.T, fixture string) (*fakes, string) {
	t.Helper()
	f := sendFakes(state.NeedsInput)
	since := f.now.Add(-time.Minute)
	q, err := state.ParseQuestions(stateFixture(t, fixture))
	if err != nil {
		t.Fatal(err)
	}
	q.Since = since
	f.questions["id-a"] = q
	f.states["id-a"] = state.Report{State: state.NeedsInput, Since: since, Last: q.Ask.Questions[0].Question, LastStop: "On it."}
	f.details["id-a"] = state.Detail{Reply: "On it.", Ask: q.Ask}
	return f, state.DialogID(since)
}

// declinedScreen is the agent's screen after its question dialog was
// cancelled, as Esc or a deny that interrupts leaves it (#89).
var declinedScreen = "●\u00a0User declined to answer questions\n  ⎿  · Which colour do you pick: red or blue? (Red / Blue)\n" + strings.Repeat("─", 30) + "\n❯ \n" + strings.Repeat("─", 30) + "\n  ⏵⏵ bypass permissions on (shift+tab to cycle)\n"

// decision is a decision as Claude reads it.
type decision struct {
	HookSpecificOutput struct {
		Decision struct {
			Behavior     string
			Interrupt    bool
			UpdatedInput struct{ Answers map[string]any }
		}
	}
}

// holding has a hook hold on agent a's dialog: at hq's next look it takes
// the answer left, and Claude acts on it as #89 saw: an allow closes the
// dialog, Claude reporting the answers (a list joined with ", ") as the
// agent works on; a deny that interrupts ends the turn as Esc does, which
// only the screen shows. reported, when set, is what Claude reports
// instead.
func (f *fakes) holding(reported map[string]string) {
	f.onSleep = func() {
		left := f.answerLeft["id-a"]
		if left == nil {
			return
		}
		delete(f.answerLeft, "id-a")
		var d decision
		_ = json.Unmarshal(left, &d)
		if d.HookSpecificOutput.Decision.Behavior != "allow" {
			f.tmux.screens["%4"] = declinedScreen
			return
		}
		got := map[string]string{}
		for q, a := range d.HookSpecificOutput.Decision.UpdatedInput.Answers {
			if labels, ok := a.([]any); ok {
				var ss []string
				for _, l := range labels {
					ss = append(ss, l.(string))
				}
				a = strings.Join(ss, ", ")
			}
			got[q] = a.(string)
		}
		if reported != nil {
			got = reported
		}
		f.got["id-a"] = got
		f.states["id-a"] = state.Report{State: state.Working, Since: f.now, Last: "On it."}
	}
}

func TestAnswerAnswersTheQuestionDialogAsAtTheTerminal(t *testing.T) {
	f, id := askFakes(t, "dialog-ask")
	f.holding(nil)
	code, out, errOut := f.run("answer", "a", "--dialog", id, "2")
	if code != 0 || out != "answered: a is working\n" {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	if want := map[string]string{"Which colour do you pick: red or blue?": "Blue"}; !reflect.DeepEqual(f.got["id-a"], want) {
		t.Fatalf("Claude got %v", f.got["id-a"])
	}
	// Nothing typed: the answer went through the hook alone.
	if len(f.tmux.pasted) != 0 || len(f.tmux.submitted) != 0 || len(f.answerLeft) != 0 {
		t.Fatalf("pasted %v submitted %v left %v", f.tmux.pasted, f.tmux.submitted, f.answerLeft)
	}
}

// Several questions take one answer each, in the dialog's order: a number,
// numbers joined with commas where several may be picked, free text.
func TestAnswerGivesEveryQuestionItsAnswer(t *testing.T) {
	f, id := askFakes(t, "dialog-ask-several")
	f.holding(nil)
	code, out, errOut := f.run("answer", "a", "--dialog="+id, "2", "1,3", "Gamma, typed by hq", "--json")
	if code != 0 {
		t.Fatalf("exit %d %q", code, errOut)
	}
	want := map[string]string{"Which color?": "Blue", "Which sizes?": "Small, Large", "Which name?": "Gamma, typed by hq"}
	if !reflect.DeepEqual(f.got["id-a"], want) {
		t.Fatalf("Claude got %v", f.got["id-a"])
	}
	// The state once it left the dialog, and the moment before the answer
	// was left, for hq wait --since.
	var v struct {
		State     string    `json:"state"`
		NextSince time.Time `json:"next_since"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil || v.State != state.Working || !v.NextSince.Equal(lookedAt(time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC))) {
		t.Fatalf("%q: %+v %v", out, v, err)
	}
}

func TestAnswerCancelEndsTheTurnAsEscDoes(t *testing.T) {
	f, id := askFakes(t, "dialog-ask")
	f.holding(nil)
	code, out, errOut := f.run("answer", "a", "--cancel", "--dialog", id)
	if code != 0 || out != "cancelled: a is done\n" {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	if _, out, _ := f.run("ls"); !strings.Contains(out, "User declined to answer questions") {
		t.Fatalf("ls:\n%s", out)
	}
}

// The answer names its dialog: one for another, or none, is refused naming
// the open one, and nothing reaches the agent.
func TestAnswerNamesTheOpenDialog(t *testing.T) {
	f, id := askFakes(t, "dialog-ask")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"answer", "a", "2"}, "hq: the dialog's id is required: a is at dialog " + id + " (Which colour do you pick: red or blue?)\n"},
		{[]string{"answer", "a", "--dialog", "abc123", "2"}, "hq: dialog abc123 is not open: a is at dialog " + id + " now (Which colour do you pick: red or blue?); read it (hq read a) before answering\n"},
	} {
		if code, _, errOut := f.run(tc.args...); code != ExitUsage || errOut != tc.want {
			t.Errorf("%v: exit %d %q", tc.args, code, errOut)
		}
	}
	// The dialog changed between the look and the read of its questions.
	q := f.questions["id-a"]
	q.Since = q.Since.Add(time.Second)
	f.questions["id-a"] = q
	if code, _, errOut := f.run("answer", "a", "--dialog", id, "2"); code != ExitUsage || !strings.Contains(errOut, "a's dialog changed meanwhile") {
		t.Errorf("changed: exit %d %q", code, errOut)
	}
	if len(f.answerLeft) != 0 {
		t.Fatalf("left %v", f.answerLeft)
	}
}

// An answer that does not fit the dialog is refused whole.
func TestAnswerRefusesAShapeThatDoesNotFit(t *testing.T) {
	f, id := askFakes(t, "dialog-ask-several")
	for _, tc := range []struct {
		answers []string
		want    string
	}{
		{[]string{"1", "2"}, "the dialog has 3 questions, 2 answers given"},
		{[]string{"3", "1", "x"}, "Which color? has no option 3"},
		{[]string{"1,2", "1", "x"}, "Which color? takes one option, 2 given"},
		{[]string{"1", "2,2", "x"}, "Which sizes?: option 2 twice"},
	} {
		code, _, errOut := f.run(append([]string{"answer", "a", "--dialog", id}, tc.answers...)...)
		if code != ExitUsage || !strings.HasPrefix(errOut, "hq: a not answered: ") || !strings.Contains(errOut, tc.want) {
			t.Errorf("%v: exit %d %q", tc.answers, code, errOut)
		}
	}
	if len(f.answerLeft) != 0 {
		t.Fatalf("left %v", f.answerLeft)
	}
}

func TestParseAnswerTellsNumbersFromText(t *testing.T) {
	for arg, want := range map[string]state.Answer{
		"2": {Options: []int{2}}, "1,3": {Options: []int{1, 3}}, "1, 3": {Options: []int{1, 3}},
		"Blue": {Text: "Blue"}, "1,x": {Text: "1,x"}, "+2": {Text: "+2"}, "02": {Text: "02"}, "": {Text: ""}, "1,": {Text: "1,"},
	} {
		if got := parseAnswer(arg); !reflect.DeepEqual(got, want) {
			t.Errorf("%q: %+v, want %+v", arg, got, want)
		}
	}
}

func TestAnswerUsage(t *testing.T) {
	f, id := askFakes(t, "dialog-ask")
	for _, args := range [][]string{
		{"answer"}, {"answer", "a", "--dialog", id}, {"answer", "a", "--dialog"}, {"answer", "a", "--cancel", "--dialog", id, "2"}, {"answer", "a", "--dialog", id, "-y"},
	} {
		if code, _, errOut := f.run(args...); code != ExitUsage || !strings.Contains(errOut, "hq answer NAME --dialog ID ANSWER... | --cancel") {
			t.Errorf("%v: exit %d %q", args, code, errOut)
		}
	}
	if code, _, errOut := f.run("answer", "nobody", "--dialog", id, "1"); code != ExitNotFound {
		t.Errorf("unknown agent: exit %d %q", code, errOut)
	}
}

// Only the agent's own question dialog is answered: anything else is
// refused with the reason, and nothing is left for the agent.
func TestAnswerRefusesWhatHqDoesNotAnswer(t *testing.T) {
	for _, tc := range []struct {
		name   string
		report state.Report
		detail state.Detail
		screen string
		setup  func(f *fakes)
		want   string
	}{
		{name: "permission prompt", report: state.Report{State: state.NeedsInput, Last: "Bash: make test"},
			detail: state.Detail{Ask: &state.Ask{Tool: "Bash", Command: "make test"}},
			want:   "hq: a is at a permission prompt (Bash: make test); permission prompts are answered at the agent (hq go a)\n"},
		{name: "MCP form", report: state.Report{State: state.NeedsInput, Last: "Which color should I use?"},
			detail: state.Detail{Ask: &state.Ask{Server: "elicit", Message: "Which color should I use?", Fields: []state.AskField{{Name: "color", Type: "string"}}}},
			want:   "hq: a is at an MCP server's form (elicit: Which color should I use?), which hq does not answer; answer it at the agent (hq go a)\n"},
		// An agent of hq 0.6.3 or older reports a form by Claude's own
		// notification, which says nothing of it (#100).
		{name: "elicitation", report: state.Report{State: state.NeedsInput, Last: "Claude Code needs your input"},
			detail: state.Detail{Ask: &state.Ask{Message: "Claude Code needs your input"}},
			want:   "hq: a is at a dialog hq does not answer (Claude Code needs your input), such as an MCP elicitation; answer it at the agent (hq go a)\n"},
		{name: "subagent", report: state.Report{State: state.NeedsInput, Last: "Which colour?", SubagentDialog: true},
			detail: state.Detail{Ask: &state.Ask{Tool: "AskUserQuestion", Questions: []state.AskQuestion{{Question: "Which colour?"}}}},
			want:   "hq: a's dialog is one of its subagents' (Which colour?), which hq does not answer; answer it at the agent (hq go a)\n"},
		{name: "Claude's own", report: state.Report{State: state.Done, Last: "hi"},
			screen: "● hi\n" + strings.Repeat("▔", 40) + "\n   Change effort level?\n   Your next response will be slower\n\n   ❯ 1. Yes, switch to max\n     2. No, go back\n",
			want:   "hq: a is at a dialog of Claude's own (Change effort level?), which hq does not answer; answer it at the agent (hq go a)\n"},
		{name: "working", report: state.Report{State: state.Working},
			want: "hq: a is at no dialog: it is working\n"},
		{name: "question", report: state.Report{State: state.Question, Last: "Shall I?"},
			want: "hq: a asks in its reply, not in a dialog: answer it with hq send a TEXT\n"},
		{name: "ended", report: state.Report{State: state.NeedsInput}, setup: func(f *fakes) { f.tmux.windows[1].PaneDead = true },
			want: "hq: a has ended: it is at no dialog\n"},
		{name: "older hq", report: state.Report{State: state.NeedsInput, Last: "Which colour?"}, setup: func(f *fakes) { f.tmux.windows[1].Options["inbox"] = "2" },
			want: "hq: a was started by an older hq and cannot be answered from hq; relaunch it with hq sandbox restart app, or kill it and start it again\n"},
	} {
		f, _ := askFakes(t, "dialog-ask")
		tc.report.Since = f.now.Add(-time.Minute)
		f.states["id-a"], f.details["id-a"] = tc.report, tc.detail
		if tc.screen != "" {
			f.tmux.screens["%4"] = tc.screen
		}
		if tc.setup != nil {
			tc.setup(f)
		}
		_, ls, _ := f.run("ls", "--json")
		id := ""
		if i := strings.Index(ls, `"dialog": "`); i >= 0 {
			id = ls[i+11 : i+17]
		}
		code, _, errOut := f.run("answer", "a", "--dialog", id, "1")
		if code != ExitUsage || errOut != tc.want {
			t.Errorf("%s: exit %d %q, want %q", tc.name, code, errOut, tc.want)
		}
		if len(f.answerLeft) != 0 {
			t.Errorf("%s: left %v", tc.name, f.answerLeft)
		}
	}
}

// No hook takes the answer within 3 s (an agent whose hook no longer
// holds): hq takes it back and refuses, unless the agent left the dialog
// meanwhile, which it says with its state.
func TestAnswerNotTakenIsTakenBackAndRefused(t *testing.T) {
	f, id := askFakes(t, "dialog-ask")
	start := f.now
	code, _, errOut := f.run("answer", "a", "--dialog", id, "1")
	if code != ExitUsage || errOut != "hq: a's dialog cannot be answered from hq: its hook no longer holds it; it is needs input, answer it at the agent (hq go a)\n" {
		t.Fatalf("exit %d %q", code, errOut)
	}
	if len(f.answerLeft) != 0 || f.withdrawals != 1 || f.now.Sub(start) < takeWait || f.now.Sub(start) > takeWait+time.Second {
		t.Fatalf("left %v, withdrawn %d, after %s", f.answerLeft, f.withdrawals, f.now.Sub(start))
	}

	// Answered at the terminal first: the hook went with the dialog.
	f, id = askFakes(t, "dialog-ask")
	f.onSleep = func() { f.states["id-a"] = state.Report{State: state.Working, Since: f.now} }
	if code, _, errOut := f.run("answer", "a", "--dialog", id, "1"); code != ExitUsage || errOut != "hq: a left the dialog before hq's answer reached it; nothing was answered from hq: it is working\n" {
		t.Fatalf("answered first: exit %d %q", code, errOut)
	}

	// Another hq answers the same dialog: one answer is on its way.
	f, id = askFakes(t, "dialog-ask")
	f.leaveErr = state.ErrAnswerWaiting
	if code, _, errOut := f.run("answer", "a", "--dialog", id, "1"); code != ExitUsage || errOut != "hq: a not answered: another answer to its dialog is on its way\n" {
		t.Fatalf("racing: exit %d %q", code, errOut)
	}
	f.leaveErr = errors.New("disk full")
	if code, _, errOut := f.run("answer", "a", "--dialog", id, "1"); code != ExitEnvironment || !strings.Contains(errOut, "disk full") {
		t.Fatalf("cannot leave: exit %d %q", code, errOut)
	}
}

// Taken, but the agent stays at the dialog for 5 s: a template whose
// claude-approve still answers AskUserQuestion first (#89, case 3).
func TestAnswerTakenButNotClosedNamesTheLikelyCause(t *testing.T) {
	f, id := askFakes(t, "dialog-ask")
	f.onSleep = func() { delete(f.answerLeft, "id-a") }
	start := f.now
	code, _, errOut := f.run("answer", "a", "--dialog", id, "1")
	want := "hq: a took the answer but did not leave its dialog within 5s: it is needs input; its sandbox's template may have a claude-approve hook that still answers AskUserQuestion (recreate the sandbox from a newer image), or answer it at the agent (hq go a)\n"
	if code != ExitEnvironment || errOut != want {
		t.Fatalf("exit %d %q", code, errOut)
	}
	if f.now.Sub(start) < closeWait || f.now.Sub(start) > closeWait+2*time.Second {
		t.Fatalf("after %s", f.now.Sub(start))
	}
}

// What Claude reports it got is compared with what was sent: a difference
// is an error saying what the agent got.
func TestAnswerSaysWhenTheAgentGotOtherAnswers(t *testing.T) {
	f, id := askFakes(t, "dialog-ask-several")
	f.holding(map[string]string{"Which color?": "Red", "Which sizes?": "Small, Large", "Which name?": "Gamma"})
	code, _, errOut := f.run("answer", "a", "--dialog", id, "2", "1,3", "Gamma")
	if code != ExitUsage || errOut != `hq: a got other answers than hq sent: Which color? -> "Red" (sent "Blue"); it is working`+"\n" {
		t.Fatalf("exit %d %q", code, errOut)
	}
}

// hq read and hq ls --json give the open dialog's id, and hq read the
// command that answers it while hq can.
func TestReadAndLsGiveTheDialogsID(t *testing.T) {
	f, id := askFakes(t, "dialog-ask-several")
	_, out, _ := f.run("read", "a")
	want := "\nasks      dialog " + id + "\n  Color: Which color?\n    1. Red - Red\n    2. Blue - Blue\n  Sizes: Which sizes? (any of)\n    1. Small - Small\n    2. Medium - Medium\n    3. Large - Large\n  Name: Which name?\n    1. Alpha - Alpha\n    2. Beta - Beta\n\nanswer    hq answer a --dialog " + id + " N N,N N\n\nreply\n"
	if !strings.Contains(out, want) {
		t.Fatalf("read:\n%s", out)
	}
	for _, cmd := range [][]string{{"read", "a", "--json"}, {"ls", "--json"}} {
		if _, out, _ := f.run(cmd...); !strings.Contains(out, `"dialog": "`+id+`"`) {
			t.Errorf("%v:\n%s", cmd, out)
		}
	}
	// Not answerable from hq: no command; no dialog: no id.
	f.tmux.windows[1].Options["inbox"] = "2"
	if _, out, _ := f.run("read", "a"); strings.Contains(out, "\nanswer ") || !strings.Contains(out, "asks      dialog "+id) {
		t.Fatalf("older hq:\n%s", out)
	}
	f.states["id-a"] = state.Report{State: state.Working, Since: f.now}
	for _, cmd := range [][]string{{"read", "a", "--json"}, {"ls", "--json"}} {
		if _, out, _ := f.run(cmd...); strings.Contains(out, `"dialog"`) {
			t.Errorf("%v at no dialog:\n%s", cmd, out)
		}
	}
}

// The agent killed while hq waits for it to leave the dialog: it says so.
func TestAnswerToAnAgentKilledMeanwhile(t *testing.T) {
	f, id := askFakes(t, "dialog-ask")
	f.onSleep = func() {
		delete(f.answerLeft, "id-a")
		f.tmux.windows = f.tmux.windows[:1]
	}
	if code, _, errOut := f.run("answer", "a", "--dialog", id, "1"); code != ExitNotFound || errOut != "hq: no agent 'a' (see hq ls)\n" {
		t.Fatalf("exit %d %q", code, errOut)
	}
}

// The dialog closed with no end of its tool: cancelled at the agent while
// hq answered, the answer not taken as given. That is no success.
func TestAnswerToADialogCancelledMeanwhileIsNoSuccess(t *testing.T) {
	f, id := askFakes(t, "dialog-ask")
	f.onSleep = func() {
		delete(f.answerLeft, "id-a")
		f.tmux.screens["%4"] = declinedScreen
	}
	if code, out, errOut := f.run("answer", "a", "--dialog", id, "1"); code != ExitUsage || out != "" || errOut != "hq: a left the dialog without taking hq's answer: it is done; read it (hq read a)\n" {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
}
