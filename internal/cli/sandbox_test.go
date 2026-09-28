package cli

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/agent"
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
		for _, a := range agent.Collect(f.tmux.windows, read, nil) {
			s = append(s, a.Name+"="+a.State)
		}
		during = append(during, strings.Join(s, " "))
	}
	stop, exec := f.sbx.onStop, f.sbx.onExec
	f.sbx.onStop = func(sandbox string) { collect(); stop(sandbox); collect() }
	f.sbx.onExec = func(args []string) error { err := exec(args); collect(); return err }
	// a reported with its Claude session and resumes it; b's state file
	// cannot smuggle an option in, so b starts fresh.
	const session = "06d5c99c-1079-47e5-ad5f-d92403ccb28d"
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
			w.Options["started"] != agent.Stamp(f.now) {
			t.Fatalf("relaunched window %+v", w)
		}
		argv := strings.Join(f.tmux.argv[w.ID], " ")
		if !strings.HasPrefix(argv, "sbx run --name claude-x -- --settings") || !strings.Contains(argv, `HQ_ID":"id-`+w.Name+`"`) {
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
		{[]string{"sandbox", "restart", "app"}, ExitUsage, "'app' names 2 repositories; give the repository's directory"},
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
