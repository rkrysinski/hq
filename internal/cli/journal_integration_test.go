//go:build integration

package cli

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/journal"
	"github.com/rkrysinski/hq/internal/journal/journaltest"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/testutil"
)

// agentID is the id of the agent named name, from its home window.
func (h *realHQ) agentID(name string) string {
	h.t.Helper()
	ws, err := h.d.tmux.Windows()
	if err != nil {
		h.t.Fatal(err)
	}
	for _, w := range ws {
		if w.Options["name"] == name {
			return w.Options["id"]
		}
	}
	h.t.Fatalf("no agent %s", name)
	return ""
}

// eventFiles are the event files agent id's hook left in the repository
// at root and no look has ingested yet.
func eventFiles(root, id string) []string {
	entries, _ := os.ReadDir(state.EventsDir(root, id))
	var names []string
	for _, e := range entries {
		if state.IsEventFile(e.Name()) {
			names = append(names, e.Name())
		}
	}
	return names
}

func TestTheJournalHoldsTheSessionInTheOrderTheHookReportedIt(t *testing.T) {
	testutil.FakeClaude(t)
	h := newRealHQ(t)
	root := testutil.GitRepo(t, "app")
	h.cwd = root
	start := time.Now().Add(-time.Second)
	if code, _, errOut := h.run("new", "a", "run a tool"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	h.waitReport("a", "done", "Done: run a tool")
	h.typeIn("a", "needs input")
	h.waitReport("a", "needs input", "Which colour do you pick?")
	time.Sleep(time.Second) // Claude's late Notification for the open dialog
	h.typeIn("a", "yes")
	h.waitReport("a", "done", "Done: needs input")
	h.typeIn("a", "/exit")
	h.waitReport("a", "ended", "Done: needs input")
	h.ls()

	id := h.agentID("a")
	j, err := h.d.journal.Read(id)
	if err != nil {
		t.Fatal(err)
	}
	want := "SessionStart UserPromptSubmit PostToolUse Stop UserPromptSubmit PermissionRequest PostToolUse Stop SessionEnd"
	if got := journaltest.Kinds(j.Events); got != want {
		t.Fatalf("journal %s\nwant    %s", got, want)
	}
	for i, e := range j.Events {
		if e.Source != state.SourceHook || e.Time.Before(start) || e.Time.After(time.Now()) || e.Facts.Branch != "main" ||
			(i > 0 && (e.Number <= j.Events[i-1].Number || e.File <= j.Events[i-1].File)) {
			t.Errorf("event %d: %+v", i, e)
		}
	}
	if f := j.Events[5].Facts; f.Tool != "AskUserQuestion" || f.Text != "Which colour do you pick?" {
		t.Errorf("the dialog: %+v", f)
	}
	if !strings.HasPrefix(j.Reply.Text, "Done: needs input") || j.Reply.Number != j.Events[7].Number {
		t.Errorf("reply %+v", j.Reply)
	}
	if left := eventFiles(root, id); len(left) != 0 {
		t.Errorf("event files left after the look: %v", left)
	}

	// The kill takes the journal and the event files with the agent.
	if code, _, errOut := h.run("kill", "a", "-y"); code != 0 {
		t.Fatalf("kill: exit %d %s", code, errOut)
	}
	if j, err := h.d.journal.Read(id); err != nil || len(j.Events) != 0 {
		t.Errorf("the journal after the kill: %+v %v", j, err)
	}
	if _, err := os.Stat(state.EventsDir(root, id)); !os.IsNotExist(err) {
		t.Errorf("the event files after the kill: %v", err)
	}
}

func TestTwoAgentsInTwoRepositoriesNeverShareANumber(t *testing.T) {
	testutil.FakeClaude(t)
	h := newRealHQ(t)
	one, two := testutil.GitRepo(t, "one"), testutil.GitRepo(t, "two")
	for name, dir := range map[string]string{"a": one, "b": two} {
		h.cwd = dir
		if code, _, errOut := h.run("new", name, "hello "+name); code != 0 {
			t.Fatalf("exit %d: %s", code, errOut)
		}
	}
	h.waitReport("a", "done", "Done: hello a")
	h.waitReport("b", "done", "Done: hello b")
	h.typeIn("a", "again")
	h.typeIn("b", "again")
	h.waitReport("a", "done", "Done: again")
	h.waitReport("b", "done", "Done: again")
	// Another process, as after a lost tmux server: the same counter.
	h.d.journal = journal.Files{Dir: h.d.journal.(journal.Files).Dir}
	h.ls()
	seen := map[int64]string{}
	for _, name := range []string{"a", "b"} {
		j, _ := h.d.journal.Read(h.agentID(name))
		if len(j.Events) != 5 {
			t.Fatalf("%s: %s", name, journaltest.Kinds(j.Events))
		}
		for i, e := range j.Events {
			if other, ok := seen[e.Number]; ok || (i > 0 && e.Number <= j.Events[i-1].Number) {
				t.Errorf("%s: number %d, also %s's", name, e.Number, other)
			}
			seen[e.Number] = name
		}
	}
}
