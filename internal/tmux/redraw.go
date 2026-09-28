package tmux

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// LastScreen returns what shows an agent's latest output on its pane's
// screen once its session has ended, or "" when the screen shows it already
// (S7, design §3.3). sbx run resets the terminal (ESC c) when the session it
// serves is cut off (the sandbox stopped, or sbx run itself was ended), and
// tmux then clears the screen, pushing the session into the pane's history:
// the pane would stay blank. And Claude keeps its prompt box at the bottom
// of the screen, the rows above it blank, which leaves little of the
// conversation on screen once the pane is made smaller, as docking does. Run
// in the pane itself, after the session and before the pane dies, the
// result draws the latest lines again, each run of blank lines as one.
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
	keep := latestLines(strings.Split(strings.TrimSuffix(plain, "\n"), "\n"), hist, height)
	if keep == nil {
		return "", nil
	}
	// Captured again, colours included, from the first line kept to the
	// last, so each starts with its own attributes.
	first, last := keep[0], keep[len(keep)-1]
	out, err = c.tmux("capture-pane", "-p", "-e", "-t", pane, "-S", strconv.Itoa(first-hist), "-E", strconv.Itoa(last-hist))
	if err != nil {
		return "", err
	}
	colored := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	var lines []string
	for _, i := range keep {
		if i-first < len(colored) {
			lines = append(lines, colored[i-first])
		}
	}
	return redraw(lines, height), nil
}

// latestLines picks, from a pane's lines (its history, then its screen, as
// capture-pane prints them), the latest ones that fill its screen but for
// the bottom line, which tmux takes for its "Pane is dead" line, each run of
// blank lines taken as one. It returns their indexes in lines, or nil when
// there is nothing to draw: the pane printed nothing, or the screen shows
// just those lines already.
func latestLines(lines []string, hist, height int) []int {
	blank := func(i int) bool { return strings.TrimSpace(lines[i]) == "" }
	n := len(lines)
	for n > 0 && blank(n-1) {
		n--
	}
	var keep []int
	for i := n - 1; i >= 0 && len(keep) < max(height-1, 1); i-- {
		if blank(i) && i+1 < n && blank(i+1) {
			continue
		}
		keep = append(keep, i)
	}
	if len(keep) == 0 {
		return nil
	}
	slices.Reverse(keep)
	// The screen shows them already when they are all on it, with no blank
	// run between them.
	if keep[0] >= hist && keep[len(keep)-1]-keep[0] == len(keep)-1 {
		return nil
	}
	return keep
}

// redraw is what draws lines (captured with their colours) from the top of
// a screen of height lines, blanking the screen first. Each line is erased
// on its own, never the screen as a whole, which tmux would push into the
// history again (scroll-on-clear).
func redraw(lines []string, height int) string {
	var b strings.Builder
	b.WriteString("\x1b[0m")
	for y := 1; y <= height; y++ {
		fmt.Fprintf(&b, "\x1b[%d;1H\x1b[2K", y)
	}
	b.WriteString("\x1b[H")
	b.WriteString(strings.Join(lines, "\r\n"))
	b.WriteString("\x1b[0m")
	return b.String()
}
