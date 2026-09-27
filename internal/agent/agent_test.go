package agent

import (
	"testing"
	"time"

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
	if len(as) != 1 || as[0].State != Ended || as[0].Started.Unix() != 100 {
		t.Fatalf("%+v", as)
	}
}

func TestNewIDIsFresh(t *testing.T) {
	if a, b := NewID(), NewID(); a == b || len(a) != 12 {
		t.Fatalf("%q %q", a, b)
	}
}
