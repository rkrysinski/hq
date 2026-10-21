package state

import (
	"bytes"
	"reflect"
	"testing"
)

// The sbx template's own PermissionRequest hook runs on every request,
// AskUserQuestion included (#128).
func TestAskHooksFindsTheTemplatesApproveHook(t *testing.T) {
	got := AskHooks(fixture(t, "managed-settings-template"))
	want := []AskHook{{Command: "/usr/local/bin/claude-approve"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v", got)
	}
}

// Claude runs a PermissionRequest hook for AskUserQuestion when its matcher
// takes the tool's name: none, "*", the name, or a pattern that matches it
// whole; a hook of another event, matched to another tool or not a
// command is not run for it.
func TestAskHooksAreThoseClaudeRunsForAskUserQuestion(t *testing.T) {
	settings := []byte(`{"hooks":{
		"PreToolUse":[{"hooks":[{"type":"command","command":"pre"}]}],
		"PermissionRequest":[
			{"hooks":[{"type":"command","command":"all"}]},
			{"matcher":"","hooks":[{"type":"command","command":"empty"}]},
			{"matcher":"*","hooks":[{"type":"command","command":"star"}]},
			{"matcher":"AskUserQuestion","hooks":[{"type":"command","command":"named"},{"type":"command","command":"sh","args":["-c","exec-form"]}]},
			{"matcher":"Bash|AskUserQuestion","hooks":[{"type":"command","command":"listed"}]},
			{"matcher":"Ask.*","hooks":[{"type":"command","command":"pattern"}]},
			{"matcher":"Bash","hooks":[{"type":"command","command":"bash"}]},
			{"matcher":"Ask","hooks":[{"type":"command","command":"part"}]},
			{"matcher":"mcp__.*","hooks":[{"type":"command","command":"mcp"}]},
			{"hooks":[{"type":"prompt","prompt":"judge it"}]}
		]}}`)
	var got []string
	for _, h := range AskHooks(settings) {
		got = append(got, h.Command)
	}
	want := []string{"all", "empty", "star", "named", "sh", "listed", "pattern"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%q", got)
	}
}

// The managed settings may be several files, read one after the other
// (managed-settings.json, then managed-settings.d): each one's hooks count.
func TestAskHooksReadsEverySettingsFile(t *testing.T) {
	settings := append(fixture(t, "managed-settings-template"), []byte(`{"hooks":{"PermissionRequest":[{"hooks":[{"type":"command","command":"second"}]}]}}`)...)
	settings = append(settings, []byte(`{"permissions":{}}`)...)
	got := AskHooks(settings)
	if len(got) != 2 || got[1].Command != "second" {
		t.Fatalf("%+v", got)
	}
}

func TestAskHooksOfNoSettingsIsNone(t *testing.T) {
	for _, s := range []string{"", "not json", `{"hooks":"odd"}`, `{"hooks":{"PermissionRequest":{}}}`} {
		if got := AskHooks([]byte(s)); len(got) != 0 {
			t.Errorf("%q: %+v", s, got)
		}
	}
	// What was read before a broken file still counts.
	if got := AskHooks(append(fixture(t, "managed-settings-template"), "{broken"...)); len(got) != 1 {
		t.Fatalf("%+v", got)
	}
}

// A hook runs as Claude runs it: a command line through the shell, or the
// command with its arguments when it has some.
func TestAskHookArgv(t *testing.T) {
	if got := (AskHook{Command: "/usr/local/bin/claude-approve --quiet"}).Argv(); !reflect.DeepEqual(got, []string{"sh", "-c", "/usr/local/bin/claude-approve --quiet"}) {
		t.Fatalf("%q", got)
	}
	if got := (AskHook{Command: "sh", Args: []string{"-c", "x"}}).Argv(); !reflect.DeepEqual(got, []string{"sh", "-c", "x"}) {
		t.Fatalf("%q", got)
	}
}

// A hook's output decides a PermissionRequest when it carries a decision:
// the template's bare allow does, and Claude then takes no other hook's
// answer to AskUserQuestion (ADR 0013, #128).
func TestDecides(t *testing.T) {
	for out, want := range map[string]bool{
		`{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}` + "\n":        true,
		`{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"deny","message":"no"}}}`: true,
		"":         false,
		"\n":       false,
		"approved": false,
		`{"terminalSequence":"\u001b]9;hi\u0007"}`:                                   false,
		`{"hookSpecificOutput":{"hookEventName":"PermissionRequest"}}`:               false,
		`{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{}}}`: false,
	} {
		if got := Decides([]byte(out)); got != want {
			t.Errorf("%q: %v", out, got)
		}
	}
}

// The dialog's payload is what Claude gave its hooks, for a hook of the
// template to be run with it as Claude ran it.
func TestQuestionsPayloadIsTheDialogsReport(t *testing.T) {
	payload := fixture(t, "dialog-ask-three")
	q, err := ParseQuestions(append([]byte("branch main\n"), payload...))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(q.Payload(), payload) {
		t.Fatalf("%q", q.Payload())
	}
}

// Claude Code 2.1.304's AskUserQuestion of three questions, as hq's hook
// sees it (#128): read, and answered with the first option of each.
func TestQuestionsOf2_1_304(t *testing.T) {
	q := questions(t, "dialog-ask-three")
	if len(q.Ask.Questions) != 3 || q.Ask.Questions[2].Question != "Which shape?" || len(q.Ask.Questions[1].Options) != 2 {
		t.Fatalf("%+v", q.Ask)
	}
	d, sent, err := q.Decision([]Answer{{Options: []int{1}}, {Options: []int{1}}, {Options: []int{1}}})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"Which colour?": "Red", "Which size?": "Small", "Which shape?": "Circle"}
	if !reflect.DeepEqual(sent, want) {
		t.Fatalf("%v", sent)
	}
	if got := decode(t, d).HookSpecificOutput.Decision.UpdatedInput["answers"]; !reflect.DeepEqual(got, map[string]any{"Which colour?": "Red", "Which size?": "Small", "Which shape?": "Circle"}) {
		t.Fatalf("%v", got)
	}
}
