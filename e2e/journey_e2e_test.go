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
		"hq: nothing docked - select an agent above or press n", "r refresh   q quit")

	// An agent started from another shell appears, and follows its session.
	if code, out := j.hq("new", "a", "say hi"); code != 0 {
		t.Fatalf("new: exit %d %q", code, out)
	}
	// S0: the attention view hides an agent that is done; a shows all.
	shows("a done, hidden", "hq  1 agent · 1 done  view: attention", "nothing needs you - press a for all")
	keys("a")
	shows("a's row, done", "view: all", "● done", "Done: say hi", "a view: attention")
	if s := screen(); strings.Contains(s, "1:a") {
		t.Fatalf("tmux's window list shows:\n%s", s)
	}
	keys("s")
	shows("the repo sort", "REPO ▾", "s sort: repo")

	// S3: Enter docks a below the list, keys in its session; the row is
	// outlined.
	keys("Enter")
	shows("a docked", "▸ a · ", "fake claude: ready", "> say hi", "│ a ")
	if s := screen(); strings.Contains(s, "placeholder shell") {
		t.Fatalf("placeholder still shown:\n%s", s)
	}
	keys("more please", "Enter")
	shows("keys reach Claude", "> more please")
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
	shows("hq go a", "▸ a · ", "> say hi", "> more please", "│ a ", "q quit")
	if code, out := j.hq("kill", "a", "-y"); code != 0 {
		t.Fatalf("kill: exit %d %q", code, out)
	}
	shows("the list empty again, the placeholder back", "hq  0 agents", "▸ placeholder shell", "hq: a killed - select an agent above or press n")

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
