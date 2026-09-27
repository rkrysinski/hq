package cli

import (
	"strings"
	"testing"

	"github.com/rkrysinski/hq/internal/prefs"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
)

func TestHqBindsTheChordsToItself(t *testing.T) {
	f := newFakes()
	f.exe = "/opt/hq"
	if code, _, errOut := f.run(); code != ExitOK {
		t.Fatalf("exit %d %q", code, errOut)
	}
	if !strings.HasPrefix(f.tmux.chords, "/opt/hq __chord | ") || !strings.Contains(f.tmux.chords, "alt+j/k#[default] dock next/previous") {
		t.Fatalf("chords %q", f.tmux.chords)
	}
	if _, out, _ := runCLI("help"); strings.Contains(out, "__chord") {
		t.Fatal("help shows __chord")
	}
}

// chordFakes has a, b and c in the view all, sorted by repo: /w/app b a
// (b needs input), /w/lib c.
func chordFakes() *fakes {
	f := newFakes()
	f.tmux.windows = []tmux.Window{
		{ID: "@0", Name: "hq", Options: map[string]string{}},
		agentWindow("@4", "a", "/w/app", f.now, false),
		agentWindow("@5", "b", "/w/app", f.now, false),
		agentWindow("@6", "c", "/w/lib", f.now, false),
	}
	for _, n := range []string{"a", "b", "c"} {
		f.states["id-"+n] = state.Report{State: state.Working, Since: f.now}
	}
	f.states["id-b"] = state.Report{State: state.NeedsInput, Since: f.now}
	f.prefs = prefs.Prefs{Sort: "repo", View: "all"}
	f.tmux.session = map[string]string{}
	return f
}

func TestAltJAndKDockTheNextAndPreviousRow(t *testing.T) {
	f := chordFakes()
	f.tmux.Dock("@5", "")
	for _, tc := range []struct{ arg, want string }{{"next", "@4"}, {"next", "@6"}, {"next", "@6"}, {"previous", "@4"}} {
		if code, _, errOut := f.run("__chord", tc.arg); code != 0 {
			t.Fatalf("%s: exit %d %q", tc.arg, code, errOut)
		}
		if f.tmux.docked != tc.want {
			t.Fatalf("%s: docked %s, want %s", tc.arg, f.tmux.docked, tc.want)
		}
	}
	if f.tmux.session["cursor"] != "a" || !strings.HasPrefix(f.tmux.dockTitle, "a · ") {
		t.Fatalf("cursor %q title %q", f.tmux.session["cursor"], f.tmux.dockTitle)
	}
}

func TestAltADocksTheFirstAgentNeedingYou(t *testing.T) {
	f := chordFakes()
	f.run("__chord", "attention")
	if f.tmux.docked != "@5" || f.tmux.session["cursor"] != "b" {
		t.Fatalf("docked %s cursor %q", f.tmux.docked, f.tmux.session["cursor"])
	}
	// b stays needing input until its answer is reported: not docked again.
	f.tmux.docked = ""
	if code, _, _ := f.run("__chord", "attention"); code != 0 || f.tmux.docked != "" {
		t.Fatalf("exit %d docked %q", code, f.tmux.docked)
	}
	if strings.Join(f.tmux.messages, "|") != "hq: no other agent needs you" {
		t.Fatalf("messages %q", f.tmux.messages)
	}
}

func TestAltNOpensTheNewAgentDialogOnTheDockedRepository(t *testing.T) {
	f := chordFakes()
	f.exe = "/opt/hq"
	f.tmux.session["cursor"] = "a"
	f.run("__chord", "new")
	f.tmux.Dock("@6", "")
	f.run("__chord", "new")
	d := f.tmux.dash.List
	want := d + " /w/app 76x20 /opt/hq __new-dialog /w/app\n" + d + " /w/app 76x20 /opt/hq __new-dialog /w/lib"
	if got := strings.Join(f.tmux.popups, "\n"); got != want {
		t.Fatalf("popups\n%s\nwant\n%s", got, want)
	}
}

func TestAChordThatFailsSaysSoOnTheStatusLine(t *testing.T) {
	f := chordFakes()
	f.tmux.dockErr = tmux.ErrNoDashboard
	if code, _, _ := f.run("__chord", "next"); code == 0 {
		t.Fatal("exit 0")
	}
	if len(f.tmux.messages) != 1 || !strings.HasPrefix(f.tmux.messages[0], "hq: ") {
		t.Fatalf("messages %q", f.tmux.messages)
	}
	for _, args := range [][]string{{"__chord"}, {"__chord", "sideways"}} {
		if code, _, _ := f.run(args...); code != ExitUsage {
			t.Errorf("%q: exit %d", args, code)
		}
	}
}
