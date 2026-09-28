package cli

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/sbx"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
)

// killFakes has three agents in the running sandbox claude-x: a and b, and
// gone, whose pane died.
func killFakes() *fakes {
	f := newFakes()
	f.sbx.sandboxes = []sbx.Sandbox{{Name: "claude-x", Status: "running"}}
	f.tmux.windows = []tmux.Window{
		{ID: "@0", Name: "hq", Options: map[string]string{}},
		agentWindow("@4", "a", "/w/app", f.now, false),
		agentWindow("@5", "b", "/w/lib", f.now, false),
		agentWindow("@6", "gone", "/w/lib", f.now, true),
	}
	return f
}

func (f *fakes) names() string {
	var ns []string
	for _, w := range f.tmux.windows {
		ns = append(ns, w.Name)
	}
	return strings.Join(ns, " ")
}

func TestKillAsksAndEndsTheSessionInItsSandbox(t *testing.T) {
	f := killFakes()
	f.stdin = "y\n"
	code, out, errOut := f.run("kill", "a")
	if code != 0 || out != "Kill a (app)? [y/N] killed a; the sandbox stays\n" {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	if got := strings.Join(f.sbx.execs[0], " "); got != `claude-x pkill -TERM -f HQ_ID":"id-a"` {
		t.Fatalf("exec %q", got)
	}
	if f.names() != "hq b gone" {
		t.Fatalf("windows %q", f.names())
	}
}

func TestKillRemovesThatAgentsStateFilesOnly(t *testing.T) {
	f := killFakes()
	for _, id := range []string{"id-a", "id-b", "id-gone"} {
		f.states[id] = state.Report{State: state.Done}
	}
	if code, _, _ := f.run("kill", "a", "-y"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got := strings.Join(f.removed, ", "); got != "/w/app id-a" {
		t.Fatalf("removed %q", got)
	}
	// The others keep reporting.
	if _, ok := f.states["id-b"]; !ok || len(f.states) != 2 {
		t.Fatalf("states %v", f.states)
	}
	// A session that outlives the wait loses its files with its window.
	f.sbx.onExec = nil
	if code, _, _ := f.run("kill", "b", "-y"); code != 0 || strings.Join(f.removed, ", ") != "/w/app id-a, /w/lib id-b" {
		t.Fatalf("exit %d removed %v", code, f.removed)
	}
}

func TestKillDefaultsToNo(t *testing.T) {
	for _, typed := range []string{"\n", "n\n", "", "later\n"} {
		f := killFakes()
		f.stdin = typed
		if code, _, _ := f.run("kill", "a"); code != 0 || f.names() != "hq a b gone" || len(f.sbx.execs) != 0 || len(f.removed) != 0 {
			t.Fatalf("%q: exit %d, windows %q, execs %v, removed %v", typed, code, f.names(), f.sbx.execs, f.removed)
		}
	}
}

func TestKillWithYesDoesNotAsk(t *testing.T) {
	f := killFakes()
	f.tty = false
	code, out, _ := f.run("kill", "-y", "a")
	if code != 0 || strings.Contains(out, "[y/N]") || f.names() != "hq b gone" {
		t.Fatalf("exit %d %q windows %q", code, out, f.names())
	}
}

func TestKillWithoutTerminalIsRefused(t *testing.T) {
	f := killFakes()
	f.tty = false
	code, _, errOut := f.run("kill", "a")
	if code != ExitUsage || errOut != "hq: no terminal to confirm on; add -y to skip the confirmation\n" || f.names() != "hq a b gone" {
		t.Fatalf("exit %d %q windows %q", code, errOut, f.names())
	}
}

func TestKillEndedAgentRemovesItsRowOnly(t *testing.T) {
	f := killFakes()
	// Its session ended with its pane: pkill finds nothing to end.
	if code, _, _ := f.run("kill", "gone", "-y"); code != 0 || f.names() != "hq a b" || strings.Join(f.removed, ", ") != "/w/lib id-gone" {
		t.Fatalf("exit %d windows %q removed %v", code, f.names(), f.removed)
	}
	if got := f.execs(); got != `claude-x pkill -TERM -f HQ_ID":"id-gone"` {
		t.Fatalf("execs %q", got)
	}
}

func (f *fakes) execs() string {
	var es []string
	for _, e := range f.sbx.execs {
		es = append(es, strings.Join(e, " "))
	}
	return strings.Join(es, "; ")
}

func TestKillEndsTheSessionOfAPaneThatDied(t *testing.T) {
	f := killFakes()
	// gone's sbx run ended, its Claude did not (#101). On SIGTERM it takes
	// two looks to exit and writes its state file on the way out.
	f.sbx.orphans = map[string]bool{"id-gone": true}
	exiting, looks := false, 0
	fake := f.sbx.onExec
	f.sbx.onExec = func(args []string) error {
		switch {
		case args[0] == "pkill" && f.sbx.orphans["id-gone"]:
			exiting = true
			return nil
		case args[0] == "pgrep" && exiting:
			if looks++; looks < 2 {
				return nil
			}
			f.states["id-gone"] = state.Report{State: state.Ended}
			delete(f.sbx.orphans, "id-gone")
		}
		return fake(args)
	}
	start := f.now
	if code, out, _ := f.run("kill", "gone", "-y"); code != 0 || out != "killed gone; the sandbox stays\n" || f.names() != "hq a b" {
		t.Fatalf("exit %d %q windows %q", code, out, f.names())
	}
	want := `claude-x pkill -TERM -f HQ_ID":"id-gone"; claude-x pgrep -f HQ_ID":"id-gone"; claude-x pgrep -f HQ_ID":"id-gone"`
	if got := f.execs(); got != want {
		t.Fatalf("execs\n%s\nwant\n%s", got, want)
	}
	// Its files go after its last write, so none is left behind.
	if _, ok := f.states["id-gone"]; ok || strings.Join(f.removed, ", ") != "/w/lib id-gone" {
		t.Fatalf("states %v removed %v", f.states, f.removed)
	}
	if waited := f.now.Sub(start); waited >= time.Second {
		t.Fatalf("waited %v", waited)
	}
}

func TestKillGivesUpOnAnOrphanThatDoesNotEnd(t *testing.T) {
	f := killFakes()
	f.sbx.orphans = map[string]bool{"id-gone": true}
	f.sbx.onExec = nil // Claude ignores SIGTERM: pgrep always finds it
	start := f.now
	if code, _, _ := f.run("kill", "gone", "-y"); code != 0 || f.names() != "hq a b" || strings.Join(f.removed, ", ") != "/w/lib id-gone" {
		t.Fatalf("exit %d windows %q removed %v", code, f.names(), f.removed)
	}
	if waited := f.now.Sub(start); waited < endWait || waited > endWait+time.Second {
		t.Fatalf("waited %v", waited)
	}
}

func TestKillLeavesAStoppedSandboxAlone(t *testing.T) {
	// Nothing runs in a stopped or removed sandbox, and sbx exec would
	// start it again: the ended agent's row just goes.
	for _, sandboxes := range [][]sbx.Sandbox{{{Name: "claude-x", Status: "stopped"}}, nil} {
		f := killFakes()
		f.sbx.sandboxes = sandboxes
		if code, _, _ := f.run("kill", "gone", "-y"); code != 0 || f.execs() != "" || f.names() != "hq a b" || strings.Join(f.removed, ", ") != "/w/lib id-gone" {
			t.Fatalf("%v: exit %d execs %q windows %q removed %v", sandboxes, code, f.execs(), f.names(), f.removed)
		}
		if len(sandboxes) > 0 && f.sbx.sandboxes[0].Status != "stopped" {
			t.Fatalf("the sandbox was started: %v", f.sbx.sandboxes)
		}
	}
	// When sbx cannot tell, the session may run: hq ends it.
	f := killFakes()
	f.sbx.err = errors.New("sbx: daemon not responding")
	f.sbx.orphans = map[string]bool{"id-gone": true}
	if code, _, _ := f.run("kill", "gone", "-y"); code != 0 || len(f.sbx.orphans) != 0 || f.names() != "hq a b" {
		t.Fatalf("sbx ls failed: exit %d orphans %v windows %q", code, f.sbx.orphans, f.names())
	}
}

func TestKillRemovesTheWindowWhenTheSessionDoesNotEnd(t *testing.T) {
	f := killFakes()
	f.sbx.onExec = nil // Claude ignores SIGTERM
	start := f.now
	if code, _, _ := f.run("kill", "a", "-y"); code != 0 || f.names() != "hq b gone" {
		t.Fatalf("exit %d windows %q", code, f.names())
	}
	if waited := f.now.Sub(start); waited < endWait || waited > endWait+time.Second {
		t.Fatalf("waited %v", waited)
	}
}

func TestKillErrors(t *testing.T) {
	f := killFakes()
	for _, tc := range []struct {
		args []string
		code int
		msg  string
	}{
		{[]string{"kill"}, ExitUsage, "usage: hq kill NAME [-y]"},
		{[]string{"kill", "a", "b"}, ExitUsage, "usage: hq kill NAME [-y]"},
		{[]string{"kill", "a", "-f"}, ExitUsage, "usage: hq kill NAME [-y]"},
		{[]string{"kill", "nope"}, ExitNotFound, "no agent 'nope' (see hq ls)"},
		{[]string{"stop", "a"}, ExitUsage, "usage: hq stop [-y]"},
	} {
		if code, _, errOut := f.run(tc.args...); code != tc.code || errOut != "hq: "+tc.msg+"\n" {
			t.Errorf("%v: exit %d %q", tc.args, code, errOut)
		}
	}
	f.tmux.missing = true
	if code, _, _ := f.run("stop"); code != ExitEnvironment {
		t.Fatalf("tmux missing: exit %d", code)
	}
}

func TestStopAsksOnceWithTheCountAndEndsAll(t *testing.T) {
	f := killFakes()
	f.sbx.orphans = map[string]bool{"id-gone": true}
	f.stdin = "y\n"
	code, out, _ := f.run("stop")
	if code != 0 || out != "End all 3 agents? [y/N] ended 3 agents; sandboxes stay\n" {
		t.Fatalf("exit %d %q", code, out)
	}
	// Every session is ended, gone's too, which runs on without its pane.
	if len(f.sbx.orphans) != 0 || f.names() != "hq" {
		t.Fatalf("orphans %v windows %q", f.sbx.orphans, f.names())
	}
	if got := strings.Join(f.removed, ", "); got != "/w/app id-a, /w/lib id-b, /w/lib id-gone" {
		t.Fatalf("removed %q", got)
	}
}

func TestStopDeclinedOrWithYes(t *testing.T) {
	f := killFakes()
	if code, _, _ := f.run("stop"); code != 0 || f.names() != "hq a b gone" || len(f.removed) != 0 {
		t.Fatalf("declined: exit %d windows %q removed %v", code, f.names(), f.removed)
	}
	f.tty = false
	if code, out, _ := f.run("stop", "-y"); code != 0 || out != "ended 3 agents; sandboxes stay\n" || f.names() != "hq" {
		t.Fatalf("-y: exit %d %q windows %q", code, out, f.names())
	}
}

func TestStopWithoutAgents(t *testing.T) {
	f := newFakes()
	if code, out, _ := f.run("stop"); code != 0 || out != "no agents\n" {
		t.Fatalf("exit %d %q", code, out)
	}
}

func TestKillOneOfOneAndTmuxFailures(t *testing.T) {
	f := newFakes()
	f.tmux.windows = []tmux.Window{agentWindow("@4", "a", "/w/app", f.now, false)}
	if code, out, _ := f.run("stop", "-y"); code != 0 || out != "ended 1 agent; sandboxes stay\n" {
		t.Fatalf("exit %d %q", code, out)
	}
	f.tmux.windowsErr = errors.New("server exited")
	if code, _, errOut := f.run("kill", "a", "-y"); code == 0 || !strings.Contains(errOut, "server exited") {
		t.Fatalf("exit %d %q", code, errOut)
	}
}

func TestIsTerminal(t *testing.T) {
	if isTerminal(strings.NewReader("y\n")) {
		t.Fatal("a string is not a terminal")
	}
	file, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if isTerminal(file) {
		t.Fatal("a regular file is not a terminal")
	}
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	if isTerminal(null) {
		t.Fatal("/dev/null is not a terminal")
	}
}
