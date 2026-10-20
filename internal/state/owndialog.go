package state

import (
	"fmt"
	"regexp"
	"strings"
)

// OwnDialog reads the screen of an agent's pane that waits at its prompt
// and reports the dialog Claude Code shows there in place of its prompt
// box, which no hook reports: a question of its own outside any turn. At
// the start of a session (#29) it asks before the first prompt, such as
// "Make auto mode your default permission mode?" (Claude Code 2.1.285), or
// whether to trust the folder; after a turn (#66) a slash command opens
// one, such as "Change effort level?" for /effort, or the picker of
// /model (Claude Code 2.1.300). Until the user answers it, Claude takes no
// prompt.
//
// Such a dialog is, below the last rule on the screen (a line of ─, or of
// ▔ that may carry a text after it): its title, a text, then its options
// on consecutive lines, the chosen one marked with ❯. The prompt box is a
// rule, the input line and a rule: a screen that shows it has no dialog.
// ask carries the title as its question, the options without their
// numbers, and the text as its description.
//
// AskUserQuestion, a dialog of a turn, is drawn the same way under its
// tabs, one per question: its review step after several questions (#94)
// has no footer below its options, so the tabs tell it apart.
func OwnDialog(screen string) (ask *Ask, ok bool) {
	v, ok := ViewOwnDialog(screen)
	return v.Ask, ok
}

// OwnView is a dialog of Claude's own as one screen shows it (OwnDialog):
// what it asks, the option its cursor is on, by its index in the options,
// and whether its list scrolls to options the screen does not show, as
// the picker of /model does (a ↓ or ↑ before an option, "… +3 models"
// below them). hq answer moves the cursor to an option and presses Enter
// (#120).
type OwnView struct {
	Ask     *Ask
	Cursor  int
	Scrolls bool
}

// ViewOwnDialog is OwnDialog with where the dialog's cursor is.
func ViewOwnDialog(screen string) (OwnView, bool) {
	if _, box := promptBox(screen); box {
		return OwnView{}, false
	}
	lines := strings.Split(strings.ReplaceAll(screen, "\u00a0", " "), "\n")
	for i := range lines {
		lines[i] = strings.Join(strings.Fields(lines[i]), " ")
	}
	// The option the cursor is on: the last line marked, not an input line
	// (which has a rule right above it).
	at := -1
	for i := len(lines) - 1; i > 0; i-- {
		if strings.HasPrefix(lines[i], prompt+" ") && !isRule(lines[i-1]) {
			at = i
			break
		}
	}
	if at < 0 {
		return OwnView{}, false
	}
	top := -1
	for i := at - 1; i >= 0; i-- {
		if isRule(lines[i]) || strings.HasPrefix(lines[i], dialogTop) {
			top = i
			break
		}
	}
	title := top + 1
	for title < at && lines[title] == "" {
		title++
	}
	if top < 0 || title >= at || askTabs.MatchString(lines[title]) {
		return OwnView{}, false
	}
	// The options: the lines around the marked one, up to a blank line.
	first, last := at, at
	for first-1 > title && lines[first-1] != "" {
		first--
	}
	for last+1 < len(lines) && lines[last+1] != "" && !isRule(lines[last+1]) {
		last++
	}
	if last == first {
		return OwnView{}, false
	}
	for _, l := range lines[last+1:] {
		if isRule(l) || strings.Contains(l, working) {
			return OwnView{}, false
		}
	}
	q := AskQuestion{Question: Clean(lines[title]), Options: []AskOption{}}
	v := OwnView{}
	for i, l := range lines[first : last+1] {
		l = strings.TrimPrefix(l, prompt)
		if i == at-first {
			v.Cursor = len(q.Options)
		}
		v.Scrolls = v.Scrolls || scrollMark.MatchString(l)
		// "… +3 models": more options the list scrolls to.
		if strings.HasPrefix(l, "…") {
			v.Scrolls = true
			continue
		}
		q.Options = append(q.Options, AskOption{Label: Clean(optionMark.ReplaceAllString(l, ""))})
	}
	var text []string
	for _, l := range lines[title+1 : first] {
		if l != "" {
			text = append(text, l)
		}
	}
	v.Ask = &Ask{Description: Clean(strings.Join(text, " ")), Questions: []AskQuestion{q}}
	return v, true
}

// Pick is the option of the dialog answers picks, by its index: the
// dialog asks one question and takes one of its options, by its number as
// hq read lists them. An answer that does not fit is refused, as
// Questions.Decision refuses one.
func (v OwnView) Pick(answers []Answer) (int, error) {
	if len(answers) != 1 {
		return 0, fmt.Errorf("the dialog has 1 question, %s given: one per question, in its order", count(len(answers), "answer"))
	}
	q, a := v.Ask.Questions[0], answers[0]
	switch {
	case len(a.Options) == 0 || a.Text != "":
		return 0, fmt.Errorf("%s takes an option's number, not text: it is a dialog of Claude's own", q.Question)
	case len(a.Options) > 1:
		return 0, fmt.Errorf("%s takes one option, %d given", q.Question, len(a.Options))
	case a.Options[0] < 1 || a.Options[0] > len(q.Options):
		return 0, fmt.Errorf("%s has no option %d: it has %d", q.Question, a.Options[0], len(q.Options))
	}
	return a.Options[0] - 1, nil
}

// dialogTop starts the rule of ▔ Claude Code 2.1.300 draws above a dialog
// it opens after a turn; the rule may carry a text after it.
var dialogTop = strings.Repeat("▔", 8)

// askTabs is the first line under the rule of AskUserQuestion: its
// questions' tabs, each with a box ticked once answered (☐ Color, ☒ Color),
// after an arrow when there are several (← ☐ Color ☐ Sizes ✔ Submit →).
var askTabs = regexp.MustCompile(`^(?:← )?[☐☒] `)

// optionMark is what precedes an option's label on its line: the arrow
// that says the list scrolls on, and the option's number.
var optionMark = regexp.MustCompile(`^ ?(?:[↑↓] )?(?:\d+\. )?`)

// scrollMark is the arrow before an option that says the list scrolls on.
var scrollMark = regexp.MustCompile(`^ ?[↑↓] `)
