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
	t.Setenv("HQ_TMUX_SOCKET", j.socket)
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

	// See.
	eventually(t, "a listed as running", func() bool {
		_, out := j.hq("ls")
		return strings.Contains(out, "a     app   -       running")
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
		return strings.Contains(screen(), "claude in claude-app:") && strings.Contains(screen(), "say hi")
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
