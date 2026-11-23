package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/journal/journaltest"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
)

// threeAgents has a working, b done and c in question, so their attention
// order is c, b, a, whatever order they are named in.
func threeAgents() *fakes {
	f := newLsFakes()
	start := f.now.Add(-time.Hour)
	f.tmux.windows = []tmux.Window{
		{ID: "@0", Name: "hq", Options: map[string]string{}},
		agentWindow("@1", "a", "/w/app", start, false),
		agentWindow("@2", "b", "/w/app", start, false),
		agentWindow("@3", "c", "/w/app", start, false),
	}
	f.states["id-a"] = journaltest.HookState{State: state.Working, Since: f.now.Add(-time.Minute), Last: "on it"}
	f.states["id-b"] = journaltest.HookState{State: state.Done, Since: f.now.Add(-2 * time.Minute), Last: "PR #9 opened"}
	f.states["id-c"] = journaltest.HookState{State: state.Question, Since: f.now.Add(-3 * time.Minute), Last: "Which one?"}
	return f
}

// lsNames are the names of the rows hq ls --json printed, in their order.
func lsNames(t *testing.T, out string) string {
	t.Helper()
	var l waitOutput
	if err := json.Unmarshal([]byte(out), &l); err != nil {
		t.Fatalf("%v: %q", err, out)
	}
	var names []string
	for _, r := range l.Agents {
		names = append(names, r.Name)
	}
	return strings.Join(names, " ")
}

// hq ls NAME... lists the agents named, in attention order, each once,
// with the names before and after --json; with no name, every agent
// (#291).
func TestLsListsTheAgentsNamedInAttentionOrder(t *testing.T) {
	for args, want := range map[string]string{
		"ls --json":          "c b a",
		"ls a --json":        "a",
		"ls --json a c":      "c a",
		"ls a --json c":      "c a",
		"ls a c b a --json":  "c b a",
		"ls b b --json":      "b",
		"ls --json --json c": "c",
	} {
		code, out, errOut := threeAgents().run(strings.Fields(args)...)
		if code != 0 || errOut != "" {
			t.Fatalf("%s: exit %d %q", args, code, errOut)
		}
		if got := lsNames(t, out); got != want {
			t.Errorf("%s: %q, want %q", args, got, want)
		}
	}
}

// hq ls refuses a name no agent has as hq wait refuses it, the look's
// refusal in its words with its exit code, and lists nothing, the other
// names' agents included, as a table or as JSON (#291).
func TestLsRefusesANameNoAgentHasAndListsNothing(t *testing.T) {
	_, _, waitErr := threeAgents().run("wait", "nope", "--timeout", "1s")
	waitCode, _, _ := threeAgents().run("wait", "nope", "--timeout", "1s")
	for _, args := range [][]string{{"ls", "nope"}, {"ls", "a", "nope"}, {"ls", "a", "nope", "--json"}, {"ls", "--json", "nope", "c"}} {
		code, out, errOut := threeAgents().run(args...)
		if code != ExitNotFound || code != waitCode || out != "" || errOut != noAgent("nope") || errOut != waitErr {
			t.Errorf("%v: exit %d %q %q (hq wait: exit %d %q)", args, code, out, errOut, waitCode, waitErr)
		}
	}
}

// An outdated agent that is named is listed, as outdated: hq ls shows
// every agent it is asked for, where hq wait refuses to wait for one
// (#291).
func TestLsListsAnOutdatedAgentNamed(t *testing.T) {
	code, out, errOut := outdatedFakes().run("ls", "old", "--json")
	var l waitOutput
	if code != 0 || errOut != "" || json.Unmarshal([]byte(out), &l) != nil || len(l.Agents) != 1 || l.Agents[0].Name != "old" || l.Agents[0].State != state.Outdated {
		t.Fatalf("exit %d %q\n%s", code, errOut, out)
	}
	code, out, _ = outdatedFakes().run("ls", "old")
	if lines := strings.Split(strings.TrimSpace(out), "\n"); code != 0 || len(lines) != 2 || !strings.HasPrefix(lines[1], "old ") || !strings.Contains(lines[1], state.Outdated) {
		t.Fatalf("table: exit %d\n%s", code, out)
	}
}

// Without --json, hq ls NAME... prints the header and the named agents'
// rows only, LAST cut at 60 characters as ever (#291).
func TestLsTableShowsTheNamedAgentsOnly(t *testing.T) {
	f := threeAgents()
	f.states["id-a"] = journaltest.HookState{State: state.Working, Since: f.now.Add(-time.Minute), Last: strings.Repeat("x", 100)}
	code, out, _ := f.run("ls", "a")
	want := "NAME  REPO  BRANCH  STATE    AGE  LAST\n" +
		"a     app   -       working  1m   " + strings.Repeat("x", 59) + "…\n"
	if code != 0 || out != want {
		t.Fatalf("exit %d, got\n%s\nwant\n%s", code, out, want)
	}
}

// hq ls still refuses an option it does not take, names given or not,
// with its usage (#291).
func TestLsRefusesAnUnknownOptionWithItsUsage(t *testing.T) {
	for _, args := range [][]string{{"ls", "-l"}, {"ls", "a", "--all"}, {"ls", "--json=yes"}} {
		code, out, errOut := threeAgents().run(args...)
		if want := "hq: unknown option '" + args[len(args)-1] + "' (usage: hq ls [NAME...] [--json])\n"; code != ExitUsage || out != "" || errOut != want {
			t.Errorf("%v: exit %d %q %q", args, code, out, errOut)
		}
	}
}

// lastOf is the last of the one row a list command printed as JSON.
func lastOf(t *testing.T, args []string, out string) string {
	t.Helper()
	var l waitOutput
	if err := json.Unmarshal([]byte(out), &l); err != nil || len(l.Agents) != 1 {
		t.Fatalf("%v: %v %q", args, err, out)
	}
	return l.Agents[0].Last
}

// In JSON, a row of the list carries last cut to 120 characters, counted
// in characters, not bytes, the cut marked by … as its last one: in hq
// ls, hq wait and hq send --wait (#291). One of 120 is given whole.
func TestAListsRowsCarryLastCutTo120Characters(t *testing.T) {
	for last, want := range map[string]string{
		strings.Repeat("ą", 120): strings.Repeat("ą", 120),
		strings.Repeat("ą", 121): strings.Repeat("ą", 119) + "…",
		strings.Repeat("x", 500): strings.Repeat("x", 119) + "…",
	} {
		done := func() *fakes {
			f := sendFakes(state.Done)
			f.states["id-a"] = journaltest.HookState{State: state.Done, Since: f.now.Add(-time.Second), Last: last}
			return f
		}
		for _, args := range [][]string{{"ls", "--json"}, {"ls", "a", "--json"}, {"wait", "--since", "c0", "--json"}, {"wait", "a", "--since", "c0", "--json"}} {
			if got := lastOf(t, args, mustRun(t, done(), args...)); got != want {
				t.Errorf("%v of %d: %q, want %q", args, len([]rune(last)), got, want)
			}
		}
		f := sendFakes(state.Done)
		onSubmit(f, func() { turnFor(f, 6, last) })
		if got := sendWaitJSON(t, f, "a", "go on"); len(got.Agents) != 1 || got.Agents[0].Last != want {
			t.Errorf("send --wait of %d: %+v, want %q", len([]rune(last)), got.Agents, want)
		}
	}
}

// hq read gives last whole, as JSON and as text, beside the reply, where
// hq ls --json gives the same agent's cut: reading an agent is how the
// rest of a list's last is got. It compares the whole last with the reply,
// so it still leaves the line last out when the two are the same (#291).
func TestReadGivesLastWhole(t *testing.T) {
	long := strings.Repeat("ą", 300)
	f := sendFakes(state.NeedsInput)
	f.states["id-a"] = journaltest.HookState{State: state.NeedsInput, Since: f.now.Add(-time.Second), Last: long}
	f.details["id-a"] = state.Detail{Ask: &state.Ask{Tool: "AskUserQuestion", Questions: []state.AskQuestion{{Question: long, Options: []state.AskOption{{Label: "Yes"}}}}}}
	if args := []string{"ls", "a", "--json"}; lastOf(t, args, mustRun(t, f, args...)) != strings.Repeat("ą", 119)+"…" {
		t.Fatalf("hq ls --json gives last whole")
	}
	var r struct {
		Last string `json:"last"`
	}
	if out := mustRun(t, f, "read", "a", "--json"); json.Unmarshal([]byte(out), &r) != nil || r.Last != long {
		t.Fatalf("read --json: %q", out)
	}
	if out := mustRun(t, f, "read", "a"); !strings.Contains(out, "\nlast      "+long+"\n") {
		t.Fatalf("read:\n%s", out)
	}
	f = sendFakes(state.Done)
	f.states["id-a"] = journaltest.HookState{State: state.Done, Since: f.now.Add(-time.Second), Last: long}
	f.details["id-a"] = state.Detail{Reply: long}
	if out := mustRun(t, f, "read", "a"); strings.Contains(out, "last ") || !strings.Contains(out, "\n  "+long+"\n") {
		t.Fatalf("read with last the reply:\n%s", out)
	}
}

// The next_since of hq ls NAME... --json is for a wait that names the same
// agents: from it, a wait naming a does not return the state the list
// showed, and returns a change of a made after the list, while b, not
// named, changes at every look and keeps the list looking again
// (agent.Looker.Cover; #291).
func TestAWaitFromTheCursorOfAListWithNamesReturnsOnlyWhatChangedAfterIt(t *testing.T) {
	looks := 0
	f := changingDialogOnB(t, &looks)
	listed := waitJSON(t, mustRun(t, f, "ls", "a", "--json"))
	if len(listed.Agents) != 1 || listed.Agents[0].Name != "a" || listed.Agents[0].State != state.Done || looks < 2 {
		t.Fatalf("after %d looks, listed %+v", looks, listed)
	}
	count := 0
	f.onThrough = func() {
		count++
		f.tmux.screens["%2"] = strings.Replace(f.tmux.screens["%2"], "level", "level"+strings.Repeat("!", count%2), 1)
	}
	if out := mustRun(t, f, "wait", "a", "--since", listed.NextSince, "--timeout", "1s"); !strings.HasPrefix(out, "nothing yet\n") {
		t.Fatalf("the state the list showed came back: %q", out)
	}
	f.after(time.Second, func() {
		f.states["id-a"] = journaltest.HookState{State: state.Question, Since: f.now, Last: "Shall I go on?"}
	})
	got := waitJSON(t, mustRun(t, f, "wait", "a", "--since", listed.NextSince, "--timeout", "10s", "--json"))
	if len(got.Agents) != 1 || got.Agents[0].Name != "a" || got.Agents[0].State != state.Question {
		t.Fatalf("the change after the list: %+v", got)
	}
}

// hq ls NAME... types in the messages left waiting for every agent of its
// look, not only for those it names, as hq ls does, and also when it
// refuses a name (#291); each agent goes
// to the delivery as the look gave it, with its home window, so a delivery
// whose journal is gone meanwhile is still written while that window
// stands (#318).
func TestLsWithNamesTypesInWhatWaitsForEveryAgentOfItsLook(t *testing.T) {
	for args, wantCode := range map[string]int{"ls b": 0, "ls b --json": 0, "ls b nope": ExitNotFound} {
		f := waitingFakes()
		f.tmux.windows = append(f.tmux.windows, agentWindow("@5", "b", "/w/app", f.now.Add(-time.Minute), false))
		f.states["id-b"] = journaltest.HookState{State: state.Working, Since: f.now.Add(-time.Second)}
		lost := false
		f.onAppend = func(id string, events []state.Event) {
			if id != "id-a" || lost || events[0].Kind != state.KindDelivery {
				return
			}
			lost = true
			if err := f.journal.Remove("/w/app", journalID("id-a")); err != nil {
				t.Fatal(err)
			}
		}
		if code, _, errOut := f.run(strings.Fields(args)...); code != wantCode || !lost {
			t.Fatalf("%v: exit %d %q, lost %v", args, code, errOut, lost)
		}
		if got := strings.Join(f.tmux.pasted, "|"); got != "%4 /effort medium" || len(f.inbox["id-a"]) != 0 {
			t.Fatalf("%v: pasted %q inbox %v", args, got, f.inbox)
		}
		if got := journaltest.Kinds(journalOf(t, f, journalID("id-a"))); got != state.KindDelivery {
			t.Fatalf("%v: journal %s", args, got)
		}
	}
}
