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

func TestDashInTheListPaneRunsTheListThereAndLeavesTheHint(t *testing.T) {
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
	if n := len(f.tmux.resized); n == 0 || f.tmux.resized[n-1] != quitHeight {
		t.Errorf("list pane heights %v, want the last %d", f.tmux.resized, quitHeight)
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

func TestListSourceFitsTheListToTheWindow(t *testing.T) {
	f := newFakes()
	src := listSource(f.deps(), "%1")
	for _, tc := range []struct{ window, want int }{{40, 10}, {24, 10}, {23, 7}} {
		f.tmux.height = tc.window
		src.Layout()
		if got := f.tmux.resized[len(f.tmux.resized)-1]; got != tc.want {
			t.Errorf("window %d: list %d lines, want %d", tc.window, got, tc.want)
		}
	}
}

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
	if f.releases.lookups != 1 {
		t.Errorf("%d lookups within a day, want 1", f.releases.lookups)
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
	if err := src.Dock("gone"); err == nil {
		t.Error("docked an agent that is not there")
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
