package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
)

func TestNameChars(t *testing.T) {
	for name, ok := range map[string]bool{"42": true, "bok-17": true, "a_b": true, strings.Repeat("a", 40): true, "": false, "a b": false, "a/b": false, "ą": false} {
		if NameChars(name) != ok {
			t.Errorf("NameChars(%q) != %v", name, ok)
		}
	}
}

func TestAge(t *testing.T) {
	for d, want := range map[time.Duration]string{3 * time.Second: "3s", 2 * time.Minute: "2m", 90 * time.Minute: "1h", 50 * time.Hour: "2d"} {
		if got := Age(d); got != want {
			t.Errorf("Age(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestFromWindowsSkipsNonAgentsAndMarksDeadPanesEnded(t *testing.T) {
	as := FromWindows([]tmux.Window{
		{ID: "@0", Name: "hq", Options: map[string]string{}},
		{ID: "@1", Name: "a", PaneDead: true, Options: map[string]string{"id": "x", "name": "a", "started": "100"}},
	})
	if len(as) != 1 || as[0].State != state.Ended || as[0].Alive || as[0].Started.Unix() != 100 {
		t.Fatalf("%+v", as)
	}
}

func TestFromWindowsKnowsTheDockedAgent(t *testing.T) {
	as := FromWindows([]tmux.Window{
		{ID: "@1", Name: "a", Docked: true, Options: map[string]string{"id": "x"}},
		{ID: "@2", Name: "b", Options: map[string]string{"id": "y"}},
	})
	if !as[0].Docked || as[1].Docked || !as[0].Alive {
		t.Fatalf("%+v", as)
	}
}

func TestNewIDIsFresh(t *testing.T) {
	if a, b := NewID(), NewID(); a == b || len(a) != 12 {
		t.Fatalf("%q %q", a, b)
	}
}

func TestCollectEndsAgentsWhoseSandboxIsNotRunning(t *testing.T) {
	ws := []tmux.Window{
		{ID: "@1", Options: map[string]string{"id": "a", "name": "a", "sandbox": "claude-app", "started": "100"}},
		{ID: "@2", Options: map[string]string{"id": "b", "name": "b", "sandbox": "claude-lib", "started": "100"}},
	}
	read := func(root, id string) (state.Report, bool) {
		return state.Report{State: state.Working, Since: time.Unix(200, 0), Last: "on it " + id}, true
	}
	states := func(as []Agent) string {
		var s []string
		for _, a := range as {
			s = append(s, a.Name+"="+a.State+"/"+a.Last)
		}
		return strings.Join(s, " ")
	}
	if got := states(Collect(ws, read, map[string]bool{"claude-app": true})); got != "a=working/on it a b=ended/on it b" {
		t.Fatalf("claude-lib stopped: %s", got)
	}
	if got := states(Collect(ws, read, nil)); got != "a=working/on it a b=working/on it b" {
		t.Fatalf("sbx could not say: %s", got)
	}
}

func TestApplyTakesTheReportedStateWhileThePaneLives(t *testing.T) {
	since := time.Unix(500, 0)
	r := state.Report{State: state.Question, Since: since, Last: "Shall I?", Branch: "feat/42-x", Cwd: "/w/app/.claude/worktrees/x"}
	a := FromWindows([]tmux.Window{{ID: "@1", Options: map[string]string{"id": "x", "started": "100"}}})[0]
	if a.State != state.Starting || a.Since.Unix() != 100 {
		t.Fatalf("before any report: %+v", a)
	}
	a.Apply(r, true)
	if a.State != state.Question || a.Since != since || a.Last != "Shall I?" || a.Branch != "feat/42-x" || a.Worktree != "/w/app/.claude/worktrees/x" {
		t.Fatalf("%+v", a)
	}
	dead := FromWindows([]tmux.Window{{ID: "@2", PaneDead: true, Options: map[string]string{"id": "y", "started": "100"}}})[0]
	dead.Apply(r, true)
	if dead.State != state.Ended || dead.Last != "Shall I?" || dead.Since != since {
		t.Fatalf("dead pane: %+v", dead)
	}
	// A report older than the start (a reused state file) does not move it back.
	old := FromWindows([]tmux.Window{{ID: "@3", PaneDead: true, Options: map[string]string{"id": "z", "started": "900"}}})[0]
	old.Apply(r, true)
	if old.Since.Unix() != 900 {
		t.Fatalf("old report: %+v", old)
	}
}

func TestAnAgentIsNewUntilItsFirstReport(t *testing.T) {
	ws := []tmux.Window{
		{ID: "@1", Name: "a", Options: map[string]string{"id": "a", "new": "1"}},
		{ID: "@2", Name: "b", Options: map[string]string{"id": "b", "new": "1"}},
		{ID: "@3", Name: "c", PaneDead: true, Options: map[string]string{"id": "c", "new": "1"}},
		{ID: "@4", Name: "d", Options: map[string]string{"id": "d", "new": "1", "sandbox": "gone"}},
		{ID: "@5", Name: "e", Options: map[string]string{"id": "e"}}, // relaunched, not new
	}
	read := func(_, id string) (state.Report, bool) {
		return state.Report{State: state.Working}, id == "b"
	}
	var got []string
	for _, a := range Collect(ws, read, map[string]bool{"": true}) {
		if a.New {
			got = append(got, a.Name)
		}
	}
	if strings.Join(got, " ") != "a" {
		t.Fatalf("new: %v, want a (b reported, c ended, d's sandbox stopped, e relaunched)", got)
	}
}

func TestCollectSaysSessionEndedForAnEndedAgentWithoutAMessage(t *testing.T) {
	ws := []tmux.Window{
		{ID: "@1", Name: "dead", PaneDead: true, Options: map[string]string{"id": "a", "sandbox": "claude-app"}},
		{ID: "@2", Name: "stopped", Options: map[string]string{"id": "b", "sandbox": "claude-lib"}},
		{ID: "@3", Name: "said", PaneDead: true, Options: map[string]string{"id": "c", "sandbox": "claude-app"}},
		{ID: "@4", Name: "quiet", Options: map[string]string{"id": "d", "sandbox": "claude-app"}},
		{ID: "@5", Name: "silent", PaneDead: true, Options: map[string]string{"id": "e", "sandbox": "claude-app"}},
	}
	read := func(_, id string) (state.Report, bool) {
		switch id {
		case "c":
			return state.Report{State: state.Done, Last: "hi"}, true
		case "e": // reported, but never a message
			return state.Report{State: state.Working}, true
		}
		return state.Report{}, false
	}
	var got []string
	for _, a := range Collect(ws, read, map[string]bool{"claude-app": true}) {
		got = append(got, a.Name+"="+a.Last)
	}
	if want := "dead=[session ended] stopped=[session ended] said=hi quiet= silent=[session ended]"; strings.Join(got, " ") != want {
		t.Fatalf("got  %s\nwant %s", strings.Join(got, " "), want)
	}
}
