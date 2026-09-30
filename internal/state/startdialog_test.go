package state

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The screens are captures of Claude Code 2.1.285 at the start of a session
// (tmux capture-pane -p), the working directory renamed.
func TestStartDialogReadsClaudesOwnDialogOffTheScreen(t *testing.T) {
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	ask, ok := StartDialog(read("screen-start-auto-mode.txt"))
	want := &Ask{
		Description: "Auto mode lets Claude handle permission prompts automatically. Claude checks each tool call for risky actions and prompt injection before executing, runs the ones it assesses as lower-risk, and blocks the rest.",
		Questions: []AskQuestion{{Question: "Make auto mode your default permission mode?", Options: []AskOption{
			{Label: "Yes, set auto mode as my default permission mode"}, {Label: "No, keep bypass permissions"}}}},
	}
	if !ok || !reflect.DeepEqual(ask, want) {
		t.Fatalf("auto mode: %v %+v", ok, ask)
	}
	// The cursor on the second option: the same dialog.
	moved := strings.Replace(strings.Replace(read("screen-start-auto-mode.txt"), "❯ Yes, set", "  Yes, set", 1), "  No, keep", "❯ No, keep", 1)
	if ask, ok := StartDialog(moved); !ok || !reflect.DeepEqual(ask, want) {
		t.Fatalf("cursor moved: %v %+v", ok, ask)
	}
	ask, ok = StartDialog(read("screen-start-trust.txt"))
	if !ok || ask.Questions[0].Question != "Accessing workspace:" || !strings.Contains(ask.Description, "Is this a project you created or one you trust?") ||
		!reflect.DeepEqual(ask.Questions[0].Options, []AskOption{{Label: "No, exit"}, {Label: "Yes, I trust this folder"}}) {
		t.Fatalf("trust: %v %+v", ok, ask)
	}
	// Claude at its prompt box, just started: no dialog.
	if ask, ok := StartDialog(read("screen-start-ready.txt")); ok {
		t.Fatalf("ready: %+v", ask)
	}
	for _, s := range []string{"", "\n\n", "fake claude: ready\n", "────────────\n Title\n\n ❯ only one option\n"} {
		if ask, ok := StartDialog(s); ok {
			t.Errorf("%q: %+v", s, ask)
		}
	}
}

// No screen of a session past its start is taken for a start dialog, the
// dialog of a turn included, which the hooks report.
func TestStartDialogIsNoScreenOfATurn(t *testing.T) {
	names, _ := filepath.Glob(filepath.Join("testdata", "screen-*.txt"))
	if len(names) < 20 {
		t.Fatalf("only %d screens", len(names))
	}
	for _, name := range names {
		b, _ := os.ReadFile(name)
		_, ok := StartDialog(WithoutHints(string(b)))
		base := filepath.Base(name)
		dialog := strings.HasPrefix(base, "screen-start-") && base != "screen-start-ready.txt"
		if ok != dialog {
			t.Errorf("%s: dialog %v, want %v", base, ok, dialog)
		}
	}
}
