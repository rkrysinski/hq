package cli

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/rkrysinski/hq/internal/tmux"
)

// chunks is typed input arriving one read at a time.
type chunks struct{ parts []string }

func (c *chunks) Read(p []byte) (int, error) {
	if len(c.parts) == 0 {
		return 0, io.EOF
	}
	n := copy(p, c.parts[0])
	c.parts = c.parts[1:]
	return n, nil
}

func TestPlaceholderShowsTheHintAndRunsNothingTyped(t *testing.T) {
	var out bytes.Buffer
	focused := 0
	in := &chunks{parts: []string{"ls\r", "\x03", "\x1b[<0;10;5M", "\x1b[I", "\x1b[200~rm -rf x\x1b[201~"}}
	if err := placeholder(in, &out, "hq: nothing docked", func() { focused++ }); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != slotStart+"hq: nothing docked\r\n" {
		t.Fatalf("shown %q", got)
	}
	// Keys, Ctrl+C, a click, focus and a paste each send the keys back to
	// the list; nothing is echoed or run.
	if focused != 5 {
		t.Fatalf("keys sent to the list %d times, want 5", focused)
	}
}

func TestPlaceholderKeepsTheKeysWhenItLosesTheFocus(t *testing.T) {
	focused := 0
	if err := placeholder(&chunks{parts: []string{focusOut}}, io.Discard, "h", func() { focused++ }); err != nil || focused != 0 {
		t.Fatalf("%v, focused %d", err, focused)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("input/output error") }

func TestPlaceholderEndsWithItsTerminal(t *testing.T) {
	if err := placeholder(failingReader{}, io.Discard, "h", func() {}); err == nil {
		t.Fatal("a lost terminal is not reported")
	}
}

func TestSlotCommandSaysTheHintGivenInRawModeAndFocusesTheList(t *testing.T) {
	f := newFakes()
	f.stdin = "x"
	code, out, errOut := f.run(slotCommand, tmux.KilledHint("a"))
	if code != ExitOK || errOut != "" || !strings.Contains(out, "hq: a killed - select an agent above or press n\r\n") {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	if f.tmux.focused != 1 || f.rawOn != 1 || f.rawOff != 1 {
		t.Fatalf("focused %d, raw on %d off %d", f.tmux.focused, f.rawOn, f.rawOff)
	}
	if _, out, _ = f.run(slotCommand); !strings.Contains(out, tmux.PlaceholderHint) {
		t.Fatalf("without a hint %q", out)
	}
	if code, _, errOut = f.run(slotCommand, "a", "b"); code != ExitUsage || !strings.Contains(errOut, "usage: hq __slot [HINT]") {
		t.Fatalf("exit %d %q", code, errOut)
	}
}
