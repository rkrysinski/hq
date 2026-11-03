package cli

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/rkrysinski/hq/internal/dialog"
	"github.com/rkrysinski/hq/internal/gh"
	"github.com/rkrysinski/hq/internal/kills"
	"github.com/rkrysinski/hq/internal/prefs"
	"github.com/rkrysinski/hq/internal/proc"
	"github.com/rkrysinski/hq/internal/sbx"
	"github.com/rkrysinski/hq/internal/state"
)

func TestNOpensTheNewAgentDialogInAPopup(t *testing.T) {
	f := newFakes()
	f.exe = "/opt/hq"
	src := listSource(f.deps(), "%1")
	if err := src.NewAgent("/w/lib"); err != nil {
		t.Fatal(err)
	}
	if err := src.NewAgent(""); err != nil {
		t.Fatal(err)
	}
	want := []string{"%1 /w/app 76x20 /opt/hq __new-dialog /w/lib", "%1 /w/app 76x20 /opt/hq __new-dialog /w/app"}
	if strings.Join(f.tmux.popups, "\n") != strings.Join(want, "\n") {
		t.Fatalf("popups %q", f.tmux.popups)
	}
}

func TestTheNewAgentDialogStartsWhereTheLatestHqWasStarted(t *testing.T) {
	f := newFakes()
	f.exe = "/opt/hq"
	f.run("dash")
	f.cwd = "/w/lib"
	f.run("dash")
	f.cwd = "/w/app" // the list program's own directory, from the first run
	if err := listSource(f.deps(), "%1").NewAgent(""); err != nil {
		t.Fatal(err)
	}
	f.tmux.session[startedValue] = "/w/gone"
	if err := listSource(f.deps(), "%1").NewAgent(""); err != nil {
		t.Fatal(err)
	}
	want := []string{"%1 /w/lib 76x20 /opt/hq __new-dialog /w/lib", "%1 /w/app 76x20 /opt/hq __new-dialog /w/app"}
	if strings.Join(f.tmux.popups, "\n") != strings.Join(want, "\n") {
		t.Fatalf("popups %q", f.tmux.popups)
	}
}

func TestTheDialogCommandShowsTheDirUnderHomeWithATilde(t *testing.T) {
	f := newFakes()
	f.env["HOME"] = "/w"
	if code, _, errOut := f.run("__new-dialog", "/w/app"); code != 0 {
		t.Fatalf("exit %d %q", code, errOut)
	}
	m, ok := f.dialog.(dialog.NewAgent)
	if !ok || !strings.Contains(ansi.Strip(m.View()), "~/app ") {
		t.Fatalf("dialog %T:\n%s", f.dialog, ansi.Strip(m.View()))
	}
	if code, _, _ := f.run("__new-dialog"); code != ExitUsage {
		t.Fatalf("no dir: exit %d", code)
	}
	if _, out, _ := runCLI("help"); strings.Contains(out, "__new-dialog") {
		t.Fatal("help shows __new-dialog")
	}
}

func TestTheDialogStartsAgentsAsHqNewDoes(t *testing.T) {
	f := newFakes()
	f.env["HOME"] = "/w"
	f.sbx.sandboxes = sandboxesFor("/w/app")
	start := dialogStart(f.deps(), "/w/app")
	for _, dir := range []string{"~/app", "sub", "/w/app/"} {
		name := "a" + string(rune('0'+len(f.tmux.windows)))
		if err := start(name, dir, "say hi", ""); err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		w := f.tmux.windows[len(f.tmux.windows)-1]
		if w.Name != name || w.Options["repo"] != "/w/app" || w.Options["new"] == "" || !f.tmux.started[w.ID] {
			t.Fatalf("%s: window %+v", dir, w)
		}
		if argv := f.tmux.argv[w.ID]; argv[len(argv)-1] != "say hi" {
			t.Fatalf("argv %q", argv)
		}
		// The user's own new agent takes the slot, keys in its session
		// (spec S2, §6.3).
		if f.tmux.docked != w.ID || !f.tmux.dockKeys {
			t.Fatalf("%s: docked %q keys %v", dir, f.tmux.docked, f.tmux.dockKeys)
		}
	}
	// A release's name, its version and all (#119).
	if err := start("rel-1.10.5-qa", "/w/app", "", ""); err != nil || f.tmux.windows[len(f.tmux.windows)-1].Name != "rel-1.10.5-qa" {
		t.Fatalf("a release's name: %v", err)
	}
	// A failure to dock leaves the agent started, the dialog closing.
	f.tmux.dockErr = errors.New("tmux gone")
	if err := start("b1", "/w/app", "", ""); err != nil {
		t.Fatalf("a failed dock failed the start: %v", err)
	}
}

func TestTheDialogsErrorsBelongToTheirFields(t *testing.T) {
	f := newFakes()
	f.sbx.sandboxes = sandboxesFor("/w/app")
	start := dialogStart(f.deps(), "/w/app")
	if err := start("a", "/w/app", "", ""); err != nil {
		t.Fatal(err)
	}
	before := len(f.tmux.windows)
	for _, tc := range []struct {
		name, dir string
		field     int
		msg       string
	}{
		{"a", "/w/app", dialog.Name, "agent 'a' already exists (see hq ls; hq kill a frees the name)"},
		{"bad name", "/w/app", dialog.Name, "invalid name 'bad name': use letters, digits, '-', '_' and '.'"},
		{".", "/w/app", dialog.Name, "invalid name '.': it needs a letter or a digit"},
		{strings.Repeat("b", 33), "/w/app", dialog.Name, "name '" + strings.Repeat("b", 33) + "' is too long: at most 32 characters"},
		{"b", "/w/plain", dialog.Dir, "/w/plain is not in a git repository"},
	} {
		var fe dialog.FieldError
		err := start(tc.name, tc.dir, "", "")
		if !errors.As(err, &fe) || fe.Field != tc.field || err.Error() != tc.msg {
			t.Errorf("%s in %s: %v", tc.name, tc.dir, err)
		}
	}
	if len(f.tmux.windows) != before {
		t.Fatal("a window was created")
	}
	f.sbx.createErr = errors.New("daemon not running")
	var fe dialog.FieldError
	if err := start("b", "/w/lib", "", ""); err == nil || errors.As(err, &fe) {
		t.Fatalf("sbx failure: %v", err)
	}
}

func TestTheDialogOffersTheSetupsOfThePreferences(t *testing.T) {
	f := newFakes()
	f.run("__new-dialog", "/w/app")
	if v := ansi.Strip(f.dialog.View()); strings.Contains(v, "setup") {
		t.Fatalf("a setup field without setups:\n%s", v)
	}
	f = setupFakes()
	f.prefs.Sandboxes["fast"] = prefs.Sandbox{}
	if code, _, errOut := f.run("__new-dialog", "/w/app"); code != 0 {
		t.Fatalf("exit %d %q", code, errOut)
	}
	if v := ansi.Strip(f.dialog.View()); !strings.Contains(v, "│ ● default  ○ fast  ○ local") {
		t.Fatalf("no setups:\n%s", v)
	}
}

func TestTheNewAgentPopupMakesRoomForTheSetupField(t *testing.T) {
	f := setupFakes()
	f.exe = "/opt/hq"
	if err := listSource(f.deps(), "%1").NewAgent("/w/app"); err != nil {
		t.Fatal(err)
	}
	f.prefs.Sandboxes = map[string]prefs.Sandbox{}
	if err := listSource(f.deps(), "%1").NewAgent("/w/app"); err != nil {
		t.Fatal(err)
	}
	want := []string{"%1 /w/app 76x23 /opt/hq __new-dialog /w/app", "%1 /w/app 76x20 /opt/hq __new-dialog /w/app"}
	if strings.Join(f.tmux.popups, "\n") != strings.Join(want, "\n") {
		t.Fatalf("popups %q", f.tmux.popups)
	}
}

func TestTheDialogNamesTheSandboxTheAgentWillRunIn(t *testing.T) {
	f := setupFakes()
	f.env["HOME"] = "/w"
	f.repos["/w/My_Repo x"] = "/w/My_Repo x"
	f.sbx.sandboxes = append(f.sbx.sandboxes,
		sbx.Sandbox{Name: "mine", Status: "running", Workspaces: []string{"/w/lib"}},
		sbx.Sandbox{Name: "hq-local-lib", Status: "running", Workspaces: []string{"/w/lib"}})
	note := dialogSandbox(f.deps(), "/w/app")
	for _, tc := range []struct {
		setup, dir, name string
		exists           bool
	}{
		{"default", "/w/app", "claude-app", true},
		{"local", "/w/app", "hq-local-app", false},
		{"local", "sub", "hq-local-app", false}, // as typed: relative to the cwd
		{"default", "~/lib", "mine", true},      // the default whatever its name
		{"local", "/w/lib", "hq-local-lib", true},
		{"default", "/w/My_Repo x", "claude-my-repo-x", false}, // sbx's own name
		{"local", "/w/My_Repo x", "hq-local-my-repo-x", false},
		{"local", "/w/plain", "", false}, // no repository
	} {
		if name, exists := note(tc.setup, tc.dir); name != tc.name || exists != tc.exists {
			t.Errorf("%s in %s: %q %v, want %q %v", tc.setup, tc.dir, name, exists, tc.name, tc.exists)
		}
	}
	// sbx is asked once per dialog; when it cannot answer, no note.
	f.sbx.sandboxes = nil
	if name, _ := note("default", "/w/app"); name != "claude-app" {
		t.Fatalf("asked sbx again: %q", name)
	}
	f.sbx.err = errors.New("daemon not running")
	if name, _ := dialogSandbox(f.deps(), "/w/app")("default", "/w/app"); name != "" {
		t.Fatalf("sbx failing: %q", name)
	}
}

func TestTheDialogStartsTheAgentOnTheSetupPicked(t *testing.T) {
	f := setupFakes()
	start := dialogStart(f.deps(), "/w/app")
	if err := start("43", "/w/app", "", "local"); err != nil {
		t.Fatal(err)
	}
	want := sbx.Options{Name: "hq-local-app", Template: "img:local", StaticMCP: []string{"docs"}, Model: "qwen-local", Provider: "lmstudio"}
	if len(f.sbx.createdWith) != 1 || !reflect.DeepEqual(f.sbx.createdWith[0], want) {
		t.Fatalf("created with %+v", f.sbx.createdWith)
	}
	w := f.tmux.windows[len(f.tmux.windows)-1]
	if w.Name != "43" || w.Options["sandbox"] != "hq-local-app" || f.tmux.docked != w.ID {
		t.Fatalf("window %+v, docked %q", w, f.tmux.docked)
	}
	for _, setup := range []string{"default", ""} {
		name := "d" + setup
		if err := start(name, "/w/app", "", setup); err != nil {
			t.Fatal(err)
		}
		if w := f.tmux.windows[len(f.tmux.windows)-1]; w.Options["sandbox"] != "claude-app" || len(f.sbx.createdWith) != 1 {
			t.Fatalf("%q: in %q, created %d", setup, w.Options["sandbox"], len(f.sbx.createdWith))
		}
	}
}

// A setup sbx refuses fails the dialog with what hq new prints.
func TestTheDialogsErrorForARefusedSetupIsHqNews(t *testing.T) {
	f := setupFakes()
	f.sbx.sandboxes = nil
	f.sbx.createErr = &proc.Error{Name: "sbx", Msg: "error: unknown provider 'lmstudoi'"}
	_, _, errOut := f.run("new", "a", "--setup", "local")
	err := dialogStart(f.deps(), "/w/app")("a", "/w/app", "", "local")
	var fe dialog.FieldError
	if err == nil || errors.As(err, &fe) || "hq: "+err.Error()+"\n" != errOut {
		t.Fatalf("dialog %v, hq new %q", err, errOut)
	}
}

func TestExpandDir(t *testing.T) {
	for in, want := range map[string]string{"~": "/h", "~/x": "/h/x", "": "/c", "x/y": "/c/x/y", "/a/b/": "/a/b"} {
		if got := expandDir(in, "/h", "/c"); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
	if tildeDir("/hx", "/h") != "/hx" || tildeDir("/h", "/h") != "~" {
		t.Error("tildeDir")
	}
}

func TestKOpensTheKillDialogInAPopup(t *testing.T) {
	f := newFakes()
	f.exe = "/opt/hq"
	if err := listSource(f.deps(), "%1").Kill("a"); err != nil {
		t.Fatal(err)
	}
	if want := "%1 /w/app 60x10 /opt/hq __kill-dialog a"; strings.Join(f.tmux.popups, "\n") != want {
		t.Fatalf("popups %q", f.tmux.popups)
	}
}

func TestTheKillDialogEndsTheAgentAsHqKillDoes(t *testing.T) {
	f := killFakes()
	if code, _, errOut := f.run("__kill-dialog", "a"); code != 0 {
		t.Fatalf("exit %d %q", code, errOut)
	}
	m, ok := f.dialog.(dialog.Confirm)
	// The branch is not known yet: the repository stands for it.
	if !ok || !strings.Contains(ansi.Strip(m.View()), "Kill a (app)?") {
		t.Fatalf("dialog %T:\n%s", f.dialog, ansi.Strip(m.View()))
	}
	f.states["id-a"] = state.Report{State: state.Working, Branch: "feat/1"}
	f.run("__kill-dialog", "a")
	if m = f.dialog.(dialog.Confirm); !strings.Contains(ansi.Strip(m.View()), "Kill a (feat/1)?") {
		t.Fatalf("dialog:\n%s", ansi.Strip(m.View()))
	}
	if f.names() != "hq a b gone" || len(f.removed) != 0 {
		t.Fatalf("killed before the answer: %q, removed %v", f.names(), f.removed)
	}
	tm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if tm, _ = tm.Update(cmd()); !tm.(dialog.Confirm).Done {
		t.Fatalf("not done:\n%s", ansi.Strip(tm.View()))
	}
	if got := strings.Join(f.sbx.execs[0], " "); got != `claude-x pkill -TERM -f HQ_ID":"id-a"` || f.names() != "hq b gone" || strings.Join(f.removed, ", ") != "/w/app id-a" {
		t.Fatalf("exec %q, windows %q, removed %v", got, f.names(), f.removed)
	}
	// The trace says the dashboard asked for it (#147).
	if len(f.kills) != 1 || f.kills[0].Name != "a" || f.kills[0].By != kills.ByDashboard || f.kills[0].Command != "" {
		t.Fatalf("trace %+v", f.kills)
	}
}

func TestTheKillDialogNeedsAnAgent(t *testing.T) {
	f := killFakes()
	if code, _, _ := f.run("__kill-dialog", "missing"); code != ExitNotFound || f.dialog != nil {
		t.Fatalf("missing: exit %d, dialog %T", code, f.dialog)
	}
	if code, _, _ := f.run("__kill-dialog"); code != ExitUsage {
		t.Fatalf("no name: exit %d", code)
	}
}

func TestTheListAsksGhAndOpensPullRequestsThroughTheBrowser(t *testing.T) {
	f := newFakes()
	f.prs = map[string]map[string]gh.PR{"/w/app": {"feat/1": {Number: 1, URL: "https://github.com/o/r/pull/1"}}}
	src := listSource(f.deps(), "%1")
	prs, err := src.PullRequests("/w/app")
	if err != nil || prs["feat/1"].Number != 1 {
		t.Fatalf("%v %v", prs, err)
	}
	if err := src.Browse("https://github.com/o/r/pull/1"); err != nil || strings.Join(f.browsed, " ") != "https://github.com/o/r/pull/1" {
		t.Fatalf("browsed %q %v", f.browsed, err)
	}
}
