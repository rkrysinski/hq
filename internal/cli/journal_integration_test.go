//go:build integration

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
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
	// hq's own events beside them (#195) are another test's.
	j.Events = ofSource(j.Events, state.SourceHook)
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
		if len(ofSource(j.Events, state.SourceHook)) != 5 {
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

// What hq does and sees joins the hook's reports in the agent's journal
// (#195): its launch first, a message hq typed in before the prompt Claude
// reports with it, and the pane's death after the session's end, dated by
// tmux.
func TestTheJournalHoldsWhatHqDidAndSawBesideTheHooksReports(t *testing.T) {
	testutil.FakeClaude(t)
	h := newRealHQ(t)
	root := testutil.GitRepo(t, "app")
	h.cwd = root
	if code, _, errOut := h.run("new", "a", "run a tool"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	h.waitReport("a", "done", "Done: run a tool")
	if code, out, errOut := h.run("send", "a", "hello there"); code != 0 || !strings.HasPrefix(out, "delivered: typed") {
		t.Fatalf("send: exit %d %q %q", code, out, errOut)
	}
	h.waitReport("a", "done", "Done: hello there")
	h.typeIn("a", "/exit")
	h.waitReport("a", "ended", "Done: hello there")
	// The session reports its end a moment before its pane dies.
	var j journal.Journal
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		h.ls()
		var err error
		if j, err = h.d.journal.Read(h.agentID("a")); err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(journaltest.Kinds(j.Events), state.KindPaneDied) || time.Now().After(deadline) {
			break
		}
	}
	want := "Launched SessionStart UserPromptSubmit PostToolUse Stop delivery UserPromptSubmit Stop SessionEnd PaneDied"
	if got := journaltest.Kinds(j.Events); got != want {
		t.Fatalf("journal %s\nwant    %s", got, want)
	}
	launched, typed, prompt, died := j.Events[0], j.Events[5], j.Events[6], j.Events[9]
	if launched.Source != state.SourceHQ || launched.Facts.Start != "new" || launched.Basis != 0 {
		t.Errorf("launched %+v", launched)
	}
	if typed.Source != state.SourceHQ || typed.Facts.Hook != "" || len(typed.Facts.Messages) != 1 || typed.Basis != j.Events[4].Number {
		t.Errorf("typed %+v", typed)
	}
	if prompt.Source != state.SourceHook || prompt.Facts.Prompt != "hello there" || prompt.Number <= typed.Number {
		t.Errorf("the prompt Claude reported %+v", prompt)
	}
	if died.Source != state.SourceTmux || died.Facts.From.IsZero() || died.Facts.From.After(died.Time) || died.Basis != j.Events[8].Number {
		t.Errorf("the pane's death %+v", died)
	}
}

// An agent started by this hq leaves no state file and no copy of its
// latest turn end beside the repository, and its home window carries none
// of the records the old look kept there; nothing hq shows comes from the
// hook's own working files: with them removed or garbled by hand, hq ls
// and hq read show every agent as before (#198, #199).
func TestTheHooksWorkingFilesAndTheHomeWindowsHoldNothingHqReads(t *testing.T) {
	testutil.FakeClaude(t)
	h := newRealHQ(t)
	h.cwd = testutil.GitRepo(t, "app")
	for _, name := range []string{"a", "b"} {
		if code, _, errOut := h.run("new", name, "hello "+name); code != 0 {
			t.Fatalf("new %s: exit %d %s", name, code, errOut)
		}
		h.waitReport(name, "done", "Done: hello "+name)
	}
	shown := func() string {
		var out []string
		for _, r := range h.ls() {
			out = append(out, strings.Join([]string{r.Name, r.Branch, r.State, r.Since.String(), r.Last}, " "))
		}
		for _, name := range []string{"a", "b"} {
			code, read, errOut := h.run("read", name)
			if code != 0 {
				t.Fatalf("read %s: exit %d %s", name, code, errOut)
			}
			out = append(out, read)
		}
		return strings.Join(out, "\n")
	}
	before := shown()
	for _, name := range []string{"a", "b"} {
		id := h.agentID(name)
		for _, old := range []string{id, id + ".stop", id + ".prev"} {
			if _, err := os.Lstat(filepath.Join(state.Dir(h.cwd), old)); !os.IsNotExist(err) {
				t.Errorf("%s left %s: %v", name, old, err)
			}
		}
		ws, _ := h.d.tmux.Windows()
		for _, w := range ws {
			if w.Options["id"] != id {
				continue
			}
			out, err := exec.Command("tmux", "-u", "-L", h.socket, "show-options", "-w", "-t", w.ID).Output()
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"turnend", "restseen", "dialogseen", "answered", "endseen", "wokeseen"} {
				if strings.Contains(string(out), "@hq_"+key+" ") {
					t.Errorf("%s's home window carries @hq_%s:\n%s", name, key, out)
				}
			}
		}
		entries, _ := os.ReadDir(state.Dir(h.cwd))
		var garbled int
		for _, e := range entries {
			n := e.Name()
			if !strings.HasPrefix(n, id+".") || strings.HasSuffix(n, ".hq") {
				continue
			}
			garbled++
			path := filepath.Join(state.Dir(h.cwd), n)
			if name == "a" {
				err := os.WriteFile(path, []byte("{\"hook_event_name\":\"SessionEnd\"}\n"), 0o644)
				if err != nil {
					t.Fatal(err)
				}
			} else if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}
		if garbled == 0 {
			t.Errorf("%s: no working files of its hook", name)
		}
	}
	if after := shown(); after != before {
		t.Fatalf("before\n%s\nafter\n%s", before, after)
	}
}
