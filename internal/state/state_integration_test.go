//go:build integration

package state

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync/atomic"
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
	// Only the state file, and the mark that a tool of the turn ended,
	// naming the turn.
	if got := files(t, root, id); len(got) != 2 || got[actedSuffix] != "b4e5fe0f-6e79-4ac5-8c2c-b9181444b06f\n" {
		t.Fatalf("left files behind: %v", got)
	}
}

// A tool of the agent's own that ended (PostToolUse or PostToolUseFailure
// without an agent_id) tells hq that Claude has acted on the turn, which
// no early Esc rewinds any more (#77): the hook marks it beside the state
// file, without restarting the agent's time, until the next turn starts.
// A subagent's tool marks nothing.
func TestAToolOfItsOwnThatEndedMarksTheTurnActedOnUntilTheNextTurn(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	const id = "0a1b2c3d4e5f"
	acted := func(when string, want bool) {
		t.Helper()
		r, ok := Read(root, id)
		_, err := os.Lstat(filepath.Join(Dir(root), id+actedSuffix))
		if !ok || r.Acted != want || (err == nil) != want {
			t.Fatalf("%s: acted %v, mark %v, want %v (%+v)", when, r.Acted, err == nil, want, r)
		}
	}
	failed := bytes.Replace(toolEnd("Bash", "", `{"stdout":""}`), []byte(`"PostToolUse"`), []byte(`"PostToolUseFailure"`), 1)

	fire(t, root, root, id, "prompt", fixture(t, "prompt"))
	acted("prompt", false)
	fire(t, root, root, id, "answer", toolEnd("Bash", "a1", `{"stdout":"x"}`))
	acted("a subagent's tool", false)
	r, _ := Read(root, id)
	fire(t, root, root, id, "answer", toolEnd("Bash", "", `{"stdout":"x"}`))
	acted("its own tool", true)
	if now, _ := Read(root, id); now.State != Working || !now.Since.Equal(r.Since) {
		t.Fatalf("its own tool restarted the turn: %+v", now)
	}
	// Claude woken while it works goes on with the same turn.
	fire(t, root, root, id, "prompt", wakeUp("t1"))
	acted("a wake-up while working", true)
	fire(t, root, root, id, "prompt", newTurn(fixture(t, "prompt")))
	acted("the next prompt", false)
	fire(t, root, root, id, "answer", failed)
	acted("its own tool failed", true)
	fire(t, root, root, id, "stop", fixture(t, "stop-done"))
	acted("stop", false)
	// A turn that ends while a subagent runs: its wake-up is a turn of its
	// own.
	fire(t, root, root, id, "prompt", fixture(t, "prompt"))
	fire(t, root, root, id, "answer", toolEnd("Agent", "", launched("a2")))
	acted("a subagent launched", true)
	fire(t, root, root, id, "stop", turnEnd("Waiting.", "a2"))
	if r, _ := Read(root, id); r.State != Working || !r.Background {
		t.Fatalf("kept stop: %+v", r)
	}
	acted("a kept stop", false)
	fire(t, root, root, id, "answer", toolEnd("Bash", "", `{"stdout":"x"}`))
	fire(t, root, root, id, "start", fixture(t, "session-start"))
	acted("a new session", false)
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

func TestReadRefusesAnythingButARegularFile(t *testing.T) {
	root := t.TempDir()
	dir := Dir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(root, "secret")
	os.WriteFile(secret, fixture(t, "stop-done"), 0o600)
	os.Symlink(secret, filepath.Join(dir, "aaa"))
	syscall.Mkfifo(filepath.Join(dir, "ccc"), 0o644)
	os.Mkdir(filepath.Join(dir, "ddd"), 0o755)
	os.WriteFile(filepath.Join(dir, "eee"), fixture(t, "stop-done"), 0o644)
	os.Symlink(secret, filepath.Join(dir, "eee.stop"))
	for _, id := range []string{"aaa", "ccc", "ddd", "../secret"} {
		if r, ok := Read(root, id); ok {
			t.Errorf("%s read: %+v", id, r)
		}
		if d := ReadDetail(root, id); d.Reply != "" || !d.ReplyAt.IsZero() {
			t.Errorf("%s detail: %+v", id, d)
		}
	}
	// A linked .stop file is ignored; the event file itself still counts.
	if r, ok := Read(root, "eee"); !ok || r.State != Done {
		t.Fatalf("eee: %+v %v", r, ok)
	}
}

// A turn that ends with a reply of 82k characters, as in #64, written by
// the hook: the agent is done, notified, and its reply is read in full.
func TestALongReplyIsDoneAndReadInFull(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	const id = "c73ccdf123a7"
	reply := strings.Repeat("A line of the recon report, pasted verbatim.\n", 1800) + "\nAll done."
	payload, _ := json.Marshal(map[string]string{"hook_event_name": "Stop", "session_id": "s1", "cwd": root, "last_assistant_message": reply})
	out := run(t, root, root, id, hook("stop", "%s"), payload)
	var v struct{ TerminalSequence string }
	if err := json.Unmarshal([]byte(out), &v); err != nil || !strings.HasPrefix(v.TerminalSequence, "Done: ") {
		t.Fatalf("hook: %q %v", out, err)
	}
	info, err := os.Stat(filepath.Join(Dir(root), id+".stop"))
	if err != nil || info.Size() <= 64<<10 {
		t.Fatalf("the reply's state file is %v bytes (%v): not past the old cap", info.Size(), err)
	}
	if r, ok := Read(root, id); !ok || r.State != Done || r.Last != Clean(reply) {
		t.Fatalf("read: %v %s %q", ok, r.State, r.Last)
	}
	d := ReadDetail(root, id)
	if d.Reply != CleanText(reply) || !d.ReplyAt.Equal(info.ModTime()) {
		t.Fatalf("detail: %d characters of %d, at %v (want %v)", len(d.Reply), len(reply), d.ReplyAt, info.ModTime())
	}
}

// A state file over the cap is no state hq reads, but it is the agent's
// report: it counts, and hq says it was too large, never starting with
// nothing (#64).
func TestAReportTooLargeToReadSaysSo(t *testing.T) {
	root := t.TempDir()
	dir := Dir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	tooLarge := func(name, payload string) {
		p := filepath.Join(dir, name)
		os.WriteFile(p, fixture(t, payload), 0o600)
		if err := os.Truncate(p, maxFile+1); err != nil {
			t.Fatal(err)
		}
	}
	const say = "[report too large to read: 16.0 MiB]"
	tooLarge("aaa", "stop-done")
	tooLarge("aaa.stop", "stop-done")
	info, _ := os.Stat(filepath.Join(dir, "aaa"))
	if r, ok := Read(root, "aaa"); !ok || r.State != Done || r.Last != say || !r.Since.Equal(info.ModTime()) {
		t.Fatalf("aaa: %v %+v", ok, r)
	}
	if d := ReadDetail(root, "aaa"); d.Reply != say || d.ReplyAt.IsZero() {
		t.Fatalf("aaa detail: %+v", d)
	}
	// The agent works on since; its last message is that reply.
	os.WriteFile(filepath.Join(dir, "bbb"), fixture(t, "prompt"), 0o600)
	tooLarge("bbb.stop", "stop-done")
	if r, ok := Read(root, "bbb"); !ok || r.State != Working || r.Last != say {
		t.Fatalf("bbb: %v %+v", ok, r)
	}
	if d := ReadDetail(root, "bbb"); d.Reply != say {
		t.Fatalf("bbb detail: %+v", d)
	}
}

func TestReadParsesTheStateFilesOncePerChange(t *testing.T) {
	root := t.TempDir()
	dir := Dir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "abc")
	os.WriteFile(path, fixture(t, "prompt"), 0o600)
	if r, ok := Read(root, "abc"); !ok || r.State != Working {
		t.Fatalf("prompt: %v %+v", ok, r)
	}
	// Other bytes of the same size at the same time: no change, so the
	// report parsed before stands (parsed, they would say starting).
	info, _ := os.Stat(path)
	os.WriteFile(path, bytes.Repeat([]byte("x"), int(info.Size())), 0o600)
	os.Chtimes(path, info.ModTime(), info.ModTime())
	if r, _ := Read(root, "abc"); r.State != Working {
		t.Fatalf("unchanged: %+v", r)
	}
	// A new time is a change.
	later := info.ModTime().Add(time.Second)
	os.Chtimes(path, later, later)
	if r, _ := Read(root, "abc"); r.State != Starting {
		t.Fatalf("touched: %+v", r)
	}
	// So is a file beside it that appears.
	os.WriteFile(path, fixture(t, "prompt"), 0o600)
	os.Chtimes(path, later, later)
	os.WriteFile(path+".stop", fixture(t, "stop-done"), 0o600)
	if r, _ := Read(root, "abc"); r.State != Working || r.Last != "Hi. The tests pass and PR #58 is open." {
		t.Fatalf("beside: %+v", r)
	}
	// Removed, the agent has nothing to report, whatever was parsed.
	if err := Remove(root, "abc"); err != nil {
		t.Fatal(err)
	}
	if r, ok := Read(root, "abc"); ok {
		t.Fatalf("removed: %+v", r)
	}
}

func TestRemoveDeletesThatAgentsFilesOnly(t *testing.T) {
	root := t.TempDir()
	dir := Dir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(dir, "..", "..", "keep")
	for _, p := range []string{"abc", "abc.stop", "abc.prev", "abc.on", "abc.owe", "abc.sup", "abc.next", "abc.acted", "abc.queued", "abcd", "abcd.stop", "def", "../../keep"} {
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

// A subagent's MCP elicitation, while the agent waits on it at its prompt,
// is reported by Claude's Notification only, with no agent_id (Claude Code
// 2.1.301): the hook keeps the turn end beside it, so the report says the
// dialog is the subagent's (#70). Claude's wake-up gives the agent its turn
// back.
func TestADialogNotifiedWhileTheAgentWaitsOnASubagentIsTheSubagents(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	const id = "0a1b2c3d4e5f"
	read := func() Report {
		t.Helper()
		r, ok := Read(root, id)
		if !ok {
			t.Fatal("no report")
		}
		return r
	}
	fire(t, root, root, id, "prompt", fixture(t, "prompt"))
	fire(t, root, root, id, "stop", fixture(t, "stop-background"))
	if out := run(t, root, root, id, hook("input", "[%s]"), fixture(t, "notification-elicitation")); out != `{"terminalSequence":"[Needs input: main]"}`+"\n" {
		t.Fatalf("the Notification printed %q", out)
	}
	if r := read(); r.State != NeedsInput || !r.SubagentDialog || r.Last != "Claude Code needs your input" {
		t.Fatalf("a subagent's elicitation: %+v", r)
	}
	fire(t, root, root, id, "prompt", fixture(t, "prompt-wake"))
	if r := read(); r.State != Working || r.SubagentDialog {
		t.Fatalf("woken: %+v", r)
	}
	// In a turn of its own, the same Notification is the agent's.
	fire(t, root, root, id, "input", fixture(t, "notification-elicitation"))
	if r := read(); r.State != NeedsInput || r.SubagentDialog {
		t.Fatalf("the agent's own elicitation: %+v", r)
	}
}

func TestOnlyRunningBackgroundWorkThatIsNoCommandOrWatchOfTheAgentsOwnKeepsATurnEndWorking(t *testing.T) {
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
		{"a watch", []any{task("monitor", "running")}, "Published.", "Done"},
		{"a watch and a subagent", []any{task("monitor", "running"), task("subagent", "running")}, "Started.", ""},
		{"a task of a kind not seen", []any{task("cron", "running")}, "Started.", ""},
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
	if err := Post(root, id, Message{Text: "also run the linter"}, false, time.Now()); err != nil {
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
	if err := Post(root, id, Message{Text: "and the docs"}, false, time.Now()); err != nil {
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

var promptIDs atomic.Int64

// newTurn is the payload of a prompt as one that starts a turn: Claude
// gives each a prompt_id of its own, while one it queues while the agent
// works carries the id of the turn at work (#82), as the fixtures that
// share an id do.
func newTurn(payload []byte) []byte {
	id := fmt.Sprintf(`"prompt_id":"0000%04x-0000-4000-8000-000000000000"`, promptIDs.Add(1))
	return regexp.MustCompile(`"prompt_id"\s*:\s*"[^"]*"`).ReplaceAll(payload, []byte(id))
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
		if watch, ok := strings.CutPrefix(id, "monitor:"); ok {
			tasks = append(tasks, map[string]string{"id": watch, "type": "monitor", "status": "running", "description": "live updates for artifact https://claude.ai/artifact/x (auto-armed on publish)"})
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

func TestAShellCommandASubagentStartedKeepsItsAgentWorkingWhileItRuns(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	const id = "0a1b2c3d4e5f"
	notify := func(kind string, payload []byte) string {
		t.Helper()
		return run(t, root, root, id, hook(kind, "[%s]"), payload)
	}
	quiet := func(step, out string, last string) {
		t.Helper()
		if r, _ := Read(root, id); out != "" || r.State != Working || r.Owed || r.Last != last {
			t.Fatalf("%s: printed %q, report %+v", step, out, r)
		}
	}
	const done = `{"terminalSequence":"[Done: main]"}` + "\n"

	// As seen (#60): a subagent starts a shell command in the background
	// and stops; Claude wakes the agent with its interim report, and lists
	// the command alone. The agent works on, with that report's message,
	// until Claude has woken the subagent for the command and the agent for
	// the subagent's report: one notification, at the end of that turn.
	notify("prompt", fixture(t, "prompt"))
	quiet("turn end, the subagent runs", notify("stop", turnEnd("started", "a1")), "started")
	notify("answer", toolEnd("Bash", "a1", shellStarted("b9")))
	quiet("the interim report", notify("prompt", wakeUp("a1")), "started")
	quiet("turn end, its command runs", notify("stop", turnEnd("it waits on its command", "shell:b9")), "it waits on its command")
	if f := files(t, root, id); f[owedSuffix] != "~b9\n" {
		t.Fatalf("owed: %q", f[owedSuffix])
	}
	// The user's prompt meanwhile keeps the command.
	notify("prompt", fixture(t, "prompt-pasted"))
	quiet("the user's turn, its command runs", notify("stop", turnEnd("noted", "shell:b9")), "noted")
	// The command ends; the subagent, woken for it, runs again.
	quiet("turn end, the subagent woken", notify("stop", turnEnd("it goes on", "a1")), "it goes on")
	quiet("the final report", notify("prompt", wakeUp("a1")), "it goes on")
	if out := notify("stop", turnEnd("finished")); out != done {
		t.Fatalf("the closing turn printed %q", out)
	}
	if f := files(t, root, id); len(f) != 2 {
		t.Fatalf("files left: %v", f)
	}

	// A subagent that leaves a server running looks the same: the agent
	// works until the server ends.
	notify("prompt", fixture(t, "prompt"))
	notify("answer", toolEnd("Agent", "", launched("a2")))
	notify("answer", toolEnd("Bash", "a2", shellStarted("c1")))
	notify("prompt", wakeUp("a2"))
	for range 3 {
		quiet("turn end, its server runs", notify("stop", turnEnd("the server is up", "shell:c1")), "the server is up")
	}
	if out := notify("stop", turnEnd("the server is gone")); out != done {
		t.Fatalf("the turn end after the server printed %q", out)
	}

	// A watch a subagent started (the Monitor tool, a monitor task) counts
	// the same. A task id in a subagent's tool input, a watch the agent
	// itself keeps, or a command the agent itself started, does not; a
	// command of a subagent's that no Stop lists any more is dropped at
	// the first Stop.
	notify("prompt", fixture(t, "prompt"))
	notify("answer", toolEnd("Monitor", "a3", `{"taskId":"m1","timeoutMs":300000,"persistent":false}`))
	notify("answer", []byte(`{"agent_id":"a3","hook_event_name":"PostToolUse","tool_name":"TaskOutput","tool_input":{"taskId":"m2"},"tool_response":{"status":"done"}}`))
	notify("answer", toolEnd("Bash", "", shellStarted("m3")))
	notify("answer", toolEnd("Bash", "a3", shellStarted("m4")))
	quiet("turn end, its watch runs", notify("stop", turnEnd("watching", "monitor:m1", "monitor:m2", "shell:m3")), "watching")
	if f := files(t, root, id); f[owedSuffix] != "~m1\n" {
		t.Fatalf("owed: %q", f[owedSuffix])
	}
	if out := notify("stop", turnEnd("the watch ended", "monitor:m2", "shell:m3", "shell:m4")); out != done {
		t.Fatalf("the turn end after the watch printed %q", out)
	}

	// As seen (#62): the agent publishes an artifact, and Claude arms a
	// watch on it, a monitor task that runs to the session's end and wakes
	// nobody but for a comment. The turn is the agent's end; a subagent it
	// starts later counts as ever, beside the watch; and so is a turn that
	// leaves the agent's own Monitor tool running.
	notify("prompt", fixture(t, "prompt"))
	notify("answer", toolEnd("Artifact", "", `{"url":"https://claude.ai/artifact/x","title":"QA evidence"}`))
	if out := notify("stop", turnEnd("published", "monitor:w1")); out != done {
		t.Fatalf("the turn end after the publish printed %q", out)
	}
	if f := files(t, root, id); len(f) != 2 {
		t.Fatalf("files left: %v", f)
	}
	notify("prompt", fixture(t, "prompt"))
	notify("answer", toolEnd("Agent", "", launched("a5")))
	quiet("turn end, a subagent beside the watch", notify("stop", turnEnd("started", "a5", "monitor:w1")), "started")
	notify("prompt", wakeUp("a5"))
	if out := notify("stop", turnEnd("finished", "monitor:w1")); out != done {
		t.Fatalf("the closing turn beside the watch printed %q", out)
	}
	notify("prompt", fixture(t, "prompt"))
	notify("answer", toolEnd("Monitor", "", `{"taskId":"w2","timeoutMs":300000,"persistent":false}`))
	if out := notify("stop", turnEnd("watching", "monitor:w1", "monitor:w2")); out != done {
		t.Fatalf("the turn end with the agent's own watch printed %q", out)
	}
	if f := files(t, root, id); len(f) != 2 {
		t.Fatalf("files left: %v", f)
	}

	// A session that starts over forgets the command.
	notify("prompt", fixture(t, "prompt"))
	notify("answer", toolEnd("Bash", "a4", shellStarted("d1")))
	notify("start", fixture(t, "session-start-clear"))
	notify("prompt", fixture(t, "prompt"))
	if out := notify("stop", turnEnd("done", "shell:d1")); out != done {
		t.Fatalf("the turn of the session started over printed %q", out)
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

// Claude Code runs the hook for a prompt typed while the agent works when
// it queues the prompt, not when it takes it, and that payload carries the
// prompt_id of the turn at work (#82). A turn that ends with the prompt
// still queued is followed at once by the turn Claude takes it for, with
// no hook of its own: the agent works on, since the turn end, on that
// prompt. A /clear queued before it runs first, in between: the agent
// works on in the new session. The fixtures are Claude Code 2.1.301's
// (run A3 of #82).
func TestAPromptQueuedWhileTheAgentWorksIsATurnOfItsOwnWhenTheTurnEndsWithIt(t *testing.T) {
	for _, clear := range []bool{false, true} {
		t.Run(fmt.Sprintf("clear=%v", clear), func(t *testing.T) { testAQueuedPromptsTurn(t, clear) })
	}
}

func testAQueuedPromptsTurn(t *testing.T, clear bool) {
	root := testutil.GitRepo(t, "app")
	const id = "0a1b2c3d4e5f"
	report := func(when string) Report {
		t.Helper()
		r, ok := Read(root, id)
		if !ok {
			t.Fatalf("%s: no report", when)
		}
		return r
	}
	const queued = "Run exactly this bash command in the foreground and show me its output: for i in $(seq 25); do echo tock $i; sleep 1; done"

	fire(t, root, root, id, "prompt", fixture(t, "queued-first"))
	fire(t, root, root, id, "prompt", fixture(t, "queued-while-working"))
	before := time.Now()
	time.Sleep(10 * time.Millisecond)
	// The turn's end hands over to the queued prompt and notifies nobody.
	if out := run(t, root, root, id, hook("stop", "[%s]"), fixture(t, "queued-stop")); out != "" {
		t.Fatalf("the turn end before a queued prompt printed %q", out)
	}
	r := report("the turn end")
	if r.State != Working || r.Prompt != queued || r.Last != "**The Song That Came Back** For eleven years, the whale sang a song no other whale could answer." || !r.Since.After(before) {
		t.Fatalf("the turn end before a queued prompt: %+v", r)
	}
	if clear {
		fire(t, root, root, id, "end", fixture(t, "queued-session-end-clear"))
		if now := report("the queued /clear's end"); !reflect.DeepEqual(now, r) {
			t.Fatalf("the queued /clear's end: %+v, was %+v", now, r)
		}
		fire(t, root, root, id, "start", fixture(t, "queued-session-start-clear"))
		now := report("the queued /clear's start")
		if now.State != Working || now.Prompt != queued || now.SessionID != "168b94e0-a750-4edd-9f35-6e49d2b44c41" || now.Last != r.Last {
			t.Fatalf("the queued /clear's start: %+v", now)
		}
	}
	// The queued prompt's turn reports as any other.
	fire(t, root, root, id, "answer", fixture(t, "queued-turn-tool-end"))
	if r := report("its tool"); r.State != Working || r.Prompt != queued || !r.Acted {
		t.Fatalf("its tool: %+v", r)
	}
	if out := run(t, root, root, id, hook("stop", "[%s]"), fixture(t, "queued-turn-stop")); out != `{"terminalSequence":"[Done: main]"}`+"\n" {
		t.Fatalf("its turn end printed %q", out)
	}
	if r := report("its turn end"); r.State != Done || r.Last != "I ran the command in the foreground. It printed all 25 lines, one per second." {
		t.Fatalf("its turn end: %+v", r)
	}
}

// The prompt_ids of the queued-* fixtures: the turn at work, which
// queued-first starts and in which queued-while-working is queued, and the
// turn Claude takes that prompt for.
const (
	turnAtWork = "a5c44ac7-4166-41f7-96d0-9d535b9cdeb7"
	turnTaken  = "f6babc86-be46-4229-afb6-d2051df9f374"
)

// inTurnAtWork is the queued-* fixture name as an event of the turn at
// work.
func inTurnAtWork(t *testing.T, name string) []byte {
	return bytes.Replace(fixture(t, name), []byte(turnTaken), []byte(turnAtWork), 1)
}

// A prompt Claude has queued is taken into the turn at work at the next
// end of a tool of the agent's own (#82): that turn's end is the agent's.
// One the user typed while a turn ran that they then interrupted, which no
// hook reports, is a turn of its own at once, under a prompt_id of its own:
// its end is the agent's too.
func TestAQueuedPromptClaudeTakesIntoTheTurnOrRunsAtOnceEndsWithItsTurn(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	const id = "0a1b2c3d4e5f"
	ends := func(when string, stop []byte) {
		t.Helper()
		if out := run(t, root, root, id, hook("stop", "[%s]"), stop); out != `{"terminalSequence":"[Done: main]"}`+"\n" {
			t.Fatalf("%s: the turn end printed %q", when, out)
		}
		if r, _ := Read(root, id); r.State != Done {
			t.Fatalf("%s: %+v", when, r)
		}
	}

	fire(t, root, root, id, "prompt", fixture(t, "queued-first"))
	fire(t, root, root, id, "prompt", fixture(t, "queued-while-working"))
	// A subagent's tool ending takes nothing into the agent's turn.
	fire(t, root, root, id, "answer", bytes.Replace(inTurnAtWork(t, "queued-turn-tool-end"), []byte(`"hook_event_name"`), []byte(`"agent_id":"a1","hook_event_name"`), 1))
	fire(t, root, root, id, "answer", inTurnAtWork(t, "queued-turn-tool-end"))
	ends("taken into the turn", inTurnAtWork(t, "queued-turn-stop"))

	fire(t, root, root, id, "prompt", fixture(t, "queued-first"))
	fire(t, root, root, id, "prompt", fixture(t, "queued-while-working"))
	ends("interrupted, then run at once", fixture(t, "queued-turn-stop"))
}

// A prompt Claude queues starts no turn, so the turn at work keeps its
// mark that Claude has acted on it (#77, #82); the turn Claude takes the
// prompt for has not acted yet. In that turn, a prompt queued once a tool
// of its own has ended is known by the id the tool's end carries, though
// no prompt of that turn's id was reported: its end hands over again.
func TestAQueuedPromptLeavesTheTurnAtWorkActedOnAndQueuesAgainInTheTurnItIsTakenFor(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	const id = "0a1b2c3d4e5f"
	report := func(when string, state string, acted bool) {
		t.Helper()
		if r, _ := Read(root, id); r.State != state || r.Acted != acted {
			t.Fatalf("%s: %+v, want %s, acted %v", when, r, state, acted)
		}
	}

	fire(t, root, root, id, "prompt", fixture(t, "queued-first"))
	fire(t, root, root, id, "answer", inTurnAtWork(t, "queued-turn-tool-end"))
	report("its tool", Working, true)
	fire(t, root, root, id, "prompt", fixture(t, "queued-while-working"))
	report("a prompt queued", Working, true)
	fire(t, root, root, id, "stop", fixture(t, "queued-stop"))
	report("the turn it is taken for", Working, false)

	fire(t, root, root, id, "answer", fixture(t, "queued-turn-tool-end"))
	report("that turn's tool", Working, true)
	again := bytes.Replace(fixture(t, "queued-while-working"), []byte(turnAtWork), []byte(turnTaken), 1)
	fire(t, root, root, id, "prompt", again)
	report("a prompt queued in that turn", Working, true)
	fire(t, root, root, id, "stop", fixture(t, "queued-turn-stop"))
	report("the turn that one is taken for", Working, false)
}

// The user interrupts a turn with a prompt queued behind it: Claude runs
// the queued prompt at once as a turn of its own, with no hook until that
// turn's first tool ends (#88). That tool's end carries a prompt_id that
// is neither the turn at work's nor the queued prompt's, so the hook hands
// over there: the state file becomes the queued prompt, dated now, and the
// mark that Claude has acted on the turn is that turn's. A subagent's tool
// hands over nothing, and nor does a tool of the turn Claude took the
// prompt for at its Stop, whose prompt the state file already is.
func TestAToolOfATurnTakenAfterAnEscHandsOverToTheQueuedPrompt(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	const id = "0a1b2c3d4e5f"
	report := func(when string) Report {
		t.Helper()
		r, ok := Read(root, id)
		if !ok {
			t.Fatalf("%s: no report", when)
		}
		return r
	}
	const queued = "Run exactly this bash command in the foreground and show me its output: for i in $(seq 25); do echo tock $i; sleep 1; done"

	fire(t, root, root, id, "prompt", fixture(t, "queued-first"))
	fire(t, root, root, id, "prompt", fixture(t, "queued-while-working"))
	// The queued prompt is written as any other (#82): the report is it,
	// from when it was queued, with the copy pending beside it.
	at := report("queued")
	if at.State != Working || at.Prompt != queued || !at.Queued {
		t.Fatalf("queued: %+v", at)
	}
	time.Sleep(10 * time.Millisecond)
	fire(t, root, root, id, "answer", bytes.Replace(fixture(t, "queued-turn-tool-end"), []byte(`"hook_event_name"`), []byte(`"agent_id":"a1","hook_event_name"`), 1))
	if r := report("a subagent's tool"); !reflect.DeepEqual(r, at) {
		t.Fatalf("a subagent's tool: %+v, was %+v", r, at)
	}
	before := time.Now()
	time.Sleep(10 * time.Millisecond)
	fire(t, root, root, id, "answer", fixture(t, "queued-turn-tool-end"))
	r := report("the taken turn's tool")
	if r.State != Working || r.Prompt != queued || !r.Acted || r.Queued || !r.Since.After(before) {
		t.Fatalf("the taken turn's tool: %+v", r)
	}
	if _, err := os.Lstat(filepath.Join(Dir(root), id+queuedSuffix)); err == nil {
		t.Fatal("the queued copy stayed after the hand-over")
	}
	if out := run(t, root, root, id, hook("stop", "[%s]"), fixture(t, "queued-turn-stop")); out != `{"terminalSequence":"[Done: main]"}`+"\n" {
		t.Fatalf("its turn end printed %q", out)
	}
	if r := report("its turn end"); r.State != Done || r.Last != "I ran the command in the foreground. It printed all 25 lines, one per second." {
		t.Fatalf("its turn end: %+v", r)
	}

	// Taken at the Stop (#82): the tool of that turn restarts nothing.
	fire(t, root, root, id, "prompt", fixture(t, "queued-first"))
	fire(t, root, root, id, "prompt", fixture(t, "queued-while-working"))
	fire(t, root, root, id, "stop", fixture(t, "queued-stop"))
	taken := report("taken at the Stop")
	if taken.State != Working || taken.Prompt != queued || taken.Queued {
		t.Fatalf("taken at the Stop: %+v", taken)
	}
	time.Sleep(10 * time.Millisecond)
	fire(t, root, root, id, "answer", fixture(t, "queued-turn-tool-end"))
	if r := report("that turn's tool"); r.State != Working || r.Prompt != queued || !r.Acted || !r.Since.Equal(taken.Since) {
		t.Fatalf("that turn's tool: %+v, taken %+v", r, taken)
	}
}

// hq hands over to a queued prompt itself when the agent's screen shows
// Claude took it (#88): the queued copy becomes the state file, dated now,
// and what the hook kept beside the file for the turn at work (.on,
// .acted) goes with it. The copy is renamed, not rewritten, so two hq
// processes hand over once between them, and a hook that has just taken
// the copy away is not written over. A link planted as the copy is refused.
func TestTakeQueuedMakesTheStateFileTheQueuedPromptDatedNow(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	const id = "0a1b2c3d4e5f"
	path := filepath.Join(Dir(root), id)
	const queued = "Run exactly this bash command in the foreground and show me its output: for i in $(seq 25); do echo tock $i; sleep 1; done"

	if err := TakeQueued(root, id); err == nil {
		t.Fatal("handed over with nothing queued")
	}
	fire(t, root, root, id, "prompt", fixture(t, "queued-first"))
	fire(t, root, root, id, "answer", inTurnAtWork(t, "queued-turn-tool-end"))
	fire(t, root, root, id, "prompt", fixture(t, "queued-while-working"))
	if err := os.WriteFile(path+keptSuffix, wakeUp("t1"), 0o644); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(path + queuedSuffix)
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := Read(root, id); !r.Queued || !r.Acted {
		t.Fatalf("before: %+v", r)
	}
	before := time.Now()
	time.Sleep(10 * time.Millisecond)
	if err := TakeQueued(root, id); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("the state file is not the queued copy: %v %q", err, got)
	}
	for _, suffix := range []string{queuedSuffix, keptSuffix, actedSuffix} {
		if _, err := os.Lstat(path + suffix); err == nil {
			t.Errorf("%s stayed", suffix)
		}
	}
	r, ok := Read(root, id)
	if !ok || r.State != Working || r.Prompt != queued || r.Acted || r.Queued || !r.Since.After(before) || r.Last != "" {
		t.Fatalf("after: %+v", r)
	}
	if err := TakeQueued(root, id); err == nil {
		t.Fatal("handed over twice")
	}
	// An acted mark the hook wrote for the queued turn's own tool, a
	// moment before, is that turn's: it stays.
	fire(t, root, root, id, "prompt", fixture(t, "queued-first"))
	fire(t, root, root, id, "prompt", fixture(t, "queued-while-working"))
	if err := os.WriteFile(path+actedSuffix, []byte(turnTaken+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := TakeQueued(root, id); err != nil {
		t.Fatal(err)
	}
	if r, _ := Read(root, id); !r.Acted || r.Queued || r.Prompt != queued {
		t.Fatalf("the queued turn's own mark: %+v", r)
	}

	r, _ = Read(root, id)
	if err := os.Symlink(filepath.Join(root, "secret"), path+queuedSuffix); err != nil {
		t.Fatal(err)
	}
	if err := TakeQueued(root, id); err == nil {
		t.Fatal("followed a link planted as the queued copy")
	}
	if now, _ := Read(root, id); now.Prompt != queued || !now.Since.Equal(r.Since) {
		t.Fatalf("the link changed the report: %+v", now)
	}
}
