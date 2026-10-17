package state

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// decoded is a decision as Claude reads it.
type decoded struct {
	HookSpecificOutput struct {
		HookEventName string
		Decision      struct {
			Behavior     string
			Message      string
			Interrupt    bool
			UpdatedInput map[string]any
		}
	}
}

func decode(t *testing.T, decision []byte) decoded {
	t.Helper()
	var d decoded
	if err := json.Unmarshal(decision, &d); err != nil || strings.Contains(string(decision), "\n") {
		t.Fatalf("%q: %v", decision, err)
	}
	return d
}

func questions(t *testing.T, name string) Questions {
	t.Helper()
	q, err := ParseQuestions(append([]byte("branch main\n"), fixture(t, name)...))
	if err != nil {
		t.Fatal(err)
	}
	return q
}

// A question dialog reads as hq read shows it, with what its answer needs.
func TestParseQuestionsReadsTheDialogsQuestions(t *testing.T) {
	q := questions(t, "dialog-ask-several")
	if len(q.Ask.Questions) != 3 || q.Ask.Questions[1].Question != "Which sizes?" || !q.Ask.Questions[1].MultiSelect || q.Ask.Questions[2].Options[1].Label != "Beta" {
		t.Fatalf("%+v", q.Ask)
	}
	for _, name := range []string{"dialog-bash", "answer-ask", "stop-done", "notification"} {
		if _, err := ParseQuestions(fixture(t, name)); err == nil {
			t.Errorf("%s: a question dialog", name)
		}
	}
	// A subagent's question dialog is not the agent's own.
	sub := strings.Replace(string(fixture(t, "dialog-ask")), `"hook_event_name"`, `"agent_id":"a1","hook_event_name"`, 1)
	if _, err := ParseQuestions([]byte(sub)); err == nil {
		t.Error("a subagent's dialog")
	}
}

// An answer is an allow whose updated input is the tool's input with the
// answers added, keyed by question text: a label, a list of labels for a
// multi-select question, free text stripped as a message is. What Claude
// reports for each is that, a list joined with ", " (#89).
func TestTheDecisionCarriesTheAnswersInTheToolsInput(t *testing.T) {
	q := questions(t, "dialog-ask-several")
	decision, sent, err := q.Decision([]Answer{{Options: []int{2}}, {Options: []int{3, 1}}, {Text: " Gamma, \x1b[31m<typed> & \"quoted\" "}})
	if err != nil {
		t.Fatal(err)
	}
	d := decode(t, decision).HookSpecificOutput
	if d.HookEventName != "PermissionRequest" || d.Decision.Behavior != "allow" || d.Decision.Interrupt {
		t.Fatalf("%s", decision)
	}
	var in map[string]any
	_ = json.Unmarshal(fixture(t, "dialog-ask-several"), &struct {
		In *map[string]any `json:"tool_input"`
	}{&in})
	want := map[string]any{"Which color?": "Blue", "Which sizes?": []any{"Large", "Small"}, "Which name?": "Gamma, <typed> & \"quoted\""}
	if got := d.Decision.UpdatedInput["answers"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("answers %#v", got)
	}
	if !reflect.DeepEqual(d.Decision.UpdatedInput["questions"], in["questions"]) || len(d.Decision.UpdatedInput) != 2 {
		t.Fatalf("input %#v", d.Decision.UpdatedInput)
	}
	if !strings.Contains(string(decision), "<typed> &") {
		t.Errorf("escaped for HTML: %s", decision)
	}
	wantSent := map[string]string{"Which color?": "Blue", "Which sizes?": "Large, Small", "Which name?": "Gamma, <typed> & \"quoted\""}
	if !reflect.DeepEqual(sent, wantSent) {
		t.Fatalf("sent %#v", sent)
	}
}

// The answer is checked whole against the dialog, and refused whole on any
// fault, saying which.
func TestADecisionThatDoesNotFitTheDialogIsRefused(t *testing.T) {
	q := questions(t, "dialog-ask-several")
	ok := []Answer{{Options: []int{1}}, {Options: []int{2}}, {Text: "x"}}
	for _, tc := range []struct {
		answers []Answer
		want    string
	}{
		{ok[:2], "3 questions, 2 answers"},
		{append(ok, Answer{Text: "y"}), "3 questions, 4 answers"},
		{[]Answer{{Options: []int{3}}, ok[1], ok[2]}, "Which color? has no option 3"},
		{[]Answer{{Options: []int{0}}, ok[1], ok[2]}, "Which color? has no option 0"},
		{[]Answer{{Options: []int{1, 2}}, ok[1], ok[2]}, "Which color? takes one option"},
		{[]Answer{ok[0], {Options: []int{2, 2}}, ok[2]}, "Which sizes?: option 2 twice"},
		{[]Answer{ok[0], ok[1], {Text: "  "}}, "Which name?: no answer"},
		{[]Answer{ok[0], ok[1], {}}, "Which name?: no answer"},
		{[]Answer{ok[0], ok[1], {Options: []int{1}, Text: "x"}}, "Which name?: an option or text, not both"},
		{nil, "3 questions, 0 answers"},
		{[]Answer{ok[0]}, "3 questions, 1 answer given"},
	} {
		if _, _, err := q.Decision(tc.answers); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: %v, want %q", tc.answers, err, tc.want)
		}
	}
	if _, _, err := q.Decision([]Answer{{Text: strings.Repeat("x", MaxMessage+1)}, ok[1], ok[2]}); err == nil {
		t.Error("free text over the message cap")
	}
}

// A cancel is a deny that interrupts the turn, which ends it as Esc does.
func TestTheCancelIsADenyThatInterrupts(t *testing.T) {
	d := decode(t, questions(t, "dialog-ask").Cancel()).HookSpecificOutput
	if d.HookEventName != "PermissionRequest" || d.Decision.Behavior != "deny" || !d.Decision.Interrupt || d.Decision.Message == "" {
		t.Fatalf("%+v", d)
	}
}

// The answer's file is named for the dialog's report, by the POSIX cksum of
// the state file the hook can take too: another report, the same questions
// asked in another turn included, names another.
func TestTheAnswersFileIsNamedForTheDialogsReport(t *testing.T) {
	for _, tc := range []struct {
		data string
		crc  uint32
	}{{"hello", 3287646509}, {"", 4294967295}, {"branch main\n{\"a\":1}", 354087003}} {
		if crc := cksum([]byte(tc.data)); crc != tc.crc {
			t.Errorf("cksum %q: %d, want %d", tc.data, crc, tc.crc)
		}
	}
	q := questions(t, "dialog-ask-several")
	again, _ := ParseQuestions(append([]byte("branch main\n"), []byte(strings.Replace(string(fixture(t, "dialog-ask-several")), "292b", "392b", 1))...))
	if q.name == again.name || !strings.HasPrefix(q.name, answerPrefix) {
		t.Fatalf("%q %q", q.name, again.name)
	}
}

// A dialog's id is six hex characters of its report's moment.
func TestADialogsIDIsSixHexCharactersOfItsMoment(t *testing.T) {
	at := time.Unix(1000, 123456789)
	id := DialogID(at)
	if len(id) != 6 || strings.Trim(id, "0123456789abcdef") != "" || DialogID(at) != id {
		t.Fatalf("%q", id)
	}
	if DialogID(at.Add(time.Nanosecond)) == id || DialogID(time.Time{}) != "" {
		t.Fatal("not of the moment")
	}
}

// What Claude reports it got is read off the tool's end that closed the
// dialog, answered by hq or at the terminal alike.
func TestParseAnswersReadsWhatTheDialogsToolEndCarries(t *testing.T) {
	got, ok := ParseAnswers(fixture(t, "answer-ask-hook"))
	want := map[string]string{"Which color?": "Blue", "Which sizes?": "Small, Large", "Which name?": "Gamma, typed by hq"}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("%v %v", got, ok)
	}
	if got, ok := ParseAnswers(fixture(t, "answer-ask-several")); !ok || got["Which sizes?"] != "Small, Large, XL" {
		t.Fatalf("at the terminal: %v %v", got, ok)
	}
	for _, name := range []string{"dialog-ask-several", "answer-bash", "stop-done"} {
		if _, ok := ParseAnswers(fixture(t, name)); ok {
			t.Errorf("%s: answers", name)
		}
	}
}
