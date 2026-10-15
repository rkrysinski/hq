package state

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func readScreen(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The screens are captures of Claude Code 2.1.285 at the start of a session
// (tmux capture-pane -p), the working directory renamed.
func TestOwnDialogReadsClaudesDialogAtTheStartOffTheScreen(t *testing.T) {
	ask, ok := OwnDialog(readScreen(t, "screen-own-auto-mode.txt"))
	want := &Ask{
		Description: "Auto mode lets Claude handle permission prompts automatically. Claude checks each tool call for risky actions and prompt injection before executing, runs the ones it assesses as lower-risk, and blocks the rest.",
		Questions: []AskQuestion{{Question: "Make auto mode your default permission mode?", Options: []AskOption{
			{Label: "Yes, set auto mode as my default permission mode"}, {Label: "No, keep bypass permissions"}}}},
	}
	if !ok || !reflect.DeepEqual(ask, want) {
		t.Fatalf("auto mode: %v %+v", ok, ask)
	}
	// The cursor on the second option: the same dialog.
	moved := strings.Replace(strings.Replace(readScreen(t, "screen-own-auto-mode.txt"), "❯ Yes, set", "  Yes, set", 1), "  No, keep", "❯ No, keep", 1)
	if ask, ok := OwnDialog(moved); !ok || !reflect.DeepEqual(ask, want) {
		t.Fatalf("cursor moved: %v %+v", ok, ask)
	}
	ask, ok = OwnDialog(readScreen(t, "screen-own-trust.txt"))
	if !ok || ask.Questions[0].Question != "Accessing workspace:" || !strings.Contains(ask.Description, "Is this a project you created or one you trust?") ||
		!reflect.DeepEqual(ask.Questions[0].Options, []AskOption{{Label: "No, exit"}, {Label: "Yes, I trust this folder"}}) {
		t.Fatalf("trust: %v %+v", ok, ask)
	}
	// Claude at its prompt box, just started: no dialog.
	if ask, ok := OwnDialog(readScreen(t, "screen-start-ready.txt")); ok {
		t.Fatalf("ready: %+v", ask)
	}
	for _, s := range []string{"", "\n\n", "fake claude: ready\n", "────────────\n Title\n\n ❯ only one option\n"} {
		if ask, ok := OwnDialog(s); ok {
			t.Errorf("%q: %+v", s, ask)
		}
	}
}

// The screens are captures of Claude Code 2.1.300 after a turn (#66), 160
// and 80 columns wide: /effort max opened its effort dialog, /model its
// picker. They are drawn under a rule of ▔, which may carry text.
func TestOwnDialogReadsClaudesDialogAfterATurnOffTheScreen(t *testing.T) {
	effort := &Ask{
		Description: "Your next response will be slower and use more tokens This conversation is cached for the current effort level. Switching to max means the full history gets re-read on your next message.",
		Questions:   []AskQuestion{{Question: "Change effort level?", Options: []AskOption{{Label: "Yes, switch to max"}, {Label: "No, go back"}}}},
	}
	for _, name := range []string{"screen-own-effort.txt", "screen-own-effort-80.txt"} {
		if ask, ok := OwnDialog(readScreen(t, name)); !ok || !reflect.DeepEqual(ask, effort) {
			t.Errorf("%s: %v %+v", name, ok, ask)
		}
	}
	ask, ok := OwnDialog(readScreen(t, "screen-own-model.txt"))
	if !ok || ask.Questions[0].Question != "Select model" || !strings.HasPrefix(ask.Description, "Switch between Claude models.") {
		t.Fatalf("model: %v %+v", ok, ask)
	}
	// The options as they read, without their numbers and the arrow that
	// says the list scrolls; "… +3 models" is none.
	opts := ask.Questions[0].Options
	if len(opts) != 10 || opts[0].Label != "Default (recommended) ✔ Opus 5.5 · Best for everyday, complex tasks" || opts[9].Label != "Opus 4.8 Best for everyday, complex tasks" {
		t.Fatalf("model options: %+v", opts)
	}
}

// No screen of Claude at its prompt box or at work is taken for a dialog of
// its own, the dialog of a turn included, which the hooks report.
func TestOwnDialogIsNoScreenOfATurn(t *testing.T) {
	names, _ := filepath.Glob(filepath.Join("testdata", "screen-*.txt"))
	if len(names) < 20 {
		t.Fatalf("only %d screens", len(names))
	}
	for _, name := range names {
		b, _ := os.ReadFile(name)
		_, ok := OwnDialog(WithoutHints(string(b)))
		base := filepath.Base(name)
		dialog := strings.HasPrefix(base, "screen-own-")
		if ok != dialog {
			t.Errorf("%s: dialog %v, want %v", base, ok, dialog)
		}
	}
}
