package cli

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/sbx"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
)

// Sessions of the lost agents' conversations.
const (
	sessionA = "0000000a-0000-4000-8000-00000000000a"
	sessionC = "0000000c-0000-4000-8000-00000000000c"
	sessionD = "0000000d-0000-4000-8000-00000000000d"
)

// lostFakes is the host after a restart: tmux has no server, so no window,
// while app's default sandbox claude-x and its local one hq-local-app, and
// lib's claude-lib, are there, stopped, and hq's records of agents a and d
// (app) and c (lib) are kept beside their state files, each with the
// conversation it continues.
func lostFakes() *fakes {
	f := newFakes()
	f.tmux.windows = nil
	f.sbx.sandboxes = []sbx.Sandbox{
		{Name: "claude-x", Status: "stopped", Workspaces: []string{"/w/app"}},
		{Name: "hq-local-app", Status: "stopped", Workspaces: []string{"/w/app"}},
		{Name: "claude-lib", Status: "stopped", Workspaces: []string{"/w/lib"}},
	}
	f.records = map[string][]state.Record{
		"/w/app": {{ID: "id-a", Name: "a", Sandbox: "claude-x"}, {ID: "id-d", Name: "d", Sandbox: "hq-local-app"}},
		"/w/lib": {{ID: "id-c", Name: "c", Sandbox: "claude-lib"}},
	}
	f.states["id-a"] = state.Report{State: state.Working, SessionID: sessionA, Cwd: "/w/app/.claude/worktrees/x", Branch: "feat/a"}
	f.states["id-c"] = state.Report{State: state.Done, SessionID: sessionC, Cwd: "/w/lib"}
	f.states["id-d"] = state.Report{State: state.Done, SessionID: sessionD, Cwd: "/w/app"}
	return f
}

// window is the agent window named name.
func (f *fakes) window(name string) (tmux.Window, bool) {
	for _, w := range f.tmux.windows {
		if w.Options["name"] == name {
			return w, true
		}
	}
	return tmux.Window{}, false
}

// A host restart takes tmux with it: hq restore brings each agent back
// under its name and id, in its repository and sandbox, setup included,
// continuing its conversation, as hq sandbox restart relaunches one (#140).
func TestRestoreRelaunchesEveryAgentLostWithTmux(t *testing.T) {
	f := lostFakes()
	f.tmux.sessions = 0
	code, out, errOut := f.run("restore")
	want := "restored a in app (sandbox claude-x)\nrestored d in app (sandbox hq-local-app)\nrestored c in lib (sandbox claude-lib)\n"
	if code != 0 || out != want {
		t.Fatalf("exit %d\n%s%s", code, out, errOut)
	}
	for _, c := range []struct{ name, repo, sandbox, session string }{
		{"a", "/w/app", "claude-x", sessionA}, {"d", "/w/app", "hq-local-app", sessionD}, {"c", "/w/lib", "claude-lib", sessionC},
	} {
		w, ok := f.window(c.name)
		if !ok {
			t.Fatalf("%s not restored: %s", c.name, f.names())
		}
		o := w.Options
		if w.Name != c.name || o["id"] != "id-"+c.name || o["repo"] != c.repo || o["sandbox"] != c.sandbox || o["started"] != agent.Stamp(f.now) ||
			o["inbox"] != inboxHooks || o["new"] != "" || !f.tmux.started[w.ID] {
			t.Fatalf("restored window %+v", w)
		}
		argv := strings.Join(f.tmux.argv[w.ID], " ")
		if want := "sbx run --name " + c.sandbox + " -- --settings hq-settings:" + c.name + ":id-" + c.name + " --resume " + c.session; argv != want {
			t.Fatalf("argv %q, want %q", argv, want)
		}
	}
	// The record is kept for the next time; a prompt hq mcp left word of
	// went with the session, so the next prompt is the user's.
	if got := f.recordOf("id-d"); got != (state.Record{ID: "id-d", Name: "d", Sandbox: "hq-local-app"}) {
		t.Fatalf("record %+v", got)
	}
	if got := strings.Join(f.withdrawn, " "); got != "id-a id-d id-c" {
		t.Fatalf("withdrawn %q", got)
	}
	// Nothing ran in the stopped sandboxes: sbx run starts each.
	if len(f.sbx.calls) != 0 {
		t.Fatalf("sbx calls %q", f.sbx.calls)
	}
	// The rows show what the agents last said until they report anew.
	_, out, _ = f.run("ls")
	if !strings.Contains(out, "a     app   feat/a  starting") {
		t.Fatalf("hq ls:\n%s", out)
	}
	// Run again, it finds nothing missing and changes nothing.
	before := f.names()
	if code, out, _ := f.run("restore"); code != 0 || out != "nothing to restore\n" || f.names() != before {
		t.Fatalf("again: exit %d %q, windows %q", code, out, f.names())
	}
}

// An agent hq cannot bring back is reported with the reason and the
// remedy, and the others are restored all the same; that is no error
// (#140).
func TestRestoreReportsTheAgentsItCannotRelaunch(t *testing.T) {
	f := lostFakes()
	f.records["/w/app"] = append(f.records["/w/app"],
		state.Record{ID: "id-e", Name: "e", Sandbox: "claude-x"},
		state.Record{ID: "id-g", Name: "g", Sandbox: "claude-x"},
		state.Record{ID: "id-h", Name: "h", Sandbox: "hq-gone-app"},
		state.Record{ID: "id-i", Name: "i", Sandbox: "claude-x"})
	f.states["id-e"] = state.Report{State: state.Done, SessionID: "0000000e-0000-4000-8000-00000000000e", Cwd: "/w/app/.claude/worktrees/gone"}
	f.states["id-g"] = state.Report{State: state.Done} // a session that never had a prompt
	f.states["id-h"] = state.Report{State: state.Done, SessionID: "0000000f-0000-4000-8000-00000000000f", Cwd: "/w/app"}
	// i never reported.
	code, out, errOut := f.run("restore", "app")
	want := "restored a in app (sandbox claude-x)\nrestored d in app (sandbox hq-local-app)\n" +
		"not restored e in app: its worktree /w/app/.claude/worktrees/gone is gone (hq kill e forgets it)\n" +
		"not restored g in app: it has no conversation to continue (hq kill g forgets it)\n" +
		"not restored h in app: its sandbox hq-gone-app is gone (hq kill h forgets it)\n" +
		"not restored i in app: it has no conversation to continue (hq kill i forgets it)\n"
	if code != 0 || out != want {
		t.Fatalf("exit %d\n%s%s", code, out, errOut)
	}
	if f.names() != "hq a d" {
		t.Fatalf("windows %q", f.names())
	}
	// Their files stay until they are killed.
	if len(f.removed) != 0 {
		t.Fatalf("removed %v", f.removed)
	}
}

// Only the agents of this tmux server count: a record of another server's
// agent (another worktree's local-dev, a tester), of an agent whose window
// is there, ended or not, or of a name a window holds now, is no lost
// agent (#140).
func TestRestoreLeavesWhatIsNotLost(t *testing.T) {
	f := lostFakes()
	f.tmux.windows = []tmux.Window{{ID: "@0", Name: "hq", Options: map[string]string{}},
		agentWindow("@1", "a", "/w/app", f.now.Add(-time.Hour), true)}
	f.tmux.sessions, f.tmux.next = 1, 2
	other := agentWindow("@2", "c", "/w/lib", f.now.Add(-time.Hour), false)
	other.Options["id"] = "id-new-c"
	f.tmux.windows = append(f.tmux.windows, other)
	f.tmux.next = 3
	f.records["/w/app"][1].Server = "hq-dev-123"
	if code, out, _ := f.run("restore"); code != 0 || out != "nothing to restore\n" || f.names() != "hq a c" {
		t.Fatalf("exit %d %q, windows %q", code, out, f.names())
	}
	// That server's hq restores it.
	f.env["HQ_TMUX_SOCKET"] = "hq-dev-123"
	if code, out, _ := f.run("restore"); code != 0 || out != "restored d in app (sandbox hq-local-app)\n" {
		t.Fatalf("exit %d %q", code, out)
	}
	if got := f.recordOf("id-d"); got.Server != "hq-dev-123" {
		t.Fatalf("record %+v", got)
	}
}

// hq restore REPO restores that repository's agents only; REPO is as for
// hq sandbox: its directory or its name.
func TestRestoreOfARepository(t *testing.T) {
	f := lostFakes()
	if code, out, _ := f.run("restore", "/w/lib"); code != 0 || out != "restored c in lib (sandbox claude-lib)\n" {
		t.Fatalf("exit %d %q", code, out)
	}
	f = lostFakes()
	if code, _, errOut := f.run("restore", "nope"); code != ExitNotFound || errOut != "hq: no repository 'nope' (see hq ls)\n" {
		t.Fatalf("exit %d %q", code, errOut)
	}
	if code, _, errOut := f.run("restore", "/w/plain"); code != ExitNotFound || errOut != "hq: /w/plain is not in a git repository\n" {
		t.Fatalf("exit %d %q", code, errOut)
	}
	for _, args := range [][]string{{"restore", "app", "lib"}, {"restore", "-y"}} {
		if code, _, errOut := f.run(args...); code != ExitUsage || errOut != "hq: usage: hq restore [REPO]\n" {
			t.Fatalf("%v: exit %d %q", args, code, errOut)
		}
	}
}

// Killing the tmux server alone leaves Claude running in the sandbox: hq
// restore ends that session first, so the conversation is not continued
// twice under one id.
func TestRestoreEndsTheSessionStillRunningInTheSandbox(t *testing.T) {
	f := lostFakes()
	f.sbx.setStatus("claude-x", "running")
	f.sbx.orphans = map[string]bool{"id-a": true}
	if code, _, errOut := f.run("restore", "app"); code != 0 {
		t.Fatalf("exit %d %q", code, errOut)
	}
	if got := strings.Join(f.sbx.calls, "; "); got != `exec claude-x pkill -TERM -f HQ_ID":"id-a"; exec claude-x pgrep -f HQ_ID":"id-a"` {
		t.Fatalf("sbx calls %q", got)
	}
	if _, ok := f.window("a"); !ok || len(f.sbx.orphans) != 0 {
		t.Fatalf("windows %q orphans %v", f.names(), f.sbx.orphans)
	}
}

// After a host restart hq sandbox restart relaunches the agents of the
// sandbox that tmux lost, as hq restore does, besides those it lists
// (#140).
func TestSandboxRestartAlsoRestoresItsLostAgents(t *testing.T) {
	f := sandboxFakes()
	f.records = map[string][]state.Record{
		"/w/app": {{ID: "id-e", Name: "e", Sandbox: "claude-x"}, {ID: "id-g", Name: "g", Sandbox: "claude-x"}},
		"/w/lib": {{ID: "id-l", Name: "l", Sandbox: "claude-lib"}},
	}
	f.states["id-e"] = state.Report{State: state.Done, SessionID: sessionA, Cwd: "/w/app"}
	f.states["id-l"] = state.Report{State: state.Done, SessionID: sessionC, Cwd: "/w/lib"}
	f.stdin = "y\n"
	code, out, errOut := f.run("sandbox", "restart", "app")
	if code != 0 || !strings.Contains(out, "Restart the sandbox claude-x? This ends and relaunches 2 agents (a, b) and restores 2 lost agents (e, g).") ||
		!strings.HasSuffix(out, "restarted the sandbox claude-x; relaunched a, b; restored e\nnot restored g in app: it has no conversation to continue (hq kill g forgets it)\n") {
		t.Fatalf("exit %d\n%s%s", code, out, errOut)
	}
	w, ok := f.window("e")
	if !ok || !strings.HasSuffix(strings.Join(f.tmux.argv[w.ID], " "), " --resume "+sessionA) || w.Options["sandbox"] != "claude-x" {
		t.Fatalf("e: %+v %q", w, f.tmux.argv[w.ID])
	}
	if _, ok := f.window("l"); ok {
		t.Fatal("an agent of another sandbox was restored")
	}
	if got := strings.Join(f.sbx.calls, "; "); got != "stop claude-x; exec claude-x true" {
		t.Fatalf("sbx calls %q", got)
	}
}

// A lost agent holds its name as an ended one does: hq new refuses it,
// naming both remedies, and hq kill forgets it, files and all (#140).
func TestALostAgentHoldsItsNameUntilKilled(t *testing.T) {
	f := lostFakes()
	f.sbx.setStatus("claude-lib", "running")
	f.sbx.orphans = map[string]bool{"id-c": true}
	code, _, errOut := f.run("new", "c", "say hi")
	if code != ExitUsage || errOut != "hq: agent 'c' was lost with tmux (hq restore brings it back; hq kill c forgets it)\n" || f.names() != "" {
		t.Fatalf("new: exit %d %q, windows %q", code, errOut, f.names())
	}
	code, out, errOut := f.run("kill", "c", "-y")
	if code != 0 || out != "killed c; the sandbox stays\n" {
		t.Fatalf("kill: exit %d %q %q", code, out, errOut)
	}
	if got := strings.Join(f.removed, ";"); got != "/w/lib id-c" || len(f.sbx.orphans) != 0 {
		t.Fatalf("removed %q orphans %v", got, f.sbx.orphans)
	}
	// It is in the trace as any other kill (#147).
	if len(f.kills) != 1 || f.kills[0].Name != "c" || f.kills[0].ID != "id-c" || f.kills[0].Repo != "/w/lib" || f.kills[0].Command != "kill" {
		t.Fatalf("trace %+v", f.kills)
	}
	if code, _, _ := f.run("new", "c", "say hi"); code != 0 {
		t.Fatalf("the name stays taken after the kill")
	}
	// A name no agent has, lost or not, is still not found.
	if code, _, errOut := f.run("kill", "zz", "-y"); code != ExitNotFound || errOut != "hq: no agent 'zz' (see hq ls)\n" {
		t.Fatalf("exit %d %q", code, errOut)
	}
}

// hq new keeps a record of the agent it starts, for hq restore, and hq
// sandbox restart one of each agent it relaunches, so an agent an older hq
// started is restorable once relaunched (#140).
func TestNewAndRelaunchKeepTheAgentsRecord(t *testing.T) {
	f := newFakes()
	f.env["HQ_TMUX_SOCKET"] = "hq-dev-9"
	f.sbx.sandboxes = []sbx.Sandbox{{Name: "claude-app", Status: "running", Workspaces: []string{"/w/app"}}}
	if code, _, errOut := f.run("new", "n", "say hi"); code != 0 {
		t.Fatalf("exit %d %q", code, errOut)
	}
	w, _ := f.window("n")
	if got := f.recordOf(w.Options["id"]); got != (state.Record{ID: w.Options["id"], Name: "n", Sandbox: "claude-app", Server: "hq-dev-9"}) {
		t.Fatalf("record %+v", got)
	}

	f = sandboxFakes()
	if code, _, errOut := f.run("sandbox", "restart", "app", "-y"); code != 0 {
		t.Fatalf("exit %d %q", code, errOut)
	}
	if got := f.recordOf("id-b"); got != (state.Record{ID: "id-b", Name: "b", Sandbox: "claude-x"}) {
		t.Fatalf("record %+v", got)
	}
	if got := f.recordOf("id-c"); got.ID != "" {
		t.Fatalf("an agent of another sandbox got a record: %+v", got)
	}
}

// A new agent whose window could not start leaves no record behind: it
// would hold its name as a lost agent that never ran.
func TestANewAgentThatCouldNotStartLeavesNoRecord(t *testing.T) {
	f := newFakes()
	f.sbx.sandboxes = []sbx.Sandbox{{Name: "claude-app", Status: "running", Workspaces: []string{"/w/app"}}}
	f.tmux.startErr = errors.New("start failed")
	if code, _, _ := f.run("new", "n"); code != ExitEnvironment {
		t.Fatalf("exit %d", code)
	}
	if len(f.records["/w/app"]) != 0 {
		t.Fatalf("records %+v", f.records)
	}
}

// When one lost agent cannot be launched, hq restore still says which it
// could not bring back and why (#140).
func TestRestoreReportsTheOthersWhenALaunchFails(t *testing.T) {
	f := lostFakes()
	f.records["/w/app"] = append(f.records["/w/app"], state.Record{ID: "id-g", Name: "g", Sandbox: "claude-x"})
	f.tmux.startErr = errors.New("start failed")
	code, out, errOut := f.run("restore", "app")
	if code != ExitEnvironment || out != "not restored g in app: it has no conversation to continue (hq kill g forgets it)\n" || errOut != "hq: start failed\n" {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	// The record of an agent that was already lost stays.
	if f.recordOf("id-a").ID == "" {
		t.Fatal("a's record went")
	}
}

// A lost agent whose sandbox was removed is no longer in a repository hq
// knows by its sandboxes; hq kill finds it in the current directory's
// repository, where hq new finds it too.
func TestKillFindsALostAgentOfTheCurrentRepositoryWithoutASandbox(t *testing.T) {
	f := lostFakes()
	f.sbx.sandboxes = f.sbx.sandboxes[2:] // only lib's
	if code, out, errOut := f.run("kill", "a", "-y"); code != 0 || out != "killed a; the sandbox stays\n" {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	if got := strings.Join(f.removed, ";"); got != "/w/app id-a" {
		t.Fatalf("removed %q", got)
	}
}

// A host restart restarted the sandboxes, and sbx lost what hq mcp load
// loaded: hq restore says which sandbox to load hq into again.
func TestRestoreNamesTheSandboxesHqWasLoadedInto(t *testing.T) {
	f := lostFakes()
	f.tmux.sessions = 0
	f.prefs.MCPLoaded = []string{"claude-x"}
	code, out, _ := f.run("restore", "/w/app")
	if code != 0 || !strings.HasSuffix(out, "\nhq was loaded into the sandbox claude-x; if it restarted, hq mcp load claude-x loads it again\n") || len(f.sbx.loads) != 0 {
		t.Fatalf("exit %d %q", code, out)
	}
}
