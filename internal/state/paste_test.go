package state

import (
	"strings"
	"testing"
)

// The first four cases are the captures' (testdata/README.md, setup L,
// screen-styled-paste-*): what Claude Code showed in its box for each
// paste, all of it ASCII. The others are extrapolated from them, backed by
// no capture: a newline at the end, blank lines, characters against bytes
// and columns, and the newline counting toward the 800 characters. A
// capture that shows one of them otherwise settles it (docs/hq/agents.md,
// Rules).
func TestAPasteOfMoreThanThreeLinesOrEightHundredCharactersIsCollapsed(t *testing.T) {
	captured := map[string]bool{
		"/probe one\ntwo\nthree":       false, // screen-styled-paste-3-lines
		"/probe one\ntwo\nthree\nfour": true,  // screen-styled-paste-4-lines
		strings.Repeat("x", 800):       false, // screen-styled-paste-800-chars
		strings.Repeat("x", 801):       true,  // screen-styled-paste-801-chars
	}
	extrapolated := map[string]bool{
		"a\nb\nc\n":              true,  // a newline at the end is a line too: "+3 lines"
		"a\n\n\nb":               true,  // blank lines count
		strings.Repeat("é", 800): false, // characters, not bytes
		strings.Repeat("字", 800): false, // nor columns
		strings.Repeat("字", 801): true,
		strings.Repeat("x", 399) + "\n" + strings.Repeat("x", 400): false, // the newline counts as a character
		strings.Repeat("x", 400) + "\n" + strings.Repeat("x", 400): true,
		"": false,
	}
	for name, cases := range map[string]map[string]bool{"captured": captured, "extrapolated": extrapolated} {
		for text, want := range cases {
			if got := Collapses(text); got != want {
				t.Errorf("%s: Collapses(%d characters, %d newlines) = %v, want %v", name, len([]rune(text)), strings.Count(text, "\n"), got, want)
			}
		}
	}
}
