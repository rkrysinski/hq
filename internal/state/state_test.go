package state

import (
	"encoding/json"
	"os"
	"reflect"
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
		{"dialog-ask", "stop-done", NeedsInput, "Which colour do you pick: red or blue?"},
		{"dialog-bash", "stop-done", NeedsInput, "Bash: Create empty probe file in /tmp"},
		{"answer-ask", "stop-done", Working, "Hi. The tests pass and PR #58 is open."},
		{"session-end", "stop-done", Ended, "Hi. The tests pass and PR #58 is open."},
		{"session-start", "", Done, ""}, // a new session waits at its prompt
		{"session-start-clear", "stop-done", Done, "Hi. The tests pass and PR #58 is open."},
	} {
		var lastStop []byte
		if tc.lastStop != "" {
			lastStop = stop
			if tc.lastStop != "stop-done" {
				lastStop = fixture(t, tc.lastStop)
			}
		}
		r := Parse(fixture(t, tc.latest), lastStop, nil)
		if r.State != tc.state || r.Last != tc.last {
			t.Errorf("%s after %s: got %q %q, want %q %q", tc.latest, tc.lastStop, r.State, r.Last, tc.state, tc.last)
		}
		// A new session has no conversation to resume yet.
		session := "0b1c2d3e-4f50-6172-8394-a5b6c7d8e9f0"
		if tc.latest == "session-start" {
			session = ""
		}
		if r.SessionID != session || r.Cwd != "/w/app/.claude/worktrees/feat-42" {
			t.Errorf("%s: session %q cwd %q", tc.latest, r.SessionID, r.Cwd)
		}
	}
}

func TestAResumedSessionKeepsWhatTheEventBeforeItGave(t *testing.T) {
	resume, stop := fixture(t, "session-start-resume"), fixture(t, "stop-done")
	// Relaunched with --resume, Claude starts in the repository, not in the
	// worktree its conversation left, where the event before it was a dialog.
	before := append([]byte("branch feat-42\n"), fixture(t, "dialog-bash")...)
	r := Parse(append([]byte("branch main\n"), resume...), stop, before)
	if r.State != Done || r.Branch != "feat-42" || r.Cwd != "/w/app/.claude/worktrees/feat-42" || r.Last != "Bash: Create empty probe file in /tmp" || r.Prompt != "" {
		t.Fatalf("resumed: %+v", r)
	}
	if r.SessionID != "0b1c2d3e-4f50-6172-8394-a5b6c7d8e9f0" {
		t.Fatalf("session %q", r.SessionID)
	}
	// With nothing before it (an agent that never reported, or a broken
	// file), it reports where it is.
	for _, prev := range [][]byte{nil, []byte("branch feat-42\nnot json")} {
		r := Parse(append([]byte("branch main\n"), resume...), stop, prev)
		if r.State != Done || r.Branch != "main" || r.Cwd != "/w/app" || r.Last != "Hi. The tests pass and PR #58 is open." {
			t.Errorf("%q: %+v", prev, r)
		}
	}
	// Only a resumed session looks at the event before it.
	if r := Parse(append([]byte("branch main\n"), fixture(t, "session-start")...), stop, before); r.Branch != "main" || r.Last != "Hi. The tests pass and PR #58 is open." {
		t.Errorf("startup: %+v", r)
	}
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
		if r := Parse([]byte(tc.payload), fixture(t, "stop-done"), nil); r.State != NeedsInput || r.Last != tc.want {
			t.Errorf("%s: %q %q, want %q", tc.payload, r.State, r.Last, tc.want)
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
		r := Parse(append([]byte(tc.header), fixture(t, "prompt")...), stop, nil)
		if r.State != Working || r.Branch != tc.branch || r.Last != "Hi. The tests pass and PR #58 is open." {
			t.Errorf("%q: %+v", tc.header, r)
		}
	}
}

func TestParseTreatsUnknownOrBrokenEventsAsStarting(t *testing.T) {
	for _, latest := range []string{`not json`, `{"hook_event_name":"PreCompact"}`, ``} {
		if r := Parse([]byte(latest), nil, nil); r.State != Starting {
			t.Errorf("%q: %q", latest, r.State)
		}
	}
}

// A session cut off while it fed its hook an event (Claude killed during
// its SessionEnd) leaves a report the hook wrote with what it got: the
// header and the start of the payload, or none of it. Its last known
// message is still the latest Stop's (#73).
func TestParseKeepsTheLastStopsMessageBeyondAnUnreadableEvent(t *testing.T) {
	stop := append([]byte("branch main\n"), fixture(t, "stop-done")...)
	for _, latest := range []string{"branch main\n", "branch main\n" + `{"session_id":"fa4e`} {
		r := Parse([]byte(latest), stop, nil)
		if r.State != Starting || r.Last != "Hi. The tests pass and PR #58 is open." || r.Branch != "main" {
			t.Errorf("%q: %+v", latest, r)
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
		"Elicitation": "dialog \\u0007", "ElicitationResult": "result",
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
	// KiB of the settings.
	if len(hookScript) > 9<<10 {
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

func TestParseKeepsThePromptOfAWorkingTurn(t *testing.T) {
	if r := Parse(fixture(t, "prompt"), nil, nil); r.Prompt != "say hi" {
		t.Errorf("prompt: %q", r.Prompt)
	}
	for _, name := range []string{"answer-ask", "stop-done", "dialog-bash"} {
		if r := Parse(fixture(t, name), nil, nil); r.Prompt != "" {
			t.Errorf("%s: %q", name, r.Prompt)
		}
	}
}

func TestATurnEndWithBackgroundWorkKeptBesideAWorkingReportLeavesItWorking(t *testing.T) {
	since := time.Unix(1000, 0)
	working := Parse(fixture(t, "prompt"), fixture(t, "stop-background"), nil)
	working.Since, working.Latest = since, since
	at := since.Add(time.Minute)

	// The turn ended while a subagent still runs: working since the prompt,
	// with that turn's message, waiting at its prompt.
	r := working.Kept(fixture(t, "prompt"), fixture(t, "stop-background"), nil, at)
	if r.State != Working || !r.Background || r.Last != "The tests are running in a subagent." {
		t.Fatalf("kept turn end: %+v", r)
	}
	if !r.Since.Equal(since) || !r.Latest.Equal(at) {
		t.Fatalf("times: since %v latest %v", r.Since, r.Latest)
	}
	if r.Owed {
		t.Fatal("a subagent runs: no turn is owed")
	}
	// The same turn end with nothing running any more, kept because Claude
	// owes a turn for work that finished together with other work.
	owed := []byte(strings.Replace(string(fixture(t, "stop-done")), `"background_tasks": []`, `"background_tasks": [{"id": "b1", "type": "shell", "status": "running"}]`, 1))
	if r = working.Kept(fixture(t, "prompt"), owed, nil, at); r.State != Working || !r.Background || !r.Owed {
		t.Fatalf("a turn owed: %+v", r)
	}
	// The same shell command started by a subagent, which the hook's list
	// names: it runs, so no turn is owed (#60). Another command, or the
	// list naming it otherwise, leaves the turn owed.
	for list, wantOwed := range map[string]bool{"~b1\n": false, "a1\n~b1\n!a2\n": false, "~b2\n": true, "b1\n!b1\n": true, "~\n": true} {
		if r = working.Kept(fixture(t, "prompt"), owed, []byte(list), at); r.State != Working || !r.Background || r.Owed != wantOwed {
			t.Errorf("list %q: %+v", list, r)
		}
	}
	// A watch (a monitor task) counts like a shell command (#62): the
	// artifact watch Claude arms on publish, or the agent's own Monitor
	// tool, runs to the session's end and wakes nobody, so a turn is owed
	// for it; one a subagent started, named in the list, runs as work, and
	// a subagent beside it does too.
	stop := func(tasks string) []byte {
		return []byte(strings.Replace(string(fixture(t, "stop-done")), `"background_tasks": []`, `"background_tasks": [`+tasks+`]`, 1))
	}
	const monitor = `{"id": "m1", "type": "monitor", "status": "running", "description": "live updates for artifact https://claude.ai/artifact/x (auto-armed on publish)"}`
	for _, tc := range []struct {
		name  string
		tasks string
		list  string
		owed  bool
	}{
		{"a monitor alone", monitor, "", true},
		{"a monitor a subagent started", monitor, "~m1\n", false},
		{"a subagent beside a monitor", monitor + `, {"id": "a1", "type": "subagent", "status": "running"}`, "", false},
		{"a task of a kind not seen", `{"id": "z1", "type": "cron", "status": "running"}`, "", false},
	} {
		if r = working.Kept(fixture(t, "prompt"), stop(tc.tasks), []byte(tc.list), at); r.State != Working || !r.Background || r.Owed != tc.owed {
			t.Errorf("%s: %+v", tc.name, r)
		}
	}
	// Claude's wake-up when the subagent has finished: the closing turn is
	// at work, since the same prompt.
	r = working.Kept(fixture(t, "prompt"), fixture(t, "prompt-wake"), nil, at)
	if r.State != Working || r.Background || !r.Woken || !r.Since.Equal(since) || !r.Latest.Equal(at) || r.Prompt != "say hi" {
		t.Fatalf("wake-up: %+v", r)
	}
	// Nothing kept, or something unreadable: the report as it is.
	for _, kept := range []string{"", "{", `{"hook_event_name":"SessionEnd"}`} {
		if r := working.Kept(fixture(t, "prompt"), []byte(kept), nil, at); !reflect.DeepEqual(r, working) {
			t.Errorf("kept %q: %+v", kept, r)
		}
	}
}

func TestAKeptTurnEndCountsOnlyBesideAReportItBelongsTo(t *testing.T) {
	since := time.Unix(1000, 0)
	at := since.Add(time.Minute)
	kept := fixture(t, "stop-background")
	for _, tc := range []struct {
		latest     string
		state      string
		background bool
	}{
		{"stop-background", Working, true}, // the hook wrote the turn end as the report too
		{"answer-ask", Working, true},      // a dialog answered meanwhile (a subagent's)
		{"stop-done", Done, false},
		{"stop-background-question", Question, false},
		{"dialog-ask", NeedsInput, false},
		{"notification", NeedsInput, false},
		{"session-start", Done, false},
		{"session-end", Ended, false},
	} {
		latest := fixture(t, tc.latest)
		r := Parse(latest, kept, nil)
		r.Since, r.Latest = since, since
		r = r.Kept(latest, kept, nil, at)
		if r.State != tc.state || r.Background != tc.background || !r.Since.Equal(since) {
			t.Errorf("%s: %+v", tc.latest, r)
		}
		if wantLatest := map[bool]time.Time{true: at, false: since}[tc.background]; !r.Latest.Equal(wantLatest) {
			t.Errorf("%s: latest %v", tc.latest, r.Latest)
		}
	}
	// A wake-up that is the report itself, after done or a question, is a
	// turn Claude woke itself for too.
	if r := Parse(fixture(t, "prompt-wake"), kept, nil); r.State != Working || !r.Woken || r.Background {
		t.Errorf("wake-up as the report: %+v", r)
	}
	if r := Parse(fixture(t, "prompt"), nil, nil); r.Woken {
		t.Errorf("the user's prompt: %+v", r)
	}
	// A wake-up counts only while the report says working.
	for _, latest := range []string{"stop-done", "dialog-ask", "session-start"} {
		r := Parse(fixture(t, latest), nil, nil)
		if got := r.Kept(fixture(t, latest), fixture(t, "prompt-wake"), nil, at); !reflect.DeepEqual(got, r) {
			t.Errorf("wake-up beside %s: %+v", latest, got)
		}
	}
	// A turn end with background work that an older hook reported is done,
	// as it was: nothing is kept beside it.
	if r := Parse(kept, kept, nil).Kept(kept, nil, nil, at); r.State != Done || r.Background {
		t.Errorf("an older hook's report: %+v", r)
	}
}

// A subagent's permission prompt carries the subagent's agent_id, as its
// tool events do: the report says the dialog is the subagent's. Its MCP
// elicitation's Notification carries none, the same as the agent's own
// (both seen with Claude Code 2.1.301, a background subagent calling an
// MCP tool that elicits; paths shortened), so on its own it is the
// agent's.
func TestADialogASubagentOpensIsTheSubagents(t *testing.T) {
	if r := Parse(fixture(t, "dialog-subagent"), nil, nil); r.State != NeedsInput || !r.SubagentDialog || r.Last != "mcp__elicit__ask_color" {
		t.Errorf("a subagent's permission prompt: %+v", r)
	}
	for _, name := range []string{"dialog-bash", "dialog-ask", "notification", "notification-elicitation"} {
		if r := Parse(fixture(t, name), nil, nil); r.State != NeedsInput || r.SubagentDialog {
			t.Errorf("%s: %+v", name, r)
		}
	}
	// A subagent's other events open no dialog.
	if r := Parse(fixture(t, "post-tool-subagent"), nil, nil); r.SubagentDialog {
		t.Errorf("a subagent's tool end: %+v", r)
	}
}

// A dialog Claude's Notification reports after the agent's turn ended with
// background work running (the Stop the hook keeps) is a subagent's: the
// agent has no turn then, only its subagents work. Claude's own dialogs
// there fire no hook (#70).
func TestADialogNotifiedWhileTheAgentWaitsOnBackgroundWorkIsASubagents(t *testing.T) {
	at := time.Unix(1000, 0)
	notified := fixture(t, "notification-elicitation")
	stop := fixture(t, "stop-background")
	r := Parse(notified, stop, nil).Kept(notified, stop, nil, at)
	if r.State != NeedsInput || !r.SubagentDialog || r.Background || r.Last != "Claude Code needs your input" || r.LastStop != "The tests are running in a subagent." {
		t.Fatalf("notified on background work: %+v", r)
	}
	// Beside a wake-up the agent has a turn again: the dialog is its own.
	if r := Parse(notified, stop, nil).Kept(notified, fixture(t, "prompt-wake"), nil, at); r.SubagentDialog {
		t.Fatalf("notified after a wake-up: %+v", r)
	}
}

// An MCP elicitation is reported by its own hook as it is drawn: the agent
// needs input at once, with the form's message as its last, and is working
// again once the form is answered at the agent, accepted, declined or
// cancelled, with its turn's last message (#100). A turn whose form was
// answered has been acted on: no early Esc rewinds it.
func TestAnElicitationNeedsInputUntilItsFormIsAnswered(t *testing.T) {
	stop := fixture(t, "stop-done")
	for name, last := range map[string]string{"elicitation": "Which color should I use?", "elicitation-url": "Sign in to the example service."} {
		if r := Parse(fixture(t, name), stop, nil); r.State != NeedsInput || r.Last != last || r.LastStop != "Hi. The tests pass and PR #58 is open." || r.SubagentDialog || r.Acted {
			t.Errorf("%s: %+v", name, r)
		}
	}
	for _, name := range []string{"elicitation-result-accept", "elicitation-result-decline", "elicitation-result-cancel", "elicitation-result-url-accept"} {
		if r := Parse(fixture(t, name), stop, nil); r.State != Working || r.Last != "Hi. The tests pass and PR #58 is open." || !r.Acted || r.Prompt != "" {
			t.Errorf("%s: %+v", name, r)
		}
	}
	// An elicitation with no message says which server asks.
	if r := Parse([]byte(`{"hook_event_name":"Elicitation","mcp_server_name":"elicit","mode":"form"}`), nil, nil); r.State != NeedsInput || r.Last != "elicit" {
		t.Errorf("no message: %+v", r)
	}
}

// A background subagent's elicitation carries nothing of the subagent's
// (#89): reported after the agent's turn ended with the subagent running,
// beside the turn end the hook keeps, it is the subagent's, as its
// Notification was (#70); answered, the agent waits on its subagent again.
func TestAnElicitationWhileTheAgentWaitsOnBackgroundWorkIsASubagents(t *testing.T) {
	at := time.Unix(1000, 0)
	stop := fixture(t, "stop-background")
	elicited := fixture(t, "elicitation-subagent")
	if r := Parse(elicited, stop, nil).Kept(elicited, stop, nil, at); r.State != NeedsInput || !r.SubagentDialog || r.Last != "Which color should I use?" {
		t.Fatalf("a subagent's elicitation: %+v", r)
	}
	answered := fixture(t, "elicitation-result-accept-subagent")
	if r := Parse(answered, stop, nil).Kept(answered, stop, nil, at); r.State != Working || !r.Background || r.SubagentDialog || r.Last != "The tests are running in a subagent." {
		t.Fatalf("answered: %+v", r)
	}
}

// A dialog's text stands in for the last message while it is open; the
// report keeps the latest Stop's message beside it, which shows again once
// the dialog is answered and Claude works on (#70).
func TestADialogKeepsTheLatestStopsMessageBesideItsText(t *testing.T) {
	stop := fixture(t, "stop-done")
	for _, name := range []string{"dialog-ask", "dialog-bash", "notification", "prompt"} {
		if r := Parse(fixture(t, name), stop, nil); r.LastStop != "Hi. The tests pass and PR #58 is open." {
			t.Errorf("%s: last stop %q", name, r.LastStop)
		}
	}
	if r := Parse(fixture(t, "stop-question"), stop, nil); r.LastStop != r.Last || r.LastStop != "Should I also update the README?" {
		t.Errorf("a Stop: last stop %q, last %q", r.LastStop, r.Last)
	}
	if r := Parse(fixture(t, "dialog-ask"), nil, nil); r.LastStop != "" {
		t.Errorf("no Stop yet: last stop %q", r.LastStop)
	}
}

// A prompt in the hook's queued copy is pending until Claude takes it:
// the hook knows a taken one by the Stop it was taken at, which carries
// the same prompt_id as the copy (#82), and hq reads it the same way (#88).
func TestAQueuedPromptIsPendingUntilAStopOfItsTurnTakesIt(t *testing.T) {
	queued := fixture(t, "queued-while-working")
	if pending(queued, fixture(t, "queued-stop")) {
		t.Error("pending after the Stop that took it")
	}
	for name, stop := range map[string][]byte{"no Stop": nil, "an earlier Stop": fixture(t, "stop-done"), "the taken turn's Stop": fixture(t, "queued-turn-stop")} {
		if !pending(queued, stop) {
			t.Errorf("%s: not pending", name)
		}
	}
	if pending([]byte("branch main\nnot json"), nil) || pending(nil, nil) {
		t.Error("an unreadable copy is pending")
	}
}

// An agent done or with a question at a Stop shows its own tasks (#78): the
// shell commands and the watches it started itself that the Stop lists
// running, a shell by its command and a watch by its description, in full.
// Not a subagent's (named ~ID in the hook's list), not the artifact watch
// Claude arms on publish (its description says so), not a task that no
// longer runs, and not a task of any other type, which is background work.
func TestADoneOrQuestionAtAStopListsTheAgentsOwnTasks(t *testing.T) {
	stop := func(tasks string) []byte {
		return []byte(strings.Replace(string(fixture(t, "stop-done")), `"background_tasks": []`, `"background_tasks": [`+tasks+`]`, 1))
	}
	const (
		shell    = `{"id": "b1", "type": "shell", "status": "running", "description": "Sleep", "command": "sleep 120"}`
		monitor  = `{"id": "m1", "type": "monitor", "status": "running", "description": "Wait for CI on PR #58", "command": "gh pr checks 58 --watch"}`
		artifact = `{"id": "w1", "type": "monitor", "status": "running", "description": "live updates for artifact https://claude.ai/artifact/x (auto-armed on publish)"}`
	)
	for _, tc := range []struct {
		name, tasks, owed string
		want              []Task
	}{
		{"nothing runs", ``, ``, nil},
		{"a shell of its own", shell, ``, []Task{{"shell", "sleep 120"}}},
		{"a watch of its own", monitor, ``, []Task{{"monitor", "Wait for CI on PR #58"}}},
		{"both, in the Stop's order", monitor + `, ` + shell, `b0` + "\n", []Task{{"monitor", "Wait for CI on PR #58"}, {"shell", "sleep 120"}}},
		{"a subagent's shell", shell, "~b1\n", nil},
		{"a subagent's watch beside its own shell", shell + `, ` + strings.Replace(monitor, "m1", "m2", 1), "a1\n~m2\n", []Task{{"shell", "sleep 120"}}},
		{"the artifact watch", artifact, ``, nil},
		{"the artifact watch listed as a shell", strings.Replace(artifact, `"monitor"`, `"shell"`, 1), ``, nil},
		{"an artifact watch described otherwise", strings.Replace(artifact, "(auto-armed on publish)", "(armed)", 1), ``, []Task{{"monitor", "live updates for artifact https://claude.ai/artifact/x (armed)"}}},
		{"a shell that has ended", strings.Replace(shell, "running", "completed", 1), ``, nil},
		{"a subagent", `{"id": "a1", "type": "subagent", "status": "running", "description": "Tests"}`, ``, nil},
		{"a multi-line command, kept whole", `{"id": "b1", "type": "shell", "status": "running", "command": "cd app &&\n\u001b[31mnpm test\u001b[0m"}`, ``, []Task{{"shell", "cd app &&\nnpm test"}}},
		{"a shell its Monitor tool started", shell, "=b1\n", []Task{{"monitor", "Sleep"}}},
		{"a watch of its own beside its shell", shell + `, ` + strings.Replace(shell, "b1", "b2", 1), "b0\n=b2\n~b3\n", []Task{{"shell", "sleep 120"}, {"monitor", "Sleep"}}},
		{"the artifact watch marked", artifact, "=w1\n", nil},
		{"a mark for another task", shell, "=b2\n", []Task{{"shell", "sleep 120"}}},
	} {
		latest := stop(tc.tasks)
		r := Parse(latest, latest, nil).Own(latest, []byte(tc.owed))
		if r.State != Done || !reflect.DeepEqual(r.OwnTasks, tc.want) {
			t.Errorf("%s: %s %+v, want %+v", tc.name, r.State, r.OwnTasks, tc.want)
		}
	}
	// Claude Code 2.1.302 lists a watch the agent keeps with the Monitor tool
	// as a shell, with the watch's command: only the hook's mark of the
	// Monitor's taskId (=ID) tells it from a command the agent started with
	// Bash (#99). Without the mark, as an older hook leaves it, it shows as
	// a shell (#97).
	watch := fixture(t, "stop-own-monitor")
	if r := Parse(watch, watch, nil).Own(watch, []byte("=bdsevxlfh\n")); r.State != Done || !reflect.DeepEqual(r.OwnTasks, []Task{{"monitor", "qa/flag file appearance"}}) {
		t.Errorf("a Monitor watch: %s %+v", r.State, r.OwnTasks)
	}
	if r := Parse(watch, watch, nil).Own(watch, nil); r.State != Done || !reflect.DeepEqual(r.OwnTasks, []Task{{"shell", `f=/w/flag; until [ -e "$f" ]; do sleep 0.5; done; echo "flag file appeared: $f"`}}) {
		t.Errorf("a Monitor watch of an older hook: %s %+v", r.State, r.OwnTasks)
	}
	question := fixture(t, "stop-background-question")
	if r := Parse(question, question, nil).Own(question, nil); r.State != Question || !reflect.DeepEqual(r.OwnTasks, []Task{{"shell", "npm run dev"}}) {
		t.Errorf("a question: %s %+v", r.State, r.OwnTasks)
	}
}

// The hook's list may mark a task as a subagent's (~ID) or as the agent's
// own Monitor watch (=ID), alongside the ids owed a turn. Whatever the
// marks, busy counts exactly the running tasks ownTasks leaves out (the
// artifact watch aside, which is neither): the =ID mark changes only how
// an own task is shown, never whether it is one (#99).
func TestBusyAndOwnTasksSplitTheRunningTasksWhateverTheMarks(t *testing.T) {
	tasks := []string{
		`{"id": "b1", "type": "shell", "status": "running", "description": "Wait", "command": "sleep 60"}`,
		`{"id": "b1", "type": "monitor", "status": "running", "description": "Wait"}`,
		`{"id": "b1", "type": "subagent", "status": "running", "description": "Tests"}`,
		`{"id": "b1", "type": "workflow", "status": "running", "name": "w"}`,
		`{"id": "b1", "type": "cron", "status": "running"}`,
		`{"id": "b1", "type": "shell", "status": "completed", "command": "sleep 60"}`,
	}
	lists := []string{"", "b1\n", "!b1\n", "~b1\n", "=b1\n", "=b1\n~b1\n", "=b2\n", "+\n=b1\nb2\n"}
	for _, task := range tasks {
		for _, list := range lists {
			var p payload
			if err := json.Unmarshal([]byte(`{"hook_event_name": "Stop", "background_tasks": [`+task+`]}`), &p); err != nil {
				t.Fatal(err)
			}
			running := p.BackgroundTasks[0].Status == "running"
			if own := len(p.ownTasks([]byte(list))) == 1; p.busy([]byte(list)) != (running && !own) {
				t.Errorf("%s with list %q: busy %v, own tasks %+v", task, list, p.busy([]byte(list)), p.ownTasks([]byte(list)))
			}
		}
	}
	// The real Stop of a turn whose subagent waits on its Monitor watch,
	// beside the agent's own (Claude Code 2.1.302, both listed as shells):
	// the subagent's is work, the agent's own a watch shown by its
	// description.
	stop := fixture(t, "stop-subagent-monitor")
	var p payload
	if err := json.Unmarshal(stop, &p); err != nil {
		t.Fatal(err)
	}
	owed := []byte("=b46swdckt\n~b3ukv3517\n")
	if !p.busy(owed) || !reflect.DeepEqual(p.ownTasks(owed), []Task{{"monitor", "qa flag1 watch"}}) {
		t.Errorf("a subagent's watch beside the agent's own: busy %v, own %+v", p.busy(owed), p.ownTasks(owed))
	}
	// A turn end the hook kept, as some id is still owed a turn, with the
	// agent's own watch marked: the mark changes nothing of what is owed.
	since := time.Unix(1000, 0)
	working := Parse(fixture(t, "prompt"), nil, nil)
	working.Since, working.Latest = since, since
	watch := fixture(t, "stop-own-monitor")
	for list, owed := range map[string]bool{"a1\n": true, "a1\n=bdsevxlfh\n": true, "~bdsevxlfh\n": false} {
		if r := working.Kept(fixture(t, "prompt"), watch, []byte(list), since.Add(time.Second)); r.State != Working || r.Owed != owed {
			t.Errorf("kept with list %q: %+v", list, r)
		}
	}
}

// Own tasks are a turn end's: only a done or a question that the latest
// event, a Stop, gives. A turn end the hook kept, as background work runs,
// leaves the agent working with none; so does every other latest event,
// though the latest Stop beside it still lists the task running.
func TestOwnTasksAreOnlyThoseOfTheTurnEndTheStateComesFrom(t *testing.T) {
	since := time.Unix(1000, 0)
	shell := fixture(t, "stop-background-shell")
	question := fixture(t, "stop-background-question")
	for _, latest := range []string{"prompt", "prompt-wake", "answer-ask", "dialog-ask", "notification", "session-start", "session-start-clear", "session-start-resume", "session-end"} {
		r := Parse(fixture(t, latest), shell, shell).Own(fixture(t, latest), nil)
		if r.OwnTasks != nil {
			t.Errorf("%s: %+v", latest, r.OwnTasks)
		}
	}
	// The hook wrote the turn end with a subagent running as the report too:
	// working, at its prompt.
	r := Parse(question, question, nil)
	r.Since, r.Latest = since, since
	if r = r.Kept(question, question, nil, since.Add(time.Second)).Own(question, nil); r.State != Working || r.OwnTasks != nil {
		t.Errorf("kept turn end: %s %+v", r.State, r.OwnTasks)
	}
}
