package cli

import (
	"strings"
	"testing"

	"github.com/rkrysinski/hq/internal/journal/journaltest"
	"github.com/rkrysinski/hq/internal/state"
)

// hexIDs gives the fakes' agents ids as hq issues them, by name, so their
// journals take them: a1 for a, b2 for b and so on.
func hexIDs(f *fakes) map[string]string {
	ids := map[string]string{}
	for i, w := range f.tmux.windows {
		if name := w.Options["name"]; name != "" {
			id := strings.Repeat(string(rune('a'+len(ids))), 2) + "01"
			f.tmux.windows[i].Options["id"] = id
			ids[name] = id
			f.states[id] = state.Report{State: state.Done, Since: f.now}
		}
	}
	return ids
}

func TestEveryLookIngestsWhatTheHooksLeft(t *testing.T) {
	f := killFakes()
	ids := hexIDs(f)
	stop := journaltest.Hook("main", map[string]any{"hook_event_name": "Stop", "last_assistant_message": "Done."})
	prompt := journaltest.Hook("main", map[string]any{"hook_event_name": "UserPromptSubmit"})
	f.journal.Leave("/w/app", ids["a"], "000000002.1", stop, f.now)
	f.journal.Leave("/w/app", ids["a"], "000000001.1", prompt, f.now)
	f.journal.Leave("/w/lib", ids["b"], "000000001.1", stop, f.now)
	code, out, _ := f.run("ls")
	if code != 0 || !strings.Contains(out, "a     app") {
		t.Fatalf("exit %d %q", code, out)
	}
	j, _ := f.journal.Read(ids["a"])
	if got := journaltest.Kinds(j.Events); got != "UserPromptSubmit Stop" || len(f.journal.Left("/w/app", ids["a"])) != 0 {
		t.Fatalf("journal %s, left %v", got, f.journal.Left("/w/app", ids["a"]))
	}
	if j, _ := f.journal.Read(ids["b"]); len(j.Events) != 1 || j.Events[0].Number != 3 {
		t.Fatalf("b's journal: %+v", j.Events)
	}
	// The list, a chord, hq go and the others look the same way.
	f.journal.Leave("/w/lib", ids["b"], "000000002.1", prompt, f.now)
	if code, _, _ := f.run("go", "b"); code != 0 || len(f.journal.Left("/w/lib", ids["b"])) != 0 {
		t.Fatalf("hq go: exit %d, left %v", code, f.journal.Left("/w/lib", ids["b"]))
	}
}

func TestKillAndStopRemoveTheJournal(t *testing.T) {
	f := killFakes()
	ids := hexIDs(f)
	stop := journaltest.Hook("main", map[string]any{"hook_event_name": "Stop"})
	for name, repo := range map[string]string{"a": "/w/app", "b": "/w/lib", "gone": "/w/lib"} {
		f.journal.Leave(repo, ids[name], "000000001.1", stop, f.now)
		if err := f.journal.Ingest(repo, ids[name]); err != nil {
			t.Fatal(err)
		}
	}
	f.journal.Leave("/w/app", ids["a"], "000000002.1", stop, f.now)
	if code, _, _ := f.run("kill", "a", "-y"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got := strings.Join(f.journal.Agents(), " "); got != ids["b"]+" "+ids["gone"] || len(f.journal.Left("/w/app", ids["a"])) != 0 {
		t.Fatalf("journals %s, left %v", got, f.journal.Left("/w/app", ids["a"]))
	}
	if code, _, _ := f.run("stop", "-y"); code != 0 || len(f.journal.Agents()) != 0 {
		t.Fatalf("exit %d, journals %v", code, f.journal.Agents())
	}
}
