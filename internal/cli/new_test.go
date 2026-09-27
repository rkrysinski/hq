package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rkrysinski/hq/internal/proc"
	"github.com/rkrysinski/hq/internal/tmux"
)

func (f *fakes) run(args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := mainWith(args, Env{Stdin: strings.NewReader(f.stdin), Stdout: &out, Stderr: &errOut}, f.deps())
	return code, out.String(), errOut.String()
}

func TestParseNewSecondArgumentIsDirWhenItIsADirectory(t *testing.T) {
	isDir := func(p string) bool { return p == "/w/lib" }
	n, err := parseNew([]string{"a", "/w/lib", "hi"}, isDir, "/w/app")
	if err != nil || n.dir != "/w/lib" || n.prompt != "hi" {
		t.Fatalf("%+v %v", n, err)
	}
	n, err = parseNew([]string{"42", "work on issue #42"}, isDir, "/w/app")
	if err != nil || n.dir != "/w/app" || n.prompt != "work on issue #42" {
		t.Fatalf("%+v %v", n, err)
	}
}

func TestParseNewRejectsBadInput(t *testing.T) {
	isDir := func(string) bool { return false }
	for _, args := range [][]string{{}, {"bad name"}, {"a/b"}, {"a", "p1", "p2"}, {"a", "-x"}} {
		if _, err := parseNew(args, isDir, "/w"); err == nil || err.(*Error).Code != ExitUsage {
			t.Errorf("%q: got %v, want usage error", args, err)
		}
	}
}

func TestNewStartsAgentInRepositorysSandbox(t *testing.T) {
	f := newFakes()
	f.sbx.sandboxes = sandboxesFor("/w/app")
	code, out, errOut := f.run("new", "a", "say hi")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if out != "started a in app (sandbox claude-app); enter it with: hq go a\n" {
		t.Fatalf("stdout %q", out)
	}
	w := f.tmux.windows[len(f.tmux.windows)-1]
	if w.Name != "a" || w.Options["repo"] != "/w/app" || w.Options["sandbox"] != "claude-app" || w.Options["id"] == "" {
		t.Fatalf("window %+v", w)
	}
	argv := f.tmux.argv[w.ID]
	if strings.Join(argv[:5], " ") != "sbx run --name claude-app --" || argv[len(argv)-1] != "say hi" {
		t.Fatalf("argv %q", argv)
	}
	var settings struct{ Env map[string]string }
	if err := json.Unmarshal([]byte(argv[6]), &settings); err != nil || settings.Env["HQ_ID"] != w.Options["id"] || settings.Env["HQ_AGENT"] != "a" {
		t.Fatalf("settings %q: %v", argv[6], err)
	}
	if !strings.Contains(argv[6], `"stop","[notify %s]"`) { // the platform's sequence (design §3.5)
		t.Fatalf("no notification in settings %q", argv[6])
	}
	if !f.tmux.started[w.ID] {
		t.Fatal("window not started")
	}
	if w.Options["new"] == "" {
		t.Fatal("not marked new (S2)")
	}
	if len(f.sbx.created) != 0 {
		t.Fatal("created a sandbox although one existed")
	}
}

func TestNewFromWorktreeUsesMainRepositorysSandbox(t *testing.T) {
	f := newFakes()
	f.sbx.sandboxes = sandboxesFor("/w/app")
	f.cwd = "/w/app/.claude/worktrees/x"
	if code, _, errOut := f.run("new", "a"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if w := f.tmux.windows[len(f.tmux.windows)-1]; w.Options["sandbox"] != "claude-app" {
		t.Fatalf("window %+v", w)
	}
}

func TestNewCreatesSandboxWhenRepositoryHasNone(t *testing.T) {
	f := newFakes()
	code, out, _ := f.run("new", "a", "/w/lib")
	if code != 0 || len(f.sbx.created) != 1 || f.sbx.created[0] != "/w/lib" {
		t.Fatalf("exit %d, created %v", code, f.sbx.created)
	}
	if !strings.Contains(out, "creating a sandbox for lib") {
		t.Fatalf("stdout %q", out)
	}
}

func TestNewRefusesDuplicateName(t *testing.T) {
	f := newFakes()
	f.sbx.sandboxes = sandboxesFor("/w/app")
	f.run("new", "a")
	before := len(f.tmux.windows)
	code, _, errOut := f.run("new", "a")
	if code != ExitUsage || errOut != "hq: agent 'a' already exists (see hq ls; hq kill a frees the name)\n" {
		t.Fatalf("exit %d stderr %q", code, errOut)
	}
	if len(f.tmux.windows) != before {
		t.Fatal("a window was created")
	}
}

func TestNewLosesRaceToEarlierWindowOfSameName(t *testing.T) {
	f := newFakes()
	f.sbx.sandboxes = sandboxesFor("/w/app")
	f.tmux.next = 10 // our window gets a higher id than @5
	// Between our duplicate check and our window, another hq new made "a" as @5.
	f.tmux.onNewWindow = func(ft *fakeTmux) {
		ft.windows = append(ft.windows, tmux.Window{ID: "@5", Name: "a", Options: map[string]string{"id": "other", "name": "a"}})
	}
	if code, _, _ := f.run("new", "a"); code != ExitUsage {
		t.Fatalf("exit %d, want %d", code, ExitUsage)
	}
	for _, w := range f.tmux.windows {
		if w.Name == "a" && w.ID != "@5" {
			t.Fatalf("losing window %s was not removed", w.ID)
		}
	}
	if len(f.tmux.started) != 0 {
		t.Fatal("losing window was started")
	}
}

func TestNewOutsideGitIsNotFound(t *testing.T) {
	f := newFakes()
	code, _, errOut := f.run("new", "a", "/w/plain")
	if code != ExitNotFound || errOut != "hq: /w/plain is not in a git repository\n" {
		t.Fatalf("exit %d stderr %q", code, errOut)
	}
}

func TestEnvironmentChecks(t *testing.T) {
	f := newFakes()
	f.tmux.version = "3.2a"
	if code, _, errOut := f.run("ls"); code != ExitEnvironment || !strings.Contains(errOut, "tmux 3.2a is too old, 3.4 or newer needed") {
		t.Fatalf("old tmux: exit %d %q", code, errOut)
	}
	f.tmux.missing = true
	if code, _, errOut := f.run("new", "a"); code != ExitEnvironment || !strings.Contains(errOut, "tmux not found") {
		t.Fatalf("no tmux: exit %d %q", code, errOut)
	}
	f = newFakes()
	f.sbx.err = sbxNotFound()
	if code, _, errOut := f.run("new", "a"); code != ExitEnvironment || errOut != "hq: sbx not found; install Docker Sandboxes\n" {
		t.Fatalf("no sbx: exit %d %q", code, errOut)
	}
}

func TestNewReportsSandboxCreationFailure(t *testing.T) {
	f := newFakes()
	f.sbx.createErr = &proc.Error{Name: "sbx", Msg: "daemon not running"}
	code, _, errOut := f.run("new", "a")
	if code != ExitEnvironment || errOut != "hq: sbx: daemon not running\n" {
		t.Fatalf("exit %d %q", code, errOut)
	}
	if len(f.tmux.windows) != 0 {
		t.Fatal("window created")
	}
}

func TestNewReportsTmuxFailure(t *testing.T) {
	f := newFakes()
	f.sbx.sandboxes = sandboxesFor("/w/app")
	f.tmux.newWindowErr = &proc.Error{Name: "tmux", Msg: "server exited"}
	if code, _, errOut := f.run("new", "a"); code != ExitEnvironment || errOut != "hq: tmux: server exited\n" {
		t.Fatalf("exit %d %q", code, errOut)
	}
}
