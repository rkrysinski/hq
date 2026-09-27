package state

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseDerivesTheStateFromTheLatestEvent(t *testing.T) {
	stop := fixture(t, "stop-done")
	for _, tc := range []struct {
		latest, lastStop string
		state, last      string
	}{
		{"prompt", "", Working, ""},
		{"prompt", "stop-done", Working, "Hi. The tests pass and PR #58 is open."},
		{"stop-done", "stop-done", Done, "Hi. The tests pass and PR #58 is open."},
		{"stop-question", "stop-question", Question, "Should I also update the README?"},
		{"notification", "stop-done", NeedsInput, "Claude needs your permission"},
		{"session-end", "stop-done", Ended, "Hi. The tests pass and PR #58 is open."},
	} {
		var lastStop []byte
		if tc.lastStop != "" {
			lastStop = stop
			if tc.lastStop != "stop-done" {
				lastStop = fixture(t, tc.lastStop)
			}
		}
		r := Parse(fixture(t, tc.latest), lastStop)
		if r.State != tc.state || r.Last != tc.last {
			t.Errorf("%s after %s: got %q %q, want %q %q", tc.latest, tc.lastStop, r.State, r.Last, tc.state, tc.last)
		}
		if r.SessionID != "0b1c2d3e-4f50-6172-8394-a5b6c7d8e9f0" || r.Cwd != "/w/app/.claude/worktrees/feat-42" {
			t.Errorf("%s: session %q cwd %q", tc.latest, r.SessionID, r.Cwd)
		}
	}
}

func TestParseTakesTheBranchFromTheHookHeader(t *testing.T) {
	stop := append([]byte("branch main\n"), fixture(t, "stop-done")...)
	for _, tc := range []struct{ header, branch string }{
		{"branch feat/42-x\n", "feat/42-x"},
		{"branch \n", ""},                         // detached HEAD
		{"branch feat/\x1b[31mred\n", "feat/red"}, // stripped like any field
		{"", ""}, // an older hook, no header
	} {
		r := Parse(append([]byte(tc.header), fixture(t, "prompt")...), stop)
		if r.State != Working || r.Branch != tc.branch || r.Last != "Hi. The tests pass and PR #58 is open." {
			t.Errorf("%q: %+v", tc.header, r)
		}
	}
}

func TestParseTreatsUnknownOrBrokenEventsAsStarting(t *testing.T) {
	for _, latest := range []string{`not json`, `{"hook_event_name":"PreCompact"}`, ``} {
		if r := Parse([]byte(latest), nil); r.State != Starting {
			t.Errorf("%q: %q", latest, r.State)
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
	if s.Env["HQ_AGENT"] != "a" || s.Env["HQ_ID"] != "0123abcd" || s.Env["TMUX"] == "" {
		t.Fatalf("env %v", s.Env)
	}
	// Only Stop and the attention Notifications notify, with the sequence.
	for event, args := range map[string]string{"UserPromptSubmit": "prompt", "Stop": "stop \\u0007", "Notification": "input \\u0007", "SessionEnd": "end"} {
		m := s.Hooks[event]
		if len(m) != 1 || len(m[0].Hooks) != 1 {
			t.Fatalf("%s: %+v", event, m)
		}
		h := m[0].Hooks[0]
		if h.Type != "command" || h.Command != "sh" || h.Args[0] != "-c" || h.Args[1] != hookScript || strings.Join(h.Args[3:], " ") != args {
			t.Errorf("%s: %+v", event, h)
		}
	}
	if s.Hooks["Notification"][0].Matcher != "permission_prompt|agent_needs_input|elicitation_dialog" {
		t.Errorf("matcher %q", s.Hooks["Notification"][0].Matcher)
	}
}
