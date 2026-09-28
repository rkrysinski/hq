package agent

import (
	"strconv"
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

func TestCollectLeavesAnAgentStartingWhileItsSandboxHasNotStarted(t *testing.T) {
	// hq new has released c's session into claude-lib, stopped or just
	// created; sbx run is starting it and c has not reported yet (S2).
	ws := []tmux.Window{
		{ID: "@1", Options: map[string]string{"id": "c", "name": "c", "sandbox": "claude-lib", "new": "1"}},
		{ID: "@2", PaneDead: true, Options: map[string]string{"id": "d", "name": "d", "sandbox": "claude-lib"}},
	}
	read := func(root, id string) (state.Report, bool) { return state.Report{}, false }
	as := Collect(ws, read, map[string]bool{})
	if as[0].State != state.Starting || !as[0].New {
		t.Fatalf("c while its sandbox starts: %+v", as[0])
	}
	// Its pane dying (the launch failed, or sbx run returned) still ends it.
	if as[1].State != state.Ended {
		t.Fatalf("d with a dead pane: %+v", as[1])
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
		{ID: "@4", Name: "d", Options: map[string]string{"id": "d", "new": "1", "sandbox": "booting"}},
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
	if strings.Join(got, " ") != "a d" {
		t.Fatalf("new: %v, want a d (b reported, c ended, d's sandbox not started yet, e relaunched)", got)
	}
}

func TestCollectSaysSessionEndedForAnEndedAgentWithoutAMessage(t *testing.T) {
	ws := []tmux.Window{
		{ID: "@1", Name: "dead", PaneDead: true, Options: map[string]string{"id": "a", "sandbox": "claude-app"}},
		{ID: "@2", Name: "stopped", Options: map[string]string{"id": "b", "sandbox": "claude-lib"}},
		{ID: "@3", Name: "said", PaneDead: true, Options: map[string]string{"id": "c", "sandbox": "claude-app"}},
		{ID: "@4", Name: "quiet", Options: map[string]string{"id": "d", "sandbox": "claude-app"}},
		{ID: "@5", Name: "silent", PaneDead: true, Options: map[string]string{"id": "e", "sandbox": "claude-app"}},
		// Its sandbox restarting (hq sandbox restart): ended, its pane still runs.
		{ID: "@6", Name: "restarting", Options: map[string]string{"id": "f", "sandbox": "claude-app", "ending": "1"}},
	}
	read := func(_, id string) (state.Report, bool) {
		switch id {
		case "c":
			return state.Report{State: state.Done, Last: "hi"}, true
		case "b", "e": // reported, but never a message
			return state.Report{State: state.Working}, true
		}
		return state.Report{}, false
	}
	var got []string
	for _, a := range Collect(ws, read, map[string]bool{"claude-app": true}) {
		got = append(got, a.Name+"="+a.Last)
	}
	if want := "dead=[session ended] stopped=[session ended] said=hi quiet= silent=[session ended] restarting=[session ended]"; strings.Join(got, " ") != want {
		t.Fatalf("got  %s\nwant %s", strings.Join(got, " "), want)
	}
}

func TestCollectKeepsTheTurnEndHqSawAsAnEndedAgentsMessage(t *testing.T) {
	at := time.Unix(1790000000, 0)
	key := strconv.FormatInt(at.UnixNano(), 10)
	seen := strconv.FormatInt(at.Add(3*time.Second).UnixNano(), 10)
	ws := []tmux.Window{
		// An early Esc rewound its turn, then its sandbox stopped (#111).
		{ID: "@1", Name: "rewound", PaneDead: true, Options: map[string]string{"id": "a", "sandbox": "claude-app", "turnend": key + " " + seen + " " + Rewound}},
		// The record is for an earlier report: it does not count.
		{ID: "@2", Name: "stale", PaneDead: true, Options: map[string]string{"id": "b", "sandbox": "claude-app", "turnend": "1 " + seen + " " + Rewound}},
	}
	read := func(_, id string) (state.Report, bool) {
		return state.Report{State: state.Working, Since: at}, true
	}
	var got []string
	for _, a := range Collect(ws, read, map[string]bool{"claude-app": true}) {
		got = append(got, a.Name+"="+string(a.State)+"="+a.Last)
	}
	if want := "rewound=ended=" + Rewound + " stale=ended=" + EndedLast; strings.Join(got, " ") != want {
		t.Fatalf("got  %s\nwant %s", strings.Join(got, " "), want)
	}
}

func TestAnAgentStartedWithoutAPromptIsDoneOnceClaudeWaitsAtItsPrompt(t *testing.T) {
	ws := []tmux.Window{{ID: "@1", Name: "a", Options: map[string]string{"id": "a", "new": "1", "started": "100"}}}
	var file string
	read := func(_, _ string) (state.Report, bool) {
		if file == "" {
			return state.Report{}, false
		}
		r := state.Parse([]byte(file), nil, nil)
		r.Since = time.Unix(102, 0)
		return r, true
	}
	if a := Collect(ws, read, nil)[0]; a.State != state.Starting || !a.New {
		t.Fatalf("launched: %+v", a)
	}
	// Claude's session started (SessionStart, before any prompt).
	file = "branch main\n" + `{"session_id":"s","cwd":"/w/app","hook_event_name":"SessionStart","source":"startup"}`
	if a := Collect(ws, read, nil)[0]; a.State != state.Done || a.New || a.Last != "" || a.Branch != "main" || a.Since.Unix() != 102 {
		t.Fatalf("session started: %+v", a)
	}
	// The user's first prompt makes it work as usual.
	file = "branch main\n" + `{"session_id":"s","cwd":"/w/app","hook_event_name":"UserPromptSubmit","prompt":"go"}`
	if a := Collect(ws, read, nil)[0]; a.State != state.Working || a.New {
		t.Fatalf("prompted: %+v", a)
	}
}

func TestAReportFromBeforeTheStartGivesTheMessageButNotTheState(t *testing.T) {
	// Relaunched at 900 (hq sandbox restart); the file is the previous
	// session's, written at 500.
	old := state.Report{State: state.Done, Since: time.Unix(500, 0), Last: "PR #58 opened", Branch: "feat/42", Cwd: "/w/app/.claude/worktrees/42"}
	a := FromWindows([]tmux.Window{{ID: "@1", Options: map[string]string{"id": "x", "started": "900"}}})[0]
	a.Apply(old, true)
	if a.State != state.Starting || a.Since.Unix() != 900 || a.Last != "PR #58 opened" || a.Branch != "feat/42" || a.Worktree != "/w/app/.claude/worktrees/42" {
		t.Fatalf("relaunched: %+v", a)
	}
	a.Apply(state.Report{State: state.Working, Since: time.Unix(901, 0), Branch: "feat/42"}, true)
	if a.State != state.Working || a.Since.Unix() != 901 || a.Last != "" {
		t.Fatalf("after its first report: %+v", a)
	}
}

func TestAnAgentWorkingOrNeedingInputIsUnsettledAfterAMoment(t *testing.T) {
	now := time.Unix(1000, 0)
	report := func(s string, age time.Duration) state.Report { return state.Report{State: s, Since: now.Add(-age)} }
	for _, tc := range []struct {
		name   string
		w      tmux.Window
		r      state.Report
		ok     bool
		unsett bool
	}{
		{"needs input", tmux.Window{}, report(state.NeedsInput, time.Second), true, true},
		{"just now", tmux.Window{}, report(state.NeedsInput, 100*time.Millisecond), true, false},
		{"working", tmux.Window{}, report(state.Working, time.Second), true, true},
		{"working just now", tmux.Window{}, report(state.Working, 100*time.Millisecond), true, false},
		{"question", tmux.Window{}, report(state.Question, time.Second), true, false},
		{"starting", tmux.Window{}, report(state.Starting, time.Second), true, false},
		{"done", tmux.Window{}, report(state.Done, time.Second), true, false},
		{"no report", tmux.Window{}, state.Report{}, false, false},
		{"dead", tmux.Window{PaneDead: true}, report(state.NeedsInput, time.Second), true, false},
		{"ending", tmux.Window{Options: map[string]string{"ending": "1"}}, report(state.NeedsInput, time.Second), true, false},
	} {
		tc.w.ID = "@1"
		if tc.w.Options == nil {
			tc.w.Options = map[string]string{}
		}
		tc.w.Options["id"] = "x"
		a := FromWindows([]tmux.Window{tc.w})[0]
		a.Apply(tc.r, tc.ok)
		if got := a.Unsettled(now); got != tc.unsett {
			t.Errorf("%s: unsettled %v", tc.name, got)
		}
	}
}

func TestSettleMakesATurnTheUserEndedDoneFromWhenHqFirstSawIt(t *testing.T) {
	declined := "● User declined to answer questions\n" + strings.Repeat("─", 20) + "\n❯ \n" + strings.Repeat("─", 20) + "\n  footer\n"
	asked := time.Unix(1000, 0)
	w := tmux.Window{ID: "@1", Pane: "%1", Options: map[string]string{"id": "x"}}
	r := state.Report{State: state.NeedsInput, Since: asked, Last: "Red or blue?"}
	a := FromWindows([]tmux.Window{w})[0]
	a.Apply(r, true)

	// The dialog is still open: nothing changes.
	if rec := a.Settle("Enter to select · Esc to cancel\n", asked.Add(time.Second)).Value; rec != "" || a.State != state.NeedsInput || a.Since != asked {
		t.Fatalf("open dialog: %q %+v", rec, a)
	}
	// Cancelled: done since now, and the moment is to be recorded.
	seen := asked.Add(5 * time.Second)
	rec := a.Settle(declined, seen).Value
	if a.State != state.Done || a.Last != "User declined to answer questions" || !a.Since.Equal(seen) || a.Pane != "%1" {
		t.Fatalf("cancelled: %+v", a)
	}
	if rec == "" {
		t.Fatal("nothing to record")
	}
	// Seen again later, by hq ls or the list: since the recorded moment.
	w.Options["turnend"] = rec
	b := FromWindows([]tmux.Window{w})[0]
	b.Apply(r, true)
	if rec := b.Settle(declined, seen.Add(time.Minute)).Value; rec != "" || !b.Since.Equal(seen) {
		t.Fatalf("again: %q %+v", rec, b)
	}
	// It stays done though the screen moved on (the next prompt sent, its
	// hook not yet reported), and needs no screen at all.
	b = FromWindows([]tmux.Window{w})[0]
	b.Apply(r, true)
	if rec := b.Settle("❯ next prompt\n", seen.Add(time.Minute)).Value; rec != "" || b.State != state.Done || b.Last != "User declined to answer questions" || !b.Since.Equal(seen) {
		t.Fatalf("screen moved on: %q %+v", rec, b)
	}
	b = FromWindows([]tmux.Window{w})[0]
	b.Apply(r, true)
	if !b.Recall() || b.State != state.Done || !b.Since.Equal(seen) {
		t.Fatalf("recall: %+v", b)
	}
	for _, bad := range []string{"", "x", rec[:strings.IndexByte(rec, ' ')] + " nan last"} {
		w.Options["turnend"] = bad
		b = FromWindows([]tmux.Window{w})[0]
		b.Apply(r, true)
		if b.Recall() || b.State != state.NeedsInput {
			t.Errorf("record %q recalled: %+v", bad, b)
		}
	}
	w.Options["turnend"] = rec
	// A record for an earlier report does not count for a newer one.
	r.Since = asked.Add(time.Hour)
	c := FromWindows([]tmux.Window{w})[0]
	c.Apply(r, true)
	later := r.Since.Add(3 * time.Second)
	if rec := c.Settle(declined, later).Value; rec == "" || !c.Since.Equal(later) {
		t.Fatalf("newer report: %q %+v", rec, c)
	}
}

func TestAnEndedAgentCountsFromWhenItEnded(t *testing.T) {
	at := func(s float64) time.Time { return time.UnixMilli(int64(s * 1000)) }
	win := func(id string, dead bool, deadAt float64, o map[string]string) tmux.Window {
		w := tmux.Window{ID: "@" + id, Name: id, PaneDead: dead, Options: map[string]string{"id": id, "sandbox": "claude-app", "started": "900"}}
		if deadAt > 0 {
			w.DeadAt = at(deadAt)
		}
		for k, v := range o {
			w.Options[k] = v
		}
		return w
	}
	ws := []tmux.Window{
		win("exit", true, 1005, nil),   // /exit: the session reported its end
		win("crash", true, 1010, nil),  // killed: only tmux knows when
		win("same", true, 1000, nil),   // died in the second of its last report
		win("silent", true, 1020, nil), // never reported
		win("stopped", false, 0, nil),  // its sandbox stopped, its pane still runs
		win("restart", false, 0, map[string]string{"ending": "1"}),
		win("gone", true, 0, nil), // a docked pane that is gone: tmux cannot say
		win("working", false, 0, nil),
	}
	ws[4].Options["sandbox"] = "claude-lib"
	stopped := at(950) // the stopped agent's last report
	read := func(_, id string) (state.Report, bool) {
		switch id {
		case "stopped":
			return state.Report{State: state.Working, Since: stopped}, true
		case "exit":
			return state.Report{State: state.Ended, Since: at(1003), Last: "bye"}, true
		case "same":
			return state.Report{State: state.Done, Since: at(1000.4), Last: "ok"}, true
		case "silent":
			return state.Report{}, false
		}
		return state.Report{State: state.Working, Since: at(950)}, true
	}
	as := Collect(ws, read, map[string]bool{"claude-app": true})
	got := map[string]time.Time{}
	for _, a := range as {
		got[a.Name] = a.Since
	}
	for name, want := range map[string]time.Time{"exit": at(1003), "crash": at(1010), "same": at(1000.4), "silent": at(1020), "stopped": at(950), "restart": at(950), "gone": at(950), "working": at(950)} {
		if !got[name].Equal(want) {
			t.Errorf("%s: since %v, want %v", name, got[name], want)
		}
	}

	// Newest end first among the ended rows.
	SortAttention(as)
	var order []string
	for _, a := range as {
		order = append(order, a.Name)
	}
	if want := "working silent crash exit same stopped restart gone"; strings.Join(order, " ") != want {
		t.Errorf("order %s, want %s", strings.Join(order, " "), want)
	}

	// hq dates the ends nothing else dates when it first sees them.
	now := at(1100)
	records := map[string]string{}
	for i := range as {
		if r := as[i].SeeEnd(now).Value; r != "" {
			records[as[i].Name] = r
			if !as[i].Since.Equal(now) {
				t.Errorf("%s: since %v after SeeEnd", as[i].Name, as[i].Since)
			}
		}
	}
	if len(records) != 3 || records["stopped"] == "" || records["restart"] == "" || records["gone"] == "" {
		t.Fatalf("records %v", records)
	}
	// Seen again later: from the recorded moment, nothing more to record.
	for i := range ws {
		if r, ok := records[ws[i].Name]; ok {
			ws[i].Options["endseen"] = r
		}
	}
	for _, a := range Collect(ws, read, map[string]bool{"claude-app": true}) {
		if _, ok := records[a.Name]; ok {
			if !a.Since.Equal(now) || a.SeeEnd(at(1200)).Value != "" {
				t.Errorf("%s again: since %v", a.Name, a.Since)
			}
		}
	}
	// Relaunched (a new start) and reported before its sandbox stopped
	// again: the record is the previous session's.
	ws[4].Options["started"] = "1150"
	stopped = at(1160)
	a, _ := Find(Collect(ws, read, map[string]bool{"claude-app": true}), "stopped")
	if r := a.SeeEnd(at(1300)).Value; r == "" || !a.Since.Equal(at(1300)) {
		t.Errorf("relaunched: %q %v", r, a.Since)
	}
	// A bad record counts for nothing: the last report stands.
	ws[4].Options["endseen"] = "1150 x"
	if a, _ := Find(Collect(ws, read, map[string]bool{"claude-app": true}), "stopped"); !a.Since.Equal(at(1160)) {
		t.Errorf("bad record: %v", a.Since)
	}
}

func TestSettleTakesARewoundTurnForDoneOnceItsScreenStaysAtRest(t *testing.T) {
	rule := strings.Repeat("─", 20)
	box := func(input string) string {
		return "✻ Baked for 2s · done\n" + rule + "\n❯ " + input + "\n" + rule + "\n  ⏵⏵ bypass permissions on\n"
	}
	sent := "Write a poem about the sea."
	asked := time.Unix(1000, 0)
	w := tmux.Window{ID: "@1", Pane: "%1", Options: map[string]string{"id": "x"}}
	r := state.Report{State: state.Working, Since: asked, Last: "earlier", Prompt: sent}
	look := func() Agent {
		a := FromWindows([]tmux.Window{w})[0]
		a.Apply(r, true)
		return a
	}

	// First seen at rest: still working, the moment and the look recorded.
	first := asked.Add(3 * time.Second)
	a := look()
	end, rest := split(a.Settle(box(sent), first))
	if end != "" || rest == "" || !a.Resting() || a.State != state.Working || !a.Since.Equal(asked) {
		t.Fatalf("first look: %q %q %+v", end, rest, a)
	}
	w.Options["restseen"] = rest
	// The same a moment later: not yet.
	a = look()
	if end, rest := split(a.Settle(box(sent), first.Add(RestDelay-time.Millisecond))); end != "" || rest != "" || !a.Resting() || a.State != state.Working {
		t.Fatalf("too soon: %q %q %+v", end, rest, a)
	}
	// Unchanged for RestDelay: done since first seen at rest.
	a = look()
	end, rest = split(a.Settle(box(sent), first.Add(RestDelay)))
	if end == "" || rest != "" || a.Resting() || a.State != state.Done || a.Last != Rewound || !a.Since.Equal(first) {
		t.Fatalf("at rest: %q %q %+v", end, rest, a)
	}
	w.Options["turnend"] = end
	a = look()
	if !a.Recall() || a.State != state.Done || !a.Since.Equal(first) || a.Last != Rewound {
		t.Fatalf("recall: %+v", a)
	}
	delete(w.Options, "turnend")

	// The screen changed meanwhile (the user cleared the box): at rest
	// from now, as it looks now.
	a = look()
	later := first.Add(time.Minute)
	if end, rest := split(a.Settle(box(""), later)); end != "" || rest == "" || rest == w.Options["restseen"] || !strings.HasSuffix(rest, strconv.FormatInt(later.UnixNano(), 10)) {
		t.Fatalf("changed: %q %q", end, rest)
	}
	// A newer report: an earlier look does not count.
	r.Since = asked.Add(time.Hour)
	a = look()
	if end, rest := split(a.Settle(box(sent), r.Since.Add(time.Hour))); end != "" || rest == "" {
		t.Fatalf("newer report: %q %q", end, rest)
	}
	// Not at rest: text the user typed, a turn at work, another state.
	r.Since = asked
	for name, tc := range map[string]struct {
		screen, state string
	}{
		"typed":      {box("my own draft"), state.Working},
		"at work":    {box("") + "  esc to interrupt\n", state.Working},
		"a dialog":   {box(""), state.NeedsInput},
		"no box":     {"● streaming\n", state.Working},
		"broken rec": {box(sent), state.Working},
	} {
		r.State = tc.state
		if name == "broken rec" {
			w.Options["restseen"] = "x y z"
		}
		a := look()
		end, rest := split(a.Settle(tc.screen, first.Add(time.Hour)))
		if end != "" || a.State != tc.state || (name != "broken rec" && (rest != "" || a.Resting())) {
			t.Errorf("%s: %q %q %+v", name, end, rest, a)
		}
	}
}

func TestSettleTakesARewindRightAfterACancelledDialogForARewind(t *testing.T) {
	rule := strings.Repeat("─", 20)
	sent := "Write a poem about the sea."
	screen := "❯ Ask me red or blue\n● User declined to answer questions\n  ⎿  · Red or blue?\n✻ Worked for 3s · done\n" +
		rule + "\n❯ " + sent + "\n" + rule + "\n  ⏵⏵ bypass permissions on\n"
	asked := time.Unix(1000, 0)
	w := tmux.Window{ID: "@1", Pane: "%1", Options: map[string]string{"id": "x"}}
	r := state.Report{State: state.Working, Since: asked, Prompt: sent}
	a := FromWindows([]tmux.Window{w})[0]
	a.Apply(r, true)
	first := asked.Add(3 * time.Second)
	end, rest := split(a.Settle(screen, first))
	if end != "" || rest == "" || a.State != state.Working {
		t.Fatalf("first look: %q %q %+v", end, rest, a)
	}
	w.Options["restseen"] = rest
	a = FromWindows([]tmux.Window{w})[0]
	a.Apply(r, true)
	if end := a.Settle(screen, first.Add(RestDelay)).Value; end == "" || a.State != state.Done || a.Last != Rewound || !a.Since.Equal(first) {
		t.Fatalf("at rest: %q %+v", end, a)
	}
}

// split tells a turnend record from a restseen one.
func split(r Record) (turnEnd, restSeen string) {
	switch r.Option {
	case "turnend":
		return r.Value, ""
	case "restseen":
		return "", r.Value
	}
	return "", ""
}

func TestKeepShowsTheRecordAnotherProcessStoredFirst(t *testing.T) {
	rule := strings.Repeat("─", 20)
	asked := time.Unix(1000, 0)
	earlier, now := asked.Add(4*time.Second), asked.Add(5*time.Second)

	// A turn the user ended: done from when the other process saw it.
	declined := "● User declined to answer questions\n" + rule + "\n❯ \n" + rule + "\n  footer\n"
	a := FromWindows([]tmux.Window{{ID: "@1", Options: map[string]string{"id": "x"}}})[0]
	a.Apply(state.Report{State: state.NeedsInput, Since: asked, Last: "Red or blue?"}, true)
	r := a.Settle(declined, now)
	a.Keep(r, r.Value)
	if !a.Since.Equal(now) {
		t.Fatalf("its own record: %+v", a)
	}
	a.Keep(r, r.Key+nanos(earlier)+" User declined to answer questions")
	if a.State != state.Done || !a.Since.Equal(earlier) || a.Last != "User declined to answer questions" {
		t.Fatalf("turnend: %+v", a)
	}

	// A rewound turn: at rest from when the other process first saw it, so
	// it is done once that is RestDelay ago.
	box := "✻ Baked for 2s · done\n" + rule + "\n❯ \n" + rule + "\n  ⏵⏵ bypass permissions on\n"
	b := FromWindows([]tmux.Window{{ID: "@2", Options: map[string]string{"id": "y"}}})[0]
	b.Apply(state.Report{State: state.Working, Since: asked}, true)
	r = b.Settle(box, now)
	other := asked.Add(time.Second)
	b.Keep(r, r.Key+nanos(other))
	if r := b.Settle(box, other.Add(RestDelay)); r.Option != "turnend" || b.State != state.Done || !b.Since.Equal(other) {
		t.Fatalf("restseen: %+v %+v", r, b)
	}

	// An ended agent: ended from when the other process saw it.
	c := FromWindows([]tmux.Window{{ID: "@3", Options: map[string]string{"id": "z", "started": "900"}}})[0]
	c.State = state.Ended
	r = c.SeeEnd(now)
	c.Keep(r, r.Key+nanos(earlier))
	if !c.Since.Equal(earlier) {
		t.Fatalf("endseen: %+v", c)
	}
}

func TestAnEndedAgentsEndSeenBeforeItsLastReportDoesNotCount(t *testing.T) {
	// Relaunched by hq sandbox restart at 1150, it reported at 1200, and its
	// pane died at 1300 as its sandbox stopped (#104).
	at := func(s float64) time.Time { return time.UnixMilli(int64(s * 1000)) }
	read := func(_, _ string) (state.Report, bool) {
		return state.Report{State: state.Working, Since: at(1200)}, true
	}
	since := func(endSeen string) time.Time {
		w := tmux.Window{ID: "@1", PaneDead: true, DeadAt: at(1300), Options: map[string]string{"id": "x", "sandbox": "claude-app", "started": "1150", "endseen": endSeen}}
		return Collect([]tmux.Window{w}, read, nil)[0].Since
	}
	for name, tc := range map[string]struct {
		endSeen string
		want    time.Time
	}{
		"seen during the relaunch, before a report": {"1150 0 " + nanos(at(1150.046)), at(1300)},
		"an older hq's record of the relaunch":      {"1150 " + nanos(at(1150.046)), at(1300)},
		"seen after its last report":                {"1150 " + nanos(at(1200)) + " " + nanos(at(1250)), at(1250)},
		"none":                                      {"", at(1300)},
	} {
		if got := since(tc.endSeen); !got.Equal(tc.want) {
			t.Errorf("%s: since %v, want %v", name, got, tc.want)
		}
	}
	// Ended again after a report, it is seen ended afresh.
	w := tmux.Window{ID: "@1", Options: map[string]string{"id": "x", "sandbox": "claude-app", "started": "1150", "ending": "1", "endseen": "1150 0 " + nanos(at(1150.046))}}
	a := Collect([]tmux.Window{w}, read, nil)[0]
	if r := a.SeeEnd(at(1400)); r.Value != "1150 "+nanos(at(1200))+" "+nanos(at(1400)) || !a.Since.Equal(at(1400)) {
		t.Errorf("seen afresh: %+v %v", r, a.Since)
	}
}

func TestEnteredIsWhenTheReportedStateCanBeSeen(t *testing.T) {
	// A state the hooks report is seen from the state file's time, as AGE
	// counts it; so is a turn end hq saw on the screen, from the record.
	asked := time.Unix(1000, 0)
	a := FromWindows([]tmux.Window{{ID: "@1", Options: map[string]string{"id": "x", "started": "900"}}})[0]
	a.Apply(state.Report{State: state.Question, Since: asked}, true)
	if !a.Entered().Equal(asked) {
		t.Fatalf("question: %v", a.Entered())
	}
	declined := "● User declined to answer questions\n" + strings.Repeat("─", 20) + "\n❯ \n" + strings.Repeat("─", 20) + "\n  footer\n"
	b := FromWindows([]tmux.Window{{ID: "@2", Options: map[string]string{"id": "y"}}})[0]
	b.Apply(state.Report{State: state.NeedsInput, Since: asked}, true)
	seen := asked.Add(5 * time.Second)
	b.Settle(declined, seen)
	if !b.Entered().Equal(seen) {
		t.Fatalf("cancelled dialog: %v", b.Entered())
	}
}

func TestARewoundTurnIsEnteredWhenItIsDecided(t *testing.T) {
	// A rewind counts from when its screen was first seen at rest (AGE), but
	// only RestDelay later is it done for anyone to see: in its own look,
	// and in every process that recalls the records.
	rule := strings.Repeat("─", 20)
	box := "✻ Baked for 2s · done\n" + rule + "\n❯ \n" + rule + "\n  ⏵⏵ bypass permissions on\n"
	asked := time.Unix(1000, 0)
	w := tmux.Window{ID: "@1", Pane: "%1", Options: map[string]string{"id": "x"}}
	r := state.Report{State: state.Working, Since: asked}
	look := func() Agent {
		a := FromWindows([]tmux.Window{w})[0]
		a.Apply(r, true)
		return a
	}
	first := asked.Add(3 * time.Second)
	a := look()
	_, rest := split(a.Settle(box, first))
	w.Options["restseen"] = rest
	a = look()
	end, _ := split(a.Settle(box, first.Add(RestDelay)))
	if a.State != state.Done || !a.Since.Equal(first) || !a.Entered().Equal(first.Add(RestDelay)) {
		t.Fatalf("decided: %+v entered %v", a, a.Entered())
	}
	w.Options["turnend"] = end
	a = look()
	if !a.Recall() || !a.Entered().Equal(first.Add(RestDelay)) {
		t.Fatalf("recalled: %+v entered %v", a, a.Entered())
	}
	// An Esc that printed Interrupted is no rewind, whatever the rest
	// record of the same report says.
	w.Options["turnend"] = strings.Replace(end, nanos(first), nanos(first.Add(time.Second)), 1)
	a = look()
	if !a.Recall() || a.Last != Rewound || !a.Entered().Equal(first.Add(time.Second)) {
		t.Fatalf("interrupted: %+v entered %v", a, a.Entered())
	}
}

func TestAnEndedAgentIsEnteredFromTheFirstSignOfItsEnd(t *testing.T) {
	at := func(s float64) time.Time { return time.UnixMilli(int64(s * 1000)) }
	entered := func(dead bool, deadAt float64, report state.Report, endSeen string) time.Time {
		w := tmux.Window{ID: "@1", PaneDead: dead, Options: map[string]string{"id": "x", "sandbox": "claude-app", "started": "900", "endseen": endSeen}}
		if deadAt > 0 {
			w.DeadAt = at(deadAt)
		}
		as := Collect([]tmux.Window{w}, func(_, _ string) (state.Report, bool) { return report, true }, nil)
		if as[0].State != state.Ended {
			t.Fatalf("not ended: %+v", as[0])
		}
		return as[0].Entered()
	}
	working := state.Report{State: state.Working, Since: at(1000)}
	exited := state.Report{State: state.Ended, Since: at(1000.4)}
	for name, tc := range map[string]struct {
		got, want time.Time
	}{
		// tmux dates a pane's death to the whole second: it died by a
		// second later, though AGE counts from the date.
		"its pane died":        {entered(true, 1001, working, ""), at(1002)},
		"its session reported": {entered(true, 1001, exited, ""), at(1000.4)},
		"hq first saw it":      {entered(true, 0, working, "900 "+nanos(at(1000))+" "+nanos(at(1003.5))), at(1003.5)},
		"seen, then it died":   {entered(true, 1005, working, "900 "+nanos(at(1000))+" "+nanos(at(1003.5))), at(1003.5)},
		"no date yet":          {entered(true, 0, working, ""), at(1000)},
	} {
		if !tc.got.Equal(tc.want) {
			t.Errorf("%s: entered %v, want %v", name, tc.got, tc.want)
		}
	}
}
