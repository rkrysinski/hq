package state

import "strings"

// StartDialog reads the screen of an agent's pane whose session has just
// started and reports the dialog Claude Code shows there in place of its
// prompt box, which no hook reports (#29): a question of its own, asked
// before the first prompt, such as "Make auto mode your default permission
// mode?" (Claude Code 2.1.285), whether to trust the folder, or a switch of
// the default effort. Until the user answers it, Claude takes no prompt.
//
// Such a dialog is, below the last rule on the screen: its title, a text,
// then its options on consecutive lines, the chosen one marked with ❯. The
// prompt box is a rule, the input line and a rule: a screen that shows it
// has no dialog. ask carries the title as its question, the options, and
// the text as its description.
func StartDialog(screen string) (ask *Ask, ok bool) {
	if _, box := promptBox(screen); box {
		return nil, false
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
		return nil, false
	}
	top := -1
	for i := at - 1; i >= 0; i-- {
		if isRule(lines[i]) {
			top = i
			break
		}
	}
	title := top + 1
	for title < at && lines[title] == "" {
		title++
	}
	if top < 0 || title >= at {
		return nil, false
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
		return nil, false
	}
	for _, l := range lines[last+1:] {
		if isRule(l) || strings.Contains(l, working) {
			return nil, false
		}
	}
	q := AskQuestion{Question: Clean(lines[title]), Options: []AskOption{}}
	for _, l := range lines[first : last+1] {
		q.Options = append(q.Options, AskOption{Label: Clean(strings.TrimSpace(strings.TrimPrefix(l, prompt)))})
	}
	var text []string
	for _, l := range lines[title+1 : first] {
		if l != "" {
			text = append(text, l)
		}
	}
	return &Ask{Description: Clean(strings.Join(text, " ")), Questions: []AskQuestion{q}}, true
}
