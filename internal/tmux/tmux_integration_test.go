//go:build integration

package tmux

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/proc"
	"github.com/rkrysinski/hq/internal/testutil"
)

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	if !waitFor(ok) {
		t.Fatalf("timed out waiting for %s", what)
	}
}

// waitFor polls ok for up to 5 s.
func waitFor(ok func() bool) bool {
	for i := 0; i < 50; i++ {
		if ok() {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func TestNoServerIsNoWindows(t *testing.T) {
	c := Client{Run: proc.Exec{}, Socket: testutil.TmuxSocket(t)}
	ws, err := c.Windows()
	if err != nil || len(ws) != 0 {
		t.Fatalf("%v %v", ws, err)
	}
}

func TestHomeWindowKeepsOptionsArgumentsAndOutputAfterExit(t *testing.T) {
	socket := testutil.TmuxSocket(t)
	c := Client{Run: proc.Exec{}, Socket: socket}
	dir := t.TempDir()
	if err := c.EnsureSession(dir); err != nil {
		t.Fatal(err)
	}
	if err := c.EnsureSession(dir); err != nil {
		t.Fatalf("second EnsureSession: %v", err)
	}
	id, err := c.NewWindow("a", dir, map[string]string{"id": "x1", "name": "a", "repo": "/r e/p"},
		[]string{"printf", "%s|", "a b", `'q"`, "$HOME", ";exit"})
	if err != nil {
		t.Fatal(err)
	}
	ws, _ := c.Windows()
	if len(ws) != 2 || ws[1].ID != id || ws[1].Name != "a" || ws[1].Options["repo"] != "/r e/p" || ws[1].PaneDead {
		t.Fatalf("before start: %+v", ws)
	}
	// Notifications pass from the hidden window to the terminal; the pane
	// carries its agent's id and its settings when it is docked.
	for opt, want := range map[string]string{"allow-passthrough": "all", "remain-on-exit": "on", "alternate-screen": "off", "@hq_agent": "x1"} {
		if out, _ := exec.Command("tmux", "-L", socket, "show-options", "-pv", "-t", id, opt).Output(); strings.TrimSpace(string(out)) != want {
			t.Fatalf("%s: %q, want %q", opt, out, want)
		}
	}
	if err := c.Start(id); err != nil {
		t.Fatal(err)
	}
	eventually(t, "pane to die", func() bool {
		ws, _ := c.Windows()
		return len(ws) == 2 && ws[1].PaneDead
	})
	out, _ := exec.Command("tmux", "-L", socket, "capture-pane", "-p", "-t", id).Output()
	if !strings.Contains(string(out), `a b|'q"|$HOME|;exit|`) {
		t.Fatalf("arguments were not passed verbatim:\n%s", out)
	}
	if err := c.SetOption(id, "ending", "1"); err != nil {
		t.Fatal(err)
	}
	if ws, _ := c.Windows(); ws[1].Options["ending"] != "1" || ws[1].Options["repo"] != "/r e/p" {
		t.Fatalf("after set: %+v", ws[1])
	}
	if err := c.KillWindow(id); err != nil {
		t.Fatal(err)
	}
	if ws, _ := c.Windows(); len(ws) != 1 {
		t.Fatalf("after kill: %+v", ws)
	}
}

func TestVersionOfInstalledTmuxIsParsed(t *testing.T) {
	v, err := Client{Run: proc.Exec{}}.Version()
	if err != nil || !versionRE.MatchString(v) {
		t.Fatalf("%q %v", v, err)
	}
}

func TestSocketPathAndEnterWithoutClient(t *testing.T) {
	socket := testutil.TmuxSocket(t)
	c := Client{Run: proc.Exec{}, Socket: socket}
	if err := c.EnsureSession(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	p, err := c.SocketPath()
	if err != nil || !strings.HasSuffix(p, "/"+socket) {
		t.Fatalf("%q %v", p, err)
	}
	id, err := c.NewWindow("a", t.TempDir(), map[string]string{"id": "x"}, []string{"sleep", "30"})
	if err != nil {
		t.Fatal(err)
	}
	// With no client attached, the window is still selected before switch-client fails.
	_ = c.Enter(id)
	out, _ := exec.Command("tmux", "-L", socket, "display-message", "-p", "-t", Session+":", "#{window_id}").Output()
	if strings.TrimSpace(string(out)) != id {
		t.Fatalf("active window %q, want %s", out, id)
	}
}

type recordTerminal struct{ args []string }

func (r *recordTerminal) Interactive(name string, args ...string) error {
	r.args = append([]string{name}, args...)
	return nil
}

func TestAttachSelectsWindowThenAttachesToHqSessionDetachingOthers(t *testing.T) {
	socket := testutil.TmuxSocket(t)
	c := Client{Run: proc.Exec{}, Socket: socket}
	if err := c.EnsureSession(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	id, err := c.NewWindow("a", t.TempDir(), map[string]string{"id": "x"}, []string{"sleep", "30"})
	if err != nil {
		t.Fatal(err)
	}
	term := &recordTerminal{}
	if err := c.Attach(id, term); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(term.args, " "); got != "tmux -L "+socket+" attach-session -d -t hq" {
		t.Fatalf("attach command %q", got)
	}
	out, _ := exec.Command("tmux", "-L", socket, "display-message", "-p", "-t", Session+":", "#{window_id}").Output()
	if strings.TrimSpace(string(out)) != id {
		t.Fatalf("active window %q, want %s", out, id)
	}
}
