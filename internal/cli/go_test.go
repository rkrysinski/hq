package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/rkrysinski/hq/internal/state"

	"github.com/rkrysinski/hq/internal/tmux"
)

func TestGoMode(t *testing.T) {
	for _, tc := range []struct{ tmuxEnv, socket, want string }{
		{"", "/tmp/tmux-501/default", goAttach},
		{"/tmp/tmux-501/default,123,0", "/tmp/tmux-501/default", goSwitch},
		{"/tmp/tmux-501/other,123,0", "/tmp/tmux-501/default", goRefused},
	} {
		if got := goMode(tc.tmuxEnv, tc.socket); got != tc.want {
			t.Errorf("goMode(%q, %q) = %q, want %q", tc.tmuxEnv, tc.socket, got, tc.want)
		}
	}
}

func goFakes() *fakes {
	f := newFakes()
	f.tmux.windows = []tmux.Window{{ID: "@0", Name: "hq", Options: map[string]string{}}, agentWindow("@4", "a", "/w/app", f.now, false)}
	return f
}

func TestGoFromPlainTerminalAttaches(t *testing.T) {
	f := goFakes()
	if code, _, errOut := f.run("go", "a"); code != 0 || f.tmux.docked != "@4" || f.tmux.attached != "@0" || f.tmux.entered != "" {
		t.Fatalf("exit %d %q docked %q attached %q entered %q", code, errOut, f.tmux.docked, f.tmux.attached, f.tmux.entered)
	}
	if f.tmux.dockTitle != "a · claude-x" {
		t.Errorf("frame %q", f.tmux.dockTitle)
	}
}

func TestGoInsideHqServerSwitches(t *testing.T) {
	f := goFakes()
	f.env["TMUX"] = f.tmux.socket + ",1,0"
	if code, _, _ := f.run("go", "a"); code != 0 || f.tmux.docked != "@4" || f.tmux.entered != "@0" || f.tmux.attached != "" {
		t.Fatalf("exit %d docked %q entered %q attached %q", code, f.tmux.docked, f.tmux.entered, f.tmux.attached)
	}
}

func TestGoInsideAnotherTmuxServerIsRefused(t *testing.T) {
	f := goFakes()
	f.env["TMUX"] = "/tmp/other,1,0"
	if code, _, errOut := f.run("go", "a"); code != ExitUsage || f.tmux.attached != "" || f.tmux.docked != "" || f.tmux.dash.Window != "" || errOut == "" {
		t.Fatalf("exit %d %q", code, errOut)
	}
}

func TestGoUnknownAgentIsNotFound(t *testing.T) {
	code, _, errOut := goFakes().run("go", "missing")
	if code != ExitNotFound || errOut != "hq: no agent 'missing' (see hq ls)\n" {
		t.Fatalf("exit %d %q", code, errOut)
	}
}

func TestGoNeedsExactlyOneName(t *testing.T) {
	for _, args := range [][]string{{"go"}, {"go", "a", "b"}, {"go", "-x"}} {
		if code, _, _ := goFakes().run(args...); code != ExitUsage {
			t.Errorf("%q: exit %d", args, code)
		}
	}
}

func TestGoEntersEndedAgentToo(t *testing.T) {
	f := goFakes()
	f.tmux.windows[1].PaneDead = true
	if code, _, _ := f.run("go", "a"); code != 0 || f.tmux.docked != "@4" {
		t.Fatalf("exit %d", code)
	}
}

func TestGoStartsTheListWhenItIsNotRunning(t *testing.T) {
	f := goFakes()
	f.run("go", "a")
	f.tmux.listPID = 0 // q in the list pane
	if code, _, _ := f.run("go", "a"); code != 0 || f.tmux.respawned != 1 {
		t.Fatalf("exit %d respawned %d", code, f.tmux.respawned)
	}
}

func TestGoFramesTheDockedSessionWithItsBranch(t *testing.T) {
	f := goFakes()
	f.states = map[string]state.Report{"id-a": {State: state.Working, Branch: "feat/42"}}
	if f.run("go", "a"); f.tmux.dockTitle != "a · feat/42 · claude-x" {
		t.Errorf("frame %q", f.tmux.dockTitle)
	}
}

func TestGoReportsAFailedDock(t *testing.T) {
	f := goFakes()
	f.tmux.dockErr = errors.New("boom")
	if code, _, errOut := f.run("go", "a"); code != ExitEnvironment || f.tmux.attached != "" || !strings.Contains(errOut, "boom") {
		t.Fatalf("exit %d %q", code, errOut)
	}
}

func TestGoWithADashboardOpenElsewhereRaisesItsWindow(t *testing.T) {
	f := goFakes()
	f.tmux.clients = []string{"/dev/ttys004"}
	code, out, errOut := f.run("go", "a")
	if code != 0 || f.tmux.docked != "@4" || f.tmux.attached != "" || f.tmux.shown != "@0" || strings.Join(f.raised, " ") != "/dev/ttys004" {
		t.Fatalf("exit %d %q docked %q attached %q shown %q raised %v", code, errOut, f.tmux.docked, f.tmux.attached, f.tmux.shown, f.raised)
	}
	if out != "docked a in the open dashboard\n" || errOut != "" {
		t.Errorf("out %q err %q", out, errOut)
	}
}

func TestAFailedRaiseLeavesTheAgentDockedAndSaysSo(t *testing.T) {
	f := goFakes()
	f.tmux.clients = []string{"/dev/pts/3"}
	f.raiseErr = errors.New("powershell.exe: no window titled hq, or Windows refused to bring it to the front")
	code, out, errOut := f.run("go", "a")
	if code != 0 || f.tmux.docked != "@4" || f.tmux.attached != "" || out != "" {
		t.Fatalf("exit %d out %q docked %q attached %q", code, out, f.tmux.docked, f.tmux.attached)
	}
	if errOut != "hq: docked a in the open dashboard; could not bring its window to the front: powershell.exe: no window titled hq, or Windows refused to bring it to the front\n" {
		t.Errorf("err %q", errOut)
	}
}

func TestGoInsideHqServerNeverRaises(t *testing.T) {
	f := goFakes()
	f.tmux.clients = []string{"/dev/ttys004"}
	f.env["TMUX"] = f.tmux.socket + ",1,0"
	if code, _, _ := f.run("go", "a"); code != 0 || f.tmux.entered != "@0" || len(f.raised) != 0 {
		t.Fatalf("exit %d entered %q raised %v", code, f.tmux.entered, f.raised)
	}
}

func TestTheTitleIsClearedWhenTheAttachEnds(t *testing.T) {
	for _, env := range []map[string]string{{}, {"TMUX": "/tmp/tmux-501/default,1,0"}} {
		f := goFakes()
		for k, v := range env {
			f.env[k] = v
		}
		_, out, _ := f.run("go", "a")
		// Attached: the title hq is cleared once tmux gives the terminal
		// back; switched: the terminal stays tmux's.
		if want := f.tmux.attached != ""; strings.Contains(out, resetTitle) != want {
			t.Errorf("%v: out %q, want the reset %v", env, out, want)
		}
	}
}
