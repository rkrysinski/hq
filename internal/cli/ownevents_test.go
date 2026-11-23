package cli

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/rkrysinski/hq/internal/journal/journaltest"
	"github.com/rkrysinski/hq/internal/state"
)

// killAt runs hq kill name -y from the moment hq appends its first event
// of kind to agent id's journal, before that append is done (#318), as a
// kill in another hq process lands between a command's look and its
// append; it returns whether the kill ran.
func (f *fakes) killAt(t *testing.T, id, name, kind string) *bool {
	t.Helper()
	killed := new(bool)
	f.onAppend = func(got string, events []state.Event) {
		if got != id || *killed || !slices.ContainsFunc(events, func(e state.Event) bool { return e.Source == state.SourceHQ && e.Kind == kind }) {
			return
		}
		*killed = true
		if code, _, errOut := f.run("kill", name, "-y"); code != 0 {
			t.Errorf("kill %s: exit %d %s", name, code, errOut)
		}
	}
	return killed
}

// No event of hq's own writes back the journal of an agent hq kill
// removed meanwhile (#318): whichever command found the agent before the
// kill, the store holds no journal of it once both have ended.
func TestHqsOwnEventsForAnAgentKilledMeanwhileWriteNoJournalBack(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind string // the event the kill lands at
		// fakes are the fakes the command racing the kill runs with, and
		// its arguments.
		fakes func(t *testing.T) (*fakes, []string)
	}{
		{"hq send, the message typed in", state.KindDelivery, func(*testing.T) (*fakes, []string) {
			return sendFakes(state.Done), []string{"send", "a", "hi"}
		}},
		{"hq ls, typing a waiting message in", state.KindDelivery, func(*testing.T) (*fakes, []string) {
			return waitingFakes(), []string{"ls"}
		}},
		{"hq answer, a question dialog", state.KindAnswer, func(t *testing.T) (*fakes, []string) {
			f, id := askFakes(t, "dialog-ask")
			f.holding(nil)
			return f, []string{"answer", "a", "--dialog", id, "2"}
		}},
		{"hq answer, a dialog of Claude's own", state.KindAnswer, func(t *testing.T) (*fakes, []string) {
			f, _, id := ownDialogFakes(t)
			return f, []string{"answer", "a", "--dialog", id, "2"}
		}},
		{"hq sandbox restart, killed at TakenDown", state.KindTakenDown, func(*testing.T) (*fakes, []string) {
			return sandboxFakes(), []string{"sandbox", "restart", "app", "-y"}
		}},
		{"hq sandbox restart, killed at Relaunched", state.KindRelaunched, func(*testing.T) (*fakes, []string) {
			return sandboxFakes(), []string{"sandbox", "restart", "app", "-y"}
		}},
		// A withdrawal that is the first of hq's events to meet the
		// removed journal: the typing, or the respawn, fails after the
		// kill.
		{"hq send, the typing failing after the kill", state.KindWithdrawn, func(*testing.T) (*fakes, []string) {
			f := sendFakes(state.Done)
			f.tmux.pasteErr = errors.New("no pane %4")
			return f, []string{"send", "a", "hi"}
		}},
		{"hq sandbox restart, the respawn failing after the kill", state.KindWithdrawn, func(*testing.T) (*fakes, []string) {
			f := sandboxFakes()
			f.tmux.respawnErr = errors.New("no pane %4")
			return f, []string{"sandbox", "restart", "app", "-y"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, args := tc.fakes(t)
			killed := f.killAt(t, "id-a", "a", tc.kind)
			f.run(args...)
			if !*killed {
				t.Fatalf("no %s appended for a", tc.kind)
			}
			f.noJournalOf(t, "id-a")
		})
	}
}

// An agent whose home window stands while its journal is not there (a
// lost state directory, here gone between hq send's look and its append)
// gets the delivery hq types in, which begins its journal, as before
// (#318). When tmux cannot list its windows at that moment, the message is
// typed in all the same and hq send says and exits as with tmux
// answering, but no journal is written: a kill under way would leave it
// for good.
func TestSendToAnAgentWhoseJournalIsNotThereTypesItInAndWritesItOnlyWhileItsWindowStands(t *testing.T) {
	for _, tmuxFails := range []bool{false, true} {
		f := sendFakes(state.Done)
		lost := false
		f.onAppend = func(id string, events []state.Event) {
			if id != "id-a" || lost || events[0].Kind != state.KindDelivery {
				return
			}
			lost = true
			if err := f.journal.Remove("/w/app", journalID("id-a")); err != nil {
				t.Fatal(err)
			}
			if tmuxFails {
				f.tmux.windowsErr = errors.New("tmux gone")
			}
		}
		code, out, errOut := f.run("send", "a", "hi")
		if !lost || code != 0 || out != "delivered: typed into a as its next prompt\n" || errOut != "" {
			t.Fatalf("tmux fails %v: lost %v, exit %d %q %q", tmuxFails, lost, code, out, errOut)
		}
		if got := strings.Join(f.tmux.pasted, "|"); got != "%4 hi" || strings.Join(f.tmux.submitted, " ") != "%4" {
			t.Fatalf("tmux fails %v: pasted %q submitted %v", tmuxFails, got, f.tmux.submitted)
		}
		f.tmux.windowsErr = nil
		if tmuxFails {
			f.noJournalOf(t, "id-a")
			continue
		}
		if got := journaltest.Kinds(journalOf(t, f, journalID("id-a"))); got != state.KindDelivery {
			t.Fatalf("journal %s", got)
		}
	}
}

// hq new begins the agent's journal with its launch whatever tmux says at
// that moment (#318): the window it just made is the agent's.
func TestNewBeginsTheJournalWithTheLaunchWhenTmuxCannotListItsWindows(t *testing.T) {
	f := newFakes()
	f.sbx.sandboxes = sandboxesFor("/w/app")
	failed := false
	f.onAppend = func(_ string, events []state.Event) {
		if events[0].Kind == state.KindLaunched {
			failed, f.tmux.windowsErr = true, errors.New("tmux gone")
		}
	}
	f.run("new", "a")
	f.tmux.windowsErr = nil
	if !failed {
		t.Fatal("no launch appended")
	}
	id := f.tmux.windows[len(f.tmux.windows)-1].Options["id"]
	if got := journaltest.Kinds(journalOf(t, f, id)); got != state.KindLaunched {
		t.Fatalf("journal %q", got)
	}
}

// While an agent's journal is there, hq's own events ask tmux nothing
// (#318): one hq send that types a message in lists the windows as often
// as before.
func TestSendAsksTmuxForItsWindowsNoMoreOftenWhileTheJournalIsThere(t *testing.T) {
	f := sendFakes(state.Done)
	if code, out, errOut := f.run("send", "a", "hi"); code != 0 || out != "delivered: typed into a as its next prompt\n" {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	const sendWindowsReads = 1 // measured on the base
	if f.tmux.windowsRead != sendWindowsReads {
		t.Fatalf("tmux listed its windows %d times, %d on the base", f.tmux.windowsRead, sendWindowsReads)
	}
}
