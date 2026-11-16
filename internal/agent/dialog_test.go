package agent

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/journal/journaltest"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
)

// ownEffortScreen is Claude's own dialog after a turn, opened by /effort
// (#66), in place of the prompt box.
func ownEffortScreen() string {
	top := strings.Repeat("▔", 40)
	return "❯ say hi\n● hi\n" + top + "\n   Change effort level?\n   Your next response will be slower\n\n   ❯ 1. Yes, switch to max\n     2. No, go back\n"
}

// The dialog verdict says whether an agent is at a dialog, whose, since
// when, and what it asks: a dialog its hooks reported asks what the
// journal holds of it; one of Claude's own what its screen shows.
func TestTheDialogVerdictSaysWhoseDialogTheAgentIsAt(t *testing.T) {
	now := time.Unix(1000, 0)
	asked := now.Add(-time.Minute)
	ws := []tmux.Window{lookWindow("1"), lookWindow("2"), lookWindow("3"), lookWindow("4")}
	s := newStore(map[string]string{"%1": "Bash command\n", "%2": "Bash command\n", "%3": ownEffortScreen(), "%4": "✶ Brewing…\n"})
	reported := &state.Ask{Tool: "Bash", Command: "make test"}
	s.Memory.Report("/w/app", "a1", journaltest.HookState{State: state.NeedsInput, Since: asked}, state.Detail{Ask: reported})
	s.Memory.Report("/w/app", "a2", journaltest.HookState{State: state.NeedsInput, Since: asked, SubagentDialog: true}, state.Detail{Ask: reported})
	as := s.look(ws, map[string]journaltest.HookState{
		"a3": {State: state.Done, Since: asked, Last: "hi"},
		"a4": {State: state.Working, Since: asked},
	}, Sandboxes{}, now)

	if d := as[0].Dialog(); d.Whose != state.DialogHooks || !d.Open() || !d.Since.Equal(asked) || !reflect.DeepEqual(d.Asks(as[0].Detail().Ask), reported) {
		t.Fatalf("the hooks' dialog: %+v, asks %+v", d, as[0].Detail().Ask)
	}
	if d := as[1].Dialog(); d.Whose != state.DialogSubagent || !d.Open() || !d.Since.Equal(asked) || !reflect.DeepEqual(d.Asks(as[1].Detail().Ask), reported) {
		t.Fatalf("a subagent's dialog: %+v", d)
	}
	d := as[2].Dialog()
	if d.Whose != state.DialogOwn || !d.Open() || !d.Since.Equal(now) {
		t.Fatalf("a dialog of Claude's own: %+v", d)
	}
	if ask := d.Asks(reported); ask == nil || ask.Questions[0].Question != "Change effort level?" || ask.Questions[0].Options[1].Label != "No, go back" {
		t.Fatalf("what Claude's own asks: %+v", ask)
	}
	if d := as[3].Dialog(); d.Whose != "" || d.Open() || d.Asks(reported) != nil {
		t.Fatalf("no dialog: %+v", d)
	}
}

// A dialog the hooks reported closes when the screen shows Claude at work
// after it, as after an answered MCP elicitation, which no hook reports
// (#70): the agent works, since the dialog's report, with its turn's last
// reply, entered when hq saw it. The journal holds what hq saw, so a later look at a screen that
// tells nothing (a reply streaming shows no spinner) keeps it working, and
// a turn the user ends then is done as any turn at work.
func TestADialogTheHooksReportedClosesWhenTheScreenShowsClaudeAtWork(t *testing.T) {
	now := time.Unix(1000, 0)
	asked := now.Add(-time.Minute)
	rs := map[string]journaltest.HookState{"a1": {State: state.NeedsInput, Since: asked, Last: "Claude Code needs your input", LastStop: "Hi."}}
	s := newStore(map[string]string{"%1": capture(t, "fs-elicit-answered")})
	ws := []tmux.Window{lookWindow("1")}
	if a := s.look(ws, rs, Sandboxes{}, now)[0]; a.State != state.Working || !a.Since.Equal(asked) || a.Last != "Hi." || a.Dialog().Open() || a.Entered() != now {
		t.Fatalf("answered: %+v %+v", a, a.Dialog())
	}
	if got := ofSourceKinds(s.journalOf(t, "a1"), state.SourceScreen); got != state.KindAtWork {
		t.Fatalf("appended %s", got)
	}
	s.screens["%1"] = capture(t, "fs-streaming")
	if a := s.look(ws, rs, Sandboxes{}, now.Add(time.Second))[0]; a.State != state.Working || !a.Since.Equal(asked) {
		t.Fatalf("streaming after: %+v", a)
	}
	s.screens["%1"] = declinedScreen()
	if a := s.look(ws, rs, Sandboxes{}, now.Add(2*time.Second))[0]; a.State != state.Done || a.Last != declinedLine {
		t.Fatalf("ended after: %+v", a)
	}
	// What hq saw of an earlier report closes no later dialog.
	s.screens["%1"] = capture(t, "fs-ask-open")
	later := map[string]journaltest.HookState{"a1": {State: state.NeedsInput, Since: now.Add(3 * time.Second), Last: "Bash: make"}}
	if a := s.look(ws, later, Sandboxes{}, now.Add(time.Minute))[0]; a.State != state.NeedsInput || a.Dialog().Whose != state.DialogHooks {
		t.Fatalf("a later dialog: %+v", a)
	}
}

// A subagent's dialog closes by its hooks only, whatever the screen shows.
func TestASubagentsDialogStaysOpenWhateverTheScreenShows(t *testing.T) {
	now := time.Unix(1000, 0)
	asked := now.Add(-time.Minute)
	rs := map[string]journaltest.HookState{"a1": {State: state.NeedsInput, Since: asked, Last: "Claude Code needs your input", SubagentDialog: true}}
	for _, name := range []string{"fs-subagent-elicit-open", "fs-subagent-elicit-answered", "fs-elicit-answered", "fs-thinking"} {
		s := newStore(map[string]string{"%1": capture(t, name)})
		a := s.look([]tmux.Window{lookWindow("1")}, rs, Sandboxes{}, now)[0]
		if d := a.Dialog(); a.State != state.NeedsInput || d.Whose != state.DialogSubagent || !d.Since.Equal(asked) || ofSourceKinds(s.journalOf(t, "a1"), state.SourceScreen) != "" {
			t.Errorf("%s: %+v %+v", name, a, d)
		}
	}
}

// A dialog of Claude's own opened while the agent works only on background
// work, as /model, needs input from when hq first saw it; closed, the agent
// works at its prompt again, and at rest there nothing is a turn end.
func TestADialogOfClaudesOwnCountsOnBackgroundWork(t *testing.T) {
	now := time.Unix(10000, 0)
	prompted, stopped := now.Add(-time.Hour), now.Add(-time.Minute)
	rs := map[string]journaltest.HookState{"a1": {State: state.Working, Since: prompted, Latest: stopped, Background: true, Last: "It started."}}
	s := newStore(map[string]string{"%1": capture(t, "own-model-background")})
	ws := []tmux.Window{lookWindow("1")}
	a := s.look(ws, rs, Sandboxes{}, now)[0]
	if d := a.Dialog(); a.State != state.NeedsInput || d.Whose != state.DialogOwn || !d.Since.Equal(now) || a.Last != "Select model" || d.Asks(nil) == nil || a.Entered() != now {
		t.Fatalf("open: %+v %+v", a, d)
	}
	s.screens["%1"] = capture(t, "fs-model-closed-background")
	if a := s.look(ws, rs, Sandboxes{}, now.Add(time.Second))[0]; a.State != state.Working || !a.OnBackgroundWork() || !a.Since.Equal(prompted) || a.Last != "It started." || a.Dialog().Open() {
		t.Fatalf("closed: %+v", a)
	}
	s.screens["%1"] = capture(t, "fs-background-at-prompt")
	if a := s.look(ws, rs, Sandboxes{}, now.Add(time.Hour))[0]; a.State != state.Working || !a.OnBackgroundWork() {
		t.Fatalf("at rest: %+v", a)
	}
	if got := ofSourceKinds(s.journalOf(t, "a1"), state.SourceScreen); got != state.KindOwnDialog+" "+state.KindOwnDialogGone {
		t.Fatalf("appended %s", got)
	}
}

// hq answer moves the cursor of a dialog of Claude's own to an option
// (#120): the verdict tells a fresh screen of the same dialog, the cursor
// moved, from another dialog or none, and says when its list scrolls, as
// the picker of /model does, which hq does not answer. A look that could
// not capture the screen knows the dialog by what the journal holds, which
// names it by the same fingerprint.
func TestTheVerdictOfADialogOfClaudesOwnKnowsItsScreen(t *testing.T) {
	now := time.Unix(1000, 0)
	rs := map[string]journaltest.HookState{"a1": {State: state.Done, Since: now.Add(-time.Minute), Last: "hi"}, "a2": {State: state.Done, Since: now.Add(-time.Minute), Last: "hi"}}
	s := newStore(map[string]string{"%1": capture(t, "own-effort-low"), "%2": capture(t, "own-model-2304")})
	ws := []tmux.Window{lookWindow("1"), lookWindow("2")}
	as := s.look(ws, rs, Sandboxes{}, now)
	effort, model := as[0].Dialog(), as[1].Dialog()
	if effort.Whose != state.DialogOwn || effort.Scrolls() || model.Whose != state.DialogOwn || !model.Scrolls() {
		t.Fatalf("effort %+v, model %+v", effort, model)
	}
	for name, want := range map[string]bool{"own-effort-low": true, "own-effort-low-down": true, "own-switch-model": false, "own-model-2304": false, "effort-low-set": false} {
		v, _ := state.ViewOwnDialog(capture(t, name))
		if got := effort.Shows(v); got != want {
			t.Errorf("%s: shows the effort dialog %v, want %v", name, got, want)
		}
	}
	s.captureErr = errors.New("tmux: no server")
	recalled := s.look(ws[:1], rs, Sandboxes{}, now.Add(time.Second))[0]
	v, _ := state.ViewOwnDialog(capture(t, "own-effort-low"))
	other, _ := state.ViewOwnDialog(capture(t, "own-switch-model"))
	if d := recalled.Dialog(); d.Whose != state.DialogOwn || !d.Shows(v) || d.Shows(other) || (Dialog{}).Shows(v) || !d.Since.Equal(now) {
		t.Fatalf("recalled: %+v", d)
	}
	if recalled.Dialog().Asks(nil) != nil {
		t.Fatalf("a dialog of Claude's own a look did not see asks %+v", recalled.Dialog().Asks(nil))
	}
	// Gone from the screen: closed.
	s.captureErr = nil
	s.screens["%1"] = "● hi\n" + lookRule + "\n❯ \n" + lookRule + "\n"
	if d := s.look(ws[:1], rs, Sandboxes{}, now.Add(2*time.Second))[0].Dialog(); d.Open() {
		t.Fatalf("gone: %+v", d)
	}
}
