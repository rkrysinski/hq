//go:build integration

package state

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/testutil"
)

// run runs a hook as Claude does: sh with the payload on stdin and the
// agent's environment, in Claude's current directory cwd, with the project
// directory the session started in. It returns what the hook printed.
func run(t *testing.T, cwd, project, id string, h hookCommand, payload []byte) string {
	t.Helper()
	cmd := exec.Command(h.Command, h.Args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "HQ_ID="+id, "CLAUDE_PROJECT_DIR="+project, hookEnv+"="+hookScript)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil || stderr.Len() != 0 {
		t.Fatalf("hook %v: %v %q", h.Args[3:], err, stderr.String())
	}
	return string(out)
}

// fire runs the hook of kind without notifications: it prints nothing.
func fire(t *testing.T, cwd, project, id, kind string, payload []byte) {
	t.Helper()
	if out := run(t, cwd, project, id, hook(kind, ""), payload); out != "" {
		t.Fatalf("hook %s printed %q", kind, out)
	}
}

func TestHookNotifiesOnStopAndInputNamingTheBranch(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	wt := filepath.Join(filepath.Dir(root), "feat-42")
	testutil.Git(t, root, "worktree", "add", "-q", "-b", "feat/42-x", wt)
	const id = "0a1b2c3d4e5f"
	for _, tc := range []struct{ kind, payload, want string }{
		{"stop", "stop-done", `{"terminalSequence":"[notify Done: feat/42-x]"}` + "\n"},
		{"stop", "stop-question", `{"terminalSequence":"[notify Question: feat/42-x]"}` + "\n"},
		{"input", "notification", `{"terminalSequence":"[notify Needs input: feat/42-x]"}` + "\n"},
		{"dialog", "dialog-ask", `{"terminalSequence":"[notify Needs input: feat/42-x]"}` + "\n"},
		{"answer", "answer-ask", ""},
		{"prompt", "prompt", ""},
		{"end", "session-end", ""},
		{"start", "session-start", ""},
		{"resume", "session-start-resume", ""},
	} {
		if got := run(t, wt, root, id, hook(tc.kind, "[notify %s]"), fixture(t, tc.payload)); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.payload, got, tc.want)
		}
	}

	// The real sequence reaches Claude as JSON it decodes to OSC 9; a detached
	// HEAD is named by its directory.
	testutil.Git(t, wt, "checkout", "-q", "--detach")
	out := run(t, wt, root, id, hook("stop", `\u001b]9;%s\u0007`), fixture(t, "stop-done"))
	var v struct{ TerminalSequence string }
	if err := json.Unmarshal([]byte(out), &v); err != nil || v.TerminalSequence != "\x1b]9;Done: feat-42\a" {
		t.Fatalf("%q: %+v %v", out, v, err)
	}
	// A sequence without text (BEL) is sent as it is.
	if out := run(t, wt, root, id, hook("input", `\u0007`), fixture(t, "notification")); out != `{"terminalSequence":"\u0007"}`+"\n" {
		t.Fatalf("BEL: %q", out)
	}
}

func TestHookAndHostAgreeOnQuestions(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	testutil.Git(t, root, "checkout", "-q", "-b", `a"b`) // quotes never break the JSON
	for _, m := range []string{
		"Shall I?", "Shall I? ", "Shall I?\n\n", "Shall I?\t\r\n", "Done.", "Is it \"ok?\"", `C:\?\`,
		"Why?\nBecause.", "", "?", "multi\nline\nending?", "ünïcödé?", "tab\t?",
	} {
		payload, _ := json.Marshal(map[string]string{"hook_event_name": "Stop", "last_assistant_message": m})
		out := run(t, root, root, "abc", hook("stop", "%s"), payload)
		want := "Done: ab"
		if IsQuestion(m) {
			want = "Question: ab"
		}
		var v struct{ TerminalSequence string }
		if err := json.Unmarshal([]byte(out), &v); err != nil || v.TerminalSequence != want {
			t.Errorf("%q: hook %q, host %q (%v)", m, out, want, err)
		}
	}
}

func TestHookReportsToTheMainRepositoryFromAWorktree(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	wt := filepath.Join(filepath.Dir(root), "feat-42")
	testutil.Git(t, root, "worktree", "add", "-q", "-b", "feat-42", wt)
	const id = "0a1b2c3d4e5f"

	if _, ok := Read(root, id); ok {
		t.Fatal("a report before the first event")
	}
	// Claude starts in the repository, then moves into a worktree.
	fire(t, root, root, id, "prompt", fixture(t, "prompt"))
	if r, ok := Read(root, id); !ok || r.State != Working || r.Branch != "main" || time.Since(r.Since) > time.Minute {
		t.Fatalf("after the prompt: %+v %v", r, ok)
	}
	fire(t, wt, root, id, "stop", fixture(t, "stop-question"))
	if r, _ := Read(root, id); r.Branch != "feat-42" {
		t.Fatalf("in the worktree: %+v", r)
	}
	fire(t, wt, root, id, "input", fixture(t, "notification"))
	if r, _ := Read(root, id); r.State != NeedsInput || r.Last != "Claude needs your permission" {
		t.Fatalf("after the notification: %+v", r)
	}
	fire(t, wt, root, id, "end", fixture(t, "session-end"))
	if r, _ := Read(root, id); r.State != Ended || r.Last != "Should I also update the README?" {
		t.Fatalf("after the end, the last message stays: %+v", r)
	}
	entries, _ := os.ReadDir(Dir(root))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, " ") != id+" "+id+".stop" {
		t.Fatalf("files %v", names)
	}
	out, _ := exec.Command("git", "-C", root, "status", "--porcelain").Output()
	if len(out) != 0 {
		t.Fatalf("the repository is not clean: %s", out)
	}
}

func TestADialogIsReportedOnceWhenItOpensAndClosesWhenItsToolRuns(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	const id = "0a1b2c3d4e5f"
	notify := func(kind, payload string) string {
		return run(t, root, root, id, hook(kind, "[%s]"), fixture(t, payload))
	}
	state := func() (Report, string) {
		t.Helper()
		r, ok := Read(root, id)
		if !ok {
			t.Fatal("no report")
		}
		b, _ := os.ReadFile(filepath.Join(Dir(root), id))
		return r, string(b)
	}

	// A tool ending with no dialog open is not reported: working keeps its age.
	fire(t, root, root, id, "prompt", fixture(t, "prompt"))
	_, before := state()
	if out := notify("answer", "answer-bash"); out != "" {
		t.Fatalf("answer printed %q", out)
	}
	if _, now := state(); now != before {
		t.Fatal("a tool's end with no dialog open was written")
	}

	// The question dialog opens: needs input at once, one notification.
	if out := notify("dialog", "dialog-ask"); out != `{"terminalSequence":"[Needs input: main]"}`+"\n" {
		t.Fatalf("dialog printed %q", out)
	}
	r, opened := state()
	if r.State != NeedsInput || r.Last != "Which colour do you pick: red or blue?" {
		t.Fatalf("dialog: %+v", r)
	}
	// Claude's own Notification for it, seconds later, changes nothing and
	// notifies nobody.
	if out := notify("input", "notification"); out != "" {
		t.Fatalf("the late Notification printed %q", out)
	}
	// Another tool (a background agent's) ending does not close it.
	if out := notify("answer", "answer-bash"); out != "" {
		t.Fatalf("answer printed %q", out)
	}
	if _, now := state(); now != opened {
		t.Fatal("the dialog's report changed before it was answered")
	}
	// Answered: working again.
	fire(t, root, root, id, "answer", fixture(t, "answer-ask"))
	if r, _ := state(); r.State != Working {
		t.Fatalf("answered: %+v", r)
	}
	// With no dialog open, a Notification is the attention event itself.
	if out := notify("input", "notification"); out != `{"terminalSequence":"[Needs input: main]"}`+"\n" {
		t.Fatalf("notification printed %q", out)
	}
	if r, _ := state(); r.State != NeedsInput || r.Last != "Claude needs your permission" {
		t.Fatalf("notification: %+v", r)
	}
	// A permission prompt closes when its tool has run, successfully or not.
	notify("dialog", "dialog-bash")
	failed := fixture(t, "answer-bash")
	failed = bytes.Replace(failed, []byte(`"PostToolUse"`), []byte(`"PostToolUseFailure"`), 1)
	fire(t, root, root, id, "answer", failed)
	if r, _ := state(); r.State != Working {
		t.Fatalf("permission granted, tool failed: %+v", r)
	}
	if entries, _ := os.ReadDir(Dir(root)); len(entries) != 1 {
		t.Fatalf("left files behind: %v", entries)
	}
}

func TestAResumedSessionKeepsItsBranchWorktreeAndLastMessage(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	wt := filepath.Join(filepath.Dir(root), "feat-42")
	testutil.Git(t, root, "worktree", "add", "-q", "-b", "feat-42", wt)
	const id = "0a1b2c3d4e5f"
	worktree := func(name string) []byte { // the payload, as Claude sends it in wt
		return bytes.Replace(fixture(t, name), []byte("/w/app/.claude/worktrees/feat-42"), []byte(wt), 1)
	}

	// A new agent: waiting at its prompt at once, with nothing to say yet.
	fire(t, root, root, id, "start", fixture(t, "session-start"))
	if r, _ := Read(root, id); r.State != Done || r.Branch != "main" || r.Last != "" {
		t.Fatalf("started: %+v", r)
	}
	// It works in a worktree, then its sandbox restarts under it.
	fire(t, root, root, id, "prompt", fixture(t, "prompt"))
	fire(t, wt, root, id, "stop", worktree("stop-done"))
	fire(t, wt, root, id, "end", worktree("session-end"))
	// Resumed, Claude starts in the repository; restarted again before any
	// prompt, it still keeps what the session before the first restart gave.
	for i := 0; i < 2; i++ {
		fire(t, root, root, id, "resume", fixture(t, "session-start-resume"))
		r, _ := Read(root, id)
		if r.State != Done || r.Branch != "feat-42" || r.Cwd != wt || r.Last != "Hi. The tests pass and PR #58 is open." {
			t.Fatalf("resumed %d: %+v", i+1, r)
		}
	}
	// Its next report is where it is now.
	fire(t, root, root, id, "prompt", fixture(t, "prompt"))
	if r, _ := Read(root, id); r.State != Working || r.Branch != "main" {
		t.Fatalf("prompted: %+v", r)
	}
	if err := Remove(root, id); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(Dir(root)); len(entries) != 0 {
		t.Fatalf("left files behind: %v", entries)
	}
}

func TestHookNeverFailsAndWritesNothingWithoutAnAgentOrARepository(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	for _, id := range []string{"", "../x", "ABC", "a b"} {
		fire(t, root, root, id, "stop", fixture(t, "stop-done"))
		if _, err := os.Stat(filepath.Join(root, ".git", "hq")); !os.IsNotExist(err) {
			t.Fatalf("wrote for the invalid id %q: %v", id, err)
		}
	}
	d := t.TempDir()
	fire(t, d, d, "abc", "stop", fixture(t, "stop-done")) // not a repository
}

func TestReadRefusesAnythingButASmallRegularFile(t *testing.T) {
	root := t.TempDir()
	dir := Dir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(root, "secret")
	os.WriteFile(secret, fixture(t, "stop-done"), 0o600)
	os.Symlink(secret, filepath.Join(dir, "aaa"))
	os.WriteFile(filepath.Join(dir, "bbb"), append(fixture(t, "stop-done"), bytes.Repeat([]byte(" "), maxFile)...), 0o644)
	syscall.Mkfifo(filepath.Join(dir, "ccc"), 0o644)
	os.Mkdir(filepath.Join(dir, "ddd"), 0o755)
	os.WriteFile(filepath.Join(dir, "eee"), fixture(t, "stop-done"), 0o644)
	os.Symlink(secret, filepath.Join(dir, "eee.stop"))
	for _, id := range []string{"aaa", "bbb", "ccc", "ddd", "../secret"} {
		if r, ok := Read(root, id); ok {
			t.Errorf("%s read: %+v", id, r)
		}
	}
	// A linked .stop file is ignored; the event file itself still counts.
	if r, ok := Read(root, "eee"); !ok || r.State != Done {
		t.Fatalf("eee: %+v %v", r, ok)
	}
}

func TestRemoveDeletesThatAgentsFilesOnly(t *testing.T) {
	root := t.TempDir()
	dir := Dir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(dir, "..", "..", "keep")
	for _, p := range []string{"abc", "abc.stop", "abc.prev", "abcd", "abcd.stop", "def", "../../keep"} {
		os.WriteFile(filepath.Join(dir, p), []byte("x"), 0o644)
	}
	if err := Remove(root, "abc"); err != nil {
		t.Fatal(err)
	}
	// Removed again, or never reported: nothing to do.
	if err := Remove(root, "abc"); err != nil {
		t.Fatal(err)
	}
	if err := Remove(t.TempDir(), "abc"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", "../../keep", "ABC", "abc/../def"} {
		if Remove(root, id) == nil {
			t.Errorf("removed for the invalid id %q", id)
		}
	}
	entries, _ := os.ReadDir(dir)
	var left []string
	for _, e := range entries {
		left = append(left, e.Name())
	}
	if got := strings.Join(left, " "); got != "abcd abcd.stop def" {
		t.Fatalf("left %q", got)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal(err)
	}
	// A file hq cannot remove is reported.
	os.MkdirAll(filepath.Join(dir, "fed", "x"), 0o755)
	if Remove(root, "fed") == nil {
		t.Fatal("a directory in the way went unreported")
	}
}

// files reads an agent's state files, by suffix, for comparing.
func files(t *testing.T, root, id string) map[string]string {
	t.Helper()
	got := map[string]string{}
	entries, _ := os.ReadDir(Dir(root))
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(Dir(root), e.Name()))
		got[strings.TrimPrefix(e.Name(), id)] = string(b)
	}
	return got
}

func TestATurnEndingWhileASubagentRunsLeavesTheAgentWorkingAndNotifiesNobody(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	const id = "0a1b2c3d4e5f"
	notify := func(kind, payload string) string {
		return run(t, root, root, id, hook(kind, "[%s]"), fixture(t, payload))
	}
	read := func() Report {
		t.Helper()
		r, ok := Read(root, id)
		if !ok {
			t.Fatal("no report")
		}
		return r
	}

	fire(t, root, root, id, "prompt", fixture(t, "prompt"))
	prompted := read()
	// The turn ends with a subagent running: no notification, working since
	// the prompt, with the turn's message, at its prompt.
	if out := notify("stop", "stop-background"); out != "" {
		t.Fatalf("the turn end printed %q", out)
	}
	r := read()
	if r.State != Working || !r.Background || r.Last != "The tests are running in a subagent." || !r.Since.Equal(prompted.Since) || r.Latest.Before(r.Since) {
		t.Fatalf("turn end with a subagent running: %+v", r)
	}
	// Claude wakes the agent when a subagent has finished: it works on,
	// since the same prompt; another subagent still runs at that turn's end.
	if out := notify("prompt", "prompt-wake"); out != "" {
		t.Fatalf("the wake-up printed %q", out)
	}
	if r = read(); r.State != Working || r.Background || !r.Since.Equal(prompted.Since) || r.Prompt != "say hi" {
		t.Fatalf("wake-up: %+v", r)
	}
	notify("stop", "stop-background")
	if r = read(); r.State != Working || !r.Background || !r.Since.Equal(prompted.Since) {
		t.Fatalf("turn end with another subagent running: %+v", r)
	}
	// The closing turn, with nothing running: done, one notification.
	notify("prompt", "prompt-wake")
	if out := notify("stop", "stop-done"); out != `{"terminalSequence":"[Done: main]"}`+"\n" {
		t.Fatalf("the closing turn printed %q", out)
	}
	if r = read(); r.State != Done || r.Background || r.Last != "Hi. The tests pass and PR #58 is open." || !r.Since.After(prompted.Since) {
		t.Fatalf("closing turn: %+v", r)
	}
	if f := files(t, root, id); len(f) != 2 || f[""] != f[".stop"] {
		t.Fatalf("files left: %v", f)
	}

	// A prompt of the user's while the agent waits for its subagent starts
	// a turn of its own: working since then.
	fire(t, root, root, id, "prompt", fixture(t, "prompt"))
	notify("stop", "stop-background")
	waiting := read()
	fire(t, root, root, id, "prompt", fixture(t, "prompt-pasted"))
	if r = read(); r.State != Working || r.Background || !r.Since.After(waiting.Since) {
		t.Fatalf("the user's prompt: %+v", r)
	}
	if f := files(t, root, id); len(f) != 2 {
		t.Fatalf("files left: %v", f)
	}
}

func TestOnlyRunningBackgroundWorkThatIsNoShellCommandKeepsATurnEndWorking(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	task := func(kind, status string) map[string]any {
		return map[string]any{"id": "a1", "type": kind, "status": status, "description": `say "status":"running" {[`, "command": "x"}
	}
	for _, tc := range []struct {
		name    string
		tasks   any
		message string
		want    string // the notification's kind; empty when the agent stays working
	}{
		{"nothing running", []any{}, "Done.", "Done"},
		{"no list", nil, "Done.", "Done"},
		{"a subagent", []any{task("subagent", "running")}, "Started.", ""},
		{"a shell command", []any{task("shell", "running")}, "The server is up.", "Done"},
		{"a shell command and a subagent", []any{task("shell", "running"), task("subagent", "running")}, "Started.", ""},
		{"a workflow", []any{task("workflow", "running")}, "Started.", ""},
		{"a finished subagent", []any{task("subagent", "completed")}, "Done.", "Done"},
		{"a finished subagent and a shell command", []any{task("subagent", "completed"), task("shell", "running")}, "Done.", "Done"},
		{"a subagent, with a question", []any{task("subagent", "running")}, "Started. Shall I go on?", "Question"},
		{"a task with a list of its own", []any{map[string]any{"type": "subagent", "tools": []any{map[string]string{"status": "x"}}, "status": "running"}}, "Started.", ""},
		{"a message that names a running task", []any{}, `"background_tasks":[{"type":"subagent","status":"running"}]`, "Done"},
	} {
		id := hex.EncodeToString([]byte(tc.name))[:12]
		fire(t, root, root, id, "prompt", fixture(t, "prompt"))
		p := map[string]any{"hook_event_name": "Stop", "last_assistant_message": tc.message, "session_crons": []any{map[string]string{"status": "running"}}}
		if tc.tasks != nil {
			p["background_tasks"] = tc.tasks
		}
		for _, indent := range []string{"", "  "} { // as Claude writes it, and spread over lines
			payload, _ := json.MarshalIndent(p, "", indent)
			if indent == "" {
				payload, _ = json.Marshal(p)
			}
			out := run(t, root, root, id, hook("stop", "%s"), payload)
			var v struct{ TerminalSequence string }
			if out != "" {
				if err := json.Unmarshal([]byte(out), &v); err != nil {
					t.Fatalf("%s: %q: %v", tc.name, out, err)
				}
			}
			want := ""
			if tc.want != "" {
				want = tc.want + ": main"
			}
			r, _ := Read(root, id)
			if v.TerminalSequence != want || (r.State == Working) != (tc.want == "") || r.Background != (tc.want == "") {
				t.Errorf("%s (indent %q): notified %q, report %+v", tc.name, indent, v.TerminalSequence, r)
			}
			fire(t, root, root, id, "prompt", fixture(t, "prompt"))
		}
	}
}

func TestATurnEndWithASubagentRunningStillDeliversMessagesAndFollowsWhatCameBefore(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	const id = "0a1b2c3d4e5f"
	read := func() Report {
		t.Helper()
		r, _ := Read(root, id)
		return r
	}
	notify := func(kind, payload string) string {
		return run(t, root, root, id, hook(kind, "[%s]"), fixture(t, payload))
	}

	// A message waiting at such a turn end blocks the stop, as at any other.
	fire(t, root, root, id, "prompt", fixture(t, "prompt"))
	if err := Post(root, id, "also run the linter", false, time.Now()); err != nil {
		t.Fatal(err)
	}
	if out := notify("stop", "stop-background"); !strings.Contains(out, `"decision":"block"`) || !strings.Contains(out, "also run the linter") {
		t.Fatalf("the stop with a message waiting printed %q", out)
	}
	if r := read(); r.State != Working || r.Background {
		t.Fatalf("blocked stop: %+v", r)
	}
	// A message that comes while the agent waits for its subagent rides
	// along with Claude's wake-up.
	notify("stop", "stop-background")
	if err := Post(root, id, "and the docs", false, time.Now()); err != nil {
		t.Fatal(err)
	}
	if out := notify("prompt", "prompt-wake"); !strings.Contains(out, "and the docs") {
		t.Fatalf("the wake-up with a message waiting printed %q", out)
	}

	// A question notifies and shows, whatever still runs; the wake-up after
	// it starts a turn like any prompt, and its end is the agent's.
	if out := notify("stop", "stop-background-question"); out != `{"terminalSequence":"[Question: main]"}`+"\n" {
		t.Fatalf("the question printed %q", out)
	}
	asked := read()
	if asked.State != Question || asked.Background {
		t.Fatalf("question: %+v", asked)
	}
	fire(t, root, root, id, "prompt", fixture(t, "prompt-wake"))
	if r := read(); r.State != Working || !r.Since.After(asked.Since) {
		t.Fatalf("wake-up after a question: %+v", r)
	}
	if out := notify("stop", "stop-done"); out != `{"terminalSequence":"[Done: main]"}`+"\n" {
		t.Fatalf("the closing turn printed %q", out)
	}

	// The agent needed input (no dialog reported its end): the turn end
	// with a subagent running makes it working, from then on.
	notify("input", "notification")
	needed := read()
	if out := notify("stop", "stop-background"); out != "" {
		t.Fatalf("the turn end printed %q", out)
	}
	if r := read(); r.State != Working || !r.Background || !r.Since.After(needed.Since) || r.Last != "The tests are running in a subagent." {
		t.Fatalf("turn end after needs input: %+v", r)
	}
	// A subagent's dialog, answered, leaves the agent waiting at its prompt.
	notify("dialog", "dialog-ask")
	if r := read(); r.State != NeedsInput || r.Background {
		t.Fatalf("dialog while waiting: %+v", r)
	}
	fire(t, root, root, id, "answer", fixture(t, "answer-ask"))
	if r := read(); r.State != Working || !r.Background {
		t.Fatalf("dialog answered while waiting: %+v", r)
	}

	// A session that starts, starts over or ends forgets what was kept.
	for _, tc := range []struct{ kind, payload, state string }{
		{"start", "session-start-clear", Done}, {"resume", "session-start-resume", Done}, {"end", "session-end", Ended},
	} {
		fire(t, root, root, id, "prompt", fixture(t, "prompt"))
		notify("stop", "stop-background")
		fire(t, root, root, id, tc.kind, fixture(t, tc.payload))
		if r := read(); r.State != tc.state || r.Background {
			t.Errorf("%s: %+v", tc.kind, r)
		}
		if _, kept := files(t, root, id)[keptSuffix]; kept {
			t.Errorf("%s: the turn end is still kept", tc.kind)
		}
	}
}
