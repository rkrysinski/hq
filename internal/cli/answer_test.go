package cli

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strconv"
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
		{name: "Claude's own list that scrolls", report: state.Report{State: state.Done, Last: "hi"},
			screen: "● hi\n" + strings.Repeat("▔", 40) + "\n   Select model\n   Switch between Claude models.\n\n   ❯ 1. Default (recommended)\n   ↓ 2. Opus 5.5\n      … +3 models\n",
			want:   "hq: a is at a dialog of Claude's own whose list scrolls (Select model), which hq does not answer; answer it at the agent (hq go a)\n"},
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

// The dialog's tool may end between two looks of hq: hq reports the
// agent as it was once it left the dialog, never as it was at the dialog,
// and what Claude got as it read it after that look.
func TestAnswerReportsTheAgentOnceItLeftTheDialog(t *testing.T) {
	f, id := askFakes(t, "dialog-ask")
	looks := 0
	f.onSleep = func() {
		switch looks++; looks {
		case 1: // the hook takes the answer, and Claude's tool ends
			delete(f.answerLeft, "id-a")
			f.got["id-a"] = map[string]string{"Which colour do you pick: red or blue?": "Red"}
		case 2: // hq sees the agent leave the dialog only now
			f.states["id-a"] = state.Report{State: state.Working, Since: f.now, Last: "On it."}
		}
	}
	if code, out, errOut := f.run("answer", "a", "--dialog", id, "1"); code != 0 || out != "answered: a is working\n" {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
}

// Taken, but the agent stays at the dialog for 5 s: another
// PermissionRequest hook answered AskUserQuestion first (#89, case 3), one
// hq could not run before it left the answer (#128).
func TestAnswerTakenButNotClosedNamesTheLikelyCause(t *testing.T) {
	f, id := askFakes(t, "dialog-ask")
	f.onSleep = func() { delete(f.answerLeft, "id-a") }
	start := f.now
	code, _, errOut := f.run("answer", "a", "--dialog", id, "1")
	want := "hq: a took the answer but did not leave its dialog within 5s: it is needs input; another PermissionRequest hook may have answered AskUserQuestion first (the repository's settings, a plugin, or the sandbox's template where hq could not run its hooks); answer it at the agent (hq go a)\n"
	if code != ExitEnvironment || errOut != want {
		t.Fatalf("exit %d %q", code, errOut)
	}
	if f.now.Sub(start) < closeWait || f.now.Sub(start) > closeWait+2*time.Second {
		t.Fatalf("after %s", f.now.Sub(start))
	}
}

// template is a sandbox whose managed settings are the sbx template's,
// whose PermissionRequest hook prints out for any request it is run with;
// it records the payload each hook was run with.
func (f *fakes) template(t *testing.T, out string, ran *[]string) {
	t.Helper()
	settings, err := os.ReadFile("../state/testdata/managed-settings-template.json")
	if err != nil {
		t.Fatal(err)
	}
	f.sbx.onOutput = func(args []string) ([]byte, error) {
		if !strings.Contains(strings.Join(args, " "), "/usr/local/bin/claude-approve") {
			return settings, nil
		}
		// sh -c PROBE sh PAYLOAD HOOK...
		*ran = append(*ran, args[4])
		return []byte(out), nil
	}
}

// A sandbox whose template answers AskUserQuestion by itself, as the sbx
// image's claude-approve did before it left AskUserQuestion alone: Claude
// takes no other hook's answer, so hq refuses at once, with the cause,
// leaving no answer (#128).
func TestAnswerRefusesWhenTheTemplateAnswersAskUserQuestion(t *testing.T) {
	for _, args := range [][]string{{"1", "1", "1"}, {"--cancel"}} {
		f, id := askFakes(t, "dialog-ask-three")
		var ran []string
		f.template(t, `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}`+"\n", &ran)
		f.holding(nil)
		start := f.now
		code, out, errOut := f.run(append([]string{"answer", "a", "--dialog", id}, args...)...)
		want := "hq: a's dialog cannot be answered from hq: its sandbox's template answers AskUserQuestion by itself (its PermissionRequest hook /usr/local/bin/claude-approve gives a decision), so Claude would take no answer from hq; answer it at the agent (hq go a), and recreate the sandbox from a template whose hooks leave AskUserQuestion alone (hq sandbox rm app)\n"
		if code != ExitUsage || out != "" || errOut != want {
			t.Fatalf("%v: exit %d %q %q", args, code, out, errOut)
		}
		if len(f.answerLeft) != 0 || f.got["id-a"] != nil || f.now != start {
			t.Fatalf("%v: left %v, got %v, after %s", args, f.answerLeft, f.got["id-a"], f.now.Sub(start))
		}
		// The hook ran as Claude ran it, with the dialog's payload.
		payload := string(stateFixture(t, "dialog-ask-three"))
		payload = payload[strings.Index(payload, "\n")+1:]
		if len(ran) != 1 || ran[0] != payload {
			t.Fatalf("%v: ran with %q", args, ran)
		}
	}
}

// A template whose hooks leave AskUserQuestion alone, as sbx-image #4's
// claude-approve does, and a sandbox hq cannot look into, are answered
// as ever.
func TestAnswerGoesOnWhenTheTemplateLeavesAskUserQuestionAlone(t *testing.T) {
	f, id := askFakes(t, "dialog-ask-three")
	var ran []string
	f.template(t, "", &ran)
	f.holding(nil)
	if code, out, errOut := f.run("answer", "a", "--dialog", id, "1", "1", "1"); code != 0 || out != "answered: a is working\n" {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	if len(ran) != 1 {
		t.Fatalf("ran %d hooks", len(ran))
	}
	want := map[string]string{"Which colour?": "Red", "Which size?": "Small", "Which shape?": "Circle"}
	if !reflect.DeepEqual(f.got["id-a"], want) {
		t.Fatalf("Claude got %v", f.got["id-a"])
	}

	f, id = askFakes(t, "dialog-ask-three")
	f.sbx.onOutput = func([]string) ([]byte, error) { return nil, errors.New("sbx: sandbox not running") }
	f.holding(nil)
	if code, out, errOut := f.run("answer", "a", "--dialog", id, "1", "1", "1"); code != 0 || out != "answered: a is working\n" {
		t.Fatalf("no look: exit %d %q %q", code, out, errOut)
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

// ownClaude is Claude Code 2.1.304 at its own effort dialog after a turn
// (screen-own-effort-low): it takes the keys hq presses in the agent's
// pane and draws what they did by hq's next sleep, as Claude redraws
// within 0.1 s. Down and Up move the ❯, wrapping round at the ends; Enter
// picks the option the ❯ is on and draws the box again. A key pressed
// before the last one was drawn fails the test: hq presses one at a time.
type ownClaude struct {
	t        *testing.T
	f        *fakes
	cursor   int
	picked   int      // the option Enter picked; -1 until then
	pending  string   // the key not drawn yet
	drop     bool     // keys move nothing
	pressed  []string // the keys, in order
	afterKey func(key string)
}

func ownDialogFakes(t *testing.T) (*fakes, *ownClaude, string) {
	t.Helper()
	f := sendFakes(state.Done)
	f.states["id-a"] = state.Report{State: state.Done, Since: f.now.Add(-time.Minute), Last: "hi"}
	c := &ownClaude{t: t, f: f, picked: -1}
	f.tmux.screens["%4"] = c.screen()
	f.tmux.onPress = func(pane, key string) {
		if pane != "%4" {
			t.Errorf("key %s pressed in pane %s", key, pane)
		}
		if c.pending != "" {
			t.Errorf("%s pressed before %s was drawn", key, c.pending)
		}
		c.pending, c.pressed = key, append(c.pressed, key)
	}
	f.onSleep = c.draw
	_, ls, _ := f.run("ls", "--json")
	i := strings.Index(ls, `"dialog": "`)
	if i < 0 {
		t.Fatalf("no dialog:\n%s", ls)
	}
	return f, c, ls[i+11 : i+17]
}

// draw draws what the key pressed last did.
func (c *ownClaude) draw() {
	key := c.pending
	c.pending = ""
	switch {
	case key == "" || c.picked >= 0 || c.drop:
		return
	case key == "Down":
		c.cursor = (c.cursor + 1) % 2 // of two options
	case key == "Up":
		c.cursor = (c.cursor + 2 - 1) % 2
	case key == "Enter":
		c.picked = c.cursor
	default:
		c.t.Errorf("key %q", key)
	}
	c.f.tmux.screens["%4"] = c.screen()
	if c.afterKey != nil {
		c.afterKey(key)
	}
}

func (c *ownClaude) screen() string {
	switch c.picked {
	case 0:
		return readFixture(c.t, "screen-effort-low-set.txt")
	case 1:
		return "  ⎿  Kept effort level as high\n" + emptyBox
	}
	s := readFixture(c.t, "screen-own-effort-low.txt")
	if c.cursor == 1 {
		s = strings.Replace(strings.Replace(s, "❯ 1. Yes", "  1. Yes", 1), "  2. No", "❯ 2. No", 1)
	}
	return s
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../state/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A dialog of Claude's own fires no hook, so hq answers it with keys
// (#120): the ❯ moved to the option, each key once Claude has drawn the
// last, then Enter; it returns once the dialog is gone, the agent back as
// it was before it.
func TestAnswerPicksAnOptionOfADialogOfClaudesOwnWithKeys(t *testing.T) {
	for _, tc := range []struct {
		from, option int
		keys         string
	}{
		{0, 2, "Down Enter"},
		{0, 1, "Enter"},
		{1, 1, "Up Enter"},
	} {
		f, c, id := ownDialogFakes(t)
		if tc.from == 1 {
			c.cursor = 1
			f.tmux.screens["%4"] = c.screen()
		}
		code, out, errOut := f.run("answer", "a", "--dialog", id, strconv.Itoa(tc.option))
		if code != 0 || out != "answered: a is done\n" {
			t.Errorf("%+v: exit %d %q %q", tc, code, out, errOut)
		}
		if got := strings.Join(c.pressed, " "); got != tc.keys || c.picked != tc.option-1 {
			t.Errorf("%+v: pressed %q, picked %d", tc, got, c.picked)
		}
		// Nothing for a hook: none holds on such a dialog.
		if len(f.answerLeft) != 0 || len(f.tmux.pasted) != 0 {
			t.Errorf("%+v: left %v, pasted %v", tc, f.answerLeft, f.tmux.pasted)
		}
	}
	// --json: the state, and the moment before the first key, for hq wait.
	f, _, id := ownDialogFakes(t)
	before := f.now
	_, out, _ := f.run("answer", "a", "--dialog", id, "2", "--json")
	var got answerOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.State != state.Done || got.NextSince.After(before) || before.Sub(got.NextSince) > time.Second {
		t.Fatalf("json %q: %+v %v", out, got, err)
	}
	// An agent started by an older hq has no hooks hq needs for it.
	f, c, id := ownDialogFakes(t)
	f.tmux.windows[1].Options["inbox"] = "2"
	if code, out, errOut := f.run("answer", "a", "--dialog", id, "2"); code != 0 || out != "answered: a is done\n" || c.picked != 1 {
		t.Fatalf("older hq: exit %d %q %q", code, out, errOut)
	}
}

// Nothing is pressed for an answer that does not fit the dialog, nor for a
// dialog the screen no longer shows: answered at the agent first, or
// another dialog in its place.
func TestAnswerToADialogOfClaudesOwnRefusedPressesNothing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		screen func(c *ownClaude) string // what the screen shows once hq looked at the agent
		want   string
	}{
		{name: "text", args: []string{"yes"},
			want: "hq: a not answered: Change effort level? takes an option's number, not text: it is a dialog of Claude's own\n"},
		{name: "unknown option", args: []string{"3"},
			want: "hq: a not answered: Change effort level? has no option 3: it has 2\n"},
		{name: "several options", args: []string{"1,2"},
			want: "hq: a not answered: Change effort level? takes one option, 2 given\n"},
		{name: "two answers", args: []string{"1", "2"},
			want: "hq: a not answered: the dialog has 1 question, 2 answers given: one per question, in its order\n"},
		{name: "cancel", args: []string{"--cancel"},
			want: "hq: a not answered: a dialog of Claude's own is answered with one of its options, not cancelled\n"},
		{name: "answered first", args: []string{"2"}, screen: func(*ownClaude) string { return emptyBox },
			want: "hq: a left the dialog before hq's answer reached it; nothing was answered from hq: it is done\n"},
		{name: "another dialog", args: []string{"2"}, screen: func(c *ownClaude) string { return readFixture(c.t, "screen-own-switch-model.txt") },
			want: "hq: a's dialog changed meanwhile; it needs input at another (Switch model?), read it again (hq read a)\n"},
	} {
		f, c, id := ownDialogFakes(t)
		if tc.screen != nil {
			looks := 0
			f.tmux.onScreens = func() {
				if looks++; looks == 1 {
					f.tmux.screens["%4"] = tc.screen(c)
				}
			}
		}
		code, _, errOut := f.run(append([]string{"answer", "a", "--dialog", id}, tc.args...)...)
		if code != ExitUsage || errOut != tc.want {
			t.Errorf("%s: exit %d %q, want %q", tc.name, code, errOut, tc.want)
		}
		if len(c.pressed) != 0 {
			t.Errorf("%s: pressed %v", tc.name, c.pressed)
		}
	}
	// A list that scrolls past the screen, the picker of /model: its
	// options' numbers are not those of the whole list.
	f, c, _ := ownDialogFakes(t)
	f.tmux.screens["%4"] = readFixture(t, "screen-own-model-2304.txt")
	_, ls, _ := f.run("ls", "--json")
	id := ls[strings.Index(ls, `"dialog": "`)+11:][:6]
	code, _, errOut := f.run("answer", "a", "--dialog", id, "4")
	if code != ExitUsage || errOut != "hq: a is at a dialog of Claude's own whose list scrolls (Select model), which hq does not answer; answer it at the agent (hq go a)\n" || len(c.pressed) != 0 {
		t.Fatalf("model picker: exit %d %q, pressed %v", code, errOut, c.pressed)
	}
}

// Enter is pressed only on the option asked for: when the dialog changes
// while hq moves the ❯, or the ❯ does not move, nothing is picked.
func TestAnswerToADialogOfClaudesOwnPicksNothingWhenTheCursorGoesAstray(t *testing.T) {
	f, c, id := ownDialogFakes(t)
	c.afterKey = func(string) { f.tmux.screens["%4"] = readFixture(t, "screen-own-switch-model-down.txt") }
	code, _, errOut := f.run("answer", "a", "--dialog", id, "2")
	if code != ExitUsage || errOut != "hq: a's dialog changed while hq moved to option 2: nothing was picked; it needs input at another (Switch model?), read it again (hq read a)\n" || strings.Join(c.pressed, " ") != "Down" {
		t.Fatalf("changed: exit %d %q, pressed %v", code, errOut, c.pressed)
	}

	f, c, id = ownDialogFakes(t)
	c.drop = true
	start := f.now
	code, _, errOut = f.run("answer", "a", "--dialog", id, "2")
	if code != ExitEnvironment || errOut != "hq: a's dialog did not move to option 2 within 2s: nothing was picked; answer it at the agent (hq go a)\n" || strings.Join(c.pressed, " ") != "Down" {
		t.Fatalf("stuck: exit %d %q, pressed %v", code, errOut, c.pressed)
	}
	if f.now.Sub(start) < keyWait || f.now.Sub(start) > keyWait+time.Second {
		t.Fatalf("stuck after %s", f.now.Sub(start))
	}
}

// Enter pressed, the dialog still open after 5 s: hq says so, with the
// agent's state.
func TestAnswerToADialogOfClaudesOwnThatStaysOpenFails(t *testing.T) {
	f, c, id := ownDialogFakes(t)
	c.afterKey = func(key string) {
		if key == "Enter" {
			c.picked = -1
			f.tmux.screens["%4"] = c.screen()
		}
	}
	start := f.now
	code, _, errOut := f.run("answer", "a", "--dialog", id, "1")
	if code != ExitEnvironment || errOut != "hq: a did not leave its dialog within 5s of picking option 1: it is needs input; answer it at the agent (hq go a)\n" {
		t.Fatalf("exit %d %q", code, errOut)
	}
	if f.now.Sub(start) < closeWait || f.now.Sub(start) > closeWait+time.Second {
		t.Fatalf("after %s", f.now.Sub(start))
	}
}

// hq read gives the command that answers a dialog of Claude's own, but
// not for a list that scrolls.
func TestReadGivesTheAnswerCommandForADialogOfClaudesOwn(t *testing.T) {
	f, _, id := ownDialogFakes(t)
	_, out, _ := f.run("read", "a")
	if !strings.Contains(out, "\nanswer    hq answer a --dialog "+id+" N\n") || !strings.Contains(out, "    2. No, go back\n") {
		t.Fatalf("read:\n%s", out)
	}
	f.tmux.screens["%4"] = readFixture(t, "screen-own-model-2304.txt")
	if _, out, _ := f.run("read", "a"); strings.Contains(out, "\nanswer ") || !strings.Contains(out, "Select model") {
		t.Fatalf("model picker:\n%s", out)
	}
}
