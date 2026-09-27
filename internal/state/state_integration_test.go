//go:build integration

package state

import (
	"bytes"
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
	cmd.Env = append(os.Environ(), "HQ_ID="+id, "CLAUDE_PROJECT_DIR="+project)
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
		{"prompt", "prompt", ""},
		{"end", "session-end", ""},
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
	for _, p := range []string{"abc", "abc.stop", "abcd", "abcd.stop", "def", "../../keep"} {
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
