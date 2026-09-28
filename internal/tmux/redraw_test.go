package tmux

import (
	"fmt"
	"strings"
	"testing"
)

// screen is a pane's capture: hist lines of history, then height lines of
// screen, blank where lines gives none.
func screen(hist, height int, lines map[int]string) []string {
	out := make([]string, hist+height)
	for i, l := range lines {
		out[i+hist] = l
	}
	return out
}

func TestLatestLinesAfterSbxResetTheTerminal(t *testing.T) {
	// sbx run reset the terminal as the sandbox stopped: tmux pushed the
	// session into the history (lines -20..-1, its prompt box at the
	// bottom), and sbx then printed its error on the cleared screen.
	lines := screen(20, 10, map[int]string{-20: "Claude Code", -4: "● Bye", -2: "❯ ", -1: "  ⏵⏵ bypass permissions", 0: `error: sandbox "claude-hq" was stopped`})
	first, last, ok := latestLines(lines, 20, 10)
	// The screen but for its bottom line: the session's last 8 lines and
	// the error.
	if !ok || first != -8 || last != 0 {
		t.Fatalf("got %d..%d %v, want -8..0", first, last, ok)
	}
}

func TestLatestLinesOnAScreenThatStillShowsThem(t *testing.T) {
	for name, c := range map[string]struct {
		lines        []string
		hist, height int
	}{
		// /exit: Claude's last screen stays, down to its bottom lines.
		"full screen": {screen(20, 10, map[int]string{-20: "old", 0: "top", 8: "❯ ", 9: "footer"}), 20, 10},
		// A launch that failed at once: its few lines are all there is.
		"short output":    {screen(0, 10, map[int]string{0: "Starting claude agent", 1: "error: no sandbox"}), 0, 10},
		"nothing printed": {screen(5, 10, nil), 5, 10},
	} {
		if first, last, ok := latestLines(c.lines, c.hist, c.height); ok {
			t.Errorf("%s: redraws %d..%d", name, first, last)
		}
	}
}

func TestLatestLinesTakeWhatTheHistoryHoldsWhenItIsShort(t *testing.T) {
	// Three lines of output went into the history, one line followed.
	lines := screen(3, 10, map[int]string{-3: "a", -2: "b", -1: "c", 0: "error"})
	if first, last, ok := latestLines(lines, 3, 10); !ok || first != -3 || last != 0 {
		t.Fatalf("got %d..%d %v, want -3..0", first, last, ok)
	}
	// A screen of one line keeps that line.
	if first, last, ok := latestLines(screen(3, 1, map[int]string{-1: "c"}), 3, 1); !ok || first != -1 || last != -1 {
		t.Fatalf("height 1: got %d..%d %v", first, last, ok)
	}
}

func TestRedrawBlanksEachLineThenDrawsFromTheTop(t *testing.T) {
	got := redraw("\x1b[31mred\x1b[39m\nplain\n", 3)
	want := "\x1b[0m" + "\x1b[1;1H\x1b[2K\x1b[2;1H\x1b[2K\x1b[3;1H\x1b[2K" + "\x1b[H" + "\x1b[31mred\x1b[39m\r\nplain" + "\x1b[0m"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	// Never a clear of the whole screen, which tmux would push into the
	// history (scroll-on-clear).
	for _, clear := range []string{"\x1b[2J", "\x1b[J", "\x1b[0J", "\x1bc"} {
		if strings.Contains(got, clear) {
			t.Errorf("redraw clears the screen with %q", clear)
		}
	}
}

// runner answers tmux commands from a table, keyed by the command line.
type runner map[string]string

func (r runner) Run(name string, args ...string) ([]byte, error) {
	out, ok := r[strings.Join(args, " ")]
	if !ok {
		return nil, fmt.Errorf("unexpected %s %v", name, args)
	}
	return []byte(out), nil
}

func TestLastScreenCapturesTheLatestLinesWithTheirColours(t *testing.T) {
	plain := strings.Join(screen(4, 3, map[int]string{-4: "one", -3: "two", -2: "three", -1: "four", 0: "error"}), "\n") + "\n"
	c := Client{Run: runner{
		"-L s display-message -p -t %1 #{history_size} #{pane_height} ; capture-pane -p -t %1 -S - -E -": "4 3\n" + plain,
		"-L s capture-pane -p -e -t %1 -S -1 -E 0":                                                       "four\n\x1b[31merror\x1b[39m\n",
	}, Socket: "s"}
	got, err := c.LastScreen("%1")
	if err != nil {
		t.Fatal(err)
	}
	if want := redraw("four\n\x1b[31merror\x1b[39m\n", 3); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestLastScreenIsEmptyWhenTheScreenShowsTheLatestLines(t *testing.T) {
	c := Client{Run: runner{
		"display-message -p -t %1 #{history_size} #{pane_height} ; capture-pane -p -t %1 -S - -E -": "0 3\nbye\n\n\n",
	}}
	if got, err := c.LastScreen("%1"); err != nil || got != "" {
		t.Fatalf("got %q %v", got, err)
	}
	if _, err := (Client{Run: runner{
		"display-message -p -t %1 #{history_size} #{pane_height} ; capture-pane -p -t %1 -S - -E -": "junk\n",
	}}).LastScreen("%1"); err == nil {
		t.Error("an unreadable size is no error")
	}
	if _, err := (Client{Run: runner{}}).LastScreen("%1"); err == nil {
		t.Error("a failed tmux is no error")
	}
}
