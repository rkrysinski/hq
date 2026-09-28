package state

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestParseDetailNeedsAnEvent(t *testing.T) {
	// Before the first event, or with a broken file, there is nothing to read.
	for _, latest := range [][]byte{nil, []byte("branch main\nnot json")} {
		if d := ParseDetail(latest, fixture(t, "stop-done")); d.Reply != "" || d.Ask != nil {
			t.Errorf("%q: %+v", latest, d)
		}
	}
}

func TestParseDetailGivesTheWholeLastReply(t *testing.T) {
	stop, _ := json.Marshal(map[string]string{"hook_event_name": "Stop", "session_id": "s",
		"last_assistant_message": "\n\nDone:\n\n- migration\t\x1b[31mred\x1b[0m  \r\n- API\n\nShall I open the PR?\n"})
	want := "Done:\n\n- migration    red\n- API\n\nShall I open the PR?"
	for _, tc := range []struct{ name, latest, lastStop []byte }{
		{[]byte("the latest Stop"), stop, stop},
		{[]byte("a Stop before the prompt"), fixture(t, "prompt"), stop},
	} {
		if d := ParseDetail(tc.latest, tc.lastStop); d.Reply != want {
			t.Errorf("%s: %q", tc.name, d.Reply)
		}
	}
	if d := ParseDetail(fixture(t, "prompt"), nil); d.Reply != "" || d.Ask != nil {
		t.Errorf("no reply yet: %+v", d)
	}
}

func TestParseDetailTellsWhatAnOpenDialogAsks(t *testing.T) {
	ask := ParseDetail(fixture(t, "dialog-ask"), fixture(t, "stop-done")).Ask
	want := &Ask{Tool: "AskUserQuestion", Questions: []AskQuestion{{Header: "Colour", Question: "Which colour do you pick: red or blue?",
		Options: []AskOption{{Label: "Red", Description: "Pick red."}, {Label: "Blue", Description: "Pick blue."}}}}}
	if !reflect.DeepEqual(ask, want) {
		t.Errorf("question dialog: %+v", ask)
	}
	ask = ParseDetail(fixture(t, "dialog-bash"), nil).Ask
	if !reflect.DeepEqual(ask, &Ask{Tool: "Bash", Description: "Create empty probe file in /tmp", Command: "touch /tmp/hq-probe-x"}) {
		t.Errorf("permission prompt: %+v", ask)
	}
	ask = ParseDetail(fixture(t, "notification"), nil).Ask
	if !reflect.DeepEqual(ask, &Ask{Message: "Claude needs your permission"}) {
		t.Errorf("notification: %+v", ask)
	}
	for _, latest := range []string{"prompt", "stop-question", "answer-ask", "session-end"} {
		if ask := ParseDetail(fixture(t, latest), nil).Ask; ask != nil {
			t.Errorf("%s asks %+v", latest, ask)
		}
	}
}

func TestCleanTextKeepsLinesAndStripsTheRest(t *testing.T) {
	for in, want := range map[string]string{
		"a\nb":                       "a\nb",
		"\n\n  indented\n\n":         "  indented",
		"x\x1b]0;title\x07y\x1b[2Jz": "xyz",
		"bell\a and\x00 nul":         "bell and nul",
		"tab\tstop   \nnext":         "tab    stop\nnext",
		"crlf\r\nline sep":           "crlf\nlinesep",
	} {
		if got := CleanText(in); got != want {
			t.Errorf("CleanText(%q) = %q, want %q", in, got, want)
		}
	}
}
