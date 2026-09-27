package cli

import (
	"testing"

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
	if code, _, errOut := f.run("go", "a"); code != 0 || f.tmux.attached != "@4" || f.tmux.entered != "" {
		t.Fatalf("exit %d %q attached %q entered %q", code, errOut, f.tmux.attached, f.tmux.entered)
	}
}

func TestGoInsideHqServerSwitches(t *testing.T) {
	f := goFakes()
	f.env["TMUX"] = f.tmux.socket + ",1,0"
	if code, _, _ := f.run("go", "a"); code != 0 || f.tmux.entered != "@4" || f.tmux.attached != "" {
		t.Fatalf("exit %d entered %q attached %q", code, f.tmux.entered, f.tmux.attached)
	}
}

func TestGoInsideAnotherTmuxServerIsRefused(t *testing.T) {
	f := goFakes()
	f.env["TMUX"] = "/tmp/other,1,0"
	if code, _, errOut := f.run("go", "a"); code != ExitUsage || f.tmux.attached != "" || errOut == "" {
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
	if code, _, _ := f.run("go", "a"); code != 0 || f.tmux.attached != "@4" {
		t.Fatalf("exit %d", code)
	}
}
