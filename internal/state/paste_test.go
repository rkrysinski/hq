package state

import (
	"strings"
	"testing"
)

// The cases are the captures' (testdata/README.md, setup L): what Claude
// Code showed in its box for each paste.
func TestAPasteOfMoreThanThreeLinesOrEightHundredCharactersIsCollapsed(t *testing.T) {
	for text, want := range map[string]bool{
		"/probe one\ntwo\nthree":       false,
		"/probe one\ntwo\nthree\nfour": true,
		"a\nb\nc\n":                    true, // a newline at the end is a line too: "+3 lines"
		"a\n\n\nb":                     true, // blank lines count
		strings.Repeat("x", 800):       false,
		strings.Repeat("x", 801):       true,
		strings.Repeat("é", 800):       false, // characters, not bytes
		strings.Repeat("字", 800):       false, // nor columns
		strings.Repeat("字", 801):       true,
		strings.Repeat("x", 399) + "\n" + strings.Repeat("x", 400): false, // the newline counts as a character
		strings.Repeat("x", 400) + "\n" + strings.Repeat("x", 400): true,
		"": false,
	} {
		if got := Collapses(text); got != want {
			t.Errorf("Collapses(%d characters, %d newlines) = %v, want %v", len([]rune(text)), strings.Count(text, "\n"), got, want)
		}
	}
}
