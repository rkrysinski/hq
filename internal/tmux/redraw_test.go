package tmux

import (
	"fmt"
	"reflect"
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

// span is the indexes from..to of a pane's lines, as latestLines gives them.
func span(from, to int) []int {
	var s []int
	for i := from; i <= to; i++ {
		s = append(s, i)
	}
	return s
}

// claudeScreen is a screen as Claude Code draws it, height lines: its
// conversation at the top, below a blank line and its banner, and its
// prompt box and footer at the bottom, the rows between blank.
func claudeScreen(height int, conversation ...string) []string {
	s := make([]string, height)
	s[1] = " ▐▛███▛█   Claude Code v2.1.283"
	for i, l := range conversation {
		s[3+2*i] = l
	}
	copy(s[height-4:], []string{"────", "❯ ", "────", "  ⏵⏵ bypass permissions on"})
	return s
}

// exited is Claude's screen once it exited: its resume lines written from
// its prompt row down, the screen scrolled one line up.
func exited(screen []string) []string {
	s := append([]string{}, screen...)
	h := len(s)
	s[h-2], s[h-1] = "Resume this session with:────", "claude --resume 0229"
	return append(s, "")
}

// resized is the history of a pane whose Claude was resized as often as
// heights has entries: each redraw cleared the screen, and tmux pushed the
// screen it cleared into the history (scroll-on-clear).
func resized(heights ...int) []string {
	var hist []string
	for _, h := range heights {
		hist = append(hist, claudeScreen(h, "❯ say yo", "● Yo")...)
	}
	return hist
}

// picked is the lines latestLines picks, every one of them at or after
// from, the first line of the last screen.
func picked(t *testing.T, lines []string, hist, height, from int) []string {
	t.Helper()
	var got []string
	for _, i := range latestLines(lines, hist, height) {
		if i < from {
			t.Errorf("line %d (%q) is before the last screen, which starts at %d", i, lines[i], from)
		}
		got = append(got, lines[i])
	}
	return got
}

func TestLatestLinesAreClaudesLastScreenAfterItExited(t *testing.T) {
	// Docked and undocked, Claude redrew itself at 30 and 20 rows, then
	// exited (/exit, or Ctrl-C twice) at 16: its last screen scrolled one
	// line up, the blank line above its banner went into the history.
	last := exited(claudeScreen(16, "❯ say yo", "● Yo", "❯ /exit"))
	hist := append(resized(30, 20, 30), last[0])
	lines := append(append([]string{}, hist...), last[1:]...)
	got := picked(t, lines, len(hist), 16, len(hist)-1)
	want := []string{"", " ▐▛███▛█   Claude Code v2.1.283", "", "❯ say yo", "", "● Yo", "", "❯ /exit", "", "────", "❯ ", "Resume this session with:────", "claude --resume 0229"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestLatestLinesAreClaudesLastScreenAfterSbxResetTheTerminal(t *testing.T) {
	// The sandbox stopped. Claude exited, and sbx run reset the terminal:
	// tmux pushed the screen into the history, and sbx printed its error on
	// the cleared screen.
	errLine := `error: sandbox "claude-hq" was stopped`
	final := claudeScreen(16, "❯ say yo", "● Yo")
	for name, last := range map[string][]string{
		"Claude exited first": exited(final)[:16], // the blank bottom row is not pushed
		"Claude cut off":      final,
	} {
		hist := append(resized(30, 20), last...)
		lines := append(append([]string{}, hist...), errLine)
		lines = append(lines, make([]string, 15)...)
		got := picked(t, lines, len(hist), 16, len(hist)-16)
		// The last screen from its banner down (the blank row above the
		// banner is one line too many), then sbx's error; the banner once.
		if got[len(got)-1] != errLine || got[0] != " ▐▛███▛█   Claude Code v2.1.283" || strings.Count(strings.Join(got, "\n"), "Claude Code") != 1 {
			t.Errorf("%s: got %q", name, got)
		}
	}
}

func TestLatestLinesOnAScreenThatShowsThemAlready(t *testing.T) {
	full := map[int]string{}
	for i := -3; i < 9; i++ {
		full[i] = fmt.Sprintf("line %d", i)
	}
	for name, c := range map[string]struct {
		lines        []string
		hist, height int
	}{
		"full screen":     {screen(3, 10, full), 3, 10},
		"at the bottom":   {screen(0, 10, map[int]string{7: "Starting claude agent", 8: "error: no sandbox"}), 0, 10},
		"nothing printed": {screen(5, 10, nil), 5, 10},
	} {
		if got := latestLines(c.lines, c.hist, c.height); got != nil {
			t.Errorf("%s: redraws %v", name, got)
		}
	}
	// A launch that failed at once shows its few lines at the bottom too.
	if got, want := latestLines(screen(0, 10, map[int]string{0: "Starting claude agent", 1: "error: no sandbox"}), 0, 10), span(0, 1); !reflect.DeepEqual(got, want) {
		t.Errorf("short output: got %v, want %v", got, want)
	}
}

func TestLatestLinesTakeWhatTheHistoryHoldsWhenItIsShort(t *testing.T) {
	// Three lines of output went into the history, one line followed.
	lines := screen(3, 10, map[int]string{-3: "a", -2: "b", -1: "c", 0: "error"})
	if got, want := latestLines(lines, 3, 10), span(0, 3); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	// A screen of one line keeps that line.
	if got, want := latestLines(screen(3, 1, map[int]string{-1: "c"}), 3, 1), []int{2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("height 1: got %v, want %v", got, want)
	}
}

func TestRedrawBlanksEachLineThenDrawsAboveTheBottomLine(t *testing.T) {
	got := redraw([]string{"\x1b[31mred\x1b[39m", "plain"}, 4)
	want := "\x1b[0m" + "\x1b[1;1H\x1b[2K\x1b[2;1H\x1b[2K\x1b[3;1H\x1b[2K\x1b[4;1H\x1b[2K" + "\x1b[2;1H" + "\x1b[31mred\x1b[39m\r\nplain" + "\x1b[0m"
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

// runner answers tmux commands from a table, keyed by the command line
// after the -u every one of them starts with (#41).
type runner map[string]string

func (r runner) Run(name string, args ...string) ([]byte, error) {
	if len(args) == 0 || args[0] != "-u" {
		return nil, fmt.Errorf("%s %v without -u", name, args)
	}
	out, ok := r[strings.Join(args[1:], " ")]
	if !ok {
		return nil, fmt.Errorf("unexpected %s %v", name, args)
	}
	return []byte(out), nil
}

func TestLastScreenCapturesTheLatestLinesWithTheirColours(t *testing.T) {
	plain := strings.Join(screen(4, 5, map[int]string{-4: "one", -3: "two", 0: "error"}), "\n") + "\n"
	c := Client{Run: runner{
		"-L s display-message -p -t %1 #{history_size} #{pane_height} ; capture-pane -p -t %1 -S - -E -": "4 5\n" + plain,
		"-L s capture-pane -p -e -t %1 -S -3 -E 0":                                                       "two\n\n\n\x1b[31merror\x1b[39m\n",
	}, Socket: "s"}
	got, err := c.LastScreen("%1")
	if err != nil {
		t.Fatal(err)
	}
	// The two blank lines between are drawn as one.
	if want := redraw([]string{"two", "", "\x1b[31merror\x1b[39m"}, 5); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestLastScreenIsEmptyWhenTheScreenShowsTheLatestLines(t *testing.T) {
	c := Client{Run: runner{
		"display-message -p -t %1 #{history_size} #{pane_height} ; capture-pane -p -t %1 -S - -E -": "0 3\n\nbye\n\n",
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
