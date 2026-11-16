package agent

import (
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/journal/journaltest"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
)

// reportAtCapture is a memStore whose hook reports while the look
// captures the screens (report), before Claude draws what the screen
// shows then (screen).
type reportAtCapture struct {
	*memStore
	report func()
	screen string
}

func (s reportAtCapture) Screens(panes []string) (map[string]string, error) {
	s.report()
	s.screens["%1"] = s.screen
	return s.memStore.Screens(panes)
}

// lookWhileReporting has agent a1 of evWindow at work since a second
// before now, and looks at it at now while its hook reports the event
// file name with payload p, a moment before Claude draws screen.
func lookWhileReporting(t *testing.T, now time.Time, name string, p map[string]any, screen string) (Agent, string) {
	t.Helper()
	s := reportAtCapture{memStore: newStore(map[string]string{}), screen: screen}
	s.report = func() { s.Leave("/w/app", "a1", name, journaltest.Hook("main", p), now.Add(-100*time.Millisecond)) }
	s.Leave("/w/app", "a1", "000000001.prompt.1", journaltest.Hook("main", map[string]any{"hook_event_name": "UserPromptSubmit", "prompt": "Say hi."}), now.Add(-time.Second))
	a := Look([]tmux.Window{evWindow()}, Sandboxes{}, now, s, nil)[0]
	return a, journaltest.Kinds(s.journalOf(t, "a1"))
}

// What the hook reported before the capture comes before what the screen
// shows: the look reads the event files again after the capture, and
// judges the screen only if it may still tell then (#191). The empty
// prompt box Claude drew after the turn's end is no screen at rest of a
// turn still at work, and the line of a turn the user ended, still on
// the screen of a session that has reported its end, ends no turn.
func TestWhatTheHookReportedBeforeTheCaptureComesBeforeWhatTheScreenShows(t *testing.T) {
	now := time.Unix(1000, 0)
	a, got := lookWhileReporting(t, now, "000000002.stop.1", map[string]any{"hook_event_name": "Stop", "last_assistant_message": "Hi."}, "● Hi.\n"+lookRule+"\n❯ \n"+lookRule+"\n")
	if got != "UserPromptSubmit Stop" || a.State != state.Done || a.Last != "Hi." {
		t.Fatalf("turn end: journal %s, %s %q", got, a.State, a.Last)
	}
	a, got = lookWhileReporting(t, now, "000000002.end.1", map[string]any{"hook_event_name": "SessionEnd", "reason": "prompt_input_exit"}, declinedScreen())
	if got != "UserPromptSubmit SessionEnd" || a.State != state.Ended {
		t.Fatalf("session end: journal %s, %s %q", got, a.State, a.Last)
	}
}
