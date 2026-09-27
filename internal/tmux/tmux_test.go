package tmux

import (
	"strings"
	"testing"
)

func TestAtLeast(t *testing.T) {
	for v, ok := range map[string]bool{"3.4": true, "3.7c": true, "3.3a": false, "3.2a": false, "4.0": true, "next-3.6": true, "master": true, "2.9": false, "junk": false} {
		if AtLeast(v, "3.4") != ok {
			t.Errorf("AtLeast(%q) != %v", v, ok)
		}
	}
}

// The terminal's height is the window's and the status lines tmux takes
// from it (#79): an 80x24 terminal holds a 23-line window.
func TestTerminalHeightAddsTheStatusLines(t *testing.T) {
	for out, want := range map[string]int{"23 on\n": 24, "24 off\n": 24, "21 3\n": 24, "22 2": 24} {
		if got, err := terminalHeight(out); err != nil || got != want {
			t.Errorf("%q: %d %v, want %d", out, got, err, want)
		}
	}
	for _, out := range []string{"", "23", "x on", "23 maybe"} {
		if _, err := terminalHeight(out); err == nil {
			t.Errorf("%q: no error", out)
		}
	}
}

func TestTheSlotTitleEndsWhereTheSlotEnds(t *testing.T) {
	// tmux 3.6 draws a pane's status two columns wider: the two past the
	// slot take the surround's colour.
	for v, wide := range map[string]bool{"3.4": false, "3.5a": false, "3.6": true, "3.7c": true, "next-3.8": true, "master": true} {
		f := borderFormat(v)
		if got := strings.Contains(f, "align=right bg="+surroundColour+"]  "); got != wide {
			t.Errorf("tmux %s: surround at the end %v, want %v: %s", v, got, wide, f)
		}
		if !strings.Contains(f, "fill="+slotColour) || !strings.Contains(f, " ▸ "+slotTitle) {
			t.Errorf("tmux %s: %s", v, f)
		}
	}
}
