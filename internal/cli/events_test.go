package cli

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/journal"
	"github.com/rkrysinski/hq/internal/journal/journaltest"
	"github.com/rkrysinski/hq/internal/sbx"
	"github.com/rkrysinski/hq/internal/state"
)

// journaled gives agent name the id id, shaped as hq issues them so its
// journal takes it, moving what the fakes keep of it under "id-NAME".
func journaled(f *fakes, name, id string) {
	old := "id-" + name
	for i, w := range f.tmux.windows {
		if w.Options["id"] == old {
			f.tmux.windows[i].Options["id"] = id
		}
	}
	if r, ok := f.states[old]; ok {
		f.states[id] = r
		delete(f.states, old)
	}
	if d, ok := f.details[old]; ok {
		f.details[id] = d
		delete(f.details, old)
	}
	if q, ok := f.questions[old]; ok {
		f.questions[id] = q
		delete(f.questions, old)
	}
	f.inbox[id], f.held[id] = f.inbox[old], f.held[old]
	delete(f.inbox, old)
	delete(f.held, old)
}

// hookReport leaves a report of agent id's hook in the repository at root,
// as its hook writes it, for the next look to ingest.
func hookReport(f *fakes, root, id, name, event string) {
	f.journal.Leave(root, id, name, journaltest.Hook("main", map[string]any{"hook_event_name": event}), f.now)
}

func journalOf(t *testing.T, f *fakes, id string) []state.Event {
	t.Helper()
	j, err := f.journal.Read(id)
	if err != nil {
		t.Fatal(err)
	}
	return j.Events
}

// ofSource are the events of source among es.
func ofSource(es []state.Event, source string) []state.Event {
	var out []state.Event
	for _, e := range es {
		if e.Source == source {
			out = append(out, e)
		}
	}
	return out
}

// When hq types a message in, the event that names it is in the agent's
// journal before the typing, based on the hook's last report, and the
// prompt Claude then reports is a later event (#195). It names only the
// messages of the prompt typed: one held behind it gets its event when a
// later look types it in, under the name it was first left under.
func TestHqTypingAMessageInAppendsItsDeliveryBeforeTheTyping(t *testing.T) {
	f := sendFakes(state.Done)
	journaled(f, "a", "a1")
	f.inbox["a1"] = []string{"first"}
	var atPaste []string
	f.tmux.onPaste = func() { atPaste = append(atPaste, journaltest.Kinds(journalOf(t, f, "a1"))) }
	submitted := f.tmux.onSubmit
	prompts := 1
	f.tmux.onSubmit = func() {
		submitted()
		prompts++
		hookReport(f, "/w/app", "a1", "00000000"+string(rune('0'+prompts))+".1", "UserPromptSubmit")
	}
	code, out, errOut := f.run("send", "a", "/probe")
	if code != 0 || !strings.HasPrefix(out, "queued: typed an earlier message into a") {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	if len(atPaste) != 1 || atPaste[0] != "Stop delivery" {
		t.Fatalf("the journal at the typing: %q", atPaste)
	}
	es := journalOf(t, f, "a1")
	d := es[1]
	if d.Source != state.SourceHQ || d.Kind != state.KindDelivery || d.Facts.Hook != "" || strings.Join(d.Facts.Messages, " ") != fakeName("first") || d.Basis != es[0].Number || d.Time.IsZero() {
		t.Fatalf("delivery %+v", d)
	}
	// The held command, typed in by a later look once the agent is back at
	// its prompt; the prompt of the first and its turn's end come before it.
	f.now = f.now.Add(time.Minute)
	hookReport(f, "/w/app", "a1", "000000009.1", "Stop")
	if code, _, errOut := f.run("ls"); code != 0 {
		t.Fatalf("ls: exit %d %s", code, errOut)
	}
	es = journalOf(t, f, "a1")
	if got := journaltest.Kinds(es); got != "Stop delivery UserPromptSubmit Stop delivery" {
		t.Fatalf("journal %s", got)
	}
	if held := es[4]; held.Source != state.SourceHQ || strings.Join(held.Facts.Messages, " ") != fakeName("/probe") || held.Basis != es[3].Number {
		t.Fatalf("held delivery %+v", held)
	}
	f.now = f.now.Add(time.Minute)
	if code, _, _ := f.run("ls"); code != 0 {
		t.Fatal("ls")
	}
	if es := journalOf(t, f, "a1"); len(es) < 6 || es[5].Kind != "UserPromptSubmit" || es[5].Number < es[4].Number {
		t.Fatalf("the command's prompt after its delivery: %s", journaltest.Kinds(es))
	}
}

// A typing that fails puts the messages back under their names, and the
// journal says the delivery did not reach the agent.
func TestAFailedTypingWithdrawsItsDelivery(t *testing.T) {
	f := sendFakes(state.Done)
	journaled(f, "a", "a1")
	f.inbox["a1"] = []string{"first"}
	f.tmux.pasteErr = errors.New("no pane")
	if code, _, _ := f.run("send", "a", "second"); code == 0 {
		t.Fatal("exit 0")
	}
	es := journalOf(t, f, "a1")
	names := fakeName("first") + " " + fakeName("second")
	if got := journaltest.Kinds(es); got != "Stop delivery Withdrawn" || strings.Join(es[2].Facts.Messages, " ") != names || es[2].Facts.Withdrawn != state.KindDelivery || es[2].Source != state.SourceHQ {
		t.Fatalf("journal %s: %+v", got, es)
	}
	var back []string
	for _, m := range f.posted[1:] {
		back = append(back, m.Name)
	}
	if strings.Join(back, " ") != names {
		t.Fatalf("put back as %q", back)
	}
}

// An answer given is in the agent's journal before it reaches the agent:
// left for the question dialog's hook, or typed into a dialog of Claude's
// own; one the hook did not take is withdrawn.
func TestAnAnswerIsAnEventBeforeItIsGiven(t *testing.T) {
	f, id := askFakes(t, "dialog-ask")
	journaled(f, "a", "a1")
	var atLeave string
	f.onLeaveAnswer = func() { atLeave = journaltest.Kinds(ofSource(journalOf(t, f, "a1"), state.SourceHQ)) }
	// Its hook takes the cancel, and Claude ends the turn as at Esc.
	f.onSleep = func() {
		if f.answerLeft["a1"] != nil {
			delete(f.answerLeft, "a1")
			f.tmux.screens["%4"] = declinedScreen
		}
	}
	if code, out, errOut := f.run("answer", "a", "--dialog", id, "--cancel"); code != 0 {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	es := ofSource(journalOf(t, f, "a1"), state.SourceHQ)
	if atLeave != state.KindAnswer || len(es) != 1 || es[0].Facts.Whose != state.DialogHooks {
		t.Fatalf("at the leaving %q, journal %+v", atLeave, es)
	}

	f, id = askFakes(t, "dialog-ask")
	journaled(f, "a", "a1")
	if code, _, _ := f.run("answer", "a", "--dialog", id, "1"); code == 0 {
		t.Fatal("not taken: exit 0")
	}
	if got := journaltest.Kinds(ofSource(journalOf(t, f, "a1"), state.SourceHQ)); got != "Answer Withdrawn" {
		t.Fatalf("not taken: %s", got)
	}
	f, id = askFakes(t, "dialog-ask")
	journaled(f, "a", "a1")
	f.leaveErr = state.ErrAnswerWaiting
	if code, _, _ := f.run("answer", "a", "--dialog", id, "1"); code == 0 {
		t.Fatal("not left: exit 0")
	}
	if got := journaltest.Kinds(ofSource(journalOf(t, f, "a1"), state.SourceHQ)); got != "Answer Withdrawn" {
		t.Fatalf("not left: %s", got)
	}

	f, _, id = ownDialogFakes(t)
	journaled(f, "a", "a1")
	var atEnter string
	pressed := f.tmux.onPress
	f.tmux.onPress = func(pane, key string) {
		if key == "Enter" {
			atEnter = journaltest.Kinds(ofSource(journalOf(t, f, "a1"), state.SourceHQ))
		}
		pressed(pane, key)
	}
	if code, _, errOut := f.run("answer", "a", "--dialog", id, "2"); code != 0 {
		t.Fatalf("own: exit %d %s", code, errOut)
	}
	if es := ofSource(journalOf(t, f, "a1"), state.SourceHQ); atEnter != state.KindAnswer || len(es) != 1 || es[0].Facts.Whose != state.DialogOwn {
		t.Fatalf("own: at Enter %q, journal %+v", atEnter, es)
	}
}

// hq new launches an agent with its launch in its journal before its
// session can report; a restart taking it down and its relaunch are events
// in that order.
func TestLaunchRestartAndRelaunchAreEvents(t *testing.T) {
	f := newFakes()
	f.sbx.sandboxes = sandboxesFor("/w/app")
	if code, _, errOut := f.run("new", "a"); code != 0 {
		t.Fatalf("new: exit %d %s", code, errOut)
	}
	id := f.tmux.windows[len(f.tmux.windows)-1].Options["id"]
	es := journalOf(t, f, id)
	if len(es) != 1 || es[0].Source != state.SourceHQ || es[0].Kind != state.KindLaunched || es[0].Facts.Start != "new" || !es[0].Time.Equal(f.now) {
		t.Fatalf("launched %+v", es)
	}
	// With a first prompt the launch says so: the agent is starting until it
	// reports it (#191).
	if code, _, errOut := f.run("new", "b", "/w/app", "hello"); code != 0 {
		t.Fatalf("new b: exit %d %s", code, errOut)
	}
	if es[0].Facts.Prompted {
		t.Fatalf("a, no prompt: %+v", es[0])
	}
	b := journalOf(t, f, f.tmux.windows[len(f.tmux.windows)-1].Options["id"])
	if len(b) != 1 || b[0].Kind != state.KindLaunched || !b[0].Facts.Prompted {
		t.Fatalf("b, with a prompt: %+v", b)
	}

	// A new agent that never ran leaves no journal.
	f = newFakes()
	f.sbx.sandboxes = sandboxesFor("/w/app")
	f.tmux.startErr = errors.New("no server")
	if code, _, _ := f.run("new", "a"); code == 0 {
		t.Fatalf("exit %d", code)
	}
	if left, _ := f.journal.IDs(); len(left) != 0 {
		t.Fatalf("journals %v", left)
	}

	// hq restore launches a lost agent again, continuing its conversation;
	// one whose session does not start is withdrawn.
	for _, fail := range []bool{false, true} {
		f = lostFakes()
		f.records["/w/app"] = f.records["/w/app"][:1]
		f.records["/w/app"][0].ID = "a1"
		f.states["a1"] = f.states["id-a"]
		delete(f.records, "/w/lib")
		if fail {
			f.tmux.startErr = errors.New("no server")
		}
		f.run("restore")
		want := "Launched"
		if fail {
			want = "Launched Withdrawn"
		}
		if es := ofSource(journalOf(t, f, "a1"), state.SourceHQ); journaltest.Kinds(es) != want || es[0].Facts.Start != "restore" || es[0].Facts.Session != sessionA {
			t.Fatalf("restored (start fails: %v): %+v", fail, es)
		}
	}

	f = sandboxFakes()
	journaled(f, "a", "a1")
	hookReport(f, "/w/app", "a1", "000000001.1", "Stop")
	if err := f.journal.Ingest("/w/app", "a1"); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := f.run("sandbox", "restart", "app", "-y"); code != 0 {
		t.Fatalf("restart: exit %d %s", code, errOut)
	}
	es = journalOf(t, f, "a1")
	if got := journaltest.Kinds(ofSource(es, state.SourceHQ)); got != "TakenDown Relaunched" {
		t.Fatalf("restart: %s", journaltest.Kinds(es))
	}
	basis := journal.Basis(es[:slices.IndexFunc(es, func(e state.Event) bool { return e.Source == state.SourceHQ })])
	for _, e := range ofSource(es, state.SourceHQ) {
		if e.Basis != basis || basis == 0 {
			t.Errorf("based on %d: %+v", basis, e)
		}
	}
	f = sandboxFakes()
	journaled(f, "a", "a1")
	f.tmux.respawnErr = errors.New("no pane")
	f.run("sandbox", "restart", "app", "-y")
	if got := journaltest.Kinds(journalOf(t, f, "a1")); got != "TakenDown Relaunched Withdrawn" {
		t.Fatalf("a relaunch that failed: %s", got)
	}
}

// An sbx too old ends hq ls, hq read, hq send and hq answer with exit code
// 3 before anything is ingested, appended or typed in.
func TestAnSbxTooOldEndsTheCommandBeforeAnythingIsIngested(t *testing.T) {
	for _, args := range [][]string{{"ls"}, {"read", "a"}, {"send", "a", "hi"}, {"answer", "a", "--dialog", "abcdef", "1"}} {
		f := sendFakes(state.Done)
		journaled(f, "a", "a1")
		f.sbx.err = &sbx.TooOldError{Version: "0.47.0"}
		hookReport(f, "/w/app", "a1", "000000001.1", "Stop")
		code, out, errOut := f.run(args...)
		if code != ExitEnvironment || out != "" || !strings.Contains(errOut, "too old") {
			t.Fatalf("%v: exit %d %q %q", args, code, out, errOut)
		}
		if es := journalOf(t, f, "a1"); len(es) != 0 || len(f.journal.Left("/w/app", "a1")) != 1 || len(f.tmux.input) != 0 || len(f.inbox["a1"]) != 0 {
			t.Fatalf("%v: journal %+v, left %v, typed %v, inbox %v", args, es, f.journal.Left("/w/app", "a1"), f.tmux.input, f.inbox["a1"])
		}
	}
}

// The commands that take an sbx too old as no answer (hq wait, hq kill)
// append no sbx event from it; an answer that ends an agent is one.
func TestOnlyAnAnswerOfSbxIsAnSbxEvent(t *testing.T) {
	f := sendFakes(state.Done)
	journaled(f, "a", "a1")
	f.sbx.err = &sbx.TooOldError{Version: "0.47.0"}
	if code, _, errOut := f.run("wait", "a", "--timeout", "1s"); code != 0 {
		t.Fatalf("wait: exit %d %s", code, errOut)
	}
	es := journalOf(t, f, "a1")
	if len(es) != 1 || len(ofSource(es, state.SourceSbx)) != 0 {
		t.Fatalf("too old: %s", journaltest.Kinds(es))
	}
	f.sbx.err = nil
	f.sbx.sandboxes = []sbx.Sandbox{{Name: "claude-x", Status: "stopped"}}
	asked := f.now
	if code, out, _ := f.run("ls"); code != 0 || !strings.Contains(out, "ended") {
		t.Fatalf("ls: exit %d %q", code, out)
	}
	if got := ofSource(journalOf(t, f, "a1"), state.SourceSbx); len(got) != 1 || got[0].Kind != state.KindNotRunning || !got[0].Facts.Asked.Equal(asked) || got[0].Basis != es[0].Number {
		t.Fatalf("stopped: %+v", got)
	}
}

// Every look appends what it saw: hq ls appends the turn a user ended on
// the screen, once, whatever the number of looks.
func TestEveryLookAppendsWhatItSaw(t *testing.T) {
	f := sendFakes(state.Working)
	journaled(f, "a", "a1")
	f.tmux.screens["%4"] = declinedScreen
	for range 3 {
		if code, out, _ := f.run("ls"); code != 0 || !strings.Contains(out, "done") {
			t.Fatalf("exit %d %q", code, out)
		}
		f.now = f.now.Add(time.Second)
	}
	es := journalOf(t, f, "a1")
	screen := ofSource(es, state.SourceScreen)
	if len(screen) != 1 || screen[0].Kind != state.KindTurnEnded || screen[0].Basis != es[0].Number {
		t.Fatalf("journal %s", journaltest.Kinds(es))
	}
}
