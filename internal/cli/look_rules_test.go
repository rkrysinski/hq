package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/rkrysinski/hq/internal/journal"
	"github.com/rkrysinski/hq/internal/state"
)

// meanwhile has f's agent a settled by a look, then has another process
// number a change of agent b as the next look starts reading a's journal,
// after it read the counter, and then do also: that look reads a's
// journal past the counter it began with. It gives what the counter holds
// before that look.
func meanwhile(t *testing.T, f *fakes, also func()) int64 {
	t.Helper()
	mustRun(t, f, "ls", "--json")
	b := journalID("id-b")
	f.onRead = func(string) {
		f.onRead = nil
		_, _ = f.journal.Append(b, state.Event{Source: state.SourceHQ, Kind: state.KindLaunched, Time: f.now})
		also()
	}
	n, err := f.journal.Through()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// A look whose reads of the journals are all past the counter it began
// with vouches for the lowest counter of those reads, once the windows
// listed again show no agent appeared; when one did, or tmux cannot list
// them again, only for the counter it began with.
func TestTheLooksCursorIsItsLowestReadUnlessAnAgentMayHaveAppeared(t *testing.T) {
	f := sendFakes(state.Done)
	before := meanwhile(t, f, func() {})
	f.tmux.windowsRead = 0
	ls := waitJSON(t, mustRun(t, f, "ls", "--json"))
	if got := cursorNumber(t, ls.NextSince); got != before+1 || f.tmux.windowsRead != 2 {
		t.Fatalf("next_since %s, want c%d, the counter of its read; windows listed %d times", ls.NextSince, before+1, f.tmux.windowsRead)
	}

	for name, meanwhileTmux := range map[string]func(f *fakes){
		"an agent appeared": func(f *fakes) {
			f.tmux.windows = append(f.tmux.windows, agentWindow("@9", "z", "/w/app", f.now, false))
		},
		"tmux cannot list them again": func(f *fakes) { f.tmux.windowsErr = errors.New("tmux gone") },
	} {
		f := sendFakes(state.Done)
		before := meanwhile(t, f, func() { meanwhileTmux(f) })
		ls := waitJSON(t, mustRun(t, f, "ls", "--json"))
		if got := cursorNumber(t, ls.NextSince); got != before {
			t.Fatalf("%s: next_since %s, want c%d, the counter before the look", name, ls.NextSince, before)
		}
	}
}

// hq ls --json fails as a look fails when one of its looks again does.
func TestLsJSONFailsWhenALookAgainFails(t *testing.T) {
	looks := 0
	f := dialogOnB(t, true, &looks)
	count := f.onThrough
	f.onThrough = func() {
		if count(); looks == 2 {
			f.tmux.windowsErr = errors.New("tmux gone")
		}
	}
	if code, out, errOut := f.run("ls", "--json"); code != ExitEnvironment || out != "" || errOut != "hq: tmux gone\n" {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
}

// A chord and hq explain fail as their look does when tmux cannot list
// the windows.
func TestAChordAndExplainFailWhenTmuxCannotListTheWindows(t *testing.T) {
	f := chordFakes()
	f.tmux.windowsErr = errors.New("tmux gone")
	if code, _, _ := f.run("__chord", "next"); code != ExitEnvironment || strings.Join(f.tmux.messages, "|") != "hq: tmux gone" {
		t.Fatalf("chord: exit %d, messages %q", code, f.tmux.messages)
	}
	f = sendFakes(state.Done)
	f.tmux.windowsErr = errors.New("tmux gone")
	if code, out, errOut := f.run("explain", "a"); code != ExitEnvironment || out != "" || errOut != "hq: tmux gone\n" {
		t.Fatalf("explain: exit %d %q %q", code, out, errOut)
	}
}

// noCounter is a journals' store whose counter cannot be read on its own.
type noCounter struct{ journal.Store }

func (noCounter) Through() (int64, error) { return 0, errors.New("the state directory is locked") }

// hq send's cursor, when the counter cannot be read as it leaves the
// message, is the counter of its look's read of the agent's journal: still
// below the delivery.
func TestSendsCursorWithoutTheCounterIsItsReadOfTheAgent(t *testing.T) {
	f := sendFakes(state.Done)
	d := f.deps()
	d.journal = noCounter{d.journal}
	var out, errOut bytes.Buffer
	if code := mainWith([]string{"send", "a", "No", "--json"}, Env{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errOut}, d); code != 0 {
		t.Fatalf("exit %d %q", code, errOut.String())
	}
	var sent sendOutput
	if err := json.Unmarshal(out.Bytes(), &sent); err != nil {
		t.Fatal(err)
	}
	if want := f.cursorBefore(t, "id-a", state.KindDelivery); sent.NextSince != want {
		t.Fatalf("next_since %s, want %s, just below the delivery", sent.NextSince, want)
	}
}
