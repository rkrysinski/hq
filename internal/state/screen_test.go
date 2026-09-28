package state

import (
	"os"
	"strings"
	"testing"
)

// The screens in testdata are Claude Code 2.1.283 in a tmux pane, as
// capture-pane shows them (screen-declined-80 with its blank lines left out).
// The -80 ones come from the v0.1.0 end-to-end test, the rest are 100x30.
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

func TestEndedByUserSeesAnInterruptedTurn(t *testing.T) {
	// Esc while Claude writes or before its tool starts (which puts the
	// prompt back in the box); Esc or No on a permission prompt.
	for _, name := range []string{"interrupted", "interrupted-80", "interrupted-prompt-restored", "permission-esc", "permission-no"} {
		if last, ok := EndedByUser(screen(t, name)); !ok || last != "Interrupted" {
			t.Errorf("%s: %q %v", name, last, ok)
		}
	}
}

func TestEndedByUserIgnoresAnOpenDialogAndANewTurn(t *testing.T) {
	// The dialog is still open; the user sent a prompt after cancelling or
	// interrupting one; Claude is at work.
	for _, name := range []string{"dialog-open", "declined-then-working", "interrupted-then-working", "working"} {
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
	busy := "  ⎿  Interrupted · What should Claude do instead?\n" + rule + "\n❯ \n" + rule + "\n  ⏵⏵ bypass permissions on · esc to interrupt\n"
	if last, ok := EndedByUser(busy); ok {
		t.Errorf("a turn at work: %q", last)
	}
	if _, ok := EndedByUser("● User declined to answer questions\n" + rule + "\n❯ draft\n  of a prompt\n" + rule + "\n  footer\n\n\n"); !ok {
		t.Error("a draft in the prompt box and blank lines below still count")
	}
}

func TestAtRestSeesARewoundTurn(t *testing.T) {
	// Esc about 2.5 s into a no-tool turn (Claude Code 2.1.283, #99): the
	// prompt is back in the box, after an earlier turn or as the session's
	// first; then the user cleared the box.
	const sent = "Write a 200-word poem about the sea, no tools."
	for _, name := range []string{"rewound", "rewound-first", "rewound-cleared"} {
		ok, restored := AtRest(screen(t, name), sent)
		if !ok || restored != (name != "rewound-cleared") {
			t.Errorf("%s: at rest %v, prompt restored %v", name, ok, restored)
		}
		if last, ok := EndedByUser(screen(t, name)); ok {
			t.Errorf("%s: ended by the user: %q", name, last)
		}
	}
	// Whitespace aside, the box holds what the hooks reported.
	if _, restored := AtRest(screen(t, "rewound"), "Write a 200-word poem\n about the sea,  no tools."); !restored {
		t.Error("the prompt with other whitespace")
	}
	// Another prompt was reported: the text is the user's own.
	if ok, _ := AtRest(screen(t, "rewound"), "Say hi."); ok {
		t.Error("a draft that is not the reported prompt")
	}
}

func TestAtRestIsFalseForScreensOfWorkingTurnsThatShowActivity(t *testing.T) {
	// Real screens of turns at work: a tool running, compaction, streaming,
	// the user typing or with a menu open (both drop Esc to interrupt from
	// the footer), a narrow pane with a spinner (its footer drops it too).
	for name, sent := range map[string]string{
		"working":                  "",
		"tool-running":             "Run this exact shell command",
		"compacting":               "",
		"streaming":                "Write a 120-word poem about the sea, no tools.",
		"typing-while-working":     "Write a 60-word poem about snow, no tools.",
		"menu-while-working":       "Write a 60-word poem about snow, no tools.",
		"streaming-typing":         "Write a 60-word poem about snow, no tools.",
		"dialog-open":              "",
		"interrupted-then-working": "",
	} {
		if ok, _ := AtRest(screen(t, name), sent); ok {
			t.Errorf("%s: at rest", name)
		}
	}
}

func TestAtRestCannotTellANarrowStreamingTurnFromOneScreen(t *testing.T) {
	// 50 columns: the footer drops Esc to interrupt. With its spinner
	// showing, the turn is at work; while its reply streams into an empty
	// box there is no spinner either, and one screen looks at rest: that it
	// keeps changing while Claude works tells them apart (design §3.4).
	const sent = "Write a 100-word poem about fire, no tools."
	if ok, _ := AtRest(screen(t, "narrow-working"), sent); ok {
		t.Error("narrow, spinner showing: at rest")
	}
	if ok, restored := AtRest(screen(t, "narrow-streaming"), sent); !ok || restored {
		t.Errorf("narrow, streaming: at rest %v, restored %v", ok, restored)
	}
}

func TestPutBackTellsARewindAfterAnEndedTurnFromAnInterruptedTurn(t *testing.T) {
	// A rewind right after a cancelled dialog (#99): the declined line
	// above the box is the dialog's turn's.
	if !PutBack(screen(t, "rewound-after-declined"), "Write a 300-word poem about the sea.") {
		t.Error("rewound after declined")
	}
	// Interrupted before its tool started: the prompt is back in the box,
	// its sent copy scrolled off; the prompt box holds something else; a
	// rewind after a turn that ended normally, which no line ends.
	for name, reported := range map[string]string{
		"interrupted-prompt-restored": "Run the shell command sleep 20 and then reply with the word slept. Do not edit any files.",
		"declined":                    "Pick a colour",
		"rewound-after-declined":      "Something else",
	} {
		if PutBack(screen(t, name), reported) {
			t.Errorf("%s", name)
		}
	}
	rule := strings.Repeat("─", 40)
	sent := "❯ Write a long prompt that\n  wraps\n  ⎿  Interrupted · What should Claude do instead?\n" + rule + "\n❯ Write a long prompt that wraps\n" + rule + "\n  footer\n"
	if PutBack(sent, "Write a long prompt that wraps") {
		t.Error("an interrupted turn whose prompt shows sent above the box")
	}
}
