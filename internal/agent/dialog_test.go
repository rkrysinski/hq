package agent

import (
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
	as := Look(ws, reportsOf(rs), Sandboxes{}, now, s)
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
