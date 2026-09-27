package agent

import (
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
)

func TestValidName(t *testing.T) {
	for name, ok := range map[string]bool{"42": true, "bok-17": true, "a_b": true, "": false, "a b": false, "a/b": false, "ą": false} {
		if ValidName(name) != ok {
			t.Errorf("ValidName(%q) != %v", name, ok)
		}
	}
}

func TestAge(t *testing.T) {
	for d, want := range map[time.Duration]string{3 * time.Second: "3s", 2 * time.Minute: "2m", 90 * time.Minute: "1h", 50 * time.Hour: "2d"} {
		if got := Age(d); got != want {
			t.Errorf("Age(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestFromWindowsSkipsNonAgentsAndMarksDeadPanesEnded(t *testing.T) {
	as := FromWindows([]tmux.Window{
		{ID: "@0", Name: "hq", Options: map[string]string{}},
		{ID: "@1", Name: "a", PaneDead: true, Options: map[string]string{"id": "x", "name": "a", "started": "100"}},
	})
	if len(as) != 1 || as[0].State != state.Ended || as[0].Alive || as[0].Started.Unix() != 100 {
		t.Fatalf("%+v", as)
	}
}

func TestNewIDIsFresh(t *testing.T) {
	if a, b := NewID(), NewID(); a == b || len(a) != 12 {
		t.Fatalf("%q %q", a, b)
	}
}

func TestApplyTakesTheReportedStateWhileThePaneLives(t *testing.T) {
	since := time.Unix(500, 0)
	r := state.Report{State: state.Question, Since: since, Last: "Shall I?"}
	a := FromWindows([]tmux.Window{{ID: "@1", Options: map[string]string{"id": "x", "started": "100"}}})[0]
	if a.State != state.Starting || a.Since.Unix() != 100 {
		t.Fatalf("before any report: %+v", a)
	}
	a.Apply(r, true)
	if a.State != state.Question || a.Since != since || a.Last != "Shall I?" {
		t.Fatalf("%+v", a)
	}
	dead := FromWindows([]tmux.Window{{ID: "@2", PaneDead: true, Options: map[string]string{"id": "y", "started": "100"}}})[0]
	dead.Apply(r, true)
	if dead.State != state.Ended || dead.Last != "Shall I?" || dead.Since != since {
		t.Fatalf("dead pane: %+v", dead)
	}
	// A report older than the start (a reused state file) does not move it back.
	old := FromWindows([]tmux.Window{{ID: "@3", PaneDead: true, Options: map[string]string{"id": "z", "started": "900"}}})[0]
	old.Apply(r, true)
	if old.Since.Unix() != 900 {
		t.Fatalf("old report: %+v", old)
	}
}
