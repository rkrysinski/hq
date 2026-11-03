package cli

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/proc"
	"github.com/rkrysinski/hq/internal/sbx"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
)

// sandboxFakes has app's sandbox claude-x with a running agent a and an
// ended agent b, and lib's sandbox claude-lib with a running agent c.
func sandboxFakes() *fakes {
	f := newFakes()
	f.sbx.sandboxes = []sbx.Sandbox{
		{Name: "claude-x", Status: "running", Workspaces: []string{"/w/app"}},
		{Name: "claude-lib", Status: "running", Workspaces: []string{"/w/lib"}},
	}
	c := agentWindow("@6", "c", "/w/lib", f.now.Add(-time.Hour), false)
	c.Options["sandbox"] = "claude-lib"
	f.tmux.windows = []tmux.Window{
		{ID: "@0", Name: "hq", Pane: "%0", Options: map[string]string{}},
		agentWindow("@4", "a", "/w/app", f.now.Add(-time.Hour), false),
		agentWindow("@5", "b", "/w/app", f.now.Add(-time.Hour), true),
		c,
	}
	for i := range f.tmux.windows[1:] {
		f.tmux.windows[i+1].Pane = "%" + f.tmux.windows[i+1].ID[1:]
	}
	f.tmux.sessions = 1
	f.tmux.next = 7
	return f
}

func TestSandboxRestartRelaunchesItsAgentsInTheirOwnPanes(t *testing.T) {
	f := sandboxFakes()
	f.tmux.windows[1].Docked = true
	// While sbx stops the sandbox (and still lists it running), its agents
	// already show ended; the other sandbox's agent does not. The rows stay
	// while the sandbox starts again.
	var during []string
	collect := func() {
		read := func(_, id string) (state.Report, bool) { return state.Report{State: state.Done, Since: f.now}, true }
		var s []string
		for _, a := range agent.Collect(f.tmux.windows, read, agent.Sandboxes{}) {
			s = append(s, a.Name+"="+a.State)
		}
		during = append(during, strings.Join(s, " "))
	}
	stop, exec := f.sbx.onStop, f.sbx.onExec
	f.sbx.onStop = func(sandbox string) { collect(); stop(sandbox); collect() }
	f.sbx.onExec = func(args []string) error { err := exec(args); collect(); return err }
	// a reported with its Claude session and resumes it; b's state file
	// cannot smuggle an option in, so b starts fresh.
	const session = "00000003-0000-4000-8000-000000000003"
	f.states["id-a"] = state.Report{State: state.Done, SessionID: session}
	f.states["id-b"] = state.Report{State: state.Done, SessionID: "--dangerously-x"}
	f.tmux.windows[2].Options["new"] = "1"
	code, out, errOut := f.run("sandbox", "restart", "app", "-y")
	if want := "a=ended b=ended c=done|a=ended b=ended c=done|a=ended b=ended c=done"; strings.Join(during, "|") != want {
		t.Fatalf("during the restart:\n%s\nwant\n%s", strings.Join(during, "\n"), strings.ReplaceAll(want, "|", "\n"))
	}
	if code != 0 || out != "restarted the sandbox claude-x; relaunched a, b\n" {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	if got := strings.Join(f.sbx.calls, "; "); got != "stop claude-x; exec claude-x true" {
		t.Fatalf("sbx calls %q", got)
	}
	// The same windows, so the same names, ids and the docked agent; the
	// agents keep their state files.
	if f.names() != "hq a b c" || !f.tmux.windows[1].Docked || len(f.removed) != 0 {
		t.Fatalf("windows %q docked %v removed %v", f.names(), f.tmux.windows[1].Docked, f.removed)
	}
	if got := strings.Join(f.tmux.respawns, " "); got != "%4 %5" {
		t.Fatalf("respawned %q", got)
	}
	for _, w := range f.tmux.windows[1:3] {
		if w.PaneDead || w.Options["id"] != "id-"+w.Name || w.Options["sandbox"] != "claude-x" || w.Options["ending"] != "" || w.Options["new"] != "" ||
			w.Options["started"] != agent.Stamp(f.now) || w.Options["inbox"] != "3" {
			t.Fatalf("relaunched window %+v", w)
		}
		argv := strings.Join(f.tmux.argv[w.ID], " ")
		if !strings.HasPrefix(argv, "sbx run --name claude-x -- --settings") || !strings.Contains(argv, " hq-settings:"+w.Name+":id-"+w.Name) {
			t.Fatalf("argv %q", argv)
		}
		rest := strings.Join(f.tmux.argv[w.ID][7:], " ")
		if w.Name == "a" && rest != "--resume "+session || w.Name == "b" && rest != "" {
			t.Fatalf("%s resumes with %q", w.Name, rest)
		}
	}
	if w := f.tmux.windows[3]; w.PaneDead || w.Options["started"] == agent.Stamp(f.now) {
		t.Fatal("an agent of another sandbox was touched")
	}
	// A prompt hq mcp started an agent with, lost with the session before
	// Claude took it, leaves no word behind: the next prompt is the user's.
	if got := strings.Join(f.withdrawn, " "); got != "id-a id-b" {
		t.Fatalf("withdrawn %q", got)
	}
}

func TestARelaunchedAgentKeepsItsBranchAndLastMessageUntilItReports(t *testing.T) {
	f := sandboxFakes()
	f.states["id-a"] = state.Report{State: state.Done, Since: f.now.Add(-time.Minute), Last: "PR #58 opened", Branch: "feat/42"}
	if code, _, errOut := f.run("sandbox", "restart", "app", "-y"); code != 0 {
		t.Fatalf("exit %d %q", code, errOut)
	}
	_, out, _ := f.run("ls")
	if !strings.Contains(out, "a     app   feat/42  starting  0s   PR #58 opened\n") {
		t.Fatalf("before its first report:\n%s", out)
	}
	f.now = f.now.Add(3 * time.Second)
	f.states["id-a"] = state.Report{State: state.Working, Since: f.now.Add(-time.Second), Branch: "feat/42"}
	if _, out, _ = f.run("ls"); !strings.Contains(out, "a     app   feat/42  working   1s   -\n") {
		t.Fatalf("after it reported:\n%s", out)
	}
}

func TestSandboxRestartAsksFirst(t *testing.T) {
	f := sandboxFakes()
	if code, out, _ := f.run("sandbox", "restart", "app"); code != 0 || len(f.sbx.calls) != 0 ||
		out != "Restart the sandbox claude-x? This ends and relaunches 2 agents (a, b). [y/N] " {
		t.Fatalf("declined: exit %d %q calls %v", code, out, f.sbx.calls)
	}
	f.stdin = "y\n"
	code, out, _ := f.run("sandbox", "restart", "lib")
	if code != 0 || out != "Restart the sandbox claude-lib? This ends and relaunches 1 agent (c). [y/N] restarted the sandbox claude-lib; relaunched c\n" {
		t.Fatalf("exit %d %q", code, out)
	}
	f.tty = false
	if code, _, errOut := f.run("sandbox", "restart", "app"); code != ExitUsage || errOut != "hq: no terminal to confirm on; add -y to skip the confirmation\n" || len(f.sbx.calls) != 2 {
		t.Fatalf("no terminal: exit %d %q calls %v", code, errOut, f.sbx.calls)
	}
	f = newFakes()
	f.sbx.sandboxes = []sbx.Sandbox{{Name: "claude-x", Status: "stopped", Workspaces: []string{"/w/app"}}}
	if _, out, _ := f.run("sandbox", "restart", "app"); out != "Restart the sandbox claude-x? [y/N] " {
		t.Fatalf("without agents: %q", out)
	}
}

func TestSandboxRestartWithoutAgentsStopsAndStartsIt(t *testing.T) {
	f := sandboxFakes()
	code, out, _ := f.run("sandbox", "restart", "/w/lib", "-y")
	if code != 0 || out != "restarted the sandbox claude-lib; relaunched c\n" {
		t.Fatalf("exit %d %q", code, out)
	}
	// c never reported: it starts fresh.
	if argv := f.tmux.argv[f.tmux.windows[len(f.tmux.windows)-1].ID]; len(argv) != 7 {
		t.Fatalf("argv %q", argv)
	}
	f = newFakes()
	f.sbx.sandboxes = []sbx.Sandbox{{Name: "claude-x", Status: "stopped", Workspaces: []string{"/w/app"}}}
	code, out, _ = f.run("sandbox", "restart", "app", "--yes")
	if code != 0 || out != "restarted the sandbox claude-x\n" || !f.sbx.sandboxes[0].Running() {
		t.Fatalf("exit %d %q %+v", code, out, f.sbx.sandboxes)
	}
}

func TestSandboxRestartSkipsAnAgentKilledMeanwhile(t *testing.T) {
	f := sandboxFakes()
	stop := f.sbx.onStop
	f.sbx.onStop = func(sandbox string) {
		stop(sandbox)
		_ = f.tmux.KillWindow("@5") // hq kill b, while sbx stops
	}
	code, out, _ := f.run("sandbox", "restart", "app", "-y")
	if code != 0 || out != "restarted the sandbox claude-x; relaunched a\n" || f.names() != "hq a c" {
		t.Fatalf("exit %d %q windows %q", code, out, f.names())
	}
}

func TestSandboxRestartFailures(t *testing.T) {
	f := sandboxFakes()
	f.tmux.respawnErr = errors.New("tmux: can't find pane: %5")
	if code, _, errOut := f.run("sandbox", "restart", "app", "-y"); code != ExitEnvironment || errOut != "hq: tmux: can't find pane: %5\n" {
		t.Fatalf("exit %d %q", code, errOut)
	}
}

func TestSandboxRmIsRefusedWhileItsAgentsRun(t *testing.T) {
	f := sandboxFakes()
	code, _, errOut := f.run("sandbox", "rm", "app", "-y")
	if code != ExitUsage || errOut != "hq: agents of claude-x are running (a); end them first with hq kill\n" || len(f.sbx.calls) != 0 {
		t.Fatalf("exit %d %q calls %v", code, errOut, f.sbx.calls)
	}
}

func TestSandboxRmAsksThenRemoves(t *testing.T) {
	f := sandboxFakes()
	f.tmux.windows = f.tmux.windows[:1] // no agents
	if code, _, _ := f.run("sandbox", "rm", "app"); code != 0 || len(f.sbx.calls) != 0 {
		t.Fatalf("declined: exit %d calls %v", code, f.sbx.calls)
	}
	f.stdin = "y\n"
	code, out, _ := f.run("sandbox", "rm", "/w/app/sub")
	if code != 0 || out != "Remove the sandbox claude-x and its state? [y/N] removed the sandbox claude-x\n" || len(f.sbx.sandboxes) != 1 {
		t.Fatalf("exit %d %q %+v", code, out, f.sbx.sandboxes)
	}
	f.tty = false
	if code, _, _ := f.run("sandbox", "rm", "lib"); code != ExitUsage {
		t.Fatalf("no terminal: exit %d", code)
	}
	if code, out, _ := f.run("sandbox", "rm", "lib", "-y"); code != 0 || out != "removed the sandbox claude-lib\n" {
		t.Fatalf("-y: exit %d %q", code, out)
	}
}

func TestSandboxTakesTheSandboxsName(t *testing.T) {
	f := sandboxFakes()
	// app names two repositories; each sandbox's name tells them apart.
	f.sbx.sandboxes = append(f.sbx.sandboxes, sbx.Sandbox{Name: "claude-app-2", Status: "running", Workspaces: []string{"/v/app"}})
	if code, out, errOut := f.run("sandbox", "restart", "claude-x", "-y"); code != 0 || out != "restarted the sandbox claude-x; relaunched a, b\n" {
		t.Fatalf("restart: exit %d %q %q", code, out, errOut)
	}
	f.tmux.windows = f.tmux.windows[:1]
	if code, out, errOut := f.run("sandbox", "rm", "claude-app-2", "-y"); code != 0 || out != "removed the sandbox claude-app-2\n" || len(f.sbx.sandboxes) != 2 {
		t.Fatalf("rm: exit %d %q %q %+v", code, out, errOut, f.sbx.sandboxes)
	}
}

// REPO is the repository's default sandbox: a setup's sandbox, listed
// first, is never taken for it; its own name still names it (#135).
func TestSandboxOfARepositoryIsItsDefault(t *testing.T) {
	for _, repo := range []string{"/w/app", "app", "claude-x"} {
		f := sandboxFakes()
		f.sbx.sandboxes = append([]sbx.Sandbox{{Name: "hq-local-app", Status: "running", Workspaces: []string{"/w/app"}}}, f.sbx.sandboxes...)
		if code, out, errOut := f.run("sandbox", "restart", repo, "-y"); code != 0 || out != "restarted the sandbox claude-x; relaunched a, b\n" {
			t.Errorf("%s: exit %d %q %q", repo, code, out, errOut)
		}
	}
	f := sandboxFakes()
	f.sbx.sandboxes = append([]sbx.Sandbox{{Name: "hq-local-app", Status: "running", Workspaces: []string{"/w/app"}}}, f.sbx.sandboxes...)
	if code, out, errOut := f.run("sandbox", "rm", "hq-local-app", "-y"); code != 0 || out != "removed the sandbox hq-local-app\n" {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
}

func TestDecliningAConfirmationChangesNothingAndSucceeds(t *testing.T) {
	for _, args := range [][]string{{"kill", "a"}, {"stop"}, {"sandbox", "restart", "app"}, {"sandbox", "rm", "lib"}} {
		f := sandboxFakes()
		f.tmux.windows[3].PaneDead = true // c ended, so rm lib is not refused
		f.stdin = "n\n"
		if code, _, errOut := f.run(args...); code != 0 || errOut != "" || len(f.sbx.calls) != 0 || len(f.sbx.sandboxes) != 2 || len(f.tmux.windows) != 4 {
			t.Errorf("%v declined: exit %d %q calls %v", args, code, errOut, f.sbx.calls)
		}
	}
}

func TestSandboxRmWithOnlyEndedAgents(t *testing.T) {
	f := sandboxFakes()
	f.tmux.windows = f.tmux.windows[:3]
	f.tmux.windows[1].PaneDead = true
	if code, _, errOut := f.run("sandbox", "rm", "app", "-y"); code != 0 {
		t.Fatalf("exit %d %q", code, errOut)
	}
}

func TestSandboxErrors(t *testing.T) {
	f := sandboxFakes()
	f.sbx.sandboxes = append(f.sbx.sandboxes, sbx.Sandbox{Name: "claude-app-2", Workspaces: []string{"/v/app"}})
	for _, tc := range []struct {
		args []string
		code int
		msg  string
	}{
		{[]string{"sandbox"}, ExitUsage, sandboxUsage},
		{[]string{"sandbox", "rm"}, ExitUsage, sandboxUsage},
		{[]string{"sandbox", "stop", "app"}, ExitUsage, sandboxUsage},
		{[]string{"sandbox", "rm", "app", "-f"}, ExitUsage, sandboxUsage},
		{[]string{"sandbox", "restart", "nope"}, ExitNotFound, "no sandbox for 'nope' (see hq ls)"},
		{[]string{"sandbox", "restart", "/w/plain"}, ExitNotFound, "/w/plain is not in a git repository"},
		{[]string{"sandbox", "restart", "app"}, ExitUsage, "'app' names 2 repositories; give the repository's directory or its sandbox's name"},
	} {
		if code, _, errOut := f.run(tc.args...); code != tc.code || errOut != "hq: "+tc.msg+"\n" {
			t.Errorf("%v: exit %d %q", tc.args, code, errOut)
		}
	}
	f = newFakes()
	if code, _, errOut := f.run("sandbox", "restart", "/w/lib"); code != ExitNotFound || errOut != "hq: no sandbox for lib (hq new creates it)\n" {
		t.Fatalf("exit %d %q", code, errOut)
	}
	f.sbx.err = sbxNotFound()
	if code, _, _ := f.run("sandbox", "restart", "app"); code != ExitEnvironment {
		t.Fatalf("sbx missing: exit %d", code)
	}
}

func TestARelaunchedAgentsEndCountsFromItsPanesDeathNotFromTheRestart(t *testing.T) {
	f := sandboxFakes()
	f.states["id-a"] = state.Report{State: state.Done, Since: f.now.Add(-time.Minute), Last: "hi"}
	// The list refreshes all through the restart, and dates the ends it
	// sees (#104).
	f.tmux.onSet = func(string, string) {
		read := func(_, id string) (state.Report, bool) { r, ok := f.states[id]; return r, ok }
		agent.Look(f.tmux.windows, read, nil, agent.Sandboxes{}, f.now, f.tmux)
	}
	if code, _, errOut := f.run("sandbox", "restart", "app", "-y"); code != 0 {
		t.Fatalf("exit %d %q", code, errOut)
	}
	f.tmux.onSet = nil
	restart := agent.Stamp(f.now)
	for _, w := range f.tmux.windows[1:3] {
		if strings.HasPrefix(w.Options["endseen"], restart+" ") {
			t.Fatalf("%s seen ended as its new session started: %q", w.Name, w.Options["endseen"])
		}
	}
	// b never reports again; a does. Both panes die as the sandbox stops.
	f.now = f.now.Add(time.Minute)
	f.states["id-a"] = state.Report{State: state.Working, Since: f.now, Last: "hi"}
	f.now = f.now.Add(time.Minute)
	for i := 1; i < 3; i++ {
		f.tmux.windows[i].PaneDead, f.tmux.windows[i].DeadAt = true, f.now
	}
	f.now = f.now.Add(5 * time.Second)
	_, out, _ := f.run("ls")
	if !strings.Contains(out, "a     app   -       ended     5s ") || !strings.Contains(out, "b     app   -       ended     5s ") {
		t.Fatalf("ended:\n%s", out)
	}
}

// localSandboxFakes is sandboxFakes with app's local sandbox, hq-local-app, listed
// first, and its running agent d.
func localSandboxFakes() *fakes {
	f := sandboxFakes()
	f.sbx.sandboxes = append([]sbx.Sandbox{{Name: "hq-local-app", Status: "running", Workspaces: []string{"/w/app"}}}, f.sbx.sandboxes...)
	d := agentWindow("@7", "d", "/w/app", f.now.Add(-time.Hour), false)
	d.Options["sandbox"] = "hq-local-app"
	d.Pane = "%7"
	f.tmux.windows = append(f.tmux.windows, d)
	f.tmux.next = 8
	return f
}

// --setup NAME is that setup's sandbox of the repository: its agents keep
// rm from it, the default sandbox's do not (#136).
func TestSandboxRmWithASetupRemovesThatSetupsSandboxOnly(t *testing.T) {
	for _, repo := range []string{"/w/app", "app"} {
		f := localSandboxFakes()
		code, _, errOut := f.run("sandbox", "rm", repo, "--setup", "local", "-y")
		if code != ExitUsage || errOut != "hq: agents of hq-local-app are running (d); end them first with hq kill\n" || len(f.sbx.calls) != 0 {
			t.Fatalf("%s with d running: exit %d %q calls %v", repo, code, errOut, f.sbx.calls)
		}
		f.tmux.windows[4].PaneDead = true // d ended; a still runs in claude-x
		f.stdin = "y\n"
		code, out, errOut := f.run("sandbox", "rm", repo, "--setup", "local")
		if code != 0 || out != "Remove the sandbox hq-local-app and its state? [y/N] removed the sandbox hq-local-app\n" {
			t.Fatalf("%s: exit %d %q %q", repo, code, out, errOut)
		}
		if got := strings.Join(f.sbx.calls, "; "); got != "rm hq-local-app" {
			t.Fatalf("%s: sbx calls %q", repo, got)
		}
	}
}

// A setup the repository has no sandbox of is not found, whether or not the
// preferences name it; --setup takes a repository, not a sandbox's name
// (#136).
func TestSandboxWithASetupItHasNoSandboxOf(t *testing.T) {
	f := localSandboxFakes()
	for _, tc := range []struct {
		args []string
		code int
		msg  string
	}{
		{[]string{"sandbox", "rm", "/w/app", "--setup", "big"}, ExitNotFound, "no sandbox of setup 'big' for app (hq new --setup big creates it)"},
		{[]string{"sandbox", "restart", "app", "--setup=big", "-y"}, ExitNotFound, "no sandbox of setup 'big' for app (hq new --setup big creates it)"},
		{[]string{"sandbox", "restart", "lib", "--setup", "local"}, ExitNotFound, "no sandbox of setup 'local' for lib (hq new --setup local creates it)"},
		{[]string{"sandbox", "restart", "nope", "--setup", "local"}, ExitNotFound, "no sandbox for 'nope' (see hq ls)"},
		{[]string{"sandbox", "rm", "claude-x", "--setup", "local"}, ExitUsage, "--setup takes a repository, and claude-x is a sandbox's name"},
		{[]string{"sandbox", "rm", "hq-local-app", "--setup", "local"}, ExitUsage, "--setup takes a repository, and hq-local-app is a sandbox's name"},
		{[]string{"sandbox", "rm", "app", "--setup"}, ExitUsage, "--setup needs a setup's name (" + sandboxUsage + ")"},
	} {
		if code, _, errOut := f.run(tc.args...); code != tc.code || errOut != "hq: "+tc.msg+"\n" {
			t.Errorf("%v: exit %d %q", tc.args, code, errOut)
		}
	}
	if len(f.sbx.calls) != 0 {
		t.Fatalf("sbx calls %v", f.sbx.calls)
	}
	// --setup default is the default sandbox.
	if code, out, errOut := f.run("sandbox", "restart", "app", "--setup", "default", "-y"); code != 0 || out != "restarted the sandbox claude-x; relaunched a, b\n" {
		t.Fatalf("default: exit %d %q %q", code, out, errOut)
	}
}

// --all-setups is every sandbox of the repository: one confirmation lists
// them, each is stopped and started, and every agent of the repository is
// relaunched in its own pane, continuing its conversation; another
// repository's agent is left alone (#136).
func TestSandboxRestartAllSetupsRestartsEverySandboxOfTheRepository(t *testing.T) {
	for _, repo := range []string{"/w/app", "app"} {
		f := localSandboxFakes()
		const session = "00000004-0000-4000-8000-000000000004"
		f.states["id-d"] = state.Report{State: state.Done, SessionID: session}
		code, out, _ := f.run("sandbox", "restart", repo, "--all-setups")
		if code != 0 || len(f.sbx.calls) != 0 || out != "Restart the sandboxes claude-x, hq-local-app? This ends and relaunches 3 agents (a, b, d). [y/N] " {
			t.Fatalf("%s declined: exit %d %q calls %v", repo, code, out, f.sbx.calls)
		}
		f.stdin = "y\n"
		code, out, errOut := f.run("sandbox", "restart", repo, "--all-setups")
		if code != 0 || !strings.HasSuffix(out, "[y/N] restarted the sandboxes claude-x, hq-local-app; relaunched a, b, d\n") {
			t.Fatalf("%s: exit %d %q %q", repo, code, out, errOut)
		}
		if got := strings.Join(f.sbx.calls, "; "); got != "stop claude-x; stop hq-local-app; exec claude-x true; exec hq-local-app true" {
			t.Fatalf("%s: sbx calls %q", repo, got)
		}
		if got := strings.Join(f.tmux.respawns, " "); got != "%4 %5 %7" || f.names() != "hq a b c d" {
			t.Fatalf("%s: respawned %q windows %q", repo, got, f.names())
		}
		d := f.tmux.windows[4]
		if argv := strings.Join(f.tmux.argv[d.ID], " "); !strings.HasPrefix(argv, "sbx run --name hq-local-app -- --settings") || !strings.HasSuffix(argv, " --resume "+session) {
			t.Fatalf("%s: d's argv %q", repo, argv)
		}
		if d.Options["sandbox"] != "hq-local-app" || d.Options["ending"] != "" || d.Options["started"] != agent.Stamp(f.now) {
			t.Fatalf("%s: d %+v", repo, d)
		}
		if w := f.tmux.windows[3]; w.Options["started"] == agent.Stamp(f.now) {
			t.Fatalf("%s: another repository's agent was touched", repo)
		}
	}
	// A repository with one sandbox asks as for that sandbox.
	f := sandboxFakes()
	if code, out, _ := f.run("sandbox", "restart", "/w/lib", "--all-setups"); code != 0 || out != "Restart the sandbox claude-lib? This ends and relaunches 1 agent (c). [y/N] " {
		t.Fatalf("one sandbox: exit %d %q", code, out)
	}
}

// rm --all-setups is refused while any agent of the repository runs, in
// whichever of its sandboxes; then it removes them all (#136).
func TestSandboxRmAllSetupsRemovesEverySandboxOfTheRepository(t *testing.T) {
	f := localSandboxFakes()
	f.tmux.windows[1].PaneDead = true // a ended: only d runs
	code, _, errOut := f.run("sandbox", "rm", "app", "--all-setups", "-y")
	if code != ExitUsage || errOut != "hq: agents of hq-local-app are running (d); end them first with hq kill\n" || len(f.sbx.calls) != 0 {
		t.Fatalf("d running: exit %d %q calls %v", code, errOut, f.sbx.calls)
	}
	f.tmux.windows[1].PaneDead = false
	if code, _, errOut := f.run("sandbox", "rm", "app", "--all-setups", "-y"); code != ExitUsage || errOut != "hq: agents of claude-x, hq-local-app are running (a, d); end them first with hq kill\n" {
		t.Fatalf("a and d running: exit %d %q", code, errOut)
	}
	f.tmux.windows[1].PaneDead, f.tmux.windows[4].PaneDead = true, true
	f.stdin = "y\n"
	code, out, errOut := f.run("sandbox", "rm", "/w/app", "--all-setups")
	if code != 0 || out != "Remove the sandboxes claude-x, hq-local-app and their state? [y/N] removed the sandboxes claude-x, hq-local-app\n" {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	if got := strings.Join(f.sbx.calls, "; "); got != "rm claude-x; rm hq-local-app" || len(f.sbx.sandboxes) != 1 {
		t.Fatalf("sbx calls %q, left %+v", got, f.sbx.sandboxes)
	}
	if code, _, errOut := f.run("sandbox", "rm", "/w/app", "--all-setups", "-y"); code != ExitNotFound || errOut != "hq: no sandbox for app (hq new creates it)\n" {
		t.Fatalf("again: exit %d %q", code, errOut)
	}
}

// When sbx fails to remove one of the sandboxes, rm --all-setups still
// removes the others and says which it removed and which not, with sbx's
// error (#134).
func TestSandboxRmAllSetupsSaysWhatItRemovedWhenOneFails(t *testing.T) {
	f := localSandboxFakes()
	f.tmux.windows[1].PaneDead, f.tmux.windows[4].PaneDead = true, true
	f.sbx.removeErr = map[string]error{"claude-x": &proc.Error{Name: "sbx", Msg: "error: sandbox is busy"}}
	code, out, errOut := f.run("sandbox", "rm", "app", "--all-setups", "-y")
	if code != ExitEnvironment || out != "removed the sandbox hq-local-app\n" || errOut != "hq: could not remove claude-x: sbx: error: sandbox is busy\n" {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	if got := strings.Join(f.sbx.calls, "; "); got != "rm claude-x; rm hq-local-app" {
		t.Fatalf("sbx calls %q", got)
	}
	// Neither removed: both named, nothing said removed.
	f = localSandboxFakes()
	f.tmux.windows[1].PaneDead, f.tmux.windows[4].PaneDead = true, true
	f.sbx.removeErr = map[string]error{"claude-x": &proc.Error{Name: "sbx", Msg: "error: a"}, "hq-local-app": &proc.Error{Name: "sbx", Msg: "error: b"}}
	if code, out, errOut := f.run("sandbox", "rm", "app", "--all-setups", "-y"); code != ExitEnvironment || out != "" || errOut != "hq: could not remove claude-x: sbx: error: a; hq-local-app: sbx: error: b\n" {
		t.Fatalf("both: exit %d %q %q", code, out, errOut)
	}
	// Without sbx none can be removed: said once.
	f = localSandboxFakes()
	f.tmux.windows[1].PaneDead, f.tmux.windows[4].PaneDead = true, true
	f.sbx.removeErr = map[string]error{"claude-x": sbxNotFound()}
	if code, out, errOut := f.run("sandbox", "rm", "app", "--all-setups", "-y"); code != ExitEnvironment || out != "" || errOut != "hq: sbx not found; install Docker Sandboxes\n" || len(f.sbx.calls) != 1 {
		t.Fatalf("no sbx: exit %d %q %q calls %v", code, out, errOut, f.sbx.calls)
	}
	// One sandbox fails as before: sbx's error alone.
	f = localSandboxFakes()
	f.tmux.windows[4].PaneDead = true
	f.sbx.removeErr = map[string]error{"hq-local-app": &proc.Error{Name: "sbx", Msg: "error: b"}}
	if code, out, errOut := f.run("sandbox", "rm", "app", "--setup", "local", "-y"); code != ExitEnvironment || out != "" || errOut != "hq: sbx: error: b\n" {
		t.Fatalf("one: exit %d %q %q", code, out, errOut)
	}
}

// --setup and --all-setups exclude each other, and neither takes a
// sandbox's name, which means that one sandbox (#136).
func TestSandboxSetupFlagsUsage(t *testing.T) {
	f := localSandboxFakes()
	for _, tc := range []struct {
		args []string
		msg  string
	}{
		{[]string{"sandbox", "rm", "app", "--setup", "local", "--all-setups"}, "--setup and --all-setups exclude each other (" + sandboxUsage + ")"},
		{[]string{"sandbox", "restart", "app", "--all-setups", "--setup=local"}, "--setup and --all-setups exclude each other (" + sandboxUsage + ")"},
		{[]string{"sandbox", "restart", "claude-x", "--all-setups"}, "--all-setups takes a repository, and claude-x is a sandbox's name"},
	} {
		if code, _, errOut := f.run(tc.args...); code != ExitUsage || errOut != "hq: "+tc.msg+"\n" {
			t.Errorf("%v: exit %d %q", tc.args, code, errOut)
		}
	}
	if len(f.sbx.calls) != 0 {
		t.Fatalf("sbx calls %v", f.sbx.calls)
	}
}

// A sandbox hq mcp load loaded hq into gets it again once restarted, before
// its agents are relaunched; one it did not, does not.
func TestSandboxRestartLoadsHqAgainWhereItWasLoaded(t *testing.T) {
	f := sandboxFakes()
	f.prefs.MCPLoaded = []string{"claude-x"}
	code, out, errOut := f.run("sandbox", "restart", "app", "-y")
	if code != 0 || out != "restarted the sandbox claude-x; loaded hq into it again; relaunched a, b\n" {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	if i, j := slices.Index(f.sbx.calls, "exec claude-x true"), slices.Index(f.sbx.calls, "mcp load hq claude-x"); i < 0 || j < i {
		t.Fatalf("calls %q", f.sbx.calls)
	}
	f.sbx.loads = nil
	if code, out, _ := f.run("sandbox", "restart", "lib", "-y"); code != 0 || out != "restarted the sandbox claude-lib; relaunched c\n" || len(f.sbx.loads) != 0 {
		t.Fatalf("lib: exit %d %q loads %q", code, out, f.sbx.loads)
	}
	// A load that fails leaves the agents relaunched, and says so.
	f.sbx.loadErr = map[string]error{"claude-x": errors.New("gateway down")}
	code, out, errOut = f.run("sandbox", "restart", "app", "-y")
	if code != ExitEnvironment || out != "restarted the sandbox claude-x; relaunched a, b\n" ||
		errOut != "hq: could not load hq into claude-x again (gateway down); run hq mcp load claude-x\n" {
		t.Fatalf("failed load: exit %d %q %q", code, out, errOut)
	}
}

// A removed sandbox leaves the record: a new one of its name is not loaded.
func TestSandboxRmForgetsHqWasLoaded(t *testing.T) {
	f := sandboxFakes()
	f.tmux.windows = f.tmux.windows[:1]
	f.prefs.MCPLoaded = []string{"claude-lib", "claude-x"}
	if code, _, errOut := f.run("sandbox", "rm", "app", "-y"); code != 0 || !slices.Equal(f.prefs.MCPLoaded, []string{"claude-lib"}) {
		t.Fatalf("exit %d %q recorded %q", code, errOut, f.prefs.MCPLoaded)
	}
}
