//go:build integration

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/platform/platformtest"
	"github.com/rkrysinski/hq/internal/proc"
	"github.com/rkrysinski/hq/internal/repo"
	"github.com/rkrysinski/hq/internal/sbx"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/testutil"
	"github.com/rkrysinski/hq/internal/tmux"
	"github.com/rkrysinski/hq/internal/update"
	"github.com/rkrysinski/hq/internal/version"
)

type realHQ struct {
	t      *testing.T
	d      deps
	cwd    string
	socket string // the private tmux server
}

// newRealHQ wires hq to a private tmux server and the stub sbx.
func newRealHQ(t *testing.T) *realHQ {
	bin, _ := testutil.SbxStub(t)
	h := &realHQ{t: t, socket: testutil.TmuxSocket(t)}
	d := defaultDeps()
	d.tmux = tmux.Client{Run: proc.Exec{}, Socket: h.socket}
	d.sbx = sbx.Client{Run: proc.Exec{}, Platform: platformtest.Fake{Sbx: bin}}
	d.pollSandboxes = sbx.Client{Run: proc.Exec{Timeout: sbxPollTimeout}, Platform: platformtest.Fake{Sbx: bin}}.List
	d.getwd = func() (string, error) { return h.cwd, nil }
	h.d = d
	return h
}

func (h *realHQ) run(args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := mainWith(args, Env{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errOut}, h.d)
	return code, out.String(), errOut.String()
}

func (h *realHQ) ls() []lsRow {
	h.t.Helper()
	code, out, errOut := h.run("ls", "--json")
	var ls struct {
		Agents []lsRow `json:"agents"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &ls) != nil {
		h.t.Fatalf("ls: exit %d %q %q", code, out, errOut)
	}
	return ls.Agents
}

// waitSessions waits until the stub sbx has recorded n sessions in sandbox,
// which it does a moment after tmux shows them running; a stop before that
// would miss them.
func (h *realHQ) waitSessions(sandbox string, n int) {
	h.t.Helper()
	for i := 0; i < 50; i++ {
		b, _ := os.ReadFile(os.Getenv("SBX_STUB_DIR") + "/pids-" + sandbox)
		if strings.Count(string(b), "\n") >= n {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	h.t.Fatalf("%s: the stub has not recorded %d sessions", sandbox, n)
}

func (h *realHQ) waitState(name, state string) lsRow {
	h.t.Helper()
	for i := 0; i < 50; i++ {
		for _, r := range h.ls() {
			if r.Name == name && r.State == state {
				return r
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	h.t.Fatalf("%s never became %s: %+v", name, state, h.ls())
	return lsRow{}
}

// waitReport waits until name shows state with last as its last message.
func (h *realHQ) waitReport(name, state, last string) {
	h.t.Helper()
	for i := 0; i < 50; i++ {
		if r := h.waitState(name, state); r.Last == last {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	h.t.Fatalf("%s never showed %s with %q: %+v", name, state, last, h.ls())
}

// pane is what the agent's session has printed.
func (h *realHQ) pane(name string) string {
	h.t.Helper()
	ws, _ := h.d.tmux.Windows()
	a, ok := agent.Find(agent.FromWindows(ws), name)
	if !ok {
		h.t.Fatalf("no agent %s", name)
	}
	out, _ := exec.Command("tmux", "-L", h.socket, "capture-pane", "-p", "-t", a.Window).Output()
	return string(out)
}

// history is the agent's session with the lines scrolled off its screen.
func (h *realHQ) history(name string) string {
	h.t.Helper()
	ws, _ := h.d.tmux.Windows()
	a, ok := agent.Find(agent.FromWindows(ws), name)
	if !ok {
		h.t.Fatalf("no agent %s", name)
	}
	out, _ := exec.Command("tmux", "-L", h.socket, "capture-pane", "-p", "-S", "-", "-t", a.Window).Output()
	return string(out)
}

// waitPane waits until the agent's session has printed text.
func (h *realHQ) waitPane(name, text string) {
	h.t.Helper()
	for i := 0; i < 50; i++ {
		if strings.Contains(h.pane(name), text) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	h.t.Fatalf("%s never printed %q:\n%s", name, text, h.pane(name))
}

// typeIn types a line into the agent's session, as a person in hq go does.
func (h *realHQ) typeIn(name, line string) {
	h.t.Helper()
	ws, _ := h.d.tmux.Windows()
	a, ok := agent.Find(agent.FromWindows(ws), name)
	if !ok {
		h.t.Fatalf("no agent %s", name)
	}
	if out, err := exec.Command("tmux", "-L", h.socket, "send-keys", "-t", a.Window, line, "Enter").CombinedOutput(); err != nil {
		h.t.Fatalf("send-keys: %v %s", err, out)
	}
}

func TestNewStartsSessionInSandboxAndLsListsIt(t *testing.T) {
	h := newRealHQ(t)
	r := testutil.GitRepo(t, "app")
	h.cwd = r
	code, out, errOut := h.run("new", "a", "say hi")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "creating a sandbox for app") || !strings.Contains(out, "started a in app (sandbox claude-app)") {
		t.Fatalf("stdout %q", out)
	}
	h.waitState("a", "starting")
	// The window runs as soon as it is released; the stub logs its args a moment later.
	var log []byte
	for i := 0; i < 50 && !strings.Contains(string(log), "say hi"); i++ {
		time.Sleep(100 * time.Millisecond)
		log, _ = os.ReadFile(os.Getenv("SBX_STUB_DIR") + "/runs.log")
	}
	if !strings.Contains(string(log), `"HQ_AGENT":"a"`) || !strings.HasSuffix(strings.TrimSpace(string(log)), "say hi") {
		t.Fatalf("session args %q", log)
	}

	// A second agent from a worktree of the same repository shares the sandbox.
	wt := r + "/.claude/worktrees/x"
	testutil.Git(t, r, "worktree", "add", "-q", "-b", "x", wt)
	h.cwd = wt
	if code, out, errOut := h.run("new", "b"); code != 0 || !strings.Contains(out, "sandbox claude-app") || strings.Contains(out, "creating") {
		t.Fatalf("second agent: exit %d %q %q", code, out, errOut)
	}
	if rows := h.ls(); len(rows) != 2 || !repo.Same(rows[0].RepoPath, r) {
		t.Fatalf("rows %+v", rows)
	}
	if code, _, _ := h.run("new", "a"); code != ExitUsage {
		t.Fatalf("duplicate: exit %d", code)
	}
}

func TestStatesFollowTheSessionAndLeaveTheRepositoryClean(t *testing.T) {
	testutil.FakeClaude(t)
	t.Setenv("FAKE_CLAUDE_DELAY", "600ms")
	h := newRealHQ(t)
	r := testutil.GitRepo(t, "app")
	h.cwd = r
	if code, _, errOut := h.run("new", "a", "hello"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	h.waitState("a", "starting")
	h.waitState("a", "working")
	h.waitReport("a", "done", "Done: hello")

	h.typeIn("a", "a question")
	h.waitReport("a", "working", "Done: hello")
	h.waitReport("a", "question", "Shall I go on?")

	// A question dialog needs input, with its question, until it is answered
	// (S4) or cancelled; a cancelled one ends the turn with no hook, which hq
	// sees on the agent's screen (#71).
	h.typeIn("a", "needs input")
	h.waitReport("a", "needs input", "Which colour do you pick?")
	h.typeIn("a", "yes")
	h.waitReport("a", "done", "Done: needs input")
	h.typeIn("a", "more input")
	h.waitReport("a", "needs input", "Which colour do you pick?")
	h.typeIn("a", "esc")
	h.waitReport("a", "done", "User declined to answer questions")

	// An agent in a worktree reports to the repository, like its siblings.
	wt := filepath.Join(t.TempDir(), "x")
	testutil.Git(t, r, "worktree", "add", "-q", "-b", "x", wt)
	h.cwd = wt
	if code, _, errOut := h.run("new", "b", "hi"); code != 0 {
		t.Fatalf("new b: exit %d: %s", code, errOut)
	}
	h.waitReport("b", "done", "Done: hi")
	// A turn the user interrupts ends with no hook too, which hq sees on
	// the agent's screen (#72); a turn at work stays working meanwhile.
	h.typeIn("b", "slow work")
	h.waitReport("b", "working", "Done: hi")
	time.Sleep(time.Second)
	h.waitReport("b", "working", "Done: hi")
	h.typeIn("b", "esc")
	h.waitReport("b", "done", "Interrupted")
	// An early Esc may rewind the turn instead: no line, no hook, the
	// prompt back in the box; hq sees the screen stay at rest (#99).
	h.typeIn("b", "slow again")
	h.waitReport("b", "working", "Done: hi")
	h.typeIn("b", "rewind")
	h.waitReport("b", "done", "Interrupted")

	// A session that ends keeps its last message, and its screen stays
	// readable though Claude drew it in the alternate screen (S7, #38).
	h.typeIn("a", "/exit")
	h.waitReport("a", "ended", "Done: needs input")
	if out := h.pane("a"); !strings.Contains(out, "● Done: needs input") {
		t.Fatalf("a's screen after /exit:\n%s", out)
	}

	for _, dir := range []string{r, wt} {
		cmd := exec.Command("git", "status", "--porcelain")
		cmd.Dir = dir
		if out, err := cmd.Output(); err != nil || len(out) != 0 {
			t.Fatalf("%s is not clean: %q", dir, out)
		}
	}
}

func TestReadShowsWhatTheAgentAsksAndItsWholeReply(t *testing.T) {
	testutil.FakeClaude(t)
	h := newRealHQ(t)
	h.cwd = testutil.GitRepo(t, "app")
	if code, _, errOut := h.run("new", "a", "input lines question"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	// A question dialog is open: hq read has every question with its options.
	h.waitState("a", "needs input")
	code, out, _ := h.run("read", "a")
	if code != 0 || !strings.Contains(out, "\nasks\n  Colour: Which colour do you pick?\n    1. Red - Pick red.\n    2. Blue - Pick blue.\n") {
		t.Fatalf("dialog:\n%s", out)
	}
	// Answered, the turn ends with a question: the reply in full, its lines
	// kept, and the question it ends with.
	h.typeIn("a", "red")
	h.waitState("a", "question")
	code, out, _ = h.run("read", "a")
	if code != 0 || !strings.HasSuffix(out, "\nasks\n  Shall I go on?\n\nreply\n  Done on several lines.\n\n  Shall I go on?\n") {
		t.Fatalf("question:\n%s", out)
	}
	code, out, _ = h.run("read", "a", "--json")
	var v readView
	if code != 0 || json.Unmarshal([]byte(out), &v) != nil || v.Reply != "Done on several lines.\n\nShall I go on?" || v.State != "question" || v.Asks == nil || v.Asks.Message != "Shall I go on?" {
		t.Fatalf("json: exit %d\n%s", code, out)
	}
	if code, _, errOut := h.run("read", "b"); code != ExitNotFound || errOut != "hq: no agent 'b' (see hq ls)\n" {
		t.Fatalf("unknown: exit %d %q", code, errOut)
	}
}

func TestAgentsInOneRepositoryAreToldApartByBranch(t *testing.T) {
	testutil.FakeClaude(t)
	h := newRealHQ(t)
	h.cwd = testutil.GitRepo(t, "app")
	for name, branch := range map[string]string{"a": "feat/42-x", "b": "fix/7-y"} {
		if code, _, errOut := h.run("new", name, "worktree "+branch); code != 0 {
			t.Fatalf("new %s: exit %d: %s", name, code, errOut)
		}
	}
	h.waitState("a", "done")
	h.waitState("b", "done")
	got := map[string]string{}
	for _, r := range h.ls() {
		got[r.Name] = r.Branch
	}
	if got["a"] != "feat/42-x" || got["b"] != "fix/7-y" {
		t.Fatalf("branches %v", got)
	}
	// A later turn in the worktree keeps its branch.
	h.typeIn("a", "hello")
	h.waitReport("a", "done", "Done: hello")
	if r := h.waitState("a", "done"); r.Branch != "feat/42-x" {
		t.Fatalf("a stays in its worktree: %+v", r)
	}
}

func TestAgentsOfASandboxStoppedOutsideHqAreEnded(t *testing.T) {
	testutil.FakeClaude(t)
	h := newRealHQ(t)
	for name, dir := range map[string]string{"a": testutil.GitRepo(t, "app"), "b": testutil.GitRepo(t, "lib")} {
		h.cwd = dir
		if code, _, errOut := h.run("new", name, "hello"); code != 0 {
			t.Fatalf("new %s: exit %d %s", name, code, errOut)
		}
		h.waitReport(name, "done", "Done: hello")
	}
	// sbx reports claude-lib stopped while b's pane has not caught up yet.
	f := filepath.Join(os.Getenv("SBX_STUB_DIR"), "sandboxes", "claude-lib")
	b, _ := os.ReadFile(f)
	if err := os.WriteFile(f+".new", []byte(strings.Replace(string(b), "running", "stopped", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(f+".new", f); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range h.ls() {
		got[r.Name] = r.State
	}
	if got["a"] != "done" || got["b"] != "ended" {
		t.Fatalf("states %v", got)
	}

	// With sbx failing, hq ls shows what tmux and the hooks say.
	h.d.pollSandboxes = sbx.Client{Run: proc.Exec{}, Platform: platformtest.Fake{Sbx: filepath.Join(t.TempDir(), "sbx")}}.List
	code, out, errOut := h.run("ls")
	if code != 0 || errOut != "" || strings.Count(out, " done ") != 2 {
		t.Fatalf("sbx failing: exit %d %q %q", code, out, errOut)
	}
}

func TestAgentWhoseSessionExitsIsEndedAndStaysListed(t *testing.T) {
	h := newRealHQ(t)
	h.cwd = testutil.GitRepo(t, "app")
	t.Setenv("SBX_STUB_EXIT", "1")
	booted := filepath.Join(t.TempDir(), "booted")
	t.Setenv("SBX_STUB_BOOT", booted)
	if code, _, errOut := h.run("new", "a"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	// While sbx run starts the new sandbox, the agent is starting, not ended
	// by the sandbox being stopped (S2, #94).
	if rows := h.ls(); len(rows) != 1 || rows[0].State != "starting" {
		t.Fatalf("while the sandbox starts: %+v", rows)
	}
	if err := os.WriteFile(booted, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	h.waitState("a", "ended")
	code, out, _ := h.run("ls")
	if code != 0 || !strings.Contains(out, "ended") {
		t.Fatalf("ls: %d %q", code, out)
	}
}

func TestAnAgentKilledLongAfterItsLastReportCountsFromItsEnd(t *testing.T) {
	testutil.FakeClaude(t)
	h := newRealHQ(t)
	h.cwd = testutil.GitRepo(t, "app")
	if code, _, errOut := h.run("new", "a", "say hi"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	h.waitReport("a", "done", "Done: say hi")
	time.Sleep(3 * time.Second)
	ws, _ := h.d.tmux.Windows()
	a, _ := agent.Find(agent.FromWindows(ws), "a")
	pid, err := exec.Command("tmux", "-L", h.socket, "display-message", "-p", "-t", a.Window, "#{pane_pid}").Output()
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("kill", "-KILL", strings.TrimSpace(string(pid))).CombinedOutput(); err != nil {
		t.Fatalf("kill: %v %s", err, out)
	}
	// Claude crashed without a word: AGE counts from the crash, not from
	// the report 3 s before it (#73).
	if r := h.waitState("a", "ended"); r.AgeSeconds > 1 || r.Last != "Done: say hi" {
		t.Fatalf("ended row %+v", r)
	}
}

func TestKillEndsTheSessionAndFreesTheName(t *testing.T) {
	h := newRealHQ(t)
	h.cwd = testutil.GitRepo(t, "app")
	if code, _, errOut := h.run("new", "a"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	h.waitState("a", "starting")
	ws, _ := h.d.tmux.Windows()
	a, _ := agent.Find(agent.FromWindows(ws), "a")
	marker := `HQ_ID":"` + a.ID + `"`
	if exec.Command("pgrep", "-f", marker).Run() != nil {
		t.Fatal("the session is not running before the kill")
	}
	if code, out, errOut := h.run("kill", "a", "-y"); code != 0 || out != "killed a; the sandbox stays\n" {
		t.Fatalf("kill: exit %d %q %q", code, out, errOut)
	}
	if exec.Command("pgrep", "-f", marker).Run() == nil {
		t.Fatal("the session still runs after the kill")
	}
	if rows := h.ls(); len(rows) != 0 {
		t.Fatalf("rows %+v", rows)
	}
	if code, _, errOut := h.run("new", "a"); code != 0 {
		t.Fatalf("name not freed: exit %d %s", code, errOut)
	}
}

func TestKillEndsASessionThatOutlivedItsPane(t *testing.T) {
	h := newRealHQ(t)
	h.cwd = testutil.GitRepo(t, "app")
	if code, _, errOut := h.run("new", "a"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	h.waitState("a", "starting")
	ws, _ := h.d.tmux.Windows()
	a, _ := agent.Find(agent.FromWindows(ws), "a")
	// Claude runs on in the sandbox after its host-side sbx run ended
	// (#101): a process with the agent's id in its arguments, which on
	// SIGTERM writes its state file on the way out, as SessionEnd does.
	file := filepath.Join(state.Dir(h.cwd), a.ID)
	claude := exec.Command("sh", "-c", `trap 'echo "branch main" >"$0"; exit 0' TERM; while :; do sleep 0.1; done`, file, `HQ_ID":"`+a.ID+`"`)
	if err := claude.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- claude.Wait() }()
	t.Cleanup(func() { _ = claude.Process.Kill() })
	pid, err := exec.Command("tmux", "-L", h.socket, "display-message", "-p", "-t", a.Window, "#{pane_pid}").Output()
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("kill", "-KILL", strings.TrimSpace(string(pid))).CombinedOutput(); err != nil {
		t.Fatalf("kill: %v %s", err, out)
	}
	h.waitState("a", "ended")

	if code, out, errOut := h.run("kill", "a", "-y"); code != 0 || out != "killed a; the sandbox stays\n" {
		t.Fatalf("kill: exit %d %q %q", code, out, errOut)
	}
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("the session still runs after the kill")
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("state file left behind: %v", err)
	}
	if rows := h.ls(); len(rows) != 0 {
		t.Fatalf("rows %+v", rows)
	}
}

func TestKillAndStopRemoveTheirAgentsStateFiles(t *testing.T) {
	testutil.FakeClaude(t)
	h := newRealHQ(t)
	h.cwd = testutil.GitRepo(t, "app")
	for _, name := range []string{"a", "b"} {
		if code, _, errOut := h.run("new", name, "hello"); code != 0 {
			t.Fatalf("new %s: exit %d %s", name, code, errOut)
		}
		h.waitReport(name, "done", "Done: hello")
	}
	ws, _ := h.d.tmux.Windows()
	id := map[string]string{}
	for _, a := range agent.FromWindows(ws) {
		id[a.Name] = a.ID
	}
	dir := state.Dir(h.cwd)
	// An agent of another tmux server reports to the same directory.
	other := filepath.Join(dir, "0123456789ab")
	os.WriteFile(other, []byte("branch x\n{}"), 0o644)
	files := func() string {
		es, _ := os.ReadDir(dir)
		var names []string
		for _, e := range es {
			names = append(names, e.Name())
		}
		return strings.Join(names, " ")
	}
	if got := files(); !strings.Contains(got, id["a"]+".stop") || !strings.Contains(got, id["b"]+".stop") {
		t.Fatalf("before: %q", got)
	}

	if code, _, errOut := h.run("kill", "a", "-y"); code != 0 {
		t.Fatalf("kill: exit %d %s", code, errOut)
	}
	if got := files(); strings.Contains(got, id["a"]) || !strings.Contains(got, id["b"]) {
		t.Fatalf("after kill a: %q", got)
	}
	// b keeps reporting.
	h.typeIn("b", "more")
	h.waitReport("b", "done", "Done: more")

	if code, _, errOut := h.run("stop", "-y"); code != 0 {
		t.Fatalf("stop: exit %d %s", code, errOut)
	}
	if got := files(); got != "0123456789ab" {
		t.Fatalf("after stop: %q", got)
	}
}

func TestSandboxRestartRelaunchesAgentsAndRmRemovesTheSandbox(t *testing.T) {
	testutil.FakeClaude(t)
	h := newRealHQ(t)
	h.cwd = testutil.GitRepo(t, "app")
	// a has a conversation to continue; b never reported.
	if code, _, errOut := h.run("new", "a", "hello"); code != 0 {
		t.Fatalf("new a: exit %d %s", code, errOut)
	}
	h.waitReport("a", "done", "Done: hello")
	if code, _, errOut := h.run("new", "b"); code != 0 {
		t.Fatalf("new b: exit %d %s", code, errOut)
	}
	h.waitState("b", "starting")
	h.waitSessions("claude-app", 2)
	ids := func() map[string]string {
		ws, _ := h.d.tmux.Windows()
		m := map[string]string{}
		for _, a := range agent.FromWindows(ws) {
			m[a.Name] = a.ID
		}
		return m
	}
	before := ids()
	session, ok := h.d.readState(h.cwd, before["a"])
	if !ok || session.SessionID == "" {
		t.Fatalf("a's session: %+v", session)
	}
	pidsFile := os.Getenv("SBX_STUB_DIR") + "/pids-claude-app"
	oldPids, _ := os.ReadFile(pidsFile)

	if code, out, errOut := h.run("sandbox", "restart", "app"); code != ExitUsage || !strings.Contains(errOut, "add -y") {
		t.Fatalf("restart without a terminal: exit %d %q %q", code, out, errOut)
	}
	if code, out, errOut := h.run("sandbox", "restart", "app", "-y"); code != 0 || out != "restarted the sandbox claude-app; relaunched a, b\n" {
		t.Fatalf("restart: exit %d %q %q", code, out, errOut)
	}
	// Relaunched under the same ids, each in a new session; a keeps what it
	// last said until it reports anew.
	if r := h.waitState("a", "starting"); r.Last != "Done: hello" {
		t.Fatalf("a after the restart: %+v", r)
	}
	h.waitState("b", "starting")
	if after := ids(); after["a"] != before["a"] || after["b"] != before["b"] {
		t.Fatalf("ids before %v, after %v", before, after)
	}
	for _, pid := range strings.Fields(string(oldPids)) {
		if exec.Command("kill", "-0", pid).Run() == nil {
			t.Fatalf("an old session (pid %s) still runs", pid)
		}
	}
	h.waitSessions("claude-app", 2)
	h.waitPane("a", "fake claude: resumed "+session.SessionID)
	if out := h.pane("b"); strings.Contains(out, "resumed") {
		t.Fatalf("b resumed:\n%s", out)
	}

	if code, _, errOut := h.run("sandbox", "rm", "app", "-y"); code != ExitUsage || !strings.Contains(errOut, "(a, b)") {
		t.Fatalf("rm with agents: exit %d %q", code, errOut)
	}
	if code, _, _ := h.run("stop", "-y"); code != 0 {
		t.Fatal("stop")
	}
	if code, out, errOut := h.run("sandbox", "rm", h.cwd, "-y"); code != 0 || out != "removed the sandbox claude-app\n" {
		t.Fatalf("rm: exit %d %q %q", code, out, errOut)
	}
	if code, _, _ := h.run("sandbox", "rm", "app", "-y"); code != ExitNotFound {
		t.Fatalf("rm again: exit %d", code)
	}
}

// The update check, as the detached hq runs it, reads the latest release
// over HTTP (HQ_RELEASES_URL here) and keeps it in the preferences file,
// where --version and the notice read it.
func TestUpdateCheckKeepsTheLatestReleaseInThePreferencesFile(t *testing.T) {
	base, dir := testutil.ReleaseServer(t)
	if err := os.WriteFile(dir+"/latest", []byte("v0.2.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(update.BaseEnv, base)
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	old := version.Version
	version.Version = "v0.1.0"
	defer func() { version.Version = old }()
	d := defaultDeps()
	d.detach = func([]string) error { return nil } // the built binary's test starts it for real
	var out bytes.Buffer
	env := Env{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &out}
	if code := mainWith([]string{updateCheckCommand}, env, d); code != 0 || out.Len() != 0 {
		t.Fatalf("check: exit %d %q", code, out.String())
	}
	data, err := os.ReadFile(cfg + "/hq/preferences.json")
	if err != nil || !strings.Contains(string(data), `"latest_release": "v0.2.0"`) {
		t.Fatalf("%v %s", err, data)
	}
	if code := mainWith([]string{"--version"}, env, d); code != 0 || out.String() != "hq v0.1.0\nv0.2.0 available - hq update\n" {
		t.Fatalf("exit %d %q", code, out.String())
	}
	if exe, err := d.executable(); err != nil || !filepath.IsAbs(exe) {
		t.Fatalf("executable %q %v", exe, err)
	}
}

func TestAStoppedSandboxLeavesTheSessionReadable(t *testing.T) {
	testutil.FakeClaude(t)
	h := newRealHQ(t)
	h.cwd = testutil.GitRepo(t, "app")
	if code, _, errOut := h.run("new", "a", "hello"); code != 0 {
		t.Fatalf("new a: exit %d: %s", code, errOut)
	}
	h.waitReport("a", "done", "Done: hello")
	// Claude clears its screen when its sandbox stops; what it drew goes to
	// the pane's history rather than away (S7, #38).
	if err := h.d.sbx.Stop("claude-app"); err != nil {
		t.Fatal(err)
	}
	h.waitState("a", "ended")
	if out := h.history("a"); !strings.Contains(out, "● Done: hello") {
		t.Fatalf("a's history after the sandbox stopped:\n%s", out)
	}
	// Killing the ended agent does not start the sandbox again (#101).
	if code, _, errOut := h.run("kill", "a", "-y"); code != 0 {
		t.Fatalf("kill: exit %d %s", code, errOut)
	}
	if s, ok := findSandbox(h, "claude-app"); !ok || s.Running() || len(h.ls()) != 0 {
		t.Fatalf("after the kill: sandbox %+v, rows %+v", s, h.ls())
	}
}

func findSandbox(h *realHQ, name string) (sbx.Sandbox, bool) {
	all, err := h.d.sbx.List()
	if err != nil {
		h.t.Fatal(err)
	}
	for _, s := range all {
		if s.Name == name {
			return s, true
		}
	}
	return sbx.Sandbox{}, false
}

// waitAtRest waits until the agent's screen shows its empty prompt box: a
// turn's end is reported a moment before the box is drawn again.
func (h *realHQ) waitAtRest(name string) {
	h.t.Helper()
	for i := 0; i < 50; i++ {
		if ok, _ := state.AtRest(h.pane(name), ""); ok {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	h.t.Fatalf("%s never waited at its empty prompt box:\n%s", name, h.pane(name))
}

// send runs hq send and wants its word on how the message goes.
func (h *realHQ) send(want string, args ...string) {
	h.t.Helper()
	if code, out, errOut := h.run(append([]string{"send"}, args...)...); code != 0 || out != want+"\n" {
		h.t.Fatalf("send %q: exit %d %q %q, want %q", args, code, out, errOut, want)
	}
}

func TestSendDeliversEachMessageWhenTheAgentIsReady(t *testing.T) {
	testutil.FakeClaude(t)
	t.Setenv("FAKE_CLAUDE_DELAY", "600ms")
	h := newRealHQ(t)
	r := testutil.GitRepo(t, "app")
	h.cwd = r
	if code, _, errOut := h.run("new", "a", "hello"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	h.waitReport("a", "done", "Done: hello")
	const label = state.MessageLabel

	// At its empty prompt: typed in as its next prompt, lines and all.
	h.waitAtRest("a")
	h.send("delivered: typed into a as its next prompt", "a", "first note\nsecond line")
	h.waitReport("a", "done", "Done: first note second line") // one line in hq ls

	// At work: Claude's stop is blocked with it, so the turn goes on; the
	// agent stays working meanwhile, and waits for no one.
	h.typeIn("a", "slow job")
	h.waitState("a", "working")
	h.send("queued: a is working, delivered when it stops", "a", "check the logs")
	if rows := h.ls(); rows[0].Pending != 1 {
		t.Fatalf("pending %+v", rows)
	}
	h.typeIn("a", "go on")
	h.waitReport("a", "done", "Answered: "+label+"check the logs")
	if rows := h.ls(); rows[0].Pending != 0 {
		t.Fatalf("pending %+v", rows)
	}

	// --now: with the result of its next tool call.
	h.typeIn("a", "slow tool job")
	h.waitState("a", "working")
	h.send("queued: a is working, delivered after its next tool call, or when it stops", "a", "--now", "use the staging db")
	h.typeIn("a", "go on")
	h.waitReport("a", "done", "Done: slow tool job | "+label+"use the staging db")

	// In a dialog: nothing touches it; the message goes once it is closed.
	h.typeIn("a", "needs input")
	h.waitState("a", "needs input")
	h.send("queued: a needs input, delivered once its dialog is closed, when it stops", "a", "then commit")
	time.Sleep(time.Second)
	h.waitState("a", "needs input")
	h.typeIn("a", "1")
	h.waitReport("a", "done", "Answered: "+label+"then commit")

	// The user typing in the box: it rides along with the prompt they send.
	h.typeIn("a", "draft")
	h.waitPane("a", "❯ a draft")
	h.send("queued: a has something in its prompt box, delivered with its next prompt", "a", "ride along")
	h.typeIn("a", "sent")
	h.waitReport("a", "done", "Done: sent | "+label+"ride along")

	// Killed with a message still waiting: the message goes with it.
	ws, _ := h.d.tmux.Windows()
	a, _ := agent.Find(agent.FromWindows(ws), "a")
	h.typeIn("a", "slow end")
	h.waitState("a", "working")
	h.send("queued: a is working, delivered when it stops", "a", "never read")
	if code, _, errOut := h.run("kill", "a", "-y"); code != 0 {
		t.Fatalf("kill: exit %d %s", code, errOut)
	}
	if _, err := os.Stat(state.InboxDir(r, a.ID)); !os.IsNotExist(err) {
		t.Fatalf("inbox left after the kill: %v", err)
	}
	if code, _, _ := h.run("send", "a", "hi"); code != ExitNotFound {
		t.Fatalf("send to a killed agent: exit %d", code)
	}
}

func TestSendRefusesAnEndedAgent(t *testing.T) {
	testutil.FakeClaude(t)
	h := newRealHQ(t)
	h.cwd = testutil.GitRepo(t, "app")
	if code, _, errOut := h.run("new", "a", "hello"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	h.waitReport("a", "done", "Done: hello")
	h.typeIn("a", "/exit")
	h.waitState("a", "ended")
	code, _, errOut := h.run("send", "a", "hi")
	if code != ExitUsage || !strings.Contains(errOut, "a has ended and cannot receive messages; relaunch it with hq sandbox restart app") {
		t.Fatalf("exit %d %q", code, errOut)
	}
}
