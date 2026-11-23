package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/journal/journaltest"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/version"
)

// What the commands do with a look that fails and with the look's refusal
// of a name: the rules of the look itself are tested at the look module's
// interface (internal/agent/looker_test.go, design §7.2).

// changingDialogOnB has two agents done at their prompts, b's screen
// showing a dialog of Claude's own that changes at every look, which the
// look appends for b after it has read a: every look shows b at a change
// numbered above its cursor, so hq ls --json looks again
// (agent.Looker.Cover). looks counts the looks (each reads the counter
// once, before it lists the windows).
func changingDialogOnB(t *testing.T, looks *int) *fakes {
	f := newWaitFakes()
	f.tmux.windows[1].Pane, f.tmux.windows[2].Pane = "%1", "%2"
	f.states["id-a"] = journaltest.HookState{State: state.Done, Since: f.now.Add(-time.Hour), Last: "hi"}
	f.states["id-b"] = journaltest.HookState{State: state.Done, Since: f.now.Add(-time.Hour), Last: "hi"}
	rule := strings.Repeat("─", 40)
	dialog := func(n int) string {
		return "● hi\n" + strings.Repeat("▔", 40) + fmt.Sprintf("\n   Change effort level %d?\n\n   ❯ 1. Yes, switch to max\n     2. No, go back\n", n)
	}
	f.tmux.screens = map[string]string{"%1": "● hi\n" + rule + "\n❯ \n" + rule + "\n  footer\n", "%2": dialog(0)}
	f.onThrough = func() {
		if *looks++; *looks > 2*agent.CoverLooks {
			t.Fatalf("still looking at look %d", *looks)
		}
		f.tmux.screens["%2"] = dialog(*looks)
	}
	return f
}

// hq ls --json fails as a look fails when one of its looks again does.
func TestLsJSONFailsWhenALookAgainFails(t *testing.T) {
	looks := 0
	f := changingDialogOnB(t, &looks)
	count := f.onThrough
	f.onThrough = func() {
		if count(); looks == 2 {
			f.tmux.windowsErr = errors.New("tmux gone")
		}
	}
	if code, out, errOut := f.run("ls", "--json"); code != ExitEnvironment || out != "" || errOut != "hq: tmux gone\n" {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
}

// A chord and hq explain fail as their look does when tmux cannot list
// the windows.
func TestAChordAndExplainFailWhenTmuxCannotListTheWindows(t *testing.T) {
	f := chordFakes()
	f.tmux.windowsErr = errors.New("tmux gone")
	if code, _, _ := f.run("__chord", "next"); code != ExitEnvironment || strings.Join(f.tmux.messages, "|") != "hq: tmux gone" {
		t.Fatalf("chord: exit %d, messages %q", code, f.tmux.messages)
	}
	f = sendFakes(state.Done)
	f.tmux.windowsErr = errors.New("tmux gone")
	if code, out, errOut := f.run("explain", "a"); code != ExitEnvironment || out != "" || errOut != "hq: tmux gone\n" {
		t.Fatalf("explain: exit %d %q %q", code, out, errOut)
	}
}

// hq wait fails as hq ls does when tmux cannot list the windows for a look
// of its look loop.
func TestWaitFailsWhenTmuxCannotListTheWindows(t *testing.T) {
	f := newWaitFakes()
	f.tmux.windowsErr = errors.New("tmux gone")
	if code, out, errOut := f.run("wait", "--timeout", "1s"); code != ExitEnvironment || out != "" || errOut != "hq: tmux gone\n" {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
}

// noAgent is what a command prints for a name no agent of the look has:
// the look's refusal (agent.Seen.Named), in the look module's words.
func noAgent(name string) string {
	return "hq: " + (&agent.NotFoundError{Name: name}).Error() + "\n"
}

// Every command that acts on the agent it names, as the look gives it,
// turns the look's refusal (agent.Seen.Usable) into its exit code, prints
// it alone and acts on nothing: none of that name (exit 2), or an outdated
// one (exit 1, #194), in the look module's words. hq code, which takes no
// look (spec #336, O1), refuses an outdated agent in the same words.
func TestCommandsActOnNothingTheLookRefuses(t *testing.T) {
	outdated := "hq: " + (&agent.OutdatedError{Agent: agent.Agent{Name: "old", Generation: version.Generation - 1}}).Error() + "\n"
	for _, args := range [][]string{
		{"read", "NAME"},
		{"read", "NAME", "--json"},
		{"send", "NAME", "hi"},
		{"answer", "NAME", "--dialog", "abcdef", "1"},
		{"wait", "NAME"},
		{"wait", "a", "NAME", "--timeout", "1s"},
		{"explain", "NAME"},
		{"explain", "NAME", "--all", "--json"},
	} {
		for name, want := range map[string]struct {
			code int
			err  string
		}{
			"nobody": {ExitNotFound, noAgent("nobody")},
			"old":    {ExitUsage, outdated},
		} {
			args := slices.Clone(args)
			args[slices.Index(args, "NAME")] = name
			f := outdatedFakes()
			code, out, errOut := f.run(args...)
			if code != want.code || out != "" || errOut != want.err {
				t.Errorf("%v: exit %d %q %q", args, code, out, errOut)
			}
			if len(f.inbox["id-old"]) != 0 || len(f.answerLeft) != 0 || len(f.edited) != 0 || len(f.tmux.pasted) != 0 {
				t.Errorf("%v: inbox %v, answers %v, edited %v, pasted %v", args, f.inbox, f.answerLeft, f.edited, f.tmux.pasted)
			}
		}
	}
	f := outdatedFakes()
	if code, out, errOut := f.run("code", "old"); code != ExitUsage || out != "" || errOut != outdatedLine || len(f.edited) != 0 {
		t.Errorf("code: exit %d %q %q, edited %v", code, out, errOut, f.edited)
	}
}
