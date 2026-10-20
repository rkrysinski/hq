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
// its own, the dialog of a turn included, which the hooks report. An MCP
// elicitation, the agent's or a subagent's, is drawn as Claude's own are
// (Claude Code 2.1.301, #70), and read as one: the hooks report it too,
// which makes it theirs in the dialog verdict.
func TestOwnDialogIsNoScreenOfATurn(t *testing.T) {
	names, _ := filepath.Glob(filepath.Join("testdata", "screen-*.txt"))
	if len(names) < 20 {
		t.Fatalf("only %d screens", len(names))
	}
	for _, name := range names {
		b, _ := os.ReadFile(name)
		_, ok := OwnDialog(WithoutHints(string(b)))
		base := filepath.Base(name)
		dialog := strings.HasPrefix(base, "screen-own-") || strings.HasSuffix(base, "elicit-open.txt")
		if ok != dialog {
			t.Errorf("%s: dialog %v, want %v", base, ok, dialog)
		}
	}
}

// /model opened while a subagent runs in the background (Claude Code
// 2.1.301, #70): the picker hides the agents' list below the footer.
func TestOwnDialogSeesTheModelPickerOverBackgroundWork(t *testing.T) {
	ask, ok := OwnDialog(readScreen(t, "screen-own-model-background.txt"))
	if !ok || ask.Questions[0].Question != "Select model" || ask.Questions[0].Options[1].Label != "Opus 5.5 For complex work and everyday tasks" {
		t.Fatalf("model: %v %+v", ok, ask)
	}
	if _, ok := OwnDialog(readScreen(t, "screen-fs-model-closed-background.txt")); ok {
		t.Fatal("closed with Esc: still a dialog")
	}
}

// Where the cursor of a dialog of Claude's own stands, and whether its list
// shows every option, as hq answer needs them (#120). Claude Code 2.1.304
// draws "Change effort level?" and "Switch model?" (/model NAME) after a
// turn as 2.1.300 did; Down moves the ❯. The picker of /model scrolls.
func TestViewOwnDialogSaysWhereItsCursorIsAndWhetherItsListScrolls(t *testing.T) {
	for _, tc := range []struct {
		screen   string
		question string
		cursor   int
		scrolls  bool
	}{
		{"screen-own-effort.txt", "Change effort level?", 0, false},
		{"screen-own-effort-low.txt", "Change effort level?", 0, false},
		{"screen-own-effort-low-down.txt", "Change effort level?", 1, false},
		{"screen-own-switch-model.txt", "Switch model?", 0, false},
		{"screen-own-switch-model-down.txt", "Switch model?", 1, false},
		{"screen-own-auto-mode.txt", "Make auto mode your default permission mode?", 0, false},
		{"screen-own-trust.txt", "Accessing workspace:", 0, false},
		{"screen-own-model.txt", "Select model", 0, true},
		{"screen-own-model-2304.txt", "Select model", 1, true},
		{"screen-own-model-background.txt", "Select model", 0, true},
	} {
		v, ok := ViewOwnDialog(readScreen(t, tc.screen))
		if !ok || v.Ask.Questions[0].Question != tc.question || v.Cursor != tc.cursor || v.Scrolls != tc.scrolls {
			t.Errorf("%s: %v cursor %d scrolls %v %+v", tc.screen, ok, v.Cursor, v.Scrolls, v.Ask)
			continue
		}
		if ask, _ := OwnDialog(readScreen(t, tc.screen)); !reflect.DeepEqual(ask, v.Ask) {
			t.Errorf("%s: OwnDialog %+v, view %+v", tc.screen, ask, v.Ask)
		}
	}
	effort := readScreen(t, "screen-own-effort-low.txt")
	if a, b := mustView(t, effort), mustView(t, readScreen(t, "screen-own-effort-low-down.txt")); !reflect.DeepEqual(a.Ask, b.Ask) {
		t.Errorf("cursor moved, another dialog: %+v %+v", a.Ask, b.Ask)
	}
	// Answered: the box is back, with what Claude did.
	for _, name := range []string{"screen-effort-low-set.txt", "screen-switch-model-kept.txt"} {
		if v, ok := ViewOwnDialog(readScreen(t, name)); ok {
			t.Errorf("%s: %+v", name, v)
		}
	}
}

func mustView(t *testing.T, screen string) OwnView {
	t.Helper()
	v, ok := ViewOwnDialog(screen)
	if !ok {
		t.Fatalf("no dialog:\n%s", screen)
	}
	return v
}

// A dialog of Claude's own takes one answer, one of its options by its
// number as hq read lists them; anything else is refused before a key is
// pressed (#120).
func TestOwnViewPickTakesOneOptionByItsNumber(t *testing.T) {
	v := mustView(t, readScreen(t, "screen-own-effort-low.txt"))
	if n, err := v.Pick([]Answer{{Options: []int{2}}}); err != nil || n != 1 {
		t.Fatalf("2: %d %v", n, err)
	}
	for _, tc := range []struct {
		answers []Answer
		want    string
	}{
		{nil, "the dialog has 1 question, 0 answers given: one per question, in its order"},
		{[]Answer{{Options: []int{1}}, {Options: []int{2}}}, "the dialog has 1 question, 2 answers given: one per question, in its order"},
		{[]Answer{{Options: []int{1, 2}}}, "Change effort level? takes one option, 2 given"},
		{[]Answer{{Options: []int{3}}}, "Change effort level? has no option 3: it has 2"},
		{[]Answer{{Options: []int{0}}}, "Change effort level? has no option 0: it has 2"},
		{[]Answer{{Text: "yes"}}, "Change effort level? takes an option's number, not text: it is a dialog of Claude's own"},
		{[]Answer{{Options: []int{1}, Text: "yes"}}, "Change effort level? takes an option's number, not text: it is a dialog of Claude's own"},
	} {
		if _, err := v.Pick(tc.answers); err == nil || err.Error() != tc.want {
			t.Errorf("%+v: %v, want %q", tc.answers, err, tc.want)
		}
	}
}
