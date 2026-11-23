package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/journal/journaltest"
	"github.com/rkrysinski/hq/internal/sbx"
	"github.com/rkrysinski/hq/internal/state"
)

// askCounting is f's sbx, noting the moment of each ls hq asks it for.
type askCounting struct {
	Sandboxes
	f     *fakes
	asked *[]time.Time
}

func (s askCounting) List() ([]sbx.Sandbox, error) {
	*s.asked = append(*s.asked, s.f.now)
	return s.Sandboxes.List()
}

// runAskingSbx runs hq with args against f, and gives the moments, on f's
// clock, hq asked sbx which sandboxes there are: through its poll (sbx ls
// within a time limit) or through sbx itself.
func (f *fakes) runAskingSbx(args ...string) (code int, errOut string, asked []time.Time) {
	d := f.deps()
	d.sbx = askCounting{Sandboxes: f.sbx, f: f, asked: &asked}
	d.pollSandboxes = d.sbx.List
	var out, errs bytes.Buffer
	code = mainWith(args, Env{Stdin: strings.NewReader(f.stdin), Stdout: &out, Stderr: &errs}, d)
	return code, errs.String(), asked
}

// hq explain takes a look as hq ls does, but types in no message left
// waiting for an agent at its prompt (design §3.6): the same agent gets it
// typed in by the hq ls that follows.
func TestExplainTypesInNoMessageLeftWaiting(t *testing.T) {
	f := waitingFakes()
	if code, _, errOut := f.run("explain", "a"); code != 0 {
		t.Fatalf("exit %d %q", code, errOut)
	}
	if len(f.tmux.pasted) != 0 || len(f.tmux.submitted) != 0 || strings.Join(f.inbox["id-a"], "|") != "/effort medium" {
		t.Fatalf("pasted %q submitted %v inbox %v", f.tmux.pasted, f.tmux.submitted, f.inbox)
	}
	mustRun(t, f, "ls")
	if got := strings.Join(f.tmux.pasted, "|"); got != "%4 /effort medium" || len(f.inbox["id-a"]) != 0 {
		t.Fatalf("hq ls after it: pasted %q inbox %v", got, f.inbox)
	}
}

// The chords and docking as the dashboard is entered take a look that asks
// sbx nothing: they read what the agents' journals and screens show.
func TestTheChordsAndDockingOnEntryAskSbxNothing(t *testing.T) {
	for _, args := range [][]string{{"__chord", "next"}, {"__chord", "previous"}, {"__chord", "attention"}, {}, {"dash"}} {
		f := chordFakes()
		f.tmux.session["cursor"] = "c"
		code, errOut, asked := f.runAskingSbx(args...)
		if code != ExitOK || f.tmux.docked == "" {
			t.Fatalf("%v: exit %d %q, docked %q", args, code, errOut, f.tmux.docked)
		}
		if len(asked) != 0 {
			t.Fatalf("%v: asked sbx %d times", args, len(asked))
		}
	}
}

// intervals are the times between consecutive moments of ts.
func intervals(ts []time.Time) []time.Duration {
	var ds []time.Duration
	for i := 1; i < len(ts); i++ {
		ds = append(ds, ts[i].Sub(ts[i-1]))
	}
	return ds
}

// hq send looks at a starting agent every 250 ms until it leaves starting,
// and each of its looks asks sbx.
func TestSendLooksEvery250msWhileItsAgentStartsEachLookAskingSbx(t *testing.T) {
	f := sendFakes("")
	sleeps := 0
	f.onSleep = func() {
		if sleeps++; sleeps == 4 {
			f.states["id-a"] = journaltest.HookState{State: state.Done, Since: f.now}
		}
	}
	start := f.now
	code, errOut, asked := f.runAskingSbx("send", "a", "hi")
	if code != 0 {
		t.Fatalf("exit %d %q", code, errOut)
	}
	// The look that finds it, and one after each of its four sleeps.
	if len(asked) != 5 || !asked[0].Equal(start) {
		t.Fatalf("asked sbx at %v, from %v", asked, start)
	}
	for _, d := range intervals(asked) {
		if d != 250*time.Millisecond {
			t.Fatalf("asked sbx %v apart: %v", intervals(asked), asked)
		}
	}
}

// hq answer, once the agent's hook took the answer, looks at the agent
// every 100 ms until it leaves the dialog, here for the whole 5 s it waits,
// and each of its looks asks sbx.
func TestAnswerLooksEvery100msUntilItsAgentLeavesTheDialogEachLookAskingSbx(t *testing.T) {
	f, id := askFakes(t, "dialog-ask")
	f.onSleep = func() { delete(f.answerLeft, "id-a") }
	start := f.now
	code, _, asked := f.runAskingSbx("answer", "a", "--dialog", id, "1")
	if code != ExitEnvironment {
		t.Fatalf("exit %d", code)
	}
	// The look that finds the agent, then the hook takes the answer at the
	// first sleep, 100 ms later; from then a look every 100 ms, the last
	// at 5 s.
	if len(asked) != 2+int(closeWait/(100*time.Millisecond)) || !asked[0].Equal(start) || !asked[len(asked)-1].Equal(start.Add(100*time.Millisecond+closeWait)) {
		t.Fatalf("asked sbx %d times, at %v, from %v", len(asked), asked, start)
	}
	for _, d := range intervals(asked) {
		if d != 100*time.Millisecond {
			t.Fatalf("asked sbx %v apart", intervals(asked))
		}
	}
}
