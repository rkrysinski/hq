package cli

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/tmux"
)

func killFakes() *fakes {
	f := newFakes()
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

func TestKillDefaultsToNo(t *testing.T) {
	for _, typed := range []string{"\n", "n\n", "", "later\n"} {
		f := killFakes()
		f.stdin = typed
		if code, _, _ := f.run("kill", "a"); code != 0 || f.names() != "hq a b gone" || len(f.sbx.execs) != 0 {
			t.Fatalf("%q: exit %d, windows %q, execs %v", typed, code, f.names(), f.sbx.execs)
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
	if code, _, _ := f.run("kill", "gone", "-y"); code != 0 || len(f.sbx.execs) != 0 || f.names() != "hq a b" {
		t.Fatalf("exit %d execs %v windows %q", code, f.sbx.execs, f.names())
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
	f.stdin = "y\n"
	code, out, _ := f.run("stop")
	if code != 0 || out != "End all 3 agents? [y/N] ended 3 agents; sandboxes stay\n" {
		t.Fatalf("exit %d %q", code, out)
	}
	if len(f.sbx.execs) != 2 || f.names() != "hq" {
		t.Fatalf("execs %v windows %q", f.sbx.execs, f.names())
	}
}

func TestStopDeclinedOrWithYes(t *testing.T) {
	f := killFakes()
	if code, _, _ := f.run("stop"); code != 0 || f.names() != "hq a b gone" {
		t.Fatalf("declined: exit %d windows %q", code, f.names())
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
