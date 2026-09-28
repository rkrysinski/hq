package tmux

import (
	"fmt"
	"strconv"
	"strings"
)

// LastScreen returns what shows an agent's latest output again on its pane's
// screen, or "" when the screen already shows it. sbx run resets the
// terminal (ESC c) when the session it serves is cut off (the sandbox
// stopped, or sbx run itself was ended): tmux then clears the screen,
// pushing the session into the pane's history, and an ended agent's pane
// would stay blank (S7, design §3.3). Run in the pane itself, after the
// session and before the pane dies, the result draws the latest lines back.
func (c Client) LastScreen(pane string) (string, error) {
	out, err := c.tmux("display-message", "-p", "-t", pane, "#{history_size} #{pane_height}",
		";", "capture-pane", "-p", "-t", pane, "-S", "-", "-E", "-")
	if err != nil {
		return "", err
	}
	head, plain, _ := strings.Cut(string(out), "\n")
	var hist, height int
	if _, err := fmt.Sscan(head, &hist, &height); err != nil {
		return "", fmt.Errorf("pane size %q: %w", head, err)
	}
	first, last, ok := latestLines(strings.Split(strings.TrimSuffix(plain, "\n"), "\n"), hist, height)
	if !ok {
		return "", nil
	}
	// Captured again, colours included, for exactly those lines, so each
	// starts with its own attributes.
	colored, err := c.tmux("capture-pane", "-p", "-e", "-t", pane, "-S", strconv.Itoa(first), "-E", strconv.Itoa(last))
	if err != nil {
		return "", err
	}
	return redraw(string(colored), height), nil
}

// latestLines finds, in a pane's lines (its history, then its screen, as
// capture-pane prints them) the latest ones that fill its screen but for the
// bottom line, which tmux takes for its "Pane is dead" line. first and last
// are tmux line numbers: the screen's lines count from 0, the history's are
// negative. ok is false when there is nothing to draw: the pane printed
// nothing, or those lines are all on the screen already.
func latestLines(lines []string, hist, height int) (first, last int, ok bool) {
	n := len(lines)
	for n > 0 && strings.TrimSpace(lines[n-1]) == "" {
		n--
	}
	if n == 0 {
		return 0, 0, false
	}
	last = n - 1 - hist
	first = max(last-max(height-2, 0), -hist)
	return first, last, first < 0
}

// redraw is what draws lines (captured with their colours) from the top of
// a screen of height lines, blanking the screen first. Each line is erased
// on its own, never the screen as a whole, which tmux would push into the
// history again (scroll-on-clear).
func redraw(lines string, height int) string {
	var b strings.Builder
	b.WriteString("\x1b[0m")
	for y := 1; y <= height; y++ {
		fmt.Fprintf(&b, "\x1b[%d;1H\x1b[2K", y)
	}
	b.WriteString("\x1b[H")
	b.WriteString(strings.ReplaceAll(strings.TrimSuffix(lines, "\n"), "\n", "\r\n"))
	b.WriteString("\x1b[0m")
	return b.String()
}
