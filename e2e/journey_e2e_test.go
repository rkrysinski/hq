//go:build e2e

// Package e2e drives the built hq binary the way a person does, in real tmux
// with the stub sbx (docs/agents/testing.md).
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/testutil"
)

type journey struct {
	t      *testing.T
	bin    string // the built binary
	socket string // hq's private tmux server
	repo   string
	edited string // the directories the stand-in for VS Code opened
}

func newJourney(t *testing.T) *journey {
	j := &journey{t: t, bin: filepath.Join(t.TempDir(), "hq"), socket: testutil.TmuxSocket(t)}
	if out, err := exec.Command("go", "build", "-o", j.bin, "../cmd/hq").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	// hq runs `sbx` from PATH: put the stub there.
	stub, _ := testutil.SbxStub(t)
	bin := t.TempDir()
	if err := os.Symlink(stub, filepath.Join(bin, "sbx")); err != nil {
		t.Fatal(err)
	}
	// A stand-in for VS Code's code, logging the directory it opens.
	j.edited = filepath.Join(t.TempDir(), "edited")
	if err := os.WriteFile(filepath.Join(bin, "code"), []byte("#!/bin/sh\necho \"$1\" >> "+j.edited+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	// Behind the stub runs the fake claude, which fires hq's hooks.
	testutil.FakeClaude(t)
	t.Setenv("FAKE_CLAUDE_DELAY", "1s")
	t.Setenv("HQ_TMUX_SOCKET", j.socket)
	// The dashboard keeps its modes in the preferences file: not the user's.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	os.Unsetenv("TMUX")
	j.repo = testutil.GitRepo(t, "app")
	return j
}

// hq runs the binary in the repository, the way a shell does.
func (j *journey) hq(args ...string) (int, string) {
	j.t.Helper()
	cmd := exec.Command(j.bin, args...)
	cmd.Dir = j.repo
	out, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), string(out)
	} else if err != nil {
		j.t.Fatal(err)
	}
	return 0, string(out)
}

// eventually retries check for up to 5 seconds.
func eventually(t *testing.T, what string, check func() bool) {
	t.Helper()
	for i := 0; i < 50; i++ {
		if check() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("never: %s", what)
}

func TestStartSeeEnterKill(t *testing.T) {
	j := newJourney(t)

	// Start.
	if code, out := j.hq("new", "a", "say hi"); code != 0 || !strings.Contains(out, "started a in app") {
		t.Fatalf("new: exit %d %q", code, out)
	}

	// See: the reply lands as the agent's state and last message.
	row := func() string {
		_, out := j.hq("ls")
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, "a ") {
				return strings.Join(strings.Fields(l), " ")
			}
		}
		return ""
	}
	// Before its first report the agent has no branch; then it names the
	// branch Claude works on.
	for _, want := range []string{"a app - starting ", "a app main working "} {
		eventually(t, want, func() bool { return strings.HasPrefix(row(), want) })
	}
	eventually(t, "a listed as done", func() bool {
		return strings.HasPrefix(row(), "a app main done ") && strings.HasSuffix(row(), " Done: say hi")
	})

	// Enter, from a plain terminal: an outer tmux server serves as the terminal.
	term := testutil.TmuxSocket(t)
	if out, err := exec.Command("tmux", "-L", term, "new-session", "-d", "-x", "120", "-y", "30",
		"env", "-u", "TMUX", j.bin, "go", "a").CombinedOutput(); err != nil {
		t.Fatalf("terminal: %v %s", err, out)
	}
	screen := func() string {
		out, _ := exec.Command("tmux", "-L", term, "capture-pane", "-p").Output()
		return string(out)
	}
	eventually(t, "the terminal shows a's session", func() bool {
		return strings.Contains(screen(), "claude in claude-app:") && strings.Contains(screen(), "Done: say hi")
	})

	// Reply in the session: the agent works, then asks.
	if out, err := exec.Command("tmux", "-L", term, "send-keys", "a question please", "Enter").CombinedOutput(); err != nil {
		t.Fatalf("send-keys: %v %s", err, out)
	}
	eventually(t, "a listed as asking a question", func() bool {
		return strings.HasPrefix(row(), "a app main question ") && strings.HasSuffix(row(), " Shall I go on?")
	})

	// Kill.
	if code, out := j.hq("kill", "a", "-y"); code != 0 || !strings.Contains(out, "killed a") {
		t.Fatalf("kill: exit %d %q", code, out)
	}
	if _, out := j.hq("ls"); out != "" {
		t.Fatalf("ls after kill: %q", out)
	}
	if exec.Command("pgrep", "-f", `HQ_AGENT":"a"`).Run() == nil {
		t.Fatal("a's session still runs after the kill")
	}
	if out, _ := exec.Command("sbx", "ls", "--json").Output(); !strings.Contains(string(out), "claude-app") {
		t.Fatalf("the sandbox must stay: %s", out)
	}
	if code, out := j.hq("new", "a"); code != 0 {
		t.Fatalf("the name is not free again: exit %d %q", code, out)
	}
}

func TestDashboardFollowsAgentsQuitsAndComesBack(t *testing.T) {
	j := newJourney(t)
	t.Setenv("SHELL", "/bin/sh") // the shells in the dashboard's panes
	term := testutil.TmuxSocket(t)
	open := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("tmux", append([]string{"-L", term, "new-session", "-d", "-x", "120", "-y", "30",
			"-c", j.repo, "env", "-u", "TMUX", j.bin}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("terminal: %v %s", err, out)
		}
	}
	screen := func() string {
		out, _ := exec.Command("tmux", "-L", term, "capture-pane", "-p").Output()
		return string(out)
	}
	shows := func(what string, texts ...string) {
		t.Helper()
		defer func() {
			if t.Failed() {
				t.Log(screen())
			}
		}()
		eventually(t, what, func() bool {
			s := screen()
			for _, x := range texts {
				if !strings.Contains(s, x) {
					return false
				}
			}
			return true
		})
	}
	keys := func(k ...string) {
		t.Helper()
		if out, err := exec.Command("tmux", append([]string{"-L", term, "send-keys"}, k...)...).CombinedOutput(); err != nil {
			t.Fatalf("send-keys: %v %s", err, out)
		}
	}
	listPane := func() string {
		out, _ := exec.Command("tmux", "-L", j.socket, "list-panes", "-s", "-t", "hq:", "-F", "#{@hq_role} #{pane_id} #{@hq_list_pid}").Output()
		for _, l := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(l, "list ") {
				return l
			}
		}
		return ""
	}

	// S1: hq with nothing running.
	open()
	shows("the empty dashboard", "hq  0 agents", "no agents yet - press n to start one", "▸ placeholder shell",
		"hq: nothing docked - select an agent above or press n", "r refresh  q quit")

	// S2 from the New agent dialog: a gets the cursor with the new marker
	// and, nothing being docked, the slot and the keys.
	keys("n")
	shows("the New agent dialog", "New agent", "×", "name", "prompt", "Start ⏎", "Cancel")
	keys("a", "Tab", "Tab", "say hi", "Enter")
	shows("a new, starting", "a new", "● starting")
	// The row itself may be hidden by now: done, in the attention view.
	shows("a docked", "▸ a · ", "fake claude: ready", "> say hi")
	if s := screen(); strings.Contains(s, "placeholder shell") || strings.Contains(s, "New agent") {
		t.Fatalf("placeholder or dialog still shown:\n%s", s)
	}
	keys("more please", "Enter")
	shows("keys reach Claude", "> more please")

	// A duplicate name keeps the dialog open with the error; Esc closes it.
	keys("C-b", "Up", "n")
	shows("the dialog again", "New agent")
	keys("a", "Enter")
	shows("the duplicate", "agent 'a' already exists")
	keys("Escape")
	eventually(t, "the dialog to close", func() bool { return !strings.Contains(screen(), "New agent") })

	// S0: the attention view hides an agent that is done; a shows all.
	shows("a done, hidden", "hq  1 agent · 1 done  view: attention", "nothing needs you - press a for all")
	keys("a")
	shows("a's row, done", "view: all", "● done", "Done: more please", "a view: attention")
	if s := screen(); strings.Contains(s, "1:a") {
		t.Fatalf("tmux's window list shows:\n%s", s)
	}
	keys("s")
	shows("the repo sort", "REPO ▾", "s sort: repo")
	// c opens the editor on a's worktree: here the repository itself.
	keys("c")
	eventually(t, "the editor on the repository", func() bool {
		b, _ := os.ReadFile(j.edited)
		got, _ := filepath.EvalSymlinks(strings.TrimSpace(string(b)))
		want, _ := filepath.EvalSymlinks(j.repo)
		return got != "" && got == want
	})

	// S3b: /a finds a in the footer; Enter keeps it docked and puts the
	// keys there.
	keys("/")
	keys("a")
	shows("the search", "/a 1 match: a", "esc cancel")
	keys("Enter")
	eventually(t, "the keys in a's session", func() bool {
		out, _ := exec.Command("tmux", "-L", j.socket, "display-message", "-p", "-t", "hq:", "#{@hq_agent}").Output()
		return strings.TrimSpace(string(out)) != ""
	})
	keys("once more", "Enter")
	shows("keys reach Claude again", "> once more", "│ a ")
	detach := func() {
		t.Helper()
		if out, err := exec.Command("tmux", "-L", j.socket, "detach-client", "-s", "hq").CombinedOutput(); err != nil {
			t.Fatalf("detach: %v %s", err, out)
		}
		eventually(t, "the terminal to end", func() bool {
			return exec.Command("tmux", "-L", term, "has-session").Run() != nil
		})
	}
	// hq go from a plain terminal: the dashboard with a docked, its
	// scrollback intact.
	detach()
	open("go", "a")
	shows("hq go a", "▸ a · ", "> say hi", "> more please", "│ a ", "alt+l list")
	// S2 from another shell, a docked: b gets the marker, a keeps the slot.
	if code, out := j.hq("new", "b"); code != 0 {
		t.Fatalf("new: exit %d %q", code, out)
	}
	shows("b new", "b new", "▸ a · ")
	// §6.5 from inside a's session: the footer names the chords, Alt+j and
	// Alt+k dock the next and previous row, Alt+l moves the keys to the
	// list and back.
	shows("the chords' hints", "alt+j/k dock next/previous")
	keys("M-j")
	shows("b docked by Alt+j", "▸ b · ")
	keys("M-k")
	shows("a docked by Alt+k", "▸ a · ")
	role := func() string {
		out, _ := exec.Command("tmux", "-L", j.socket, "display-message", "-p", "-t", "hq:", "#{@hq_role}").Output()
		return strings.TrimSpace(string(out))
	}
	keys("M-l")
	eventually(t, "the keys in the list", func() bool { return role() == "list" })
	shows("the list's footer", "q quit")
	keys("M-l")
	eventually(t, "the keys in a's session", func() bool { return role() != "list" })
	if code, out := j.hq("kill", "b", "-y"); code != 0 {
		t.Fatalf("kill: exit %d %q", code, out)
	}
	shows("b gone", "hq  1 agent")
	// S6 from the list: No is the default; y kills the docked a, the slot
	// gets the placeholder with its hint and the keys stay in the list.
	keys("C-b", "Up")
	keys("k")
	shows("the Kill dialog", "Kill agent", "Kill a (", "Ends the Claude session; the sandbox stays.", "No ⏎", "Yes")
	keys("Enter")
	eventually(t, "the dialog to close", func() bool { return !strings.Contains(screen(), "Kill agent") })
	shows("a kept", "hq  1 agent", "▸ a · ")
	keys("k")
	shows("the Kill dialog again", "Kill agent")
	keys("y")
	shows("the list empty again, the placeholder back", "hq  0 agents", "▸ placeholder shell", "hq: a killed - select an agent above or press n")
	if out, _ := exec.Command("tmux", "-L", j.socket, "display-message", "-p", "-t", "hq:", "#{@hq_role}").Output(); strings.TrimSpace(string(out)) != "list" {
		t.Fatalf("the keys are in the %q pane, not the list", out)
	}
	// The name is free at once.
	if code, out := j.hq("new", "a"); code != 0 {
		t.Fatalf("new a again: exit %d %q", code, out)
	}
	shows("a again", "a new")
	if code, out := j.hq("kill", "a", "-y"); code != 0 {
		t.Fatalf("kill: exit %d %q", code, out)
	}
	shows("the list empty again", "hq  0 agents", "▸ placeholder shell", "hq: a killed - select an agent above or press n")

	// S8: q leaves the hint in the list pane; hq dash there brings the list back.
	keys("q")
	shows("the quit hint", "hq dash stopped - agents keep running.")
	if s := screen(); strings.Contains(s, "q quit") || strings.Contains(s, "TAB") {
		t.Fatalf("list or footer left after q:\n%s", s)
	}
	keys(j.bin+" dash", "Enter")
	shows("the list back, in the modes left", "hq  0 agents  view: all", "REPO ▾", "q quit")
	before := listPane()

	// S9: detach, then hq again: the same layout, the same list program.
	detach()
	open()
	shows("the dashboard again", "hq  0 agents", "▸ placeholder shell", "q quit")
	if after := listPane(); after != before || before == "" {
		t.Fatalf("list pane %q before detach, %q after", before, after)
	}
}
