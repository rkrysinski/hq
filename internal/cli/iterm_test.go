package cli

import (
	"errors"
	"testing"
)

const setProfile = "\x1b]1337;SetProfile=hq\a"

func TestAttachingInItermSwitchesTheTabToTheHqProfile(t *testing.T) {
	for _, args := range [][]string{nil, {"dash"}, {"go", "a"}} {
		f := goFakes()
		f.env["TERM_PROGRAM"] = "iTerm.app"
		if code, out, errOut := f.run(args...); code != ExitOK || out != setProfile || f.tmux.attached == "" {
			t.Errorf("hq %v: exit %d out %q err %q attached %q", args, code, out, errOut, f.tmux.attached)
		}
	}
}

func TestOtherTerminalsAndSwitchingGetNoProfile(t *testing.T) {
	for _, env := range []map[string]string{
		{"TERM_PROGRAM": "Apple_Terminal"},
		{"WT_SESSION": "1"}, // Windows Terminal on WSL
		{"TERM_PROGRAM": "iTerm.app", "TMUX": "/tmp/tmux-501/default,1,0"}, // inside hq's server: a switch
	} {
		f := newFakes()
		for k, v := range env {
			f.env[k] = v
		}
		if code, out, _ := f.run("dash"); code != ExitOK || out != "" {
			t.Errorf("%v: exit %d out %q", env, code, out)
		}
	}
}

func TestItermProfileCommandAddsTheProfileOnce(t *testing.T) {
	f := newFakes()
	f.profileWrote = true
	if code, out, _ := f.run("__iterm-profile"); code != ExitOK || f.profiles != 1 || out != "added the iTerm2 profile hq (Option as Esc+) for the dashboard\n" {
		t.Fatalf("exit %d asks %d out %q", code, f.profiles, out)
	}
	// Already there, or no iTerm2: nothing to say.
	f.profileWrote = false
	if code, out, _ := f.run("__iterm-profile"); code != ExitOK || out != "" {
		t.Fatalf("exit %d out %q", code, out)
	}
	if code, _, _ := f.run("__iterm-profile", "x"); code != ExitUsage {
		t.Fatalf("exit %d", code)
	}
}

func TestAFailedProfileIsAWarningOnly(t *testing.T) {
	f := newFakes()
	f.profileErr = errors.New("read-only")
	code, out, errOut := f.run("__iterm-profile")
	if code != ExitOK || out != "" || errOut != "hq: could not add the iTerm2 profile hq (Option as Alt in the dashboard): read-only\n" {
		t.Fatalf("exit %d out %q err %q", code, out, errOut)
	}
}

func TestUpdateAddsTheProfileEvenWhenCurrent(t *testing.T) {
	withVersion(t, "v0.2.0")
	f := newFakes()
	f.publish("v0.2.0", "same")
	f.profileWrote = true
	code, out, _ := f.run("update")
	if code != ExitOK || f.profiles != 1 || out != "added the iTerm2 profile hq (Option as Esc+) for the dashboard\nhq v0.2.0 is up to date (latest release v0.2.0)\n" {
		t.Fatalf("exit %d asks %d out %q", code, f.profiles, out)
	}
}
