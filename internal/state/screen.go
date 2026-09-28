package state

import (
	"regexp"
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
	b, ok := promptBox(screen)
	if !ok {
		return "", false
	}
	for i := b.upper - 1; i >= 0 && !strings.HasPrefix(b.lines[i], prompt) && !isRule(b.lines[i]); i-- {
		for _, e := range userEnds {
			if strings.HasPrefix(b.lines[i], e.prefix) {
				return e.last, true
			}
		}
	}
	return "", false
}

// AtRest reports whether the screen of an agent's pane looks like Claude
// waiting at its prompt box, as after a turn the user rewound with an early
// Esc (design §3.4): the box at the bottom, a footer that does not offer
// Esc to interrupt, no spinner above the box, and in the box nothing, or
// the prompt the hooks last reported (reported, whitespace aside), which
// the rewind puts back: restored says it is there. A turn at work can look
// the same for a moment (the footer drops Esc to interrupt in a narrow
// pane and while the user types, and Claude shows no spinner while its
// reply streams), so a screen at rest is a turn end only when it stays the
// same for a while; the caller sees to that. Text the user typed during a
// turn, or a menu it opened, is neither nothing nor the prompt.
func AtRest(screen, reported string) (ok, restored bool) {
	b, ok := promptBox(screen)
	if !ok {
		return false, false
	}
	for i := b.upper - 1; i >= 0 && !strings.HasPrefix(b.lines[i], prompt); i-- {
		if spinner.MatchString(b.lines[i]) {
			return false, false
		}
	}
	var input []string
	for _, l := range b.lines[b.upper+1 : b.lower] {
		input = append(input, strings.TrimPrefix(l, prompt))
	}
	typed := strings.Join(strings.Fields(strings.Join(input, " ")), " ")
	if typed == "" {
		return true, false
	}
	restored = typed == strings.Join(strings.Fields(reported), " ")
	return restored, restored
}

// PutBack reports whether the screen shows the reported prompt put back in
// the box by a rewind: the box holds it (AtRest's restored), and the last
// prompt the screen shows sent is another one. A line of userEnds above the
// box is then an earlier turn's (#99), not this one's. An Esc that
// interrupts a turn may put its prompt back in the box too, but the prompt
// also stays sent above it; when no sent prompt shows, it cannot tell and
// is false.
func PutBack(screen, reported string) bool {
	if _, restored := AtRest(screen, reported); !restored {
		return false
	}
	b, _ := promptBox(screen)
	for i := b.upper - 1; i >= 0; i-- {
		if sent, ok := strings.CutPrefix(b.lines[i], prompt); ok {
			// Only its first line: a long prompt wraps.
			return !strings.HasPrefix(strings.Join(strings.Fields(reported), " "), strings.TrimSpace(sent))
		}
	}
	return false
}

// spinner is the line Claude Code 2.1.283 animates above its prompt box
// while a turn is at work, e.g. "✶ Brewing… (14s · ↓ 129 tokens)"; the
// line a finished turn leaves there has no ellipsis ("✻ Baked for 2s").
var spinner = regexp.MustCompile(`^[·✢✳✶✻✽*] \S+…`)

// box is Claude's prompt box found at the bottom of a screen: the screen's
// lines, spaces collapsed, and the lines of its upper and lower rules.
type box struct {
	lines        []string
	upper, lower int
}

// promptBox finds Claude's prompt box at the bottom of a screen with a
// footer that does not offer Esc to interrupt: a rule, the input line
// starting with ❯ (and its continuation lines), a rule, then only the
// footer.
func promptBox(screen string) (box, bool) {
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
			return box{}, false
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
		return box{}, false
	}
	return box{lines, upper, lower}, true
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
