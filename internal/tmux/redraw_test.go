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

func TestLatestLinesAfterSbxResetTheTerminal(t *testing.T) {
	// sbx run reset the terminal as the sandbox stopped: tmux pushed the
	// session into the history (lines -20..-1, its prompt box at the
	// bottom), and sbx then printed its error on the cleared screen.
	lines := screen(20, 10, map[int]string{-20: "Claude Code", -10: "● Bye", -9: "✻ Brewed", -3: "─", -2: "❯ ", -1: "─", 0: `error: sandbox "claude-hq" was stopped`})
	got := latestLines(lines, 20, 10)
	// The screen but for its bottom line, 9 lines: the session's last ones
	// and the error, each run of blank rows as one: the whole session.
	want := []int{0, 9, 10, 11, 16, 17, 18, 19, 20}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestLatestLinesFoldTheBlankRowsAboveClaudesPromptBox(t *testing.T) {
	// /exit: Claude's last screen stays, its reply at the top, its prompt
	// box at the bottom, blank rows between.
	lines := screen(2, 10, map[int]string{-2: "❯ say yo", 0: "● Yo", 7: "❯ /exit", 8: "Resume this session with:", 9: "claude --resume 0229"})
	if got, want := latestLines(lines, 2, 10), []int{0, 1, 2, 8, 9, 10, 11}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestLatestLinesOnAScreenThatShowsThemAlready(t *testing.T) {
	full := map[int]string{}
	for i := -3; i < 10; i++ {
		full[i] = fmt.Sprintf("line %d", i)
	}
	for name, c := range map[string]struct {
		lines        []string
		hist, height int
	}{
		"full screen": {screen(3, 10, full), 3, 10},
		// A launch that failed at once: its few lines are all there is.
		"short output":    {screen(0, 10, map[int]string{0: "Starting claude agent", 1: "error: no sandbox"}), 0, 10},
		"nothing printed": {screen(5, 10, nil), 5, 10},
	} {
		if got := latestLines(c.lines, c.hist, c.height); got != nil {
			t.Errorf("%s: redraws %v", name, got)
		}
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

func TestRedrawBlanksEachLineThenDrawsFromTheTop(t *testing.T) {
	got := redraw([]string{"\x1b[31mred\x1b[39m", "plain"}, 3)
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
	plain := strings.Join(screen(4, 4, map[int]string{-4: "one", -3: "two", 0: "error"}), "\n") + "\n"
	c := Client{Run: runner{
		"-L s display-message -p -t %1 #{history_size} #{pane_height} ; capture-pane -p -t %1 -S - -E -": "4 4\n" + plain,
		"-L s capture-pane -p -e -t %1 -S -3 -E 0":                                                       "two\n\n\n\x1b[31merror\x1b[39m\n",
	}, Socket: "s"}
	got, err := c.LastScreen("%1")
	if err != nil {
		t.Fatal(err)
	}
	// The two blank lines between are drawn as one.
	if want := redraw([]string{"two", "", "\x1b[31merror\x1b[39m"}, 4); got != want {
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
