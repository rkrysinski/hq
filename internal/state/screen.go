package state

import (
	"strings"
	"unicode/utf8"
)

// userEnds are the lines Claude Code prints when the user ends a turn,
// which no hook reports (design §3.4): each begins a line, as Claude Code
// 2.1.283 draws it with its spaces collapsed, and gives the last message.
var userEnds = []struct{ prefix, last string }{
	{"● User declined to answer", "User declined to answer questions"}, // a question dialog cancelled
	{"⎿ Interrupted", "Interrupted"},                                   // Esc at work, or a permission prompt refused
}

// working is what Claude Code shows below its prompt box while a turn is
// at work, and only then.
const working = "esc to interrupt"

// EndedByUser reads the screen of an agent's pane and reports whether the
// user ended Claude's turn there, and the last message to show
// for it. It is true only when Claude waits at its prompt box (a rule, the
// input line starting with ❯, a rule, then the footer at the bottom) and,
// since the user's last prompt, has printed one of the lines of userEnds.
// Anything else, a dialog or a turn at work (its footer offers Esc to
// interrupt) included, is false.
func EndedByUser(screen string) (last string, ok bool) {
	// Claude puts a no-break space after its bullets; its indents vary.
	lines := strings.Split(strings.ReplaceAll(screen, "\u00a0", " "), "\n")
	for i := range lines {
		lines[i] = strings.Join(strings.Fields(lines[i]), " ")
	}
	// The lower rule of the prompt box: only the footer follows it.
	lower, footer := -1, 0
	for i := len(lines) - 1; i >= 0 && footer <= maxFooter; i-- {
		if isRule(lines[i]) {
			lower = i
			break
		}
		if lines[i] != "" {
			footer++
		}
	}
	for _, l := range lines[max(lower, 0):] {
		if strings.Contains(l, working) {
			return "", false
		}
	}
	upper := -1
	for i := lower - 1; i >= 0; i-- {
		if isRule(lines[i]) {
			upper = i
			break
		}
	}
	if upper < 0 || !strings.HasPrefix(lines[upper+1], prompt) {
		return "", false
	}
	for i := upper - 1; i >= 0 && !strings.HasPrefix(lines[i], prompt) && !isRule(lines[i]); i-- {
		for _, e := range userEnds {
			if strings.HasPrefix(lines[i], e.prefix) {
				return e.last, true
			}
		}
	}
	return "", false
}

// prompt starts Claude's input line and every prompt the user sent.
const prompt = "❯"

// maxFooter is how many lines Claude shows below its prompt box.
const maxFooter = 3

// isRule reports whether a trimmed line is one of the horizontal rules
// around Claude's prompt box.
func isRule(line string) bool {
	return utf8.RuneCountInString(line) >= 8 && strings.Trim(line, "─") == ""
}
