package cli

import (
	"strings"
	"testing"

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
	c := agentWindow("@6", "c", "/w/lib", f.now, false)
	c.Options["sandbox"] = "claude-lib"
	f.tmux.windows = []tmux.Window{
		{ID: "@0", Name: "hq", Options: map[string]string{}},
		agentWindow("@4", "a", "/w/app", f.now, false),
		agentWindow("@5", "b", "/w/app", f.now, true),
		c,
	}
	f.tmux.sessions = 1
	f.tmux.next = 7
	return f
}

func TestSandboxRestartRelaunchesItsAgentsUnderTheSameNames(t *testing.T) {
	f := sandboxFakes()
	// While sbx stops the sandbox (and still lists it running), its agents
	// already show ended; the other sandbox's agent does not.
	var during []string
	stop := f.sbx.onStop
	f.sbx.onStop = func(sandbox string) {
		read := func(_, id string) (state.Report, bool) { return state.Report{State: state.Done}, true }
		for _, a := range agent.Collect(f.tmux.windows, read, nil) {
			during = append(during, a.Name+"="+a.State)
		}
		stop(sandbox)
	}
	// a reported with its Claude session and resumes it; b's state file
	// cannot smuggle an option in, so b starts fresh.
	const session = "06d5c99c-1079-47e5-ad5f-d92403ccb28d"
	f.states["id-a"] = state.Report{State: state.Done, SessionID: session}
	f.states["id-b"] = state.Report{State: state.Done, SessionID: "--dangerously-x"}
	code, out, errOut := f.run("sandbox", "restart", "app")
	if strings.Join(during, " ") != "a=ended b=ended c=done" {
		t.Fatalf("during the stop: %v", during)
	}
	if code != 0 || out != "restarted the sandbox claude-x; relaunched a, b\n" {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	if got := strings.Join(f.sbx.calls, "; "); got != "stop claude-x; exec claude-x true" {
		t.Fatalf("sbx calls %q", got)
	}
	if f.names() != "hq c a b" {
		t.Fatalf("windows %q", f.names())
	}
	for _, w := range f.tmux.windows[2:] {
		if w.PaneDead || !f.tmux.started[w.ID] || w.Options["id"] == "id-"+w.Name || w.Options["sandbox"] != "claude-x" || w.Options["ending"] != "" {
			t.Fatalf("relaunched window %+v", w)
		}
		argv := f.tmux.argv[w.ID]
		if !strings.HasPrefix(strings.Join(argv, " "), "sbx run --name claude-x -- --settings") {
			t.Fatalf("argv %q", argv)
		}
		rest := strings.Join(argv[7:], " ")
		if w.Name == "a" && rest != "--resume "+session || w.Name == "b" && rest != "" {
			t.Fatalf("%s resumes with %q", w.Name, rest)
		}
	}
	if f.tmux.windows[1].PaneDead {
		t.Fatal("an agent of another sandbox was touched")
	}
}

func TestSandboxRestartWithoutAgentsStopsAndStartsIt(t *testing.T) {
	f := sandboxFakes()
	code, out, _ := f.run("sandbox", "restart", "/w/lib")
	if code != 0 || out != "restarted the sandbox claude-lib; relaunched c\n" {
		t.Fatalf("exit %d %q", code, out)
	}
	// c never reported: it starts fresh.
	if argv := f.tmux.argv[f.tmux.windows[len(f.tmux.windows)-1].ID]; len(argv) != 7 {
		t.Fatalf("argv %q", argv)
	}
	f = newFakes()
	f.sbx.sandboxes = []sbx.Sandbox{{Name: "claude-x", Status: "stopped", Workspaces: []string{"/w/app"}}}
	code, out, _ = f.run("sandbox", "restart", "app")
	if code != 0 || out != "restarted the sandbox claude-x\n" || !f.sbx.sandboxes[0].Running() {
		t.Fatalf("exit %d %q %+v", code, out, f.sbx.sandboxes)
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
