package cli

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/rkrysinski/hq/internal/dialog"
	"github.com/rkrysinski/hq/internal/prefs"
	"github.com/rkrysinski/hq/internal/proc"
	"github.com/rkrysinski/hq/internal/sbx"
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

func TestNewNamesWhatIsWrongWithTheName(t *testing.T) {
	long := strings.Repeat("a", 33)
	for name, want := range map[string]string{
		strings.Repeat("a", 32): "",
		long:                    "hq: name '" + long + "' is too long: at most 32 characters\n",
		"bad name":              "hq: invalid name 'bad name': use letters, digits, - and _\n",
		long + "/b":             "hq: invalid name '" + long + "/b': use letters, digits, - and _\n",
	} {
		_, err := parseNew([]string{name}, func(string) bool { return false }, "/w")
		if want == "" {
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
			continue
		}
		f := newFakes()
		f.sbx.sandboxes = sandboxesFor("/w/app")
		code, _, errOut := f.run("new", name)
		if code != ExitUsage || errOut != want || len(f.tmux.windows) != 0 {
			t.Errorf("%s: exit %d, stderr %q, %d windows", name, code, errOut, len(f.tmux.windows))
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
	// The window carries the agent's settings by reference: hq __session
	// writes them out as the session starts, so the hook script takes no
	// room from the prompt in the window's command (#40).
	if argv[5] != "--settings" || argv[6] != "hq-settings:a:"+w.Options["id"] {
		t.Fatalf("settings %q", argv[5:7])
	}
	if !f.tmux.started[w.ID] {
		t.Fatal("window not started")
	}
	if w.Options["inbox"] != "2" {
		t.Fatalf("hooks that deliver messages and tell a supervised turn not marked: %v", w.Options)
	}
	// hq new from a shell is the user's: its prompt is not announced as
	// the supervisor's (spec §5).
	if len(f.announced) != 0 {
		t.Fatalf("announced %v", f.announced)
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

// Without settings hq creates the sandbox with sbx's defaults; with them,
// the template and MCP servers of the preferences file, each overridden by
// its environment variable (#58).
func TestNewCreatesSandboxWithTheSandboxSettings(t *testing.T) {
	for _, c := range []struct {
		name  string
		prefs prefs.Sandbox
		env   map[string]string
		want  sbx.Options
	}{
		{"none", prefs.Sandbox{}, nil, sbx.Options{}},
		{"from the file", prefs.Sandbox{Template: "sbx-image:local", StaticMCP: []string{"pencil", " ", "docs "}}, nil,
			sbx.Options{Template: "sbx-image:local", StaticMCP: []string{"pencil", "docs"}}},
		{"the environment wins", prefs.Sandbox{Template: "sbx-image:local", StaticMCP: []string{"pencil"}},
			map[string]string{"HQ_SBX_TEMPLATE": "ghcr.io/o/i:latest", "HQ_SBX_STATIC_MCP": "a, b,"},
			sbx.Options{Template: "ghcr.io/o/i:latest", StaticMCP: []string{"a", "b"}}},
		{"an empty variable is unset", prefs.Sandbox{StaticMCP: []string{"pencil"}},
			map[string]string{"HQ_SBX_TEMPLATE": " ", "HQ_SBX_STATIC_MCP": ","},
			sbx.Options{StaticMCP: []string{"pencil"}}},
	} {
		f := newFakes()
		f.prefs.Sandbox = c.prefs
		for k, v := range c.env {
			f.env[k] = v
		}
		if code, _, errOut := f.run("new", "a", "/w/lib"); code != 0 {
			t.Fatalf("%s: exit %d %q", c.name, code, errOut)
		}
		if len(f.sbx.createdWith) != 1 || !reflect.DeepEqual(f.sbx.createdWith[0], c.want) {
			t.Errorf("%s: created with %+v", c.name, f.sbx.createdWith)
		}
	}
}

// A sandbox that exists is used as it is, whatever the settings.
func TestNewReusesSandboxWhateverTheSandboxSettings(t *testing.T) {
	f := newFakes()
	f.sbx.sandboxes = sandboxesFor("/w/app")
	f.prefs.Sandbox = prefs.Sandbox{Template: "sbx-image:local"}
	if code, _, _ := f.run("new", "a"); code != 0 || len(f.sbx.createdWith) != 0 {
		t.Fatalf("exit %d, created with %+v", code, f.sbx.createdWith)
	}
}

// sbx's error for a template or server it cannot find names the settings
// and where they were set (#58).
func TestNewCreationFailureNamesTheSandboxSettings(t *testing.T) {
	f := newFakes()
	f.env["HOME"] = "/home/u"
	f.env["HQ_SBX_TEMPLATE"] = "sbx-image:gone"
	f.prefs.Sandbox = prefs.Sandbox{StaticMCP: []string{"pencil"}}
	f.sbx.createErr = &proc.Error{Name: "sbx", Msg: "error: image not found"}
	code, _, errOut := f.run("new", "a")
	want := "hq: sbx: error: image not found; created with sandbox.template sbx-image:gone (HQ_SBX_TEMPLATE), sandbox.staticMcp pencil (/home/u/.config/hq/preferences.json)\n"
	if code != ExitEnvironment || errOut != want {
		t.Fatalf("exit %d %q", code, errOut)
	}
	// Settings from one place name it once.
	delete(f.env, "HQ_SBX_TEMPLATE")
	f.prefs.Sandbox.Template = "sbx-image:local"
	_, _, errOut = f.run("new", "a")
	if want := "hq: sbx: error: image not found; created with sandbox.template sbx-image:local, sandbox.staticMcp pencil (/home/u/.config/hq/preferences.json)\n"; errOut != want {
		t.Fatalf("%q", errOut)
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

// A prompt longer than tmux takes in the window's command is the user's to
// shorten, under the prompt in the dialog (#40).
func TestNewWithAPromptTooLongForTmuxSaysSo(t *testing.T) {
	f := newFakes()
	f.sbx.sandboxes = sandboxesFor("/w/app")
	f.tmux.newWindowErr = tmux.CommandTooLong{Over: 42}
	code, _, errOut := f.run("new", "a", "say hi")
	if code != ExitUsage || errOut != "hq: the prompt is 42 bytes too long for tmux; shorten it, or send the rest with hq send once the agent runs\n" {
		t.Fatalf("exit %d %q", code, errOut)
	}
	_, _, err := createAgent(io.Discard, f.deps(), newArgs{name: "a", dir: "/w/app", prompt: "say hi"})
	var fe dialog.FieldError
	if !errors.As(err, &fe) || fe.Field != dialog.Prompt {
		t.Fatalf("%v", err)
	}
}
