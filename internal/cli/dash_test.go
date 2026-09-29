package cli

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/dash"
	"github.com/rkrysinski/hq/internal/sbx"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
	"github.com/rkrysinski/hq/internal/version"
)

func TestHqAloneMakesTheDashboardAndAttaches(t *testing.T) {
	f := newFakes()
	code, _, errOut := f.run()
	if code != ExitOK {
		t.Fatalf("exit %d %q", code, errOut)
	}
	if want := []string{f.exe, "__list"}; !reflect.DeepEqual(f.tmux.dashList, want) {
		t.Errorf("list pane runs %q, want %q", f.tmux.dashList, want)
	}
	if f.tmux.attached != f.tmux.dash.Window || f.tmux.respawned != 0 {
		t.Errorf("attached %q respawned %d, want %q and 0 (the list just started)", f.tmux.attached, f.tmux.respawned, f.tmux.dash.Window)
	}
}

func TestDashLeavesARunningListAlone(t *testing.T) {
	f := newFakes()
	f.run("dash")
	f.tmux.listPID, f.alive = 77, map[int]bool{77: true}
	if code, _, _ := f.run("dash"); code != ExitOK || f.tmux.respawned != 0 {
		t.Fatalf("exit %d respawned %d", code, f.tmux.respawned)
	}
}

func TestDashRestartsTheListAfterQuitOrCrash(t *testing.T) {
	for _, pid := range []int{0, 77} { // cleared by q; left by a crash
		f := newFakes()
		f.run("dash")
		f.tmux.listPID = pid
		if code, _, _ := f.run("dash"); code != ExitOK || f.tmux.respawned != 1 {
			t.Errorf("pid %d: exit %d respawned %d", pid, code, f.tmux.respawned)
		}
		if want := []string{f.exe, "__list"}; !reflect.DeepEqual(f.tmux.dashList, want) {
			t.Errorf("respawned %q", f.tmux.dashList)
		}
	}
}

func TestEnteringWithNothingDockedDocksTheCursorRowKeysOnTheList(t *testing.T) {
	for _, env := range []map[string]string{{}, {"TMUX": "/tmp/tmux-501/default,1,0"}} { // attach; switch
		f := chordFakes()
		f.env = env
		f.tmux.session["cursor"] = "c"
		var docked string
		var focused int
		f.tmux.onAttach = func() { docked, focused = f.tmux.docked, f.tmux.focused }
		if code, _, errOut := f.run(); code != ExitOK {
			t.Fatalf("exit %d %q", code, errOut)
		}
		if env["TMUX"] != "" {
			docked, focused = f.tmux.docked, f.tmux.focused
		}
		if docked != "@6" || !strings.HasPrefix(f.tmux.dockTitle, "c · ") || focused != 1 || f.tmux.dockKeys {
			t.Errorf("TMUX %q: docked %q title %q, keys put on the list %d times, want @6 and once", env["TMUX"], docked, f.tmux.dockTitle, focused)
		}
	}
	// The cursor's agent gone: the first row, which the cursor then shows.
	f := chordFakes()
	f.tmux.session["cursor"] = "gone"
	f.run("dash")
	if f.tmux.docked != "@5" || f.tmux.session["cursor"] != "b" {
		t.Errorf("docked %q cursor %q, want @5 (b, the first row by repo) and b", f.tmux.docked, f.tmux.session["cursor"])
	}
}

func TestEnteringLeavesWhatIsDockedAndTheEmptySlot(t *testing.T) {
	f := chordFakes()
	f.tmux.Dock("@4", "a")
	f.tmux.docked, f.tmux.session["cursor"] = "", "c"
	f.run()
	if f.tmux.docked != "" || f.tmux.focused != 0 {
		t.Errorf("something docked: docked %q, keys put on the list %d times", f.tmux.docked, f.tmux.focused)
	}
	// The attention view showing no rows: the placeholder stays.
	f = chordFakes()
	f.states["id-b"] = state.Report{State: state.Working, Since: f.now}
	f.prefs.View = "attention"
	f.run()
	if f.tmux.docked != "" {
		t.Errorf("no rows: docked %q", f.tmux.docked)
	}
	// Refused in another tmux server, and hq ls: nothing docked.
	f = chordFakes()
	f.env["TMUX"] = "/tmp/other,1,0"
	f.run()
	f.env = map[string]string{}
	f.run("ls")
	if f.tmux.docked != "" {
		t.Errorf("refused or ls: docked %q", f.tmux.docked)
	}
}

func TestDashInsideHqServerSwitches(t *testing.T) {
	f := newFakes()
	f.env["TMUX"] = f.tmux.socket + ",1,0"
	if code, _, _ := f.run("dash"); code != ExitOK || f.tmux.entered != f.tmux.dash.Window || f.tmux.attached != "" {
		t.Fatalf("exit %d entered %q attached %q", code, f.tmux.entered, f.tmux.attached)
	}
}

func TestDashInsideAnotherTmuxServerIsRefused(t *testing.T) {
	f := newFakes()
	f.env["TMUX"] = "/tmp/other,1,0"
	code, _, errOut := f.run()
	if code != ExitUsage || f.tmux.attached != "" || f.tmux.entered != "" || !strings.Contains(errOut, "another tmux server") {
		t.Fatalf("exit %d %q", code, errOut)
	}
}

func TestDashNeedsTmux(t *testing.T) {
	f := newFakes()
	f.tmux.missing = true
	if code, _, _ := f.run(); code != ExitEnvironment || f.tmux.dash.Window != "" {
		t.Fatalf("exit %d, dashboard %q", code, f.tmux.dash.Window)
	}
}

func TestDashTakesNoArguments(t *testing.T) {
	if code, _, _ := newFakes().run("dash", "x"); code != ExitUsage {
		t.Fatalf("exit %d", code)
	}
}

func TestDashInTheListPaneRunsTheListThereAndGivesTheTerminalBackAtQ(t *testing.T) {
	f := newFakes()
	f.run("dash")
	f.env["TMUX"] = f.tmux.socket + ",1,0"
	f.env["TMUX_PANE"] = f.tmux.dash.List
	var pidWhileRunning int
	f.listRan = func(src dash.Source) error {
		pidWhileRunning = f.tmux.listPID
		src.Footer([]dash.Hint{{Key: "q", Label: "quit"}})
		return nil
	}
	code, out, _ := f.run()
	if code != ExitOK || f.tmux.respawned != 0 || f.tmux.entered != "" {
		t.Fatalf("exit %d respawned %d entered %q", code, f.tmux.respawned, f.tmux.entered)
	}
	if out != clearScreen+quitHint+"\n" {
		t.Errorf("printed %q", out)
	}
	if pidWhileRunning != 4242 || f.tmux.listPID != 0 {
		t.Errorf("list pid %d while running, %d after", pidWhileRunning, f.tmux.listPID)
	}
	if f.tmux.footer != "" {
		t.Errorf("footer left %q", f.tmux.footer)
	}
	if len(f.tmux.left) != 1 || f.tmux.left[0] != closedHint {
		t.Errorf("clients left hq's session %q, want once with %q", f.tmux.left, closedHint)
	}
}

func TestQuitUnmarksTheListBeforeTheTerminalLeaves(t *testing.T) {
	f := newFakes()
	f.env["TMUX_PANE"] = "%1"
	f.tmux.leaveErr = errors.New("no such client")
	pidAtLeave := -1
	f.listRan = func(dash.Source) error { return nil }
	f.tmux.onLeave = func() { pidAtLeave = f.tmux.listPID }
	code, _, errOut := f.run("__list")
	if pidAtLeave != 0 {
		t.Errorf("list pid %d when the terminal left, want 0", pidAtLeave)
	}
	if code != ExitEnvironment || !strings.Contains(errOut, "no such client") {
		t.Errorf("exit %d %q", code, errOut)
	}
}

func TestAfterTheDashboardDetachesTheTerminalSaysHowToComeBack(t *testing.T) {
	f := newFakes()
	if code, out, _ := f.run("dash"); code != ExitOK || out != resetTitle+closedHint+"\n" {
		t.Fatalf("exit %d out %q", code, out)
	}
	// With hq's session gone (tmux ended), nothing keeps running to come
	// back to.
	f = newFakes()
	f.tmux.onAttach = func() { f.tmux.windows = nil }
	if code, out, _ := f.run("dash"); code != ExitOK || out != resetTitle {
		t.Fatalf("exit %d out %q", code, out)
	}
}

func TestListProgramFailureIsReportedAfterCleanup(t *testing.T) {
	f := newFakes()
	f.env["TMUX_PANE"] = "%1"
	f.listRan = func(dash.Source) error { return errors.New("no terminal") }
	code, _, errOut := f.run("__list")
	if code == ExitOK || !strings.Contains(errOut, "no terminal") || f.tmux.listPID != 0 {
		t.Fatalf("exit %d %q pid %d", code, errOut, f.tmux.listPID)
	}
	if len(f.tmux.left) != 0 {
		t.Fatalf("a failed list took the terminal away: %q", f.tmux.left)
	}
}

func TestListIsHiddenFromHelp(t *testing.T) {
	if _, out, _ := runCLI("help"); strings.Contains(out, "__list") {
		t.Fatal("help shows __list")
	}
}

func TestListSourceReadsTmuxStateFilesAndSbx(t *testing.T) {
	f := newFakes()
	f.tmux.windows = []tmux.Window{
		{ID: "@0", Name: "hq", Options: map[string]string{}},
		agentWindow("@4", "a", "/w/app", f.now, false),
	}
	f.states[f.tmux.windows[1].Options["id"]] = state.Report{State: state.Working, Since: f.now, Last: "Editing"}
	f.sbx.sandboxes = []sbx.Sandbox{{Name: "claude-x", Status: "running"}, {Name: "claude-lib", Status: "stopped"}}
	src := listSource(f.deps(), "%1")

	running, err := src.Running()
	if err != nil || !running["claude-x"] || running["claude-lib"] {
		t.Fatalf("running %v %v", running, err)
	}
	as, err := src.Agents(running)
	if err != nil || len(as) != 1 || as[0].Name != "a" || as[0].State != state.Working || as[0].Last != "Editing" {
		t.Fatalf("agents %+v %v", as, err)
	}
	f.sbx.err = errors.New("sbx hangs")
	if _, err := src.Running(); err == nil {
		t.Error("a failed sbx ls is not reported")
	}
	f.tmux.windowsErr = errors.New("tmux gone")
	if _, err := src.Agents(nil); err == nil {
		t.Error("a failed tmux is not reported")
	}
	if !src.Now().Equal(f.now) {
		t.Error("clock")
	}
}

func TestListSourceFitsTheListToTheTerminal(t *testing.T) {
	f := newFakes()
	src := listSource(f.deps(), "%1")
	for _, tc := range []struct{ terminal, want int }{{40, 10}, {24, 10}, {23, 7}} {
		f.tmux.height = tc.terminal
		src.Layout()
		if got := f.tmux.resized[len(f.tmux.resized)-1]; got != tc.want {
			t.Errorf("terminal %d: list %d lines, want %d", tc.terminal, got, tc.want)
		}
	}
	// A change of width reaches the margins beside the slot too, and the
	// agents' home windows take the slot's new size (#141).
	if f.tmux.margins != 3 || f.tmux.fits != 3 {
		t.Errorf("margins set back %d times, homes fitted %d, want 3", f.tmux.margins, f.tmux.fits)
	}
}

// The list runs the daily check itself, in its own background, and shows
// what it found; hq ls and the others then read the same answer.
func TestListSourceShowsTheUpdateHintOnce(t *testing.T) {
	old := version.Version
	version.Version = "v1.0.0"
	defer func() { version.Version = old }()
	f := newFakes()
	f.releases.latest = "v1.1.0"
	src := listSource(f.deps(), "%1")
	if got := src.UpdateHint(); got != "v1.1.0 available - hq update" {
		t.Fatalf("hint %q", got)
	}
	f.now = f.now.Add(time.Hour)
	src.UpdateHint()
	if f.releases.lookups != 1 || len(f.detached) != 0 || f.prefs.LatestRelease != "v1.1.0" {
		t.Errorf("%d lookups within a day, want 1; %d detached; %+v", f.releases.lookups, len(f.detached), f.prefs)
	}
}

func TestFooterShowsKeysBrightAndKeepsTextLiteral(t *testing.T) {
	got := footer([]dash.Hint{{Key: "r", Label: "refresh"}, {Key: "#", Label: "50% #1"}})
	want := " #[fg=colour255,bold]r#[default] refresh  #[fg=colour255,bold]###[default] 50% ##1"
	if got != want {
		t.Fatalf("footer\n got %q\nwant %q", got, want)
	}
}

func TestListSourceKeepsTheModesInThePreferences(t *testing.T) {
	f := newFakes()
	f.prefs.LatestRelease = "v1.0.0"
	src := listSource(f.deps(), "%1")
	if s, v := src.Modes(); s != "" || v != "" {
		t.Fatalf("fresh modes %q %q", s, v)
	}
	src.SaveModes("repo", "all")
	if s, v := src.Modes(); s != "repo" || v != "all" || f.prefs.LatestRelease != "v1.0.0" {
		t.Fatalf("modes %q %q, prefs %+v", s, v, f.prefs)
	}
}

func TestListSourceKeepsTheCursorOnTheSession(t *testing.T) {
	f := newFakes()
	src := listSource(f.deps(), "%1")
	src.SetCursor("bok-17")
	if got := src.Cursor(); got != "bok-17" || f.tmux.session["cursor"] != "bok-17" {
		t.Fatalf("cursor %q", got)
	}
}

func TestListSourceDocksByNameWithTheFrameTitle(t *testing.T) {
	f := newFakes()
	f.tmux.windows = []tmux.Window{{ID: "@0", Name: "hq", Options: map[string]string{}}, agentWindow("@4", "a", "/w/app", f.now, false)}
	f.states["id-a"] = state.Report{State: state.Working, Branch: "feat/1"}
	src := listSource(f.deps(), "%1")
	if err := src.Dock("a"); err != nil || f.tmux.docked != "@4" || f.tmux.dockTitle != "a · feat/1 · claude-x" {
		t.Fatalf("%v docked %q %q", err, f.tmux.docked, f.tmux.dockTitle)
	}
	if !f.tmux.dockKeys {
		t.Error("open left the keys on the list")
	}
	// The slot following the cursor: the keys stay on the list (§6.3).
	f.tmux.docked = ""
	if err := src.Show("a"); err != nil || f.tmux.docked != "@4" || f.tmux.dockTitle != "a · feat/1 · claude-x" || f.tmux.dockKeys {
		t.Fatalf("%v shown %q %q keys %v", err, f.tmux.docked, f.tmux.dockTitle, f.tmux.dockKeys)
	}
	if err := src.Dock("gone"); err == nil {
		t.Error("docked an agent that is not there")
	}
	if err := src.Show("gone"); err == nil {
		t.Error("showed an agent that is not there")
	}
	f.tmux.windowsErr = errors.New("tmux gone")
	if err := src.Dock("a"); err == nil {
		t.Error("a failed tmux is not reported")
	}
}

func TestListSourceKeepsTheFrameTitleCurrent(t *testing.T) {
	f := newFakes()
	w := agentWindow("@4", "a", "/w/app", f.now, false)
	w.Docked, w.Pane, w.Title = true, "%7", "a · feat/1 · claude-x"
	f.tmux.windows = []tmux.Window{{ID: "@0", Name: "hq", Options: map[string]string{}}, w, agentWindow("@5", "b", "/w/app", f.now, false)}
	f.states["id-a"] = state.Report{State: state.Working, Branch: "feat/1"}
	src := listSource(f.deps(), "%1")
	as, _ := src.Agents(nil)
	if len(f.tmux.titles) != 0 || !as[0].Docked || as[1].Docked {
		t.Fatalf("titles %v, agents %+v", f.tmux.titles, as)
	}
	f.states["id-a"] = state.Report{State: state.Working, Branch: "feat/2"}
	src.Agents(nil)
	if want := []string{"%7=a · feat/2 · claude-x"}; !reflect.DeepEqual(f.tmux.titles, want) {
		t.Fatalf("titles %v, want %v", f.tmux.titles, want)
	}
}
