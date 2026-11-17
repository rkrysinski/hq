package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// eventFile is a fixture as the hook leaves it in an event file: the branch
// line, then the payload.
func eventFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return append([]byte("branch feat-42\n"), data...)
}

func TestParseEventTakesOnlyWhatTheRulesRead(t *testing.T) {
	at := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	const session = "0b1c2d3e-4f50-6172-8394-a5b6c7d8e9f0"
	for _, tc := range []struct {
		fixture, kind string
		want          Facts
	}{
		{"prompt-wake", "UserPromptSubmit", Facts{Wake: true, Woke: "a58b43841609db047", Turn: "00000027-0000-4000-8000-000000000027"}},
		{"post-tool-own-monitor", "PostToolUse", Facts{Tool: "Monitor", Started: "=b46swdckt"}},
		{"post-tool-subagent-monitor", "PostToolUse", Facts{Tool: "Monitor", Started: "~b3ukv3517", Subagent: true}},
		{"post-tool", "PostToolUse", Facts{Tool: "Bash"}},
		{"dialog-subagent", "PermissionRequest", Facts{Subagent: true}},
		{"notification", "Notification", Facts{Notice: "permission_prompt", Text: "Claude needs your permission"}},
		{"session-start-resume", "SessionStart", Facts{Start: "resume"}},
		{"session-end", "SessionEnd", Facts{Reason: "prompt_input_exit"}},
		{"stop-background", "Stop", Facts{Tasks: []EventTask{{ID: "a58b43841609db047", Type: "subagent", Status: "running"}}}},
		{"api-error-stop-failure", "StopFailure", Facts{Reason: "authentication_failed"}},
	} {
		e, _ := ParseEvent("000000001.42", eventFile(t, tc.fixture), at)
		if e.Kind != tc.kind || e.Source != SourceHook || e.File != "000000001.42" || !e.Time.Equal(at) || e.Facts.Branch != "feat-42" {
			t.Errorf("%s: %+v", tc.fixture, e)
		}
		got := e.Facts
		got.Branch, got.Cwd, got.Text = "", "", tc.want.Text // the text: below
		if tc.want.Session == "" {
			got.Session = ""
		}
		if tc.want.Turn == "" {
			got.Turn = ""
		}
		if tc.want.Tool == "" {
			got.Tool = ""
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", tc.fixture, got, tc.want)
		}
		if tc.want.Text != "" && e.Facts.Text != tc.want.Text {
			t.Errorf("%s: text %q", tc.fixture, e.Facts.Text)
		}
	}

	// A turn end: whether its reply ends with ?, its tasks, and the reply
	// itself apart, for the journal to keep once.
	e, r := ParseEvent("000000002.1", eventFile(t, "stop-question"), at)
	if !e.Facts.Question || r == nil || strings.TrimSpace(r.Text) != "Should I also update the README?" || e.Facts.Session != session {
		t.Errorf("stop: %+v %+v", e, r)
	}
	if data, _ := json.Marshal(e); strings.Contains(string(data), "README") {
		t.Errorf("the event keeps the reply: %s", data)
	}
	// A turn an API error ended: Claude's error line is its reply.
	if e, r := ParseEvent("000000002.fail.1", eventFile(t, "api-error-stop-failure"), at); r == nil || r.Text != "Login expired · Please run /login" || e.Facts.Hook != CaseFail {
		t.Errorf("api error: %+v %+v", e, r)
	}
	_, r = ParseEvent("000000003.1", eventFile(t, "stop-own-monitor"), at)
	if r == nil || len(r.Tasks) == 0 || r.Tasks[0].ID == "" {
		t.Errorf("own monitor's texts: %+v", r)
	}
	// What a dialog asks; a prompt as sent; the watch of a published
	// artifact.
	if e, _ := ParseEvent("1", eventFile(t, "dialog-bash"), at); !strings.HasPrefix(e.Facts.Text, "Bash: ") {
		t.Errorf("dialog: %+v", e.Facts)
	}
	if e, _ := ParseEvent("1", eventFile(t, "prompt"), at); e.Facts.Prompt == "" || e.Facts.Wake {
		t.Errorf("prompt: %+v", e.Facts)
	}
	if e, _ := ParseEvent("1", eventFile(t, "elicitation"), at); e.Kind != "Elicitation" || e.Facts.Server == "" {
		t.Errorf("elicitation: %+v", e.Facts)
	}
	stop := []byte(`{"hook_event_name":"Stop","last_assistant_message":"ok","background_tasks":[{"id":"w1","type":"monitor","status":"running","description":"Watch for comments (auto-armed on publish)"}]}`)
	if e, _ := ParseEvent("1", stop, at); len(e.Facts.Tasks) != 1 || !e.Facts.Tasks[0].Artifact || e.Facts.Branch != "" {
		t.Errorf("artifact watch: %+v", e.Facts)
	}
	launched := []byte(`{"hook_event_name":"PostToolUse","tool_name":"Agent","tool_response":{"status":"async_launched","agentId":"f1"}}`)
	if e, _ := ParseEvent("1", launched, at); e.Facts.Started != "f1" {
		t.Errorf("a subagent started: %+v", e.Facts)
	}
	workflow := []byte(`{"hook_event_name":"PostToolUseFailure","tool_name":"Workflow","tool_response":{"status":"async_launched","taskId":"w9"}}`)
	if e, _ := ParseEvent("1", workflow, at); e.Facts.Started != "w9" || e.Kind != "PostToolUseFailure" {
		t.Errorf("a workflow started: %+v", e)
	}
	shell := []byte(`{"hook_event_name":"PostToolUse","agent_id":"s1","tool_name":"Bash","tool_response":{"backgroundTaskId":"b7"}}`)
	if e, _ := ParseEvent("1", shell, at); e.Facts.Started != "~b7" {
		t.Errorf("a subagent's shell: %+v", e.Facts)
	}
	quiet := []byte(`{"hook_event_name":"PostToolUse","agent_id":"s1","tool_name":"Bash","tool_response":{"stdout":"x"}}`)
	if e, _ := ParseEvent("1", quiet, at); e.Facts.Started != "" {
		t.Errorf("a subagent's tool: %+v", e.Facts)
	}
}

func TestParseEventOfADelivery(t *testing.T) {
	e, r := ParseEvent("000000004.9", []byte("delivery prompt\n0000000001700000000-0a1b2c3d.sup\n../../etc\n0000000001700000001-1a2b3c4d.now\n"), time.Unix(5, 0))
	if r != nil || e.Kind != KindDelivery || e.Facts.Hook != "prompt" || e.Source != SourceHook ||
		strings.Join(e.Facts.Messages, " ") != "0000000001700000000-0a1b2c3d.sup 0000000001700000001-1a2b3c4d.now" {
		t.Errorf("%+v %+v", e, r)
	}
}

func TestParseEventOfAFileHqCannotRead(t *testing.T) {
	for _, data := range []string{"", "branch main\n", "branch main\n{\"x\":1}", "garbage"} {
		e, r := ParseEvent("000000001.1", []byte(data), time.Unix(5, 0))
		if r != nil || e.Kind != KindUnreadable || e.Facts.Size != int64(len(data)) || e.File != "000000001.1" {
			t.Errorf("%q: %+v", data, e)
		}
	}
	if e := UnreadableEvent("000000002.1", 99, time.Unix(5, 0)); e.Kind != KindUnreadable || e.Facts.Size != 99 || e.Source != SourceHook {
		t.Errorf("%+v", e)
	}
}

func TestEventFilesAndIds(t *testing.T) {
	if EventsDir("/w/app", "a1") != "/w/app/.git/hq/events/a1" {
		t.Error(EventsDir("/w/app", "a1"))
	}
	for name, ok := range map[string]bool{"000000001.12": true, ".000000001.12": false, ".n": false, "": false, "x": false} {
		if IsEventFile(name) != ok {
			t.Errorf("%q: %v", name, !ok)
		}
	}
	if !IsID("0a1b") || IsID("../x") {
		t.Error("ids")
	}
	if size, ok := TooLargeSize(tooLargeError{size: 7}); !ok || size != 7 {
		t.Error("too large")
	}
	if _, ok := TooLargeSize(os.ErrNotExist); ok {
		t.Error("not too large")
	}
}

// The hook names each event file by the case that wrote it, which the
// reducer follows; a name without one gives none.
func TestParseEventTakesTheHooksKindFromTheFilesName(t *testing.T) {
	at := time.Unix(5, 0)
	for name, kind := range map[string]string{
		"000000001.start.42": "start", "000000012.answer.7": "answer", "000000001.42": "",
		"000000001.Stop.42": "", "00000001.stop.42": "", "000000001.stop.": "", "x000000001.stop.42": "",
	} {
		if e, _ := ParseEvent(name, eventFile(t, "prompt"), at); e.Facts.Hook != kind || e.Kind != "UserPromptSubmit" {
			t.Errorf("%s: %+v", name, e.Facts)
		}
	}
	// A report hq cannot read keeps the case and the branch the hook
	// wrote; a file too large to read the case.
	if e, _ := ParseEvent("000000003.prompt.9", []byte("branch feat-42\n{\"session_id\":\"0b1c"), at); e.Kind != KindUnreadable || e.Facts.Hook != "prompt" || e.Facts.Branch != "feat-42" {
		t.Errorf("cut off: %+v", e)
	}
	if e := UnreadableEvent("000000004.stop.9", maxFile+1, at); e.Facts.Hook != "stop" || e.Facts.Size != maxFile+1 {
		t.Errorf("too large: %+v", e)
	}
}

// An event file's name starts with the hook's count, a digit; the hook's
// temporary files and its count start with a dot, and anything else is no
// event file of the hook's.
func TestAnEventFileIsNamedForTheHooksCount(t *testing.T) {
	for name, want := range map[string]bool{
		"000000001.stop.9": true, "9": true, "0": true, "": false, ".n": false, ".000000001.stop.9": false, "a1": false, "/": false, ":": false,
	} {
		if IsEventFile(name) != want {
			t.Errorf("IsEventFile(%q) = %v", name, !want)
		}
	}
}
