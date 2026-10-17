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

// An MCP elicitation asks for a form: the server, the message and each
// field in the form's order, or for a URL-mode one, the page it sends the
// user to (#100).
func TestParseDetailTellsWhatAnElicitationAsks(t *testing.T) {
	ask := ParseDetail(fixture(t, "elicitation"), fixture(t, "stop-done")).Ask
	want := &Ask{Server: "elicit", Message: "Which color should I use?", Fields: []AskField{
		{Name: "color", Title: "Color", Description: "Pick one", Type: "string", Required: true, Options: []AskFieldOption{{Value: "red"}, {Value: "blue"}}},
	}}
	if !reflect.DeepEqual(ask, want) {
		t.Errorf("form: %+v", ask)
	}
	if ask := ParseDetail(fixture(t, "elicitation-subagent"), nil).Ask; !reflect.DeepEqual(ask, want) {
		t.Errorf("a subagent's form: %+v", ask)
	}
	ask = ParseDetail(fixture(t, "elicitation-url"), nil).Ask
	if !reflect.DeepEqual(ask, &Ask{Server: "elicit", Message: "Sign in to the example service.", URL: "https://example.com/sign-in?session=42"}) {
		t.Errorf("URL mode: %+v", ask)
	}
	for _, latest := range []string{"elicitation-result-accept", "elicitation-result-decline", "elicitation-result-cancel", "elicitation-result-url-accept"} {
		if ask := ParseDetail(fixture(t, latest), nil).Ask; ask != nil {
			t.Errorf("%s asks %+v", latest, ask)
		}
	}
}

// What a form's fields are, read off its schema: the fields in its order,
// their options from enum (with enumNames) or oneOf, a free field without,
// a default; what hq cannot read (a list, a nested object, an option that
// is no string) leaves out that field alone, and a schema
// it cannot read at all leaves the message.
func TestAnElicitationsFieldsAreReadOffItsSchema(t *testing.T) {
	elicit := func(schema string) *Ask {
		return ParseDetail([]byte(`{"hook_event_name":"Elicitation","mcp_server_name":"db\u001b[31m","message":"Set up\nthe database","mode":"form","requested_schema":`+schema+`}`), nil).Ask
	}
	ask := elicit(`{"type":"object","properties":{
		"name":{"type":"string","title":"Name","minLength":1,"format":"email"},
		"size":{"type":"integer","title":"Size","default":3,"minimum":1},
		"engine":{"type":"string","oneOf":[{"const":"pg","title":"PostgreSQL"},{"const":"my","title":"MySQL"}],"default":"pg"},
		"tier":{"type":"string","enum":["s","l"],"enumNames":["Small","Large"]},
		"backup":{"type":"boolean","description":"Daily\tbackups","default":false},
		"ratio":{"type":"number"},
		"tags":{"type":"array","items":{"type":"string","enum":["a","b"]}},
		"owner":{"type":"object","properties":{"id":{"type":"string"}}},
		"broken":"yes",
		"color":{"type":"string","enum":["red",7]}
	},"required":["engine","name","nowhere"]}`)
	want := &Ask{Server: "db", Message: "Set up the database", Fields: []AskField{
		{Name: "name", Title: "Name", Type: "string", Required: true},
		{Name: "size", Title: "Size", Type: "integer", Default: float64(3)},
		{Name: "engine", Type: "string", Required: true, Options: []AskFieldOption{{Value: "pg", Title: "PostgreSQL"}, {Value: "my", Title: "MySQL"}}, Default: "pg"},
		{Name: "tier", Type: "string", Options: []AskFieldOption{{Value: "s", Title: "Small"}, {Value: "l", Title: "Large"}}},
		{Name: "backup", Description: "Daily backups", Type: "boolean", Default: false},
		{Name: "ratio", Type: "number"},
	}}
	if !reflect.DeepEqual(ask, want) {
		t.Errorf("fields:\n got %+v\nwant %+v", ask, want)
	}
	for _, schema := range []string{`null`, `"form"`, `{"type":"object"}`, `{"type":"object","properties":[]}`, `{"type":"object","properties":{"a":1}}`} {
		if ask := elicit(schema); !reflect.DeepEqual(ask, &Ask{Server: "db", Message: "Set up the database"}) {
			t.Errorf("%s: %+v", schema, ask)
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
