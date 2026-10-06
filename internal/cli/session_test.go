package cli

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/rkrysinski/hq/internal/state"
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

// The settings an agent's window refers to are written out as its session
// starts: the agent's identity, its hooks and the platform's notification
// (#40). Only the argument after --settings is one; a prompt that reads like
// it stays as typed.
func TestSessionWritesOutTheSettingsTheWindowRefersTo(t *testing.T) {
	f := newFakes()
	code, _, errOut := f.run(sessionCommand, "sbx", "run", "--name", "claude-app", "--", "--settings", "hq-settings:a:0123456789ab", "hq-settings:b:1")
	if code != ExitOK || errOut != "" {
		t.Fatalf("exit %d %q", code, errOut)
	}
	want := []string{"sbx", "run", "--name", "claude-app", "--", "--settings", state.Settings("a", "0123456789ab", "[notify %s]"), "hq-settings:b:1"}
	if !reflect.DeepEqual(f.ran, [][]string{want}) {
		t.Fatalf("ran %q\nwant %q", f.ran, want)
	}
	if !strings.Contains(f.ran[0][6], `"stop","[notify %s]"`) { // the platform's sequence (design §3.5)
		t.Fatalf("no notification in settings %q", f.ran[0][6])
	}
	f.ran = nil
	if code, _, errOut := f.run(sessionCommand, "sbx", "--settings", "hq-settings:a"); code != ExitUsage || errOut != "hq: invalid settings reference 'hq-settings:a'\n" || f.ran != nil {
		t.Fatalf("exit %d %q, ran %q", code, errOut, f.ran)
	}
}
