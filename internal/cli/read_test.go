package cli

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
)

func readFakes() *fakes {
	f := newLsFakes()
	f.tmux.windows = []tmux.Window{
		{ID: "@0", Name: "hq", Options: map[string]string{}},
		agentWindow("@4", "42", "/w/app", f.now.Add(-time.Hour), false),
	}
	return f
}

func TestReadShowsTheAgentInFull(t *testing.T) {
	f := readFakes()
	reply := "Migration done.\n\nNext:\n- the API\n- the PR, a long line that hq ls would cut to its column long before it ends here"
	f.states["id-42"] = state.Report{State: state.Working, Since: f.now.Add(-3 * time.Minute), Last: state.Clean(reply),
		Branch: "feat/42-x", Cwd: `F:\w\app\.claude\worktrees\x`}
	f.details["id-42"] = state.Detail{Reply: reply}
	f.inbox["id-42"] = []string{"use the new API", "and add a test (now)"} // two messages wait (hq send)
	code, out, errOut := f.run("read", "42")
	if code != 0 {
		t.Fatalf("exit %d %q", code, errOut)
	}
	want := `name      42
repo      app  /w/app
branch    feat/42-x
worktree  /w/app/.claude/worktrees/x
sandbox   claude-x
state     working 3m
pending   2

reply
  Migration done.

  Next:
  - the API
  - the PR, a long line that hq ls would cut to its column long before it ends here
`
	if out != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
}

func TestReadSaysWhenThereIsNoReply(t *testing.T) {
	f := readFakes()
	code, out, _ := f.run("read", "42")
	// Not reported yet: starting, in the repository, nothing to show.
	want := "name      42\nrepo      app  /w/app\nbranch    -\nworktree  /w/app\nsandbox   claude-x\nstate     starting 1h\npending   0\n\nreply     none\n"
	if code != 0 || out != want {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestReadShowsWhatTheAgentAsks(t *testing.T) {
	since := time.Minute
	for _, tc := range []struct {
		name   string
		report state.Report
		detail state.Detail
		want   string
	}{
		{"question dialog", state.Report{State: state.NeedsInput, Last: "Which colour?"},
			state.Detail{Ask: &state.Ask{Tool: "AskUserQuestion", Questions: []state.AskQuestion{
				{Header: "Colour", Question: "Which colour?", Options: []state.AskOption{{Label: "Red", Description: "Pick red."}, {Label: "Blue"}}},
				{Question: "Which sizes?", MultiSelect: true, Options: []state.AskOption{{Label: "S"}, {Label: "M"}}},
			}}},
			"last      Which colour?\n\nasks      dialog ID\n  Colour: Which colour?\n    1. Red - Pick red.\n    2. Blue\n  Which sizes? (any of)\n    1. S\n    2. M\n\nanswer    hq answer 42 --dialog ID N N,N\n"},
		{"permission prompt", state.Report{State: state.NeedsInput, Last: "Bash: Create a file"},
			state.Detail{Ask: &state.Ask{Tool: "Bash", Description: "Create a file", Command: "touch /tmp/x"}},
			"last      Bash: Create a file\n\nasks      dialog ID\n  Bash: touch /tmp/x\n  Create a file\n"},
		{"permission without a command", state.Report{State: state.NeedsInput, Last: "WebSearch: Look it up"},
			state.Detail{Ask: &state.Ask{Tool: "WebSearch", Description: "Look it up"}},
			"last      WebSearch: Look it up\n\nasks      dialog ID\n  WebSearch: Look it up\n"},
		{"notification", state.Report{State: state.NeedsInput, Last: "Claude needs your permission"},
			state.Detail{Ask: &state.Ask{Message: "Claude needs your permission"}},
			"last      Claude needs your permission\n\nasks      dialog ID\n  Claude needs your permission\n"},
		{"MCP form", state.Report{State: state.NeedsInput, Last: "Which color should I use?"},
			state.Detail{Ask: &state.Ask{Server: "elicit", Message: "Which color should I use?", Fields: []state.AskField{
				{Name: "color", Title: "Color", Description: "Pick one", Type: "string", Required: true, Options: []state.AskFieldOption{{Value: "red"}, {Value: "blue"}}},
				{Name: "name", Title: "Name", Type: "string"},
				{Name: "engine", Type: "string", Options: []state.AskFieldOption{{Value: "pg", Title: "PostgreSQL"}, {Value: "my", Title: "MySQL"}}, Default: "pg"},
				{Name: "size", Type: "integer", Required: true, Default: float64(1000000)},
				{Name: "backup", Type: "boolean", Default: false, Description: "Keep a daily copy of every table, off site, for thirty days"},
			}}},
			"last      Which color should I use?\n\nasks      dialog ID\n  elicit asks: Which color should I use?\n" +
				"    color (Color, required): red | blue - Pick one\n    name (Name): text\n    engine: PostgreSQL | MySQL [default: PostgreSQL]\n" +
				"    size (required): integer [default: 1000000]\n    backup: boolean [default: false] - Keep a daily copy of eve…\n"},
		{"MCP URL", state.Report{State: state.NeedsInput, Last: "Sign in to the example service."},
			state.Detail{Ask: &state.Ask{Server: "elicit", Message: "Sign in to the example service.", URL: "https://example.com/sign-in?session=42"}},
			"last      Sign in to the example service.\n\nasks      dialog ID\n  elicit asks: Sign in to the example service.\n    url: https://example.com/sign-in?session=42\n"},
		// A URL is printed in full, however long: cut, it would hide where it
		// sends the user.
		{"MCP long URL", state.Report{State: state.NeedsInput, Last: "Sign in."},
			state.Detail{Ask: &state.Ask{Server: "elicit", Message: "Sign in.", URL: "https://auth.example.com/oauth/authorize?client_id=hq-agent&redirect_uri=https%3A%2F%2Fexample.com%2Fcb&state=42"}},
			"last      Sign in.\n\nasks      dialog ID\n  elicit asks: Sign in.\n    url: https://auth.example.com/oauth/authorize?client_id=hq-agent&redirect_uri=https%3A%2F%2Fexample.com%2Fcb&state=42\n"},
		{"question", state.Report{State: state.Question, Last: "Done. Shall I open the PR?"},
			state.Detail{Reply: "Done.\n\nShall I open the PR?"},
			"\nasks\n  Shall I open the PR?\n"}, // LAST is the reply, not repeated
	} {
		f := readFakes()
		tc.report.Since = f.now.Add(-since)
		f.states["id-42"] = tc.report
		f.details["id-42"] = tc.detail
		code, out, errOut := f.run("read", "42")
		head := "state     " + tc.report.State + " 1m\npending   0\n"
		_, rest, _ := strings.Cut(out, head)
		rest, _, _ = strings.Cut(rest, "\nreply")
		want := strings.ReplaceAll(tc.want, "dialog ID", "dialog "+state.DialogID(tc.report.Since))
		if code != 0 || rest != strings.TrimSuffix(want, "\n")+"\n" {
			t.Errorf("%s: exit %d %q\n%s", tc.name, code, errOut, out)
		}
	}
	// Once the agent works on, a dialog it answered asks nothing.
	f := readFakes()
	f.states["id-42"] = state.Report{State: state.Working, Since: f.now}
	f.details["id-42"] = state.Detail{Ask: &state.Ask{Message: "Claude needs your permission"}}
	if _, out, _ := f.run("read", "42"); strings.Contains(out, "\nasks") {
		t.Fatalf("a working agent asks:\n%s", out)
	}
}

func TestReadShowsAnEndedTurnsLastMessageBesideTheReply(t *testing.T) {
	f := readFakes()
	f.tmux.windows[1].PaneDead = true
	f.states["id-42"] = state.Report{State: state.Done, Since: f.now.Add(-time.Minute), Last: "PR #58 opened."}
	f.details["id-42"] = state.Detail{Reply: "PR #58 opened."}
	_, out, _ := f.run("read", "42")
	if !strings.Contains(out, "state     ended") || strings.Contains(out, "\nlast ") {
		t.Fatalf("the last message is the reply:\n%s", out)
	}
	f.states["id-42"] = state.Report{State: state.Done, Since: f.now.Add(-time.Minute)}
	f.details["id-42"] = state.Detail{}
	if _, out, _ := f.run("read", "42"); !strings.Contains(out, "last      [session ended]\n") {
		t.Fatalf("no reply:\n%s", out)
	}
}

func TestReadJSONHasWhatLsHasAndTheAgentInFull(t *testing.T) {
	f := readFakes()
	f.states["id-42"] = state.Report{State: state.NeedsInput, Since: f.now.Add(-2 * time.Minute), Last: "Bash: Create a file", Branch: "feat/42-x"}
	f.details["id-42"] = state.Detail{Reply: "Line one.\nLine two.", Ask: &state.Ask{Tool: "Bash", Description: "Create a file", Command: "touch /tmp/x"}}
	f.inbox["id-42"] = []string{"use the new API"}
	code, out, errOut := f.run("read", "--json", "42")
	if code != 0 {
		t.Fatalf("exit %d %q", code, errOut)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatal(err)
	}
	_, lsOut, _ := f.run("ls", "--json")
	var ls struct {
		Agents    []map[string]any `json:"agents"`
		NextSince string           `json:"next_since"`
	}
	if err := json.Unmarshal([]byte(lsOut), &ls); err != nil || len(ls.Agents) != 1 {
		t.Fatal(err)
	}
	if v["next_since"] != ls.NextSince {
		t.Errorf("next_since %v, hq ls has %s", v["next_since"], ls.NextSince)
	}
	for k, want := range ls.Agents[0] {
		if got, _ := json.Marshal(v[k]); string(got) != mustJSON(want) {
			t.Errorf("%s: %s, hq ls has %s", k, got, mustJSON(want))
		}
	}
	for k, want := range map[string]string{
		"pending":  `1`,
		"worktree": `"/w/app"`,
		"reply":    `"Line one.\nLine two."`,
		// It fits one page: no next one.
		"reply_size":  `19`,
		"next_offset": `null`,
		"asks":        `{"tool":"Bash","description":"Create a file","command":"touch /tmp/x"}`,
	} {
		var norm any
		json.Unmarshal([]byte(want), &norm)
		if got := mustJSON(v[k]); got != mustJSON(norm) {
			t.Errorf("%s: %s, want %s", k, got, want)
		}
	}
	// Nothing asked, and no reply yet: null, not missing.
	_ = f.deps().journal.Remove("/w/app", "id-42")
	f.states["id-42"] = state.Report{State: state.Working, Since: f.now}
	f.details["id-42"] = state.Detail{}
	_, out, _ = f.run("read", "42", "--json")
	if !strings.Contains(out, `"asks": null`) || !strings.Contains(out, `"reply": ""`) {
		t.Fatalf("nulls:\n%s", out)
	}
}

// readJSON is hq read --json as its pages are read.
type readJSON struct {
	Reply      string `json:"reply"`
	ReplySize  int    `json:"reply_size"`
	NextOffset *int   `json:"next_offset"`
	NextSince  string `json:"next_since"`
}

// longReply is a reply of 82k characters, as in #64, some of them more
// than a byte long.
func longReply() string {
	return strings.TrimSuffix(strings.Repeat("Recon: the API answers in 120 ms; zażółć gęślą jaźń.\n", 1500), "\n")
}

func TestReplyPageCutsAReplyIntoPagesOfWholeCharacters(t *testing.T) {
	if page, next, err := replyPage("Short.", 0); page != "Short." || next != nil || err != nil {
		t.Fatalf("short: %q %v %v", page, next, err)
	}
	if page, next, err := replyPage("", 0); page != "" || next != nil || err != nil {
		t.Fatalf("none: %q %v %v", page, next, err)
	}
	reply := longReply()
	var got strings.Builder
	pages := 0
	for offset := 0; ; pages++ {
		page, next, err := replyPage(reply, offset)
		if err != nil || len(page) > readPage || !utf8.ValidString(page) {
			t.Fatalf("page at %d: %d bytes, %v", offset, len(page), err)
		}
		got.WriteString(page)
		if next == nil {
			break
		}
		offset = *next
	}
	if got.String() != reply || pages+1 != 3 {
		t.Fatalf("%d pages make %d bytes of %d", pages+1, got.Len(), len(reply))
	}
	// An offset no page gave: past the end, or inside a character.
	inside := strings.Index(reply, "ż") + 1
	for _, offset := range []int{len(reply), len(reply) + 5, inside} {
		if _, _, err := replyPage(reply, offset); err == nil {
			t.Errorf("offset %d taken", offset)
		}
	}
}

func TestReadGivesALongReplyInPagesOfTheSameReply(t *testing.T) {
	f := readFakes()
	reply := longReply()
	replied := f.now.Add(-time.Minute)
	f.states["id-42"] = state.Report{State: state.Done, Since: replied, Last: state.Clean(reply)}
	f.details["id-42"] = state.Detail{Reply: reply, ReplyAt: replied}
	var got strings.Builder
	var first readJSON
	args := []string{"read", "42", "--json"}
	for pages := 1; ; pages++ {
		if pages > 3 {
			t.Fatalf("still reading at page %d: %v", pages, args)
		}
		code, out, errOut := f.run(args...)
		var v readJSON
		if code != 0 || json.Unmarshal([]byte(out), &v) != nil || v.ReplySize != len(reply) {
			t.Fatalf("page %d %v: exit %d %q\n%.300s", pages, args, code, errOut, out)
		}
		if pages == 1 {
			first = v
		}
		got.WriteString(v.Reply)
		if v.NextOffset == nil {
			if pages != 3 || got.String() != reply {
				t.Fatalf("%d pages make %d bytes of %d", pages, got.Len(), len(reply))
			}
			break
		}
		args = []string{"read", "42", "--json", "--offset", strconv.Itoa(*v.NextOffset), "--since", v.NextSince}
		f.now = f.now.Add(time.Second)
	}

	// For a person: which bytes, and the command for the next page, with
	// the read's cursor.
	_, out, _ := f.run("read", "42")
	since := first.NextSince
	page, next, _ := replyPage(reply, 0)
	if !strings.HasPrefix(since, "c") || !strings.Contains(out, fmt.Sprintf("\nreply     bytes 0-%d of %d\n  Recon: ", len(page)-1, len(reply))) || !strings.HasSuffix(out, fmt.Sprintf("\n\nnext      hq read 42 --offset %d --since %s\n", *next, since)) {
		t.Fatalf("text:\n%.200s\n...\n%s", out, out[len(out)-200:])
	}
	second, _, _ := replyPage(reply, *next)
	last := *next + len(second)
	_, out, _ = f.run("read", "42", "--offset", strconv.Itoa(last), "--since", since)
	if !strings.Contains(out, fmt.Sprintf("\nreply     bytes %d-%d of %d\n", last, len(reply)-1, len(reply))) || strings.Contains(out, "\nnext ") {
		t.Fatalf("last page:\n%.200s", out)
	}

	// A moment, as an older hq printed next_since, ties the page to the
	// reply by its time, as before: one taken after the reply gives it.
	before := replied.Add(-time.Second).UTC().Format(time.RFC3339Nano)
	after := replied.Add(time.Second).UTC().Format(time.RFC3339Nano)
	if code, _, errOut := f.run("read", "42", "--offset", strconv.Itoa(*next), "--since", after); code != 0 {
		t.Fatalf("moment after the reply: exit %d %q", code, errOut)
	}
	if code, _, _ := f.run("read", "42", "--offset", strconv.Itoa(*next), "--since", before); code != ExitUsage {
		t.Fatalf("moment before the reply: exit %d", code)
	}

	// The agent replied anew after the page before: its Stop is numbered
	// above the read's cursor, and the next page says so, however close
	// in time the two replies are.
	f.details["id-42"] = state.Detail{Reply: reply + " And one more thing.", ReplyAt: f.now.Add(-time.Minute)}
	f.states["id-42"] = state.Report{State: state.Done, Since: f.now.Add(-time.Minute), Last: "And one more thing."}
	code, out, errOut := f.run("read", "42", "--json", "--offset", strconv.Itoa(*next), "--since", since)
	if code != ExitUsage || out != "" || errOut != "hq: 42 has replied anew since the read that gave the offset: read it again from its start, without --offset\n" {
		t.Fatalf("changed: exit %d %q %q", code, out, errOut)
	}
	// A cursor hq did not give, above the counter, is refused (#166,
	// decision 11).
	if code, _, errOut := f.run("read", "42", "--offset", strconv.Itoa(*next), "--since", "c999999"); code != ExitUsage || !strings.Contains(errOut, "take a new one from hq ls --json") {
		t.Fatalf("a cursor ahead: exit %d %q", code, errOut)
	}
	// A page needs the moment of the read before it, and an offset that read gave.
	f.details["id-42"] = state.Detail{Reply: reply, ReplyAt: f.now.Add(-time.Minute)}
	for args, want := range map[string]string{
		"--offset 32768":             "hq: --offset needs --since: ",
		"--offset x --since 10s":     "hq: --offset 'x' is not a place in the reply",
		"--offset -1 --since 10s":    "hq: --offset '-1' is not a place in the reply",
		"--offset 999999 --since 1s": fmt.Sprintf("hq: --offset 999999 is not where a page of the reply starts (it has %d bytes)", len(reply)),
		"--since 1s --offset":        "hq: --offset needs a value",
	} {
		if code, _, errOut := f.run(append([]string{"read", "42"}, strings.Fields(args)...)...); code != ExitUsage || !strings.HasPrefix(errOut, want) {
			t.Errorf("%s: exit %d %q", args, code, errOut)
		}
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestReadNeedsOneKnownAgent(t *testing.T) {
	f := readFakes()
	// An unknown name fails as hq go does (spec §4.2).
	if code, out, errOut := f.run("read", "nobody"); code != ExitNotFound || out != "" || errOut != "hq: no agent 'nobody' (see hq ls)\n" {
		t.Fatalf("unknown: exit %d %q %q", code, out, errOut)
	}
	const usage = "hq read NAME [--offset N --since SINCE] [--json]"
	for args, want := range map[string]string{
		"read":        "hq: usage: " + usage + "\n",
		"read --json": "hq: usage: " + usage + "\n",
		// The words of hq send and hq wait (#166, #170).
		"read 42 43":      "hq: unexpected argument '43' (usage: " + usage + ")\n",
		"read 42 -y":      "hq: unknown option '-y' (usage: " + usage + ")\n",
		"read 42 --since": "hq: --since needs a value (usage: " + usage + ")\n",
	} {
		if code, _, errOut := f.run(strings.Fields(args)...); code != ExitUsage || errOut != want {
			t.Errorf("%v: exit %d %q, want %q", args, code, errOut, want)
		}
	}
	f.tmux.missing = true
	if code, _, _ := f.run("read", "42"); code != ExitEnvironment {
		t.Fatalf("no tmux: exit %d", code)
	}
}

// Claude's own dialog at the start of a session comes from no hook: hq ls
// and hq read take it from the screen (#29).
func TestReadShowsClaudesDialogAtTheStartOfASession(t *testing.T) {
	f := readFakes()
	f.states["id-42"] = state.Report{State: state.Done, Since: f.now.Add(-time.Minute)}
	f.tmux.windows[1].Pane = "%4"
	rule := strings.Repeat("─", 40)
	f.tmux.screens = map[string]string{"%4": rule + "\n Make auto mode your default permission mode?\n\n   Auto mode lets Claude handle permission prompts automatically.\n\n   ❯ Yes, set auto mode as my default permission mode\n     No, keep bypass permissions\n"}
	code, out, errOut := f.run("read", "42")
	want := "state     needs input 0s\npending   0\nlast      Make auto mode your default permission mode?\n\nasks      dialog " + state.DialogID(f.now) + "\n  Make auto mode your default permission mode?\n    1. Yes, set auto mode as my default permission mode\n    2. No, keep bypass permissions\n\nanswer    hq answer 42 --dialog " + state.DialogID(f.now) + " N\n\nreply     none\n"
	if code != 0 || !strings.HasSuffix(out, want) {
		t.Fatalf("exit %d %q\n%s", code, errOut, out)
	}
	if code, out, _ := f.run("ls"); code != 0 || !strings.Contains(out, "needs input") || !strings.Contains(out, "Make auto mode your default permission mode?") {
		t.Fatalf("ls: exit %d\n%s", code, out)
	}
	var v readView
	if _, out, _ := f.run("read", "42", "--json"); json.Unmarshal([]byte(out), &v) != nil || v.Asks == nil || v.Asks.Description != "Auto mode lets Claude handle permission prompts automatically." || len(v.Asks.Questions[0].Options) != 2 {
		t.Fatalf("json:\n%s", out)
	}
	// Answered: Claude at its prompt, done, asking nothing.
	f.tmux.screens["%4"] = rule + "\n❯ \n" + rule + "\n  footer\n"
	if _, out, _ := f.run("read", "42"); !strings.Contains(out, "state     done 1m\n") || strings.Contains(out, "\nasks") {
		t.Fatalf("answered:\n%s", out)
	}
}

// A dialog Claude opens after a turn, as /effort does, comes from no hook
// either: hq ls and hq read take it from the screen, from the moment hq
// first saw it, and hq send says the message waits for it (#66).
func TestCommandsSeeClaudesDialogAfterATurn(t *testing.T) {
	f := readFakes()
	f.states["id-42"] = state.Report{State: state.Done, Since: f.now.Add(-time.Hour), Last: "hi"}
	f.tmux.windows[1].Options["inbox"] = generation
	f.details["id-42"] = state.Detail{Reply: "hi"}
	f.tmux.windows[1].Pane = "%4"
	rule := strings.Repeat("─", 40)
	f.tmux.screens = map[string]string{"%4": "● hi\n" + strings.Repeat("▔", 40) + "\n   Change effort level?\n   Your next response will be slower and use more tokens\n\n   ❯ 1. Yes, switch to max\n     2. No, go back\n"}
	seen := f.now
	if code, out, _ := f.run("ls"); code != 0 || !strings.Contains(out, "needs input  0s") || !strings.Contains(out, "Change effort level?") {
		t.Fatalf("ls: exit %d\n%s", code, out)
	}
	f.now = f.now.Add(time.Minute)
	code, out, errOut := f.run("read", "42")
	want := "state     needs input 1m\npending   0\nlast      Change effort level?\n\nasks      dialog " + state.DialogID(seen) + "\n  Change effort level?\n    1. Yes, switch to max\n    2. No, go back\n\nanswer    hq answer 42 --dialog " + state.DialogID(seen) + " N\n\nreply\n  hi\n"
	if code != 0 || !strings.HasSuffix(out, want) {
		t.Fatalf("exit %d %q\n%s", code, errOut, out)
	}
	var v readView
	if _, out, _ := f.run("read", "42", "--json"); json.Unmarshal([]byte(out), &v) != nil || v.Asks == nil || v.Asks.Description != "Your next response will be slower and use more tokens" || !v.Since.Equal(seen) {
		t.Fatalf("json:\n%s", out)
	}
	if code, out, _ := f.run("send", "42", "say hi"); code != 0 || out != "queued: 42 waits at a dialog of Claude's own; once it is answered there (hq go 42), delivered with its next prompt\n" {
		t.Fatalf("send: exit %d %q", code, out)
	}
	// Answered: at its prompt again, done since its turn, with its reply.
	f.tmux.screens["%4"] = "● hi\n" + rule + "\n❯ \n" + rule + "\n  footer\n"
	if _, out, _ := f.run("read", "42"); !strings.Contains(out, "state     done 1h\n") || !strings.HasSuffix(out, "reply\n  hi\n") || strings.Contains(out, "\nasks") {
		t.Fatalf("answered:\n%s", out)
	}
}

// A turn the user ended, which hq recorded, leaves the agent at its prompt
// as a finished turn does: a dialog Claude opens there needs input too
// (#66).
func TestADialogAfterATurnTheUserEndedNeedsInput(t *testing.T) {
	f := readFakes()
	since := f.now.Add(-time.Hour)
	f.states["id-42"] = state.Report{State: state.Working, Since: since, Prompt: "go"}
	f.tmux.windows[1].Pane = "%4"
	f.seen("/w/app", "id-42", state.SourceScreen, state.KindTurnEnded, state.Facts{Text: "Interrupted"}, since.Add(time.Minute))
	f.tmux.screens = map[string]string{"%4": "⎿  Interrupted\n" + strings.Repeat("▔", 40) + "\n   Change effort level?\n\n   ❯ 1. Yes, switch to max\n     2. No, go back\n"}
	if code, out, _ := f.run("ls"); code != 0 || !strings.Contains(out, "needs input") || !strings.Contains(out, "Change effort level?") {
		t.Fatalf("ls: exit %d\n%s", code, out)
	}
	// Answered: done with the turn's end again.
	rule := strings.Repeat("─", 40)
	f.tmux.screens["%4"] = "⎿  Interrupted\n" + rule + "\n❯ \n" + rule + "\n  footer\n"
	if code, out, _ := f.run("ls"); code != 0 || !strings.Contains(out, "done") || !strings.Contains(out, "Interrupted") {
		t.Fatalf("answered: exit %d\n%s", code, out)
	}
}

// A dialog the hooks reported, as an MCP elicitation reported only by
// Claude's Notification, is closed by the screen once it shows Claude at
// work: hq read shows no question any more, and hq send no longer holds
// the message for a dialog (#70).
func TestCommandsTakeADialogTheScreenClosedForGone(t *testing.T) {
	f := readFakes()
	f.states["id-42"] = state.Report{State: state.NeedsInput, Since: f.now.Add(-time.Minute), Last: "Claude Code needs your input", LastStop: "On it."}
	f.details["id-42"] = state.Detail{Reply: "On it.", Ask: &state.Ask{Message: "Claude Code needs your input"}}
	f.tmux.windows[1].Options["inbox"] = generation
	f.tmux.windows[1].Pane = "%4"
	rule := strings.Repeat("─", 40)
	box := rule + "\n❯ \n" + rule + "\n  ⏵⏵ bypass permissions on (shift+tab to cycle)\n"
	// Open, the form on the screen: the question shows, the message waits.
	f.tmux.screens = map[string]string{"%4": "● On it.\n" + rule + "\n MCP server “elicit” requests your input\n\n Which colour?\n\n ❯ Colour: red\n   Accept\n   Decline\n"}
	if _, out, _ := f.run("read", "42"); !strings.Contains(out, "state     needs input 1m\n") || !strings.Contains(out, "\nasks      dialog "+state.DialogID(f.now.Add(-time.Minute))+"\n  Claude Code needs your input\n") {
		t.Fatalf("open:\n%s", out)
	}
	if _, out, _ := f.run("send", "42", "hi"); out != "queued: 42 needs input, delivered once its dialog is closed, when it stops\n" {
		t.Fatalf("send open: %q", out)
	}
	// Answered, the tool at work under its spinner.
	f.tmux.screens["%4"] = "● On it.\n· elicit: ask_color… (5s · ↓ 86 tokens)\n" + box
	_, out, _ := f.run("read", "42")
	if !strings.Contains(out, "state     working 1m\n") || strings.Contains(out, "\nasks") || strings.Contains(out, "\nlast ") {
		t.Fatalf("answered:\n%s", out)
	}
	var v readView
	if _, out, _ := f.run("read", "42", "--json"); json.Unmarshal([]byte(out), &v) != nil || v.Asks != nil || v.State != state.Working || v.Last != "On it." {
		t.Fatalf("answered, json:\n%s", out)
	}
	if _, out, _ := f.run("send", "42", "hi"); out != "queued: 42 is working, delivered when it stops\n" {
		t.Fatalf("send answered: %q", out)
	}
	// The reply streams, which shows no spinner: still working.
	f.tmux.screens["%4"] = "● On it.\n● Red it is.\n" + box
	if _, out, _ := f.run("read", "42"); !strings.Contains(out, "state     working 1m\n") || strings.Contains(out, "\nasks") {
		t.Fatalf("streaming:\n%s", out)
	}
}

// A dialog of Claude's own opened at an agent working only on background
// work, as /model, needs input: hq read shows what it asks and hq send
// says the message waits for it (#70).
func TestCommandsSeeClaudesDialogOnBackgroundWork(t *testing.T) {
	f := readFakes()
	f.states["id-42"] = state.Report{State: state.Working, Since: f.now.Add(-time.Hour), Latest: f.now.Add(-time.Minute), Background: true, Last: "It started."}
	f.details["id-42"] = state.Detail{Reply: "It started."}
	f.tmux.windows[1].Options["inbox"] = generation
	f.tmux.windows[1].Pane = "%4"
	f.tmux.screens = map[string]string{"%4": "● It started.\n✻ Waiting for 1 background agent to finish\n" + strings.Repeat("▔", 40) + "\n   Select model\n   Switch between Claude models.\n\n   ❯ 1. Default (recommended)\n     2. Opus 5.5\n"}
	if _, out, _ := f.run("read", "42"); !strings.Contains(out, "state     needs input 0s\n") || !strings.Contains(out, "\nasks      dialog "+state.DialogID(f.now)+"\n  Select model\n    1. Default (recommended)\n    2. Opus 5.5\n") {
		t.Fatalf("read:\n%s", out)
	}
	if _, out, _ := f.run("send", "42", "hi"); out != "queued: 42 waits at a dialog of Claude's own; once it is answered there (hq go 42), delivered with its next prompt\n" {
		t.Fatalf("send: %q", out)
	}
}

// hq read lists the agent's own tasks after its state, one line each, cut
// as LAST is; --json has them in full in own_tasks, as hq ls --json (#78).
func TestReadListsTheAgentsOwnTasks(t *testing.T) {
	f := readFakes()
	long := "until gh pr checks 58; do sleep 30; done && echo all green, and a long tail that hq read cuts"
	f.states["id-42"] = state.Report{State: state.Done, Since: f.now.Add(-time.Minute), Last: "Waiting for the background task to report back.",
		OwnTasks: []state.Task{{Type: "shell", Text: "sleep 120"}, {Type: "shell", Text: "cd app &&\n" + long}, {Type: "monitor", Text: "Wait for CI"}}}
	f.details["id-42"] = state.Detail{Reply: "Waiting for the background task to report back."}
	code, out, errOut := f.run("read", "42")
	if code != 0 {
		t.Fatalf("exit %d %q", code, errOut)
	}
	want := `name      42
repo      app  /w/app
branch    -
worktree  /w/app
sandbox   claude-x
state     done 1m
own task: shell  sleep 120
own task: shell  ` + truncate("cd app && "+long, lastWidth) + `
own task: monitor  Wait for CI
pending   0

reply
  Waiting for the background task to report back.
`
	if out != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
	_, out, _ = f.run("read", "42", "--json")
	var v struct {
		Last     string       `json:"last"`
		OwnTasks []state.Task `json:"own_tasks"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil || v.Last != "Waiting for the background task to report back." || len(v.OwnTasks) != 3 || v.OwnTasks[1].Text != "cd app &&\n"+long {
		t.Fatalf("%v %+v", err, v)
	}
}

// A reply of several megabytes comes whole from the journal: hq read
// --json gives its size, and its pages, the first, one inside and the
// last, are those of the whole reply.
func TestReadGivesAReplyOfSeveralMegabytesInFullAndInPages(t *testing.T) {
	f := readFakes()
	reply := strings.TrimSpace(strings.Repeat("a reply of several megabytes, zażółć ", 100_000))
	f.states["id-42"] = state.Report{State: state.Done, Since: f.now.Add(-time.Minute), Last: state.Clean(reply)}
	f.details["id-42"] = state.Detail{Reply: reply, ReplyAt: f.now.Add(-time.Minute)}
	offsets := []int{0}
	for {
		_, next, err := replyPage(reply, offsets[len(offsets)-1])
		if err != nil {
			t.Fatal(err)
		}
		if next == nil {
			break
		}
		offsets = append(offsets, *next)
	}
	if len(reply) < 3<<20 || len(offsets) < 90 {
		t.Fatalf("%d bytes in %d pages", len(reply), len(offsets))
	}
	since := ""
	for _, offset := range []int{0, offsets[len(offsets)/2], offsets[len(offsets)-1]} {
		args := []string{"read", "42", "--json"}
		if offset > 0 {
			args = append(args, "--offset", strconv.Itoa(offset), "--since", since)
		}
		code, out, errOut := f.run(args...)
		var v readJSON
		if code != 0 || json.Unmarshal([]byte(out), &v) != nil || v.ReplySize != len(reply) {
			t.Fatalf("%v: exit %d %q\n%.300s", args, code, errOut, out)
		}
		want, next, _ := replyPage(reply, offset)
		if v.Reply != want || (next == nil) != (v.NextOffset == nil) {
			t.Fatalf("%v: %d bytes, next %v", args, len(v.Reply), v.NextOffset)
		}
		if offset == 0 {
			since = v.NextSince
		}
	}
}
