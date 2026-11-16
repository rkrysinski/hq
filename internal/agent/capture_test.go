package agent

import (
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/journal/journaltest"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
)

// reportAtCapture is a memStore whose hook reports the turn's end while
// the look captures the screens, as Claude reports it a moment before it
// draws its empty prompt box again.
type reportAtCapture struct {
	*memStore
	at time.Time
}

func (s reportAtCapture) Screens(panes []string) (map[string]string, error) {
	s.Leave("/w/app", "a1", "000000002.stop.1", journaltest.Hook("main", map[string]any{"hook_event_name": "Stop", "last_assistant_message": "Hi."}), s.at)
	s.screens["%1"] = "● Hi.\n" + lookRule + "\n❯ \n" + lookRule + "\n"
	return s.memStore.Screens(panes)
}

// A turn's end the hook reported before the look captured the screen is
// in the journal before what the screen shows: the look reads the event
// files again after the capture, and the empty prompt box Claude drew
// after the turn's end is no screen at rest of a turn still at work
// (#191).
func TestATurnEndReportedBeforeTheCaptureComesBeforeWhatTheScreenShows(t *testing.T) {
	now := time.Unix(1000, 0)
	s := reportAtCapture{memStore: newStore(map[string]string{}), at: now.Add(-100 * time.Millisecond)}
	s.Leave("/w/app", "a1", "000000001.prompt.1", journaltest.Hook("main", map[string]any{"hook_event_name": "UserPromptSubmit", "prompt": "Say hi."}), now.Add(-time.Second))
	a := Look([]tmux.Window{evWindow()}, Sandboxes{}, now, s, nil)[0]
	if got := journaltest.Kinds(s.journalOf(t, "a1")); got != "UserPromptSubmit Stop" || a.State != state.Done || a.Last != "Hi." {
		t.Fatalf("journal %s, %s %q", got, a.State, a.Last)
	}
}
