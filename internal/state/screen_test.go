package state

import (
	"os"
	"strings"
	"testing"
)

// The screens in testdata are Claude Code 2.1.283 in a tmux pane, as
// capture-pane shows them (screen-declined-80 with its blank lines left out).
func screen(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/screen-" + name + ".txt")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestEndedByUserSeesACancelledQuestionDialog(t *testing.T) {
	for _, name := range []string{"declined", "declined-80"} {
		if last, ok := EndedByUser(screen(t, name)); !ok || last != "User declined to answer questions" {
			t.Errorf("%s: %q %v", name, last, ok)
		}
	}
}

func TestEndedByUserIgnoresAnOpenDialogAndANewTurn(t *testing.T) {
	// The dialog is still open; the user sent a prompt after cancelling one.
	for _, name := range []string{"dialog-open", "declined-then-working"} {
		if last, ok := EndedByUser(screen(t, name)); ok {
			t.Errorf("%s: %q", name, last)
		}
	}
}

func TestEndedByUserNeedsThePromptBoxAtTheBottom(t *testing.T) {
	declined := screen(t, "declined")
	rule := strings.Repeat("─", 40)
	for name, s := range map[string]string{
		"empty":            "",
		"no box":           "● User declined to answer questions\n",
		"text below":       declined + "more\nlines\nprinted\nbelow\n",
		"box without ❯":    "● User declined to answer questions\n" + rule + "\n> \n" + rule + "\n",
		"box without rule": "● User declined to answer questions\n❯ \n" + rule + "\nfooter\n",
		"short rule":       "● User declined to answer questions\n────\n❯ \n────\n",
	} {
		if last, ok := EndedByUser(s); ok {
			t.Errorf("%s: %q", name, last)
		}
	}
	if _, ok := EndedByUser("● User declined to answer questions\n" + rule + "\n❯ draft\n  of a prompt\n" + rule + "\n  footer\n\n\n"); !ok {
		t.Error("a draft in the prompt box and blank lines below still count")
	}
}
