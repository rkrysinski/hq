package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

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
			"last      Which colour?\n\nasks\n  Colour: Which colour?\n    1. Red - Pick red.\n    2. Blue\n  Which sizes? (any of)\n    1. S\n    2. M\n"},
		{"permission prompt", state.Report{State: state.NeedsInput, Last: "Bash: Create a file"},
			state.Detail{Ask: &state.Ask{Tool: "Bash", Description: "Create a file", Command: "touch /tmp/x"}},
			"last      Bash: Create a file\n\nasks\n  Bash: touch /tmp/x\n  Create a file\n"},
		{"permission without a command", state.Report{State: state.NeedsInput, Last: "WebSearch: Look it up"},
			state.Detail{Ask: &state.Ask{Tool: "WebSearch", Description: "Look it up"}},
			"last      WebSearch: Look it up\n\nasks\n  WebSearch: Look it up\n"},
		{"notification", state.Report{State: state.NeedsInput, Last: "Claude needs your permission"},
			state.Detail{Ask: &state.Ask{Message: "Claude needs your permission"}},
			"last      Claude needs your permission\n\nasks\n  Claude needs your permission\n"},
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
		if code != 0 || rest != strings.TrimSuffix(tc.want, "\n")+"\n" {
			t.Errorf("%s: exit %d %q\n%s", tc.name, code, errOut, out)
		}
	}
	// Once the agent works on, a dialog it answered asks nothing.
	f := readFakes()
	f.states["id-42"] = state.Report{State: state.Working, Since: f.now}
	f.details["id-42"] = state.Detail{Ask: &state.Ask{Message: "Claude needs your permission"}}
	if _, out, _ := f.run("read", "42"); strings.Contains(out, "\nasks\n") {
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
		"asks":     `{"tool":"Bash","description":"Create a file","command":"touch /tmp/x"}`,
	} {
		var norm any
		json.Unmarshal([]byte(want), &norm)
		if got := mustJSON(v[k]); got != mustJSON(norm) {
			t.Errorf("%s: %s, want %s", k, got, want)
		}
	}
	// Nothing asked: null, not missing.
	f.states["id-42"] = state.Report{State: state.Working, Since: f.now}
	f.details["id-42"] = state.Detail{}
	_, out, _ = f.run("read", "42", "--json")
	if !strings.Contains(out, `"asks": null`) || !strings.Contains(out, `"reply": ""`) {
		t.Fatalf("nulls:\n%s", out)
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
	for _, args := range [][]string{{"read"}, {"read", "--json"}, {"read", "42", "43"}, {"read", "42", "-y"}} {
		if code, _, errOut := f.run(args...); code != ExitUsage || errOut != "hq: usage: hq read NAME [--json]\n" {
			t.Errorf("%v: exit %d %q", args, code, errOut)
		}
	}
	f.tmux.missing = true
	if code, _, _ := f.run("read", "42"); code != ExitEnvironment {
		t.Fatalf("no tmux: exit %d", code)
	}
}
