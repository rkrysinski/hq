package cli

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/kills"
	"github.com/rkrysinski/hq/internal/sbx"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
	"github.com/rkrysinski/hq/internal/version"
)

// outdatedLine is what hq says of the outdated agent old, started by an
// older hq.
const outdatedLine = "hq: old was started by an older hq: kill it and start it again\n"

// outdatedFakes has the agent a working in app since a minute ago and old,
// started an hour ago by an hq of the generation before (#194), both in
// the running sandbox claude-x; old's state files say it is done, which
// this hq does not read.
func outdatedFakes() *fakes {
	f := newLsFakes()
	f.sbx.sandboxes = []sbx.Sandbox{{Name: "claude-x", Status: "running", Workspaces: []string{"/w/app"}}}
	old := agentWindow("@2", "old", "/w/app", f.now.Add(-time.Hour), false)
	old.Options[genOption] = strconv.Itoa(version.Generation - 1)
	f.tmux.windows = []tmux.Window{
		{ID: "@0", Name: "hq", Options: map[string]string{}},
		agentWindow("@1", "a", "/w/app", f.now.Add(-time.Hour), false),
		old,
	}
	for i := range f.tmux.windows[1:] {
		f.tmux.windows[i+1].Pane = "%" + f.tmux.windows[i+1].ID[1:]
	}
	f.states["id-a"] = state.Report{State: state.Working, Since: f.now.Add(-time.Minute), Last: "on it", Branch: "feat/a"}
	f.states["id-old"] = state.Report{State: state.Done, Since: f.now.Add(-2 * time.Minute), Last: "PR #9 opened", Branch: "feat/old"}
	f.details["id-old"] = state.Detail{Reply: "PR #9 opened"}
	return f
}

// hq ls shows an outdated agent first, with no age, no branch and the
// remedy as its last message, whatever its pane does; --json says outdated
// (#194).
func TestLsShowsAnOutdatedAgentFirstWithTheRemedy(t *testing.T) {
	for _, dead := range []bool{false, true} {
		f := outdatedFakes()
		f.tmux.windows[2].PaneDead = dead
		code, out, _ := f.run("ls")
		want := "NAME  REPO  BRANCH  STATE     AGE  LAST\n" +
			"old   app   -       outdated  -    started by an older hq: kill it and start it again\n" +
			"a     app   feat/a  working   1m   on it\n"
		if code != 0 || out != want {
			t.Errorf("dead %v: exit %d\n%s\nwant\n%s", dead, code, out, want)
		}
	}
	f := outdatedFakes()
	_, js, _ := f.run("ls", "--json")
	w := waitJSON(t, js)
	if len(w.Agents) != 2 || w.Agents[0].Name != "old" || w.Agents[0].State != state.Outdated || w.Agents[0].Branch != "" || w.Agents[0].Asks != nil {
		t.Fatalf("json:\n%s", js)
	}
	// One started by a newer hq, after a downgrade.
	f.tmux.windows[2].Options[genOption] = strconv.Itoa(version.Generation + 1)
	if _, out, _ := f.run("ls"); !strings.Contains(out, "outdated  -    started by a newer hq: kill it and start it again\n") {
		t.Fatalf("newer:\n%s", out)
	}
}

// Every command that would act on what an outdated agent's state is
// refuses it with the line and exit code 1, and touches nothing of it
// (#194).
func TestCommandsRefuseAnOutdatedAgent(t *testing.T) {
	for _, args := range [][]string{
		{"read", "old"},
		{"read", "old", "--json"},
		{"send", "old", "hi"},
		{"answer", "old", "--dialog", "abcdef", "1"},
		{"code", "old"},
		{"wait", "old"},
		{"wait", "a", "old", "--timeout", "1s"},
	} {
		f := outdatedFakes()
		code, out, errOut := f.run(args...)
		if code != ExitUsage || out != "" || errOut != outdatedLine {
			t.Errorf("%v: exit %d %q %q", args, code, out, errOut)
		}
		if len(f.inbox["id-old"]) != 0 || len(f.answerLeft) != 0 || len(f.edited) != 0 || len(f.tmux.pasted) != 0 {
			t.Errorf("%v: inbox %v, answers %v, edited %v, pasted %v", args, f.inbox, f.answerLeft, f.edited, f.tmux.pasted)
		}
	}
}

// A hq wait on every agent leaves an outdated agent out, killed or not, and
// still returns the others (#194).
func TestWaitOnEveryAgentLeavesAnOutdatedAgentOut(t *testing.T) {
	f := outdatedFakes()
	// old is killed while hq waits, then a turns done.
	f.onSleep = chain(f, 5*time.Second, func() { f.tmux.windows = f.tmux.windows[:2] }, 10*time.Second, func() {
		f.states["id-a"] = state.Report{State: state.Done, Since: f.now, Last: "PR #10 opened"}
	})
	code, out, errOut := f.run("wait", "--json")
	w := waitJSON(t, out)
	if code != 0 || len(w.Agents) != 1 || w.Agents[0].Name != "a" || w.Agents[0].State != state.Done {
		t.Fatalf("exit %d %q:\n%s", code, errOut, out)
	}
	// Alone, it times out with nothing: the outdated agent never arrives.
	f = outdatedFakes()
	f.tmux.windows[2].PaneDead = true
	if code, out, _ := f.run("wait", "--timeout", "2s"); code != 0 || !strings.HasPrefix(out, "nothing yet\n") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}

// chain runs first once the fake clock has passed d1 from now, then second
// once it has passed d2.
func chain(f *fakes, d1 time.Duration, first func(), d2 time.Duration, second func()) func() {
	at1, at2, step := f.now.Add(d1), f.now.Add(d2), 0
	return func() {
		switch {
		case step == 0 && !f.now.Before(at1):
			step++
			first()
		case step == 1 && !f.now.Before(at2):
			step++
			second()
		}
	}
}

// An outdated agent can be opened: hq go docks it, keys typed there
// reaching it as any agent's do (#194).
func TestGoOpensAnOutdatedAgent(t *testing.T) {
	f := outdatedFakes()
	if code, _, errOut := f.run("go", "old"); code != 0 || f.tmux.docked != "@2" {
		t.Fatalf("exit %d %q, docked %q", code, errOut, f.tmux.docked)
	}
}

// hq kill and hq stop end an outdated agent's session in its sandbox and
// remove everything of it, its older hook's files included, into the
// trace of kills as any agent (#194).
func TestKillAndStopEndAnOutdatedAgent(t *testing.T) {
	f := outdatedFakes()
	code, out, errOut := f.run("kill", "old", "-y")
	if code != 0 || out != "killed old; the sandbox stays\n" {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	if got := strings.Join(f.sbx.execs[0], " "); got != `claude-x pkill -TERM -f HQ_ID":"id-old"` || f.names() != "hq a" {
		t.Fatalf("exec %q, windows %q", got, f.names())
	}
	if strings.Join(f.removed, ",") != "/w/app id-old" || len(f.kills) != 1 || f.kills[0].Name != "old" || f.kills[0].By != kills.ByCLI {
		t.Fatalf("removed %v, trace %+v", f.removed, f.kills)
	}
	f = outdatedFakes()
	if code, out, _ := f.run("stop", "-y"); code != 0 || out != "ended 2 agents; sandboxes stay\n" || f.names() != "hq" || len(f.kills) != 2 {
		t.Fatalf("stop: exit %d %q, windows %q, trace %+v", code, out, f.names(), f.kills)
	}
}

// hq sandbox restart is refused while a sandbox it would restart holds an
// outdated agent, naming it and the remedy; nothing is stopped (#194).
func TestSandboxRestartIsRefusedWithAnOutdatedAgentInIt(t *testing.T) {
	f := outdatedFakes()
	code, out, errOut := f.run("sandbox", "restart", "app", "-y")
	if code != ExitUsage || out != "" || errOut != "hq: not restarting the sandbox claude-x: old was started by an older hq: kill it and start it again; nothing was stopped\n" {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	if len(f.sbx.calls) != 0 || f.tmux.windows[1].Options["ending"] != "" {
		t.Fatalf("sbx %v, a ending %q", f.sbx.calls, f.tmux.windows[1].Options["ending"])
	}
}

// A lost agent whose record has no generation, or another, is reported by
// hq restore and not relaunched; hq kill forgets it (#194).
func TestRestoreReportsALostAgentOfAnotherGeneration(t *testing.T) {
	f := lostFakes()
	f.records["/w/app"][0].Generation = 0                      // a: kept before records had one
	f.records["/w/app"][1].Generation = version.Generation + 1 // d: by a newer hq
	code, out, errOut := f.run("restore")
	want := "restored c in lib (sandbox claude-lib)\n" +
		"not restored a in app: it was started by an older hq (hq kill a forgets it)\n" +
		"not restored d in app: it was started by a newer hq (hq kill d forgets it)\n"
	if code != 0 || out != want {
		t.Fatalf("exit %d %q\n%s\nwant\n%s", code, errOut, out, want)
	}
	if _, ok := f.window("a"); ok {
		t.Fatal("a relaunched")
	}
	// Nothing of theirs is ended in their sandboxes either.
	for _, e := range f.sbx.execs {
		if strings.Contains(strings.Join(e, " "), "id-a") || strings.Contains(strings.Join(e, " "), "id-d") {
			t.Fatalf("exec %v", e)
		}
	}
	if code, out, errOut := f.run("kill", "a", "-y"); code != 0 || out != "killed a; the sandbox stays\n" || !strings.Contains(strings.Join(f.removed, ","), "/w/app id-a") {
		t.Fatalf("kill: exit %d %q %q, removed %v", code, out, errOut, f.removed)
	}
}

// The supervisor's list shows an outdated agent as outdated, its read,
// send, answer and a wait that names it fail with the line, go and kill
// work, and the server's instructions say what outdated means (#194).
func TestTheSupervisorsToolsTreatAnOutdatedAgentAsTheCommandsDo(t *testing.T) {
	f := outdatedFakes()
	cs := mcpSession(t, f)
	out, isErr := call(t, cs, "list", map[string]any{})
	if w := waitJSON(t, out); isErr || w.Agents[0].Name != "old" || w.Agents[0].State != state.Outdated || w.Agents[0].Last != "started by an older hq: kill it and start it again" {
		t.Fatalf("list %v:\n%s", isErr, out)
	}
	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"read", map[string]any{"name": "old"}},
		{"send", map[string]any{"name": "old", "text": "hi"}},
		{"answer", map[string]any{"name": "old", "dialog": "abcdef", "answers": []map[string]any{{"options": []int{1}}}, "confirmed": true}},
		{"wait", map[string]any{"names": []string{"old"}, "since": "1m"}},
	} {
		if out, isErr := call(t, cs, c.tool, c.args); !isErr || out != outdatedLine {
			t.Errorf("%s: error %v %q", c.tool, isErr, out)
		}
	}
	if out, isErr := call(t, cs, "go", map[string]any{"name": "old"}); isErr || f.tmux.docked != "@2" {
		t.Fatalf("go: %v %q", isErr, out)
	}
	if out, isErr := call(t, cs, "kill", map[string]any{"name": "old", "confirmed": true}); isErr || out != "killed old; the sandbox stays\n" || strings.Contains(f.names(), "old") {
		t.Fatalf("kill: %v %q, windows %q", isErr, out, f.names())
	}
	if got := cs.InitializeResult().Instructions; !strings.Contains(got, "An agent in the state outdated was started by another hq than the one serving you") || !strings.Contains(got, "tell the user which agent it is") {
		t.Errorf("instructions:\n%s", got)
	}
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools.Tools {
		if tl.Name == "list" && !strings.Contains(tl.Description, "outdated (started by another hq, so its state is unknown: tell the user; only go and kill work on it") {
			t.Errorf("list: %s", tl.Description)
		}
	}
}
