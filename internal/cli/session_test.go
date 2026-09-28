package cli

import (
	"errors"
	"reflect"
	"testing"
)

func TestSessionRunsTheAgentsSessionThenShowsItsLatestOutputAgain(t *testing.T) {
	f := newFakes()
	f.env["TMUX_PANE"] = "%3"
	f.tmux.lastScreens = map[string]string{"%3": "[last screen]"}
	f.ranCode = 1 // sbx run ends with 1 when its sandbox stops
	code, out, errOut := f.run(sessionCommand, "sbx", "run", "--name", "claude-app", "--", "say hi")
	if want := [][]string{{"sbx", "run", "--name", "claude-app", "--", "say hi"}}; !reflect.DeepEqual(f.ran, want) {
		t.Fatalf("ran %q, want %q", f.ran, want)
	}
	// The session's own status, and nothing of hq's on its screen but the
	// latest output drawn again.
	if code != 1 || out != "[last screen]" || errOut != "" {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	f.ranCode = 0
	if code, _, errOut = f.run(sessionCommand, "true"); code != ExitOK || errOut != "" {
		t.Fatalf("exit %d %q", code, errOut)
	}
}

func TestSessionOutsideTmuxOrWithoutTmuxsAnswerOnlyRunsIt(t *testing.T) {
	f := newFakes()
	f.tmux.lastScreens = map[string]string{"%3": "[last screen]"}
	if code, out, _ := f.run(sessionCommand, "sbx"); code != ExitOK || out != "" {
		t.Fatalf("outside tmux: exit %d %q", code, out)
	}
	f.env["TMUX_PANE"] = "%3"
	f.tmux.lastScreenErr = errors.New("tmux: no server")
	f.ranCode = 130
	if code, out, errOut := f.run(sessionCommand, "sbx"); code != 130 || out != "" || errOut != "" {
		t.Fatalf("tmux failed: exit %d %q %q", code, out, errOut)
	}
}

func TestSessionThatCannotStartOrIsNotGiven(t *testing.T) {
	f := newFakes()
	f.ranErr = errors.New(`exec: "sbx": executable file not found in $PATH`)
	if code, _, errOut := f.run(sessionCommand, "sbx"); code != exitNotStarted || errOut != "hq: exec: \"sbx\": executable file not found in $PATH\n" {
		t.Fatalf("exit %d %q", code, errOut)
	}
	if code, _, errOut := f.run(sessionCommand); code != ExitUsage || errOut != "hq: usage: hq __session COMMAND [ARGUMENT...]\n" {
		t.Fatalf("exit %d %q", code, errOut)
	}
}
