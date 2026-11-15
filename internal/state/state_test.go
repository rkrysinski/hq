package state

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestADialogShowsWhatItAsksOrTheToolItAllows(t *testing.T) {
	for _, tc := range []struct{ payload, want string }{
		{`{"hook_event_name":"PermissionRequest","tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Red\nor blue?"},{"question":"Why?"}]}}`, "Red or blue?"},
		{`{"hook_event_name":"PermissionRequest","tool_name":"AskUserQuestion","tool_input":{"questions":[]}}`, "AskUserQuestion"},
		{`{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"rm -rf build"}}`, "Bash: rm -rf build"},
		{`{"hook_event_name":"PermissionRequest","tool_name":"Edit","tool_input":{"file_path":"/w/app/x.go"}}`, "Edit: /w/app/x.go"},
		{`{"hook_event_name":"PermissionRequest","tool_name":"WebFetch","tool_input":{"url":"https://example.com"}}`, "WebFetch: https://example.com"},
		{`{"hook_event_name":"PermissionRequest","tool_name":"ExitPlanMode","tool_input":{}}`, "ExitPlanMode"},
	} {
		if e, _ := ParseEvent("000000001.dialog.1", []byte(tc.payload), time.Unix(1, 0)); e.Kind != "PermissionRequest" || e.Facts.Text != tc.want {
			t.Errorf("%s: %q %q, want %q", tc.payload, e.Kind, e.Facts.Text, tc.want)
		}
	}
}

func TestIsQuestionLooksAtTheLastCharacter(t *testing.T) {
	for msg, want := range map[string]bool{"Ready?": true, "Ready?  \n": true, "Is it? Yes.": false, "": false, "?!": false} {
		if IsQuestion(msg) != want {
			t.Errorf("IsQuestion(%q) != %v", msg, want)
		}
	}
}

func TestCleanStripsEscapesAndControlCharacters(t *testing.T) {
	for in, want := range map[string]string{
		"plain":                                "plain",
		"two\nlines\r\n\tand tab":              "two lines and tab",
		"\x1b[31mred\x1b[0m":                   "red",
		"\x1b]0;evil title\x07after":           "after",
		"\x1b]9;fake notification\x1b\\after":  "after",
		"\x1bPdcs\x1b\\x":                      "x",
		"bell\x07 nul\x00 del\x7f c1\u009b31m": "bell nul del c131m",
		"ok\xffbad utf8":                       "okbad utf8",
		"line sep":                             "linesep",
		"  spaced   out  ":                     "spaced out",
		"dangling\x1b":                         "dangling",
		"\x1b[12;3":                            "",
		"zażółć":                               "zażółć",
	} {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Clean(strings.Repeat("ą", 600)); len([]rune(got)) != maxLast {
		t.Errorf("not capped: %d", len([]rune(got)))
	}
}

func TestSettingsCarryIdentityAndOneHookPerEvent(t *testing.T) {
	raw := Settings("a", "0123abcd", `\u0007`)
	if !strings.Contains(raw, `"HQ_ID":"0123abcd"`) {
		t.Fatalf("hq kill finds the session by HQ_ID in its arguments: %s", raw)
	}
	var s struct {
		Env   map[string]string
		Hooks map[string][]hookMatcher
	}
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatal(err)
	}
	// TMUX set makes Claude wrap notifications so tmux passes them on.
	if s.Env["HQ_AGENT"] != "a" || s.Env["HQ_ID"] != "0123abcd" || s.Env["TMUX"] == "" || s.Env["HQ_HOOK"] != hookScript {
		t.Fatalf("env %v", s.Env)
	}
	// Only Stop, a dialog (an MCP elicitation's too) and the attention
	// Notifications notify, with the sequence; the end of any tool may close
	// a dialog, and the answer to an elicitation's form closes it.
	events := map[string]string{
		"UserPromptSubmit": "prompt", "PermissionRequest": "dialog \\u0007", "PostToolUse": "answer", "PostToolUseFailure": "answer",
		"Stop": "stop \\u0007", "Notification": "input \\u0007", "SessionEnd": "end",
		"Elicitation": "dialog \\u0007", "ElicitationResult": "result", "PreToolUse": "refuse",
	}
	// SessionStart: a new session or /clear starts, a resumed one resumes;
	// never a compaction, which may come in the middle of a turn. Neither
	// notifies: the user has just started the agent.
	start := s.Hooks["SessionStart"]
	if len(start) != 2 || start[0].Matcher != "startup|clear" || start[1].Matcher != "resume" {
		t.Fatalf("SessionStart: %+v", start)
	}
	for i, kind := range []string{"start", "resume"} {
		if h := start[i].Hooks; len(h) != 1 || h[0].Command != "sh" || h[0].Args[1] != `eval "$HQ_HOOK"` || strings.Join(h[0].Args[3:], " ") != kind {
			t.Errorf("SessionStart %s: %+v", start[i].Matcher, h)
		}
	}
	if len(s.Hooks) != len(events)+1 {
		t.Errorf("hooks for %d events, want %d", len(s.Hooks), len(events)+1)
	}
	for event, args := range events {
		m := s.Hooks[event]
		if event == "PermissionRequest" {
			m = m[:1]
		}
		if len(m) != 1 || len(m[0].Hooks) != 1 {
			t.Fatalf("%s: %+v", event, m)
		}
		h := m[0].Hooks[0]
		if h.Type != "command" || h.Command != "sh" || h.Args[0] != "-c" || h.Args[1] != `eval "$HQ_HOOK"` || strings.Join(h.Args[3:], " ") != args {
			t.Errorf("%s: %+v", event, h)
		}
	}
	// hq's kill, answer and send, as Claude names them whatever the server's
	// name, are checked before they run for a call naming the agent itself
	// (#142); no other tool waits for the hook.
	pre := regexp.MustCompile(s.Hooks["PreToolUse"][0].Matcher)
	for tool, checked := range map[string]bool{
		"mcp__hq__kill": true, "mcp__mcp-gateway__answer": true, "mcp__hq__send": true,
		"mcp__hq__read": false, "mcp__hq__list": false, "mcp__hq__wait": false, "mcp__hq__go": false, "mcp__hq__new": false,
		"mcp__hq__killall": false, "kill": false, "Bash": false,
	} {
		if pre.MatchString(tool) != checked {
			t.Errorf("PreToolUse matcher %q on %s: %v", pre, tool, !checked)
		}
	}
	// An MCP elicitation is reported by its own hook (#100), not by the
	// Notification Claude sends for it.
	if s.Hooks["Notification"][0].Matcher != "permission_prompt|agent_needs_input" {
		t.Errorf("matcher %q", s.Hooks["Notification"][0].Matcher)
	}
	// The settings are one argument of sbx run (hq __session writes them out,
	// #40), which on WSL is sbx.exe: Windows takes a command line of at most
	// 32 KiB, and the prompt and the rest of the line need their share.
	if len(raw) > 24<<10 {
		t.Errorf("settings of %d bytes", len(raw))
	}
	// The hook script has been kept within a budget of the settings (design
	// §3.4); a change that needs more says so here. 9 KiB since question
	// dialogs are answered from hq (#90): the hold that takes the answer
	// came on top of the elicitation's report (#100), well within the 24
	// KiB of the settings. 10 KiB since kill, answer and send refuse the
	// calling agent (#142): the walk of the tool's input took the script to
	// about 9.6 KiB. The event files of ADR 0018 took it to 10,229 bytes
	// (#193), while it writes the old files too, and 10,236 with the case
	// that wrote each in its name (#196).
	if len(hookScript) > 10<<10 {
		t.Errorf("hook script of %d bytes", len(hookScript))
	}
	for _, event := range []string{"PermissionRequest", "PostToolUse", "PostToolUseFailure"} {
		if m := s.Hooks[event][0].Matcher; m != "" {
			t.Errorf("%s: every tool, not %q", event, m)
		}
	}
	// A question dialog's PermissionRequest has a hook of its own besides,
	// which holds until hq answers or the dialog closes: Claude kills it
	// only at its timeout, a day, past any user waiting at the dialog (#90).
	pr := s.Hooks["PermissionRequest"]
	if len(pr) != 2 || pr[1].Matcher != "AskUserQuestion" || len(pr[1].Hooks) != 1 {
		t.Fatalf("PermissionRequest: %+v", pr)
	}
	if h := pr[1].Hooks[0]; h.Command != "sh" || h.Args[1] != `eval "$HQ_HOOK"` || strings.Join(h.Args[3:], " ") != "hold" || h.Timeout != 86400 {
		t.Errorf("the question dialog's hook: %+v", h)
	}
	for event, m := range s.Hooks {
		for _, g := range m {
			for _, h := range g.Hooks {
				if h.Timeout != 0 && strings.Join(h.Args[3:], " ") != "hold" {
					t.Errorf("%s: a timeout of its own: %+v", event, h)
				}
			}
		}
	}
}
