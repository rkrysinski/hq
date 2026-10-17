package agent

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

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
// report's detail holds; one of Claude's own what its screen shows.
func TestTheDialogVerdictSaysWhoseDialogTheAgentIsAt(t *testing.T) {
	now := time.Unix(1000, 0)
	asked := now.Add(-time.Minute)
	ws := []tmux.Window{lookWindow("1"), lookWindow("2"), lookWindow("3"), lookWindow("4")}
	rs := map[string]state.Report{
		"id1": {State: state.NeedsInput, Since: asked, Last: "Bash: make test"},
		"id2": {State: state.NeedsInput, Since: asked, Last: "Bash: go test", SubagentDialog: true},
		"id3": {State: state.Done, Since: asked, Last: "hi"},
		"id4": {State: state.Working, Since: asked},
	}
	s := &fakeStore{screens: map[string]string{"%1": "Bash command\n", "%2": "Bash command\n", "%3": ownEffortScreen(), "%4": "✶ Brewing…\n"}}
	as := Look(ws, reportsOf(rs), nil, Sandboxes{}, now, s)
	reported := &state.Ask{Tool: "Bash", Command: "make test"}

	if d := as[0].Dialog(); d.Whose != HooksDialog || !d.Open() || !d.Since.Equal(asked) || d.Asks(reported) != reported {
		t.Fatalf("the hooks' dialog: %+v", d)
	}
	if d := as[1].Dialog(); d.Whose != SubagentDialog || !d.Open() || !d.Since.Equal(asked) || d.Asks(reported) != reported {
		t.Fatalf("a subagent's dialog: %+v", d)
	}
	d := as[2].Dialog()
	if d.Whose != OwnDialog || !d.Open() || !d.Since.Equal(now) {
		t.Fatalf("a dialog of Claude's own: %+v", d)
	}
	if ask := d.Asks(reported); ask == nil || ask.Questions[0].Question != "Change effort level?" || ask.Questions[0].Options[1].Label != "No, go back" {
		t.Fatalf("what Claude's own asks: %+v", ask)
	}
	if d := as[3].Dialog(); d.Whose != NoDialog || d.Open() || d.Asks(reported) != nil {
		t.Fatalf("no dialog: %+v", d)
	}
}

// capture is one of the screens in state's testdata, real Claude Code
// screens as tmux captures them.
func capture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../state/testdata/screen-" + name + ".txt")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A dialog the hooks reported closes when the screen shows Claude at work
// after it, as after an answered MCP elicitation, which no hook reports
// (#70): the agent works, since the dialog's report, with its turn's last
// reply as after any answered dialog. The moment hq saw it is kept on its
// window, so a later look at a screen that tells nothing (a reply
// streaming shows no spinner) keeps it working.
func TestADialogTheHooksReportedClosesWhenTheScreenShowsClaudeAtWork(t *testing.T) {
	now := time.Unix(1000, 0)
	asked := now.Add(-time.Minute)
	rs := map[string]state.Report{"id1": {State: state.NeedsInput, Since: asked, Last: "Claude Code needs your input", LastStop: "Hi."}}
	s := &fakeStore{screens: map[string]string{"%1": capture(t, "fs-elicit-answered")}}
	as := Look([]tmux.Window{lookWindow("1")}, reportsOf(rs), nil, Sandboxes{}, now, s)
	if a := as[0]; a.State != state.Working || !a.Since.Equal(asked) || a.Last != "Hi." || a.Dialog().Open() || a.Entered() != asked {
		t.Fatalf("answered: %+v %+v", a, a.Dialog())
	}
	if got, want := s.options["@1"]["answered"], nanos(asked)+" "+nanos(now); got != want {
		t.Fatalf("kept %q, want %q", got, want)
	}

	// A later look sees the reply streaming: still working.
	w := lookWindow("1")
	w.Options["answered"] = s.options["@1"]["answered"]
	s = &fakeStore{screens: map[string]string{"%1": capture(t, "fs-streaming")}}
	if a := Look([]tmux.Window{w}, reportsOf(rs), nil, Sandboxes{}, now.Add(time.Second), s)[0]; a.State != state.Working || !a.Since.Equal(asked) || s.options["@1"]["answered"] != "" {
		t.Fatalf("streaming after: %+v %v", a, s.options)
	}
	// Then the user ends the turn: done, as for any turn at work.
	s = &fakeStore{screens: map[string]string{"%1": declinedScreen()}}
	if a := Look([]tmux.Window{w}, reportsOf(rs), nil, Sandboxes{}, now.Add(2*time.Second), s)[0]; a.State != state.Done || a.Last != declinedLine {
		t.Fatalf("ended after: %+v", a)
	}
	// A record of an earlier report closes no later dialog.
	later := map[string]state.Report{"id1": {State: state.NeedsInput, Since: now, Last: "Bash: make"}}
	s = &fakeStore{screens: map[string]string{"%1": capture(t, "fs-ask-open")}}
	if a := Look([]tmux.Window{w}, reportsOf(later), nil, Sandboxes{}, now.Add(time.Minute), s)[0]; a.State != state.NeedsInput || a.Dialog().Whose != HooksDialog {
		t.Fatalf("a later dialog: %+v", a)
	}
}

// Another hq process saw Claude at work first: its record is kept, and the
// agent works since the dialog's report all the same.
func TestTheFirstLookToSeeADialogAnsweredKeepsItsRecord(t *testing.T) {
	now := time.Unix(1000, 0)
	asked := now.Add(-time.Minute)
	rs := map[string]state.Report{"id1": {State: state.NeedsInput, Since: asked, Last: "Red or blue?"}}
	first := nanos(asked) + " " + nanos(now.Add(-time.Second))
	s := &fakeStore{screens: map[string]string{"%1": capture(t, "fs-ask-answered-tool")}, options: map[string]map[string]string{"@1": {"answered": first}}}
	a := Look([]tmux.Window{lookWindow("1")}, reportsOf(rs), nil, Sandboxes{}, now, s)[0]
	if a.State != state.Working || !a.Since.Equal(asked) || s.options["@1"]["answered"] != first {
		t.Fatalf("stored first elsewhere: %+v %q", a, s.options["@1"]["answered"])
	}
}

// Anything but Claude at work keeps a dialog the hooks reported open: the
// dialog itself, the bare prompt box (a reply streaming looks the same in
// Claude Code 2.1.301), a narrow pane. Only a line Claude prints for a turn
// the user ended closes it otherwise, as before; the same line with Claude
// at work below it ("Chat about this", #70) is the dialog answered (#88).
func TestADialogTheHooksReportedStaysOpenOnAnUnclearScreen(t *testing.T) {
	now := time.Unix(1000, 0)
	asked := now.Add(-time.Minute)
	rs := map[string]state.Report{"id1": {State: state.NeedsInput, Since: asked, Last: "Red or blue?", LastStop: "Hi."}}
	for _, name := range []string{"fs-ask-open", "fs-streaming", "fs-start-ready", "fs-narrow-ask-open", "fs-narrow-done", "dialog-open"} {
		s := &fakeStore{screens: map[string]string{"%1": capture(t, name)}}
		a := Look([]tmux.Window{lookWindow("1")}, reportsOf(rs), nil, Sandboxes{}, now, s)[0]
		if d := a.Dialog(); a.State != state.NeedsInput || a.Last != "Red or blue?" || d.Whose != HooksDialog || !d.Since.Equal(asked) || len(s.options) != 0 {
			t.Errorf("%s: %+v %+v %v", name, a, d, s.options)
		}
	}
	s := &fakeStore{screens: map[string]string{"%1": capture(t, "fs-ask-esc")}}
	if a := Look([]tmux.Window{lookWindow("1")}, reportsOf(rs), nil, Sandboxes{}, now, s)[0]; a.State != state.Done || a.Last != declinedLine || !a.Since.Equal(now) {
		t.Errorf("fs-ask-esc: %+v", a)
	}
	s = &fakeStore{screens: map[string]string{"%1": capture(t, "fs-ask-chat-working")}}
	if a := Look([]tmux.Window{lookWindow("1")}, reportsOf(rs), nil, Sandboxes{}, now, s)[0]; a.State != state.Working || a.Last != "Hi." || !a.Since.Equal(asked) || s.options["@1"]["answered"] == "" {
		t.Errorf("fs-ask-chat-working: %+v %v", a, s.options)
	}
}

// An MCP elicitation is drawn as Claude's own dialogs are, but when the
// hooks reported a dialog, the dialog is theirs, and asks what they said.
func TestADialogTheHooksReportedIsTheirsWhateverTheScreenLooksLike(t *testing.T) {
	now := time.Unix(1000, 0)
	asked := now.Add(-time.Minute)
	rs := map[string]state.Report{"id1": {State: state.NeedsInput, Since: asked, Last: "Claude Code needs your input"}}
	s := &fakeStore{screens: map[string]string{"%1": capture(t, "fs-elicit-open")}}
	a := Look([]tmux.Window{lookWindow("1")}, reportsOf(rs), nil, Sandboxes{}, now, s)[0]
	reported := &state.Ask{Message: "Claude Code needs your input"}
	if d := a.Dialog(); d.Whose != HooksDialog || !d.Since.Equal(asked) || d.Asks(reported) != reported || len(s.options) != 0 {
		t.Fatalf("%+v %+v %v", a, d, s.options)
	}
}

// A subagent's dialog closes by its hooks only, whatever the screen shows.
func TestASubagentsDialogStaysOpenWhateverTheScreenShows(t *testing.T) {
	now := time.Unix(1000, 0)
	asked := now.Add(-time.Minute)
	rs := map[string]state.Report{"id1": {State: state.NeedsInput, Since: asked, Last: "Claude Code needs your input", SubagentDialog: true}}
	for _, name := range []string{"fs-subagent-elicit-open", "fs-subagent-elicit-answered", "fs-elicit-answered", "fs-thinking"} {
		s := &fakeStore{screens: map[string]string{"%1": capture(t, name)}}
		a := Look([]tmux.Window{lookWindow("1")}, reportsOf(rs), nil, Sandboxes{}, now, s)[0]
		if d := a.Dialog(); a.State != state.NeedsInput || d.Whose != SubagentDialog || !d.Since.Equal(asked) || len(s.options) != 0 {
			t.Errorf("%s: %+v %+v %v", name, a, d, s.options)
		}
	}
}

// A dialog of Claude's own opened while the agent works only on background
// work, as /model, needs input from when hq first saw it; closed, the agent
// works at its prompt again.
func TestADialogOfClaudesOwnCountsOnBackgroundWork(t *testing.T) {
	now := time.Unix(10000, 0)
	prompted, stopped := now.Add(-time.Hour), now.Add(-time.Minute)
	rs := map[string]state.Report{"id1": {State: state.Working, Since: prompted, Latest: stopped, Background: true, Last: "It started."}}
	s := &fakeStore{screens: map[string]string{"%1": capture(t, "own-model-background")}}
	a := Look([]tmux.Window{lookWindow("1")}, reportsOf(rs), nil, Sandboxes{}, now, s)[0]
	if d := a.Dialog(); a.State != state.NeedsInput || d.Whose != OwnDialog || !d.Since.Equal(now) || a.Last != "Select model" || d.Asks(nil) == nil || a.Entered() != now {
		t.Fatalf("open: %+v %+v", a, d)
	}
	w := lookWindow("1")
	w.Options["dialogseen"] = s.options["@1"]["dialogseen"]
	s = &fakeStore{screens: map[string]string{"%1": capture(t, "fs-model-closed-background")}}
	if a := Look([]tmux.Window{w}, reportsOf(rs), nil, Sandboxes{}, now.Add(time.Second), s)[0]; a.State != state.Working || !a.OnBackgroundWork() || !a.Since.Equal(prompted) || a.Last != "It started." || a.Dialog().Open() {
		t.Fatalf("closed: %+v", a)
	}
	// At rest at its prompt, nothing of what the screen shows is a turn end.
	s = &fakeStore{screens: map[string]string{"%1": capture(t, "fs-background-at-prompt")}}
	if a := Look([]tmux.Window{lookWindow("1")}, reportsOf(rs), nil, Sandboxes{}, now.Add(time.Hour), s)[0]; a.State != state.Working || !a.OnBackgroundWork() || len(s.options) != 0 {
		t.Fatalf("at rest: %+v %v", a, s.options)
	}
}

// A dialog of Claude's own that hq recorded stays when one capture fails,
// as a turn end hq saw does, rather than the agent dropping back for a
// refresh; a record of an earlier report, or a closed one, does not.
func TestARecordedDialogOfClaudesOwnStandsWhenTheCaptureFails(t *testing.T) {
	now := time.Unix(10000, 0)
	done, seen := now.Add(-time.Hour), now.Add(-time.Minute)
	rs := map[string]state.Report{"id1": {State: state.Done, Since: done, Last: "hi"}}
	s := &fakeStore{screens: map[string]string{"%1": ownEffortScreen()}}
	Look([]tmux.Window{lookWindow("1")}, reportsOf(rs), nil, Sandboxes{}, seen, s)
	w := lookWindow("1")
	w.Options["dialogseen"] = s.options["@1"]["dialogseen"]
	failed := &fakeStore{captureErr: errors.New("tmux: no server")}
	a := Look([]tmux.Window{w}, reportsOf(rs), nil, Sandboxes{}, now, failed)[0]
	if d := a.Dialog(); a.State != state.NeedsInput || d.Whose != OwnDialog || !d.Since.Equal(seen) || a.Entered() != seen {
		t.Fatalf("failed capture: %+v %+v", a, d)
	}
	for name, record := range map[string]string{
		"closed":         nanos(done) + " -",
		"earlier report": nanos(done.Add(-time.Hour)) + " " + strings.Fields(w.Options["dialogseen"])[1] + " " + nanos(seen),
	} {
		w.Options["dialogseen"] = record
		if a := Look([]tmux.Window{w}, reportsOf(rs), nil, Sandboxes{}, now, failed)[0]; a.State != state.Done || !a.Since.Equal(done) {
			t.Errorf("%s: %+v", name, a)
		}
	}
}

// Only an agent at its prompt has its screen read for a dialog of Claude's
// own: done, with a question, or working only on background work. A
// dialog the hooks reported stays theirs, whatever else the report says,
// and a record of one of Claude's own does not relabel it when a capture
// fails.
func TestADialogTheHooksReportedIsNeverReadAsOneOfClaudesOwn(t *testing.T) {
	now := time.Unix(1000, 0)
	asked := now.Add(-time.Minute)
	rs := map[string]state.Report{"id1": {State: state.NeedsInput, Since: asked, Background: true, Last: "Claude Code needs your input"}}
	s := &fakeStore{screens: map[string]string{"%1": capture(t, "fs-elicit-open")}}
	a := Look([]tmux.Window{lookWindow("1")}, reportsOf(rs), nil, Sandboxes{}, now, s)[0]
	if d := a.Dialog(); d.Whose != HooksDialog || !d.Since.Equal(asked) || len(s.options) != 0 {
		t.Fatalf("seen: %+v %+v %v", a, d, s.options)
	}
	w := lookWindow("1")
	w.Options["dialogseen"] = nanos(asked) + " 1234 " + nanos(asked.Add(time.Second))
	failed := &fakeStore{captureErr: errors.New("tmux: no server")}
	if d := Look([]tmux.Window{w}, reportsOf(rs), nil, Sandboxes{}, now, failed)[0].Dialog(); d.Whose != HooksDialog || !d.Since.Equal(asked) {
		t.Fatalf("failed capture: %+v", d)
	}
}

// An agent the screen saw answer a dialog of its hooks' works on until
// they report again or Claude prints a line for a turn the user ended: a
// screen at rest, unchanged for RestDelay, is no rewind (Claude has acted
// on the turn, which only an early Esc rewinds), so the bare prompt box
// closes nothing here either, as a reply streaming shows it (#70).
func TestAnAnsweredDialogIsNoRewindAtTheBarePromptBox(t *testing.T) {
	now := time.Unix(1000, 0)
	asked := now.Add(-time.Minute)
	rs := map[string]state.Report{"id1": {State: state.NeedsInput, Since: asked, Last: "Claude Code needs your input", LastStop: "Hi."}}
	w := lookWindow("1")
	w.Options["answered"] = nanos(asked) + " " + nanos(now)
	for _, name := range []string{"fs-streaming", "fs-elicit-done"} {
		s := &fakeStore{screens: map[string]string{"%1": capture(t, name)}}
		for _, at := range []time.Time{now.Add(time.Second), now.Add(time.Second + RestDelay), now.Add(time.Hour)} {
			a := Look([]tmux.Window{w}, reportsOf(rs), nil, Sandboxes{}, at, s)[0]
			if a.State != state.Working || !a.Since.Equal(asked) || a.Last != "Hi." || a.Resting() || len(s.options) != 0 {
				t.Fatalf("%s at %v: %+v %v", name, at, a, s.options)
			}
		}
	}
}
