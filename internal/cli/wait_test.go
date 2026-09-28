package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
)

// newWaitFakes has two agents, a and b, working in app since a minute ago.
func newWaitFakes() *fakes {
	f := newLsFakes()
	start := f.now.Add(-time.Hour)
	f.tmux.windows = []tmux.Window{
		{ID: "@0", Name: "hq", Options: map[string]string{}},
		agentWindow("@1", "a", "/w/app", start, false),
		agentWindow("@2", "b", "/w/app", start, false),
	}
	for _, id := range []string{"id-a", "id-b"} {
		f.states[id] = state.Report{State: state.Working, Since: f.now.Add(-time.Minute), Last: "on it"}
	}
	return f
}

// after runs step once the fake clock has passed d from now.
func (f *fakes) after(d time.Duration, step func()) {
	at, done := f.now.Add(d), false
	f.onSleep = func() {
		if !done && !f.now.Before(at) {
			done = true
			step()
		}
	}
}

func waitJSON(t *testing.T, out string) waitOutput {
	t.Helper()
	var w waitOutput
	if err := json.Unmarshal([]byte(out), &w); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	return w
}

func TestWaitReturnsAnAgentThatTurnsDoneWithItsRowAndTheNextSince(t *testing.T) {
	f := newWaitFakes()
	start := f.now
	var doneAt time.Time
	f.after(10*time.Second, func() {
		doneAt = f.now
		f.states["id-b"] = state.Report{State: state.Done, Since: f.now, Last: "PR #58 opened", Branch: "feat/58"}
	})
	code, out, errOut := f.run("wait")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	// It waited while the agents only worked, and returned within a look or
	// two of the change: what a look sees is taken once it is waitSettle old.
	if waited := f.now.Sub(doneAt); f.now.Sub(start) < 10*time.Second || waited < waitSettle || waited > time.Second {
		t.Fatalf("returned %v after the change", waited)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "NAME") || !strings.HasPrefix(lines[1], "b ") ||
		!strings.Contains(lines[1], "done") || !strings.Contains(lines[1], "PR #58 opened") || !strings.Contains(lines[1], "feat/58") {
		t.Fatalf("output:\n%s", out)
	}
	next := strings.TrimPrefix(lines[2], "next: hq wait --since ")
	if at, err := time.Parse(time.RFC3339Nano, next); err != nil || at.Before(doneAt) || at.After(f.now) {
		t.Fatalf("next line %q (done at %v, now %v)", lines[2], doneAt, f.now)
	}
}

func TestWaitDoesNotReturnAnAgentAlreadyDoneWhenItStartsAndTimesOutWithNothing(t *testing.T) {
	f := newWaitFakes()
	f.states["id-a"] = state.Report{State: state.Done, Since: f.now.Add(-time.Second), Last: "Done: hello"}
	start := f.now
	code, out, _ := f.run("wait", "--timeout", "3s")
	if code != 0 || !strings.HasPrefix(out, "nothing yet\nnext: hq wait --since ") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if took := f.now.Sub(start); took < 3*time.Second || took > 3*time.Second+waitEvery {
		t.Fatalf("returned after %v", took)
	}
	code, out, _ = f.run("wait", "--timeout=1", "--json")
	w := waitJSON(t, out)
	if code != 0 || w.Agents == nil || len(w.Agents) != 0 || w.NextSince.IsZero() || !strings.Contains(out, `"agents": []`) {
		t.Fatalf("json: exit %d\n%s", code, out)
	}
}

func TestWaitSinceTheLastMomentReportsWhatChangedBetweenCalls(t *testing.T) {
	f := newWaitFakes()
	code, out, _ := f.run("wait", "--timeout", "1s", "--json")
	first := waitJSON(t, out)
	if code != 0 || len(first.Agents) != 0 {
		t.Fatalf("first call: %s", out)
	}
	// Between the calls a turns question, and b is done and working again.
	f.now = f.now.Add(5 * time.Second)
	f.states["id-a"] = state.Report{State: state.Question, Since: f.now, Last: "Shall I go on?"}
	f.states["id-b"] = state.Report{State: state.Working, Since: f.now, Last: "on it"}
	f.now = f.now.Add(10 * time.Second)
	start := f.now
	code, out, _ = f.run("wait", "--since", first.NextSince.Format(time.RFC3339Nano), "--json")
	second := waitJSON(t, out)
	if code != 0 || len(second.Agents) != 1 || second.Agents[0].Name != "a" || second.Agents[0].State != state.Question || f.now != start {
		t.Fatalf("second call: %s (took %v)", out, f.now.Sub(start))
	}
	// Reported once: the next call from its moment waits on.
	code, out, _ = f.run("wait", "--since", second.NextSince.Format(time.RFC3339Nano), "--timeout", "2s", "--json")
	if third := waitJSON(t, out); code != 0 || len(third.Agents) != 0 {
		t.Fatalf("third call: %s", out)
	}
	// Without --since the same change is no news.
	if code, out, _ := f.run("wait", "--timeout", "1s"); code != 0 || !strings.HasPrefix(out, "nothing yet") {
		t.Fatalf("no since: %s", out)
	}
	// --since also takes a duration back from now.
	if code, out, _ := f.run("wait", "--since", "1m", "--json"); code != 0 || len(waitJSON(t, out).Agents) != 1 {
		t.Fatalf("since 1m: %s", out)
	}
}

func TestWaitReportsAStateEnteredJustBeforeItsMomentInTheNextCall(t *testing.T) {
	// A look takes only what entered its state waitSettle before it began;
	// what came after is the next call's, never missed and never twice.
	f := newWaitFakes()
	var nexts []time.Time
	f.after(time.Second, func() {
		f.states["id-a"] = state.Report{State: state.Done, Since: f.now.Add(-waitSettle / 2), Last: "Done"}
	})
	code, out, _ := f.run("wait", "--timeout", "1s", "--json")
	w := waitJSON(t, out)
	if code != 0 || len(w.Agents) != 0 {
		t.Fatalf("first call: %s", out)
	}
	nexts = append(nexts, w.NextSince)
	code, out, _ = f.run("wait", "--since", w.NextSince.Format(time.RFC3339Nano), "--timeout", "1s", "--json")
	if w = waitJSON(t, out); code != 0 || len(w.Agents) != 1 || w.Agents[0].Name != "a" || !w.NextSince.After(nexts[0]) {
		t.Fatalf("second call: %s", out)
	}
}

func TestWaitOnNamedAgentsIgnoresTheOthers(t *testing.T) {
	f := newWaitFakes()
	f.after(time.Second, func() {
		f.states["id-b"] = state.Report{State: state.Done, Since: f.now, Last: "b done"}
	})
	code, out, _ := f.run("wait", "a", "--timeout", "3s")
	if code != 0 || !strings.HasPrefix(out, "nothing yet\nnext: hq wait a --since ") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	code, _, errOut := f.run("wait", "a", "nobody")
	if code != ExitNotFound || errOut != "hq: no agent 'nobody' (see hq ls)\n" {
		t.Fatalf("unknown name: exit %d %q", code, errOut)
	}
}

func TestWaitReturnsAnAgentKilledWhileWaitedOnAsEnded(t *testing.T) {
	f := newWaitFakes()
	f.after(time.Second, func() { f.tmux.windows = f.tmux.windows[:2] })
	code, out, _ := f.run("wait", "b", "--json")
	w := waitJSON(t, out)
	if code != 0 || len(w.Agents) != 1 || w.Agents[0].Name != "b" || w.Agents[0].State != state.Ended || w.Agents[0].Last != "on it" {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}

func TestWaitReturnsEachStateThatNeedsAttention(t *testing.T) {
	for _, st := range []string{state.Done, state.Question, state.NeedsInput, state.Ended} {
		f := newWaitFakes()
		f.after(time.Second, func() {
			f.states["id-a"] = state.Report{State: st, Since: f.now, Last: "x"}
		})
		code, out, _ := f.run("wait", "--json")
		if w := waitJSON(t, out); code != 0 || len(w.Agents) != 1 || w.Agents[0].State != st {
			t.Errorf("%s: %s", st, out)
		}
	}
	// A pane that dies ends its agent, dated by tmux to the whole second.
	f := newWaitFakes()
	f.after(1500*time.Millisecond, func() {
		f.tmux.windows[1].PaneDead, f.tmux.windows[1].DeadAt = true, f.now.Truncate(time.Second)
	})
	died := f.now.Add(1500 * time.Millisecond)
	code, out, _ := f.run("wait", "--json")
	if w := waitJSON(t, out); code != 0 || len(w.Agents) != 1 || w.Agents[0].State != state.Ended || f.now.Sub(died) > 2*time.Second {
		t.Errorf("pane died: %s after %v", out, f.now.Sub(died))
	}
}

func TestWaitRefusesBadArguments(t *testing.T) {
	f := newWaitFakes()
	for _, args := range [][]string{
		{"wait", "--timeout"},
		{"wait", "--timeout", "soon"},
		{"wait", "--timeout", "-1s"},
		{"wait", "--since", "yesterday"},
		{"wait", "--json=yes"},
		{"wait", "-x"},
	} {
		if code, _, errOut := f.run(args...); code != ExitUsage || !strings.HasPrefix(errOut, "hq: ") {
			t.Errorf("%v: exit %d %q", args, code, errOut)
		}
	}
	f.tmux.missing = true
	if code, _, _ := f.run("wait"); code != ExitEnvironment {
		t.Errorf("no tmux: exit %d", code)
	}
}

func TestParseWaitReadsNamesAndOptions(t *testing.T) {
	now := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	w, err := parseWait([]string{"42", "--since=2026-09-28T09:59:30.5Z", "bok-17", "--timeout", "90", "42", "--json"}, now)
	if err != nil || strings.Join(w.names, " ") != "42 bok-17" || !w.asJSON || w.timeout != 90*time.Second ||
		!w.since.Equal(now.Add(-29500*time.Millisecond)) || !w.sinceArg {
		t.Fatalf("%+v %v", w, err)
	}
	w, err = parseWait([]string{"--timeout", "0", "--since", "10m"}, now)
	if err != nil || w.timeout != 0 || !w.since.Equal(now.Add(-10*time.Minute)) || len(w.names) != 0 {
		t.Fatalf("%+v %v", w, err)
	}
	if w, _ := parseWait(nil, now); w.timeout != DefaultWaitTimeout || w.sinceArg {
		t.Fatalf("defaults: %+v", w)
	}
}
