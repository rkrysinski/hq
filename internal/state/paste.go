package state

import (
	"strings"
	"unicode/utf8"
)

// What Claude Code does with a paste into its prompt box, as captured with
// Claude Code PasteVerifiedWith in the agents' setup (testdata/README.md,
// setup L, screen-styled-paste-*): a paste of more than pasteMaxLines
// lines, or of more than pasteMaxChars characters, shows as a placeholder,
// "[Pasted text #N +K lines]" or "[Pasted text #N]", and a prompt that
// begins with a placeholder is not a slash command: Claude takes it as
// pasted text (#173). Text typed in one burst is collapsed by the same
// limits; a paste after typed text is collapsed too, but the prompt is
// still the command the typed text begins with.
const (
	PasteVerifiedWith = "2.1.325"
	pasteMaxLines     = 3
	pasteMaxChars     = 800
)

// Collapses reports whether Claude Code shows text, pasted into its prompt
// box, as a placeholder: more than pasteMaxLines lines (a newline at the
// end starts a line too) or more than pasteMaxChars characters, newlines
// included, whatever their bytes or columns.
func Collapses(text string) bool {
	return strings.Count(text, "\n")+1 > pasteMaxLines || utf8.RuneCountInString(text) > pasteMaxChars
}
