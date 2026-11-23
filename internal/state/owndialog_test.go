package state

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
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
	// Claude Code's cursor on the second option: the same dialog.
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
// 2.1.301, #70): the picker hides Claude's agents list below the footer.
func TestOwnDialogSeesTheModelPickerOverBackgroundWork(t *testing.T) {
	ask, ok := OwnDialog(readScreen(t, "screen-own-model-background.txt"))
	if !ok || ask.Questions[0].Question != "Select model" || ask.Questions[0].Options[1].Label != "Opus 5.5 For complex work and everyday tasks" {
		t.Fatalf("model: %v %+v", ok, ask)
	}
	if _, ok := OwnDialog(readScreen(t, "screen-fs-model-closed-background.txt")); ok {
		t.Fatal("closed with Esc: still a dialog")
	}
}

// Where Claude Code's cursor stands in a dialog of Claude's own, and whether
// its list shows every option, as hq answer needs them (#120). Claude Code
// 2.1.304 draws "Change effort level?" and "Switch model?" (/model NAME) after
// a turn as 2.1.300 did; Down moves the ❯. The picker of /model scrolls.
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
		// The picker as the first prompt (Claude Code 2.1.329, #220): its
		// list scrolls, so hq answer refuses it (#253).
		{"screen-own-first-model-2329.txt", "Select model", 4, true},
		{"screen-own-first-model-2329-80.txt", "Select model", 2, true},
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
	// Answered: the box is back, with what Claude did. The slider /effort
	// opens with Claude Code 2.1.329 marks no option with ❯: it is no
	// dialog hq reads, or answers (#220).
	for _, name := range []string{"screen-effort-low-set.txt", "screen-switch-model-kept.txt", "screen-first-effort-2329-open.txt", "screen-first-effort-2329-80-open.txt"} {
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

// agentsScreens are the captures of #225 (Claude Code 2.1.326, capture setup M
// in testdata/README.md): a session whose turn ended with two background
// subagents running, at three pane sizes, with a footer of its own and with
// the notice line above it. In the main view Claude's agents list stands below
// the footer with Claude Code's cursor off it (list), on main (main), on the
// first subagent's row of it (first) or on the last one's (last); Enter on the
// last opens its view, with Claude Code's cursor on its row (view-last), on
// the other's (view-first) or on main (view-main).
func agentsScreens(t *testing.T) map[string]string {
	t.Helper()
	screens := map[string]string{}
	for _, state := range []string{"list", "main", "first", "last", "view-last", "view-first", "view-main"} {
		for _, pane := range []string{"100", "80", "40"} {
			for _, footer := range []string{"", "-notice"} {
				name := "screen-agents-" + state + "-" + pane + footer + ".txt"
				screens[name] = readScreen(t, name)
			}
		}
	}
	return screens
}

// Claude's agents list, which Claude Code draws below its footer while
// subagents run, with Claude Code's cursor on it anywhere, and a subagent's
// view open in the pane, are no dialog of Claude's own: the rows of Claude's
// agents list are no options, the status line no question (#225).
func TestViewOwnDialogIsNoClaudesAgentsListNorASubagentsView(t *testing.T) {
	for name, s := range agentsScreens(t) {
		for _, screen := range []string{s, WithoutHints(s)} {
			if v, ok := ViewOwnDialog(screen); ok || v.Ask != nil {
				t.Errorf("%s: a dialog %+v", name, v.Ask)
			}
		}
	}
}

// /model opened while two subagents run in the background, in the main
// view (Claude Code 2.1.326, #225): the picker stands in place of the box,
// and of Claude's agents list, which it hides in a pane of 30 lines and of
// 50 alike; Esc puts the box and that list back.
func TestViewOwnDialogSeesTheModelPickerOverClaudesAgentsList(t *testing.T) {
	for _, name := range []string{"screen-own-model-agents.txt", "screen-own-model-agents-50.txt"} {
		v, ok := ViewOwnDialog(readScreen(t, name))
		if !ok || v.Ask.Questions[0].Question != "Select model" || v.Cursor != 4 ||
			v.Ask.Questions[0].Options[4].Label != "Haiku 5.5 ✔ Fastest for quick answers" {
			t.Errorf("%s: %v %+v", name, ok, v)
		}
	}
	if _, ok := ViewOwnDialog(readScreen(t, "screen-agents-model-closed.txt")); ok {
		t.Fatal("closed with Esc: still a dialog")
	}
}

// The picker of /model since Claude Code 2.1.326 ends its list in a row
// with no number that opens the rest of it, ▸ More models, and shows ▾ on
// that row once it is open, with a line ↑ 2 more above the options and
// ↓ 7 more below them (2.1.329, #253; Claude Code's cursor on a model
// below it, 2.1.330, #330): the list scrolls, and none of those lines is
// an option, wherever Claude Code's cursor is, at 100 columns and at 40.
func TestViewOwnDialogSeesThatTheModelPickerOfMoreModelsScrolls(t *testing.T) {
	models := []string{
		"Default (recommended) Opus 5.5 · Best for everyday, complex tasks",
		"Opus 5.5 For complex work and everyday tasks",
		"Fable 5.2 For your toughest challenges",
		"Sonnet 5.5 Most efficient for simpler tasks",
		"Haiku 5.5 ✔ Fastest for quick answers",
	}
	opened := []string{models[2], models[3], models[4],
		"Fable 5.1 Most capable for your hardest and longest-running tasks", "Haiku 4.5 Fastest for quick answers"}
	text2326 := "Switch between Claude models. Your pick becomes the default for new sessions. For other or older models, use --model."
	text2329 := "Your pick is the default for new sessions. For other models, use --model."
	for _, tc := range []struct {
		name, screen, text string
		wrapped            bool // at 40 columns: the options' wrapped lines are read as options (#321), not checked
		cursor             int  // -1: on no option
		options            []string
	}{
		{name: "screen-own-model-agents.txt", text: text2326, cursor: 4, options: models},
		{name: "screen-own-model-agents-50.txt", text: text2326, cursor: 4, options: models},
		{name: "screen-own-model-2329.txt", text: text2329, cursor: 4, options: models},
		{name: "screen-own-model-2329-more.txt", text: text2329, cursor: -1, options: models},
		{name: "screen-own-model-2329-opened.txt", text: text2329, cursor: -1, options: opened},
		{name: "screen-own-model-2330-below.txt", text: text2329, cursor: 3, options: opened},
		{name: "screen-own-model-2329-40.txt", text: text2329, wrapped: true},
	} {
		if tc.screen == "" {
			tc.screen = readScreen(t, tc.name)
		}
		v, ok := ViewOwnDialog(tc.screen)
		if !ok || !v.Scrolls || v.Ask.Questions[0].Question != "Select model" || v.Ask.Description != tc.text {
			t.Errorf("%s: %v %+v", tc.name, ok, v)
			continue
		}
		var labels []string
		for _, o := range v.Ask.Questions[0].Options {
			labels = append(labels, o.Label)
			if strings.Contains(o.Label, "More models") || strings.HasSuffix(o.Label, " more") {
				t.Errorf("%s: option %q", tc.name, o.Label)
			}
		}
		if !tc.wrapped && (v.Cursor != tc.cursor || !slices.Equal(labels, tc.options)) {
			t.Errorf("%s: cursor %d, options %q", tc.name, v.Cursor, labels)
		}
	}
}
