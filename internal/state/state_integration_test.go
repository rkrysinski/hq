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
	// What was kept is gone; the subagent still runs, and Claude owes a
	// turn for it.
	if f := files(t, root, id); len(f) != 3 || f[owedSuffix] != "a58b43841609db047\n" {
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

	// A subagent's dialog still open when the turn ends is not written
	// over: the agent needs input until it is answered, then waits on.
	fire(t, root, root, id, "prompt", fixture(t, "prompt"))
	var dialog, answer map[string]any
	_ = json.Unmarshal(fixture(t, "dialog-ask"), &dialog)
	_ = json.Unmarshal(fixture(t, "answer-ask"), &answer)
	dialog["agent_id"], answer["agent_id"] = "a495260ec0c2e3ace", "a495260ec0c2e3ace"
	theirs, _ := json.Marshal(dialog)
	answered, _ := json.Marshal(answer)
	run(t, root, root, id, hook("dialog", "[%s]"), theirs)
	if out := notify("stop", "stop-background"); out != "" {
		t.Fatalf("the turn end printed %q", out)
	}
	if r := read(); r.State != NeedsInput || r.Background || r.Last != "Which colour do you pick: red or blue?" {
		t.Fatalf("turn end with a subagent's dialog open: %+v", r)
	}
	fire(t, root, root, id, "answer", answered)
	if r := read(); r.State != Working || !r.Background {
		t.Fatalf("a subagent's dialog answered after the turn end: %+v", r)
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

// turnEnd is a Stop with the tasks ids running in the background, and
// wakeUp the prompt Claude gives itself when task id has finished, as
// Claude Code 2.1.291 writes them.
func turnEnd(message string, ids ...string) []byte {
	tasks := []map[string]string{}
	for _, id := range ids {
		if shell, ok := strings.CutPrefix(id, "shell:"); ok {
			tasks = append(tasks, map[string]string{"id": shell, "type": "shell", "status": "running", "description": "Wait", "command": "./wait.sh 60"})
			continue
		}
		tasks = append(tasks, map[string]string{"id": id, "type": "subagent", "status": "running", "description": "Run the tests", "agent_type": "general-purpose"})
	}
	p, _ := json.Marshal(map[string]any{"hook_event_name": "Stop", "last_assistant_message": message, "background_tasks": tasks})
	return p
}

// toolEnd is the PostToolUse of a tool with the response Claude Code
// 2.1.291 gives it; by is the subagent whose tool it is, empty for the
// agent's own.
func toolEnd(tool, by, response string) []byte {
	agent := ""
	if by != "" {
		agent = `"agent_id":"` + by + `","agent_type":"general-purpose",`
	}
	return []byte(`{"session_id":"s",` + agent + `"hook_event_name":"PostToolUse","tool_name":"` + tool + `","tool_input":{"description":"x","prompt":"say \"agentId\":\"no\""},"tool_response":` + response + `}`)
}

// The responses of the tools that start background work.
func launched(id string) string {
	return `{"isAsync":true,"status":"async_launched","agentId":"` + id + `","description":"Run the tests"}`
}

func workflowLaunched(id string) string {
	return `{"status":"async_launched","taskId":"` + id + `","taskType":"local_workflow","workflowName":"w"}`
}

func shellStarted(id string) string {
	return `{"stdout":"","stderr":"","interrupted":false,"backgroundTaskId":"` + id + `"}`
}

func wakeUp(id string) []byte { return wakeUpWith(id, "completed") }

func wakeUpWith(id, status string) []byte {
	return []byte(`{"hook_event_name":"UserPromptSubmit","prompt":"<task-notification>\n<task-id>` + id + `</task-id>\n<tool-use-id>toolu_01</tool-use-id>\n<status>` + status + `</status>\n<summary>Agent \"Run the tests\" finished</summary>\n</task-notification>"}`)
}

func TestSubagentsThatFinishTogetherNotifyOnceWhenClaudeHasTakenItsTurnForEach(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	const id = "0a1b2c3d4e5f"
	notify := func(kind string, payload []byte) string {
		t.Helper()
		return run(t, root, root, id, hook(kind, "[%s]"), payload)
	}
	read := func() Report {
		t.Helper()
		r, _ := Read(root, id)
		return r
	}
	quiet := func(step, out string, owed bool) {
		t.Helper()
		if r := read(); out != "" || r.State != Working || r.Owed != owed {
			t.Fatalf("%s: printed %q, report %+v", step, out, r)
		}
	}
	const done = `{"terminalSequence":"[Done: main]"}` + "\n"

	// Three subagents finish within a second (as seen: the list is empty
	// before Claude has taken its turn for the last): one notification, at
	// the end of that turn.
	notify("prompt", fixture(t, "prompt"))
	quiet("turn end, three running", notify("stop", turnEnd("started", "a1", "a2", "a3")), false)
	quiet("wake-up for the first", notify("prompt", wakeUp("a1")), false)
	quiet("turn end, one running", notify("stop", turnEnd("the first finished", "a3")), false)
	quiet("wake-up for the second", notify("prompt", wakeUp("a2")), false)
	quiet("turn end, none running, a turn owed", notify("stop", turnEnd("the second finished")), true)
	if r := read(); !r.Background || r.Last != "the second finished" {
		t.Fatalf("a turn owed: %+v", r)
	}
	quiet("wake-up for the third", notify("prompt", wakeUp("a3")), false)
	if out := notify("stop", turnEnd("all three finished")); out != done {
		t.Fatalf("the last closing turn printed %q", out)
	}
	if r := read(); r.State != Done || r.Background || r.Owed {
		t.Fatalf("the last closing turn: %+v", r)
	}
	if f := files(t, root, id); len(f) != 2 {
		t.Fatalf("files left: %v", f)
	}

	// A subagent the user has stopped: nothing runs at the end of the
	// user's turn, the wake-up for it follows, and that turn's end is the
	// agent's. The same with several ending during a turn of the user's.
	notify("prompt", fixture(t, "prompt"))
	quiet("turn end, one running", notify("stop", turnEnd("started", "b1")), false)
	notify("prompt", fixture(t, "prompt-pasted")) // the user: stop it
	quiet("the user's turn, a turn owed", notify("stop", turnEnd("stopped it")), true)
	quiet("wake-up for the stopped one", notify("prompt", wakeUp("b1")), false)
	if out := notify("stop", turnEnd("it is stopped")); out != done {
		t.Fatalf("the turn for the stopped subagent printed %q", out)
	}
	notify("prompt", fixture(t, "prompt"))
	notify("stop", turnEnd("started", "b2", "b3"))
	notify("prompt", fixture(t, "prompt-pasted"))
	quiet("the user's turn, two turns owed", notify("stop", turnEnd("noted")), true)
	notify("prompt", wakeUp("b2"))
	quiet("turn for the first, one owed", notify("stop", turnEnd("one finished")), true)
	notify("prompt", wakeUp("b3"))
	if out := notify("stop", turnEnd("both finished")); out != done {
		t.Fatalf("the turn for the last printed %q", out)
	}

	// A wake-up that names no task: nothing can be told paid, so nothing is
	// owed, and the turn end notifies.
	notify("prompt", fixture(t, "prompt"))
	notify("stop", turnEnd("started", "b4"))
	notify("prompt", []byte(`{"hook_event_name":"UserPromptSubmit","prompt":"<task-notification>\n<status>completed</status>\n</task-notification>"}`))
	if out := notify("stop", turnEnd("finished")); out != done {
		t.Fatalf("the turn after a wake-up naming no task printed %q", out)
	}

	// A turn that never comes holds back one turn end at most: the user's
	// next prompt forgets it, and so does a session that starts over.
	notify("prompt", fixture(t, "prompt"))
	notify("stop", turnEnd("started", "c1"))
	notify("prompt", wakeUp("c9")) // another task's
	quiet("turn end, none running, a turn owed", notify("stop", turnEnd("one finished")), true)
	if f := files(t, root, id); f[owedSuffix] != "!c1\n" {
		t.Fatalf("owed: %q", f[owedSuffix])
	}
	notify("prompt", fixture(t, "prompt"))
	if out := notify("stop", turnEnd("done")); out != done {
		t.Fatalf("the turn after the user's prompt printed %q", out)
	}
	notify("prompt", fixture(t, "prompt"))
	notify("stop", turnEnd("started", "d1"))
	notify("start", fixture(t, "session-start-clear"))
	notify("prompt", fixture(t, "prompt"))
	if out := notify("stop", turnEnd("done")); out != done {
		t.Fatalf("the turn of the session started over printed %q", out)
	}

	// A question shows and notifies whatever is owed; a wake-up after it
	// is the report, and still pays what it names.
	notify("prompt", fixture(t, "prompt"))
	notify("stop", turnEnd("started", "e1", "e2"))
	notify("prompt", wakeUp("e1"))
	if out := notify("stop", turnEnd("One finished. Go on?")); out != `{"terminalSequence":"[Question: main]"}`+"\n" {
		t.Fatalf("the question printed %q", out)
	}
	notify("prompt", wakeUp("e2"))
	if out := notify("stop", turnEnd("both finished")); out != done {
		t.Fatalf("the closing turn after the question printed %q", out)
	}
}

func TestBackgroundWorkIsKnownFromItsStartSoATurnTheUserInterruptedStillNotifiesOnce(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	const id = "0a1b2c3d4e5f"
	notify := func(kind string, payload []byte) string {
		t.Helper()
		return run(t, root, root, id, hook(kind, "[%s]"), payload)
	}
	quiet := func(step, out string, owed bool) {
		t.Helper()
		if r, _ := Read(root, id); out != "" || r.State != Working || r.Owed != owed {
			t.Fatalf("%s: printed %q, report %+v", step, out, r)
		}
	}
	const done = `{"terminalSequence":"[Done: main]"}` + "\n"

	// Two subagents and a workflow started, then the user interrupts the
	// turn: no Stop ever lists them. They finish together.
	notify("prompt", fixture(t, "prompt"))
	notify("answer", toolEnd("Agent", "", launched("a1")))
	notify("answer", toolEnd("Agent", "", launched("a2")))
	notify("answer", toolEnd("Workflow", "", workflowLaunched("w3")))
	quiet("wake-up for the first", notify("prompt", wakeUp("a1")), false)
	quiet("turn end, none running, two turns owed", notify("stop", turnEnd("the first finished")), true)
	quiet("wake-up for the second", notify("prompt", wakeUp("a2")), false)
	quiet("turn end, none running, a turn owed", notify("stop", turnEnd("the second finished")), true)
	quiet("wake-up for the workflow", notify("prompt", wakeUp("w3")), false)
	if out := notify("stop", turnEnd("all finished")); out != done {
		t.Fatalf("the last closing turn printed %q", out)
	}
	if f := files(t, root, id); len(f) != 2 {
		t.Fatalf("files left: %v", f)
	}

	// Started and finished within the turn, the wake-up still to come at
	// its end: that end is not the agent's.
	notify("prompt", fixture(t, "prompt"))
	notify("answer", toolEnd("Agent", "", launched("b1")))
	quiet("turn end, none running, a turn owed", notify("stop", turnEnd("started")), true)
	notify("prompt", wakeUp("b1"))
	if out := notify("stop", turnEnd("finished")); out != done {
		t.Fatalf("the closing turn printed %q", out)
	}

	// What a subagent starts is its own: Claude wakes the subagent for it,
	// not the agent. Nor does the agent's own shell command count, or a
	// subagent that ran in the foreground.
	notify("prompt", fixture(t, "prompt"))
	notify("answer", toolEnd("Agent", "a9", launched("c1")))
	notify("answer", toolEnd("Bash", "", shellStarted("c2")))
	notify("answer", toolEnd("Agent", "", `{"status":"completed","agentId":"c3","content":[]}`))
	if out := notify("stop", turnEnd("done", "shell:c2")); out != done {
		t.Fatalf("the turn end printed %q", out)
	}
}

func TestAShellCommandIsNoBackgroundWorkWhoeverStartedIt(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	const id = "0a1b2c3d4e5f"
	notify := func(kind string, payload []byte) string {
		t.Helper()
		return run(t, root, root, id, hook(kind, "[%s]"), payload)
	}
	const done = `{"terminalSequence":"[Done: main]"}` + "\n"

	// As seen: a subagent starts a shell command in the background and
	// stops; Claude wakes the agent with its result, and lists the command
	// alone. Whether the subagent waits on it or left it running (a dev
	// server) nothing tells, so it is the agent's end, as with a command of
	// its own. When the command ends and the subagent goes on, the agent
	// works again, and is done again.
	notify("prompt", fixture(t, "prompt"))
	notify("stop", turnEnd("started", "a1"))
	notify("answer", toolEnd("Bash", "a1", shellStarted("b9")))
	notify("prompt", wakeUp("a1"))
	if out := notify("stop", turnEnd("its command runs on", "shell:b9")); out != done {
		t.Fatalf("the turn end with a subagent's command running printed %q", out)
	}
	if r, _ := Read(root, id); r.State != Done || r.Background {
		t.Fatalf("a subagent's command running: %+v", r)
	}
	notify("prompt", wakeUp("a1"))
	if r, _ := Read(root, id); r.State != Working {
		t.Fatalf("the subagent went on: %+v", r)
	}
	if out := notify("stop", turnEnd("finished")); out != done {
		t.Fatalf("the closing turn printed %q", out)
	}
	if f := files(t, root, id); len(f) != 2 {
		t.Fatalf("files left: %v", f)
	}
}

func TestATaskOwedATurnThatNeverComesHoldsBackOneTurnEnd(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	const id = "0a1b2c3d4e5f"
	notify := func(kind string, payload []byte) string {
		t.Helper()
		return run(t, root, root, id, hook(kind, "[%s]"), payload)
	}
	quiet := func(step, out string, owed bool) {
		t.Helper()
		if r, _ := Read(root, id); out != "" || r.State != Working || r.Owed != owed {
			t.Fatalf("%s: printed %q, report %+v", step, out, r)
		}
	}
	const done = `{"terminalSequence":"[Done: main]"}` + "\n"

	// A subagent Claude never wakes the agent for, and a shell command of
	// the agent's own that ends later: the first turn end is held back, the
	// turn Claude takes for the command is the agent's end.
	notify("prompt", fixture(t, "prompt"))
	notify("answer", toolEnd("Agent", "", launched("a1")))
	notify("answer", toolEnd("Bash", "", shellStarted("c2")))
	quiet("turn end, a turn owed", notify("stop", turnEnd("started", "shell:c2")), true)
	notify("prompt", wakeUp("c2"))
	if out := notify("stop", turnEnd("the command ended")); out != done {
		t.Fatalf("the turn for the command printed %q", out)
	}
	if f := files(t, root, id); len(f) != 2 {
		t.Fatalf("files left: %v", f)
	}

	// Three subagents gone from the list at the first turn end: each
	// wake-up keeps the others owed, as their wake-ups follow.
	notify("prompt", fixture(t, "prompt"))
	for _, task := range []string{"d1", "d2", "d3"} {
		notify("answer", toolEnd("Agent", "", launched(task)))
	}
	quiet("turn end, three turns owed", notify("stop", turnEnd("started")), true)
	notify("prompt", wakeUp("d1"))
	quiet("turn for the first, two owed", notify("stop", turnEnd("one")), true)
	notify("prompt", wakeUp("d2"))
	quiet("turn for the second, one owed", notify("stop", turnEnd("two")), true)
	notify("prompt", wakeUp("d3"))
	if out := notify("stop", turnEnd("three")); out != done {
		t.Fatalf("the turn for the last printed %q", out)
	}

	// A wake-up pays the task it is for, not one its report mentions.
	notify("prompt", fixture(t, "prompt"))
	notify("stop", turnEnd("started", "e1", "e2"))
	quoting := strings.Replace(string(wakeUp("e1")), "</task-notification>", `<result>see <task-id>e2</task-id></result>\n</task-notification>`, 1)
	notify("prompt", []byte(quoting))
	quiet("turn for the first, the second owed", notify("stop", turnEnd("one finished")), true)
	notify("prompt", wakeUp("e2"))
	if out := notify("stop", turnEnd("both finished")); out != done {
		t.Fatalf("the turn for the second printed %q", out)
	}

	// The response names the task, not the tool's input.
	notify("prompt", fixture(t, "prompt"))
	notify("answer", []byte(`{"hook_event_name":"PostToolUse","tool_name":"Agent","tool_input":{"taskId":"zz","description":"x"},"tool_response":{"status":"async_launched","agentId":"f1"}}`))
	quiet("turn end, a turn owed", notify("stop", turnEnd("started")), true)
	notify("prompt", wakeUp("f1"))
	if out := notify("stop", turnEnd("finished")); out != done {
		t.Fatalf("the closing turn printed %q", out)
	}
}
