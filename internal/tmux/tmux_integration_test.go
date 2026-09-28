//go:build integration

package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
	if len(ws) != 2 || ws[1].ID != id || ws[1].Name != "a" || ws[1].Options["repo"] != "/r e/p" || ws[1].PaneDead || !ws[1].DeadAt.IsZero() {
		t.Fatalf("before start: %+v", ws)
	}
	// Notifications pass from the hidden window to the terminal; the pane
	// carries its agent's id and its settings when it is docked.
	for opt, want := range map[string]string{"allow-passthrough": "all", "remain-on-exit": "on", "alternate-screen": "off", "@hq_agent": "x1"} {
		if out, _ := exec.Command("tmux", "-L", socket, "show-options", "-pv", "-t", id, opt).Output(); strings.TrimSpace(string(out)) != want {
			t.Fatalf("%s: %q, want %q", opt, out, want)
		}
	}
	started := time.Now()
	if err := c.Start(id); err != nil {
		t.Fatal(err)
	}
	eventually(t, "pane to die", func() bool {
		ws, _ := c.Windows()
		return len(ws) == 2 && ws[1].PaneDead
	})
	// tmux tells when the pane died, to the second (#73).
	if ws, _ := c.Windows(); ws[1].DeadAt.Before(started.Truncate(time.Second)) || ws[1].DeadAt.After(time.Now()) {
		t.Fatalf("died at %v, started %v", ws[1].DeadAt, started)
	}
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

func TestScreensShowWhatEachPaneShows(t *testing.T) {
	c := Client{Run: proc.Exec{}, Socket: testutil.TmuxSocket(t)}
	dir := t.TempDir()
	if err := c.EnsureSession(dir); err != nil {
		t.Fatal(err)
	}
	var panes []string
	for _, text := range []string{"first\nscreen", "● User declined\n❯ "} {
		id, err := c.NewWindow("w", dir, nil, []string{"printf", "%s", text})
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Start(id); err != nil {
			t.Fatal(err)
		}
		ws, _ := c.Windows()
		panes = append(panes, ws[len(ws)-1].Pane)
	}
	var screens map[string]string
	eventually(t, "both screens", func() bool {
		var err error
		screens, err = c.Screens(panes)
		return err == nil && strings.Contains(screens[panes[0]], "screen") && strings.Contains(screens[panes[1]], "❯")
	})
	if len(screens) != 2 || !strings.HasPrefix(screens[panes[0]], "first\nscreen\n") || !strings.HasPrefix(screens[panes[1]], "● User declined\n❯") {
		t.Fatalf("%q", screens)
	}
	if s, err := c.Screens(nil); s != nil || err != nil {
		t.Fatalf("no panes: %v %v", s, err)
	}
	if _, err := c.Screens([]string{"%999"}); err == nil {
		t.Fatal("a pane that is gone is an error")
	}
}

// An agent's session that resets the terminal as it ends, as sbx run does
// when its sandbox stops, has its latest output drawn again, so its dead
// pane shows it rather than a blank screen (S7, #105).
func TestLastScreenDrawsTheLatestOutputAgainAfterAReset(t *testing.T) {
	socket := testutil.TmuxSocket(t)
	c := Client{Run: proc.Exec{}, Socket: socket}
	dir := t.TempDir()
	if err := c.EnsureSession(dir); err != nil {
		t.Fatal(err)
	}
	redrawn := filepath.Join(dir, "redrawn")
	// The session: 60 lines of output, the reset and sbx's last word; then
	// it draws what LastScreen gives, as hq's session command does.
	id, err := c.NewWindow("a", dir, map[string]string{"id": "x1", "name": "a"}, []string{"sh", "-c",
		`i=1; while [ $i -le 60 ]; do echo "line $i"; i=$((i+1)); done; printf '\033c'; echo 'error: sandbox stopped'
		while [ ! -e "$1" ]; do sleep 0.05; done; cat "$1"; exit 1`, "sh", redrawn})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(id); err != nil {
		t.Fatal(err)
	}
	pane := tm(t, socket, "display-message", "-p", "-t", id, "#{pane_id}")
	shown := func() string { return tm(t, socket, "capture-pane", "-p", "-t", pane) }
	eventually(t, "the reset", func() bool { return strings.HasPrefix(shown(), "error: sandbox stopped") })
	if strings.Contains(shown(), "line 60") {
		t.Fatalf("the reset left the output on the screen:\n%s", shown())
	}
	s, err := c.LastScreen(pane)
	if err != nil || s == "" {
		t.Fatalf("%q %v", s, err)
	}
	if err := os.WriteFile(redrawn+".tmp", []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(redrawn+".tmp", redrawn); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the pane to die", func() bool { return tm(t, socket, "display-message", "-p", "-t", pane, "#{pane_dead}") == "1" })
	// The dead pane shows the latest lines, down to sbx's last word, above
	// tmux's own line; the history still holds all of them.
	lines := strings.Split(shown(), "\n")
	height, _ := strconv.Atoi(tm(t, socket, "display-message", "-p", "-t", pane, "#{pane_height}"))
	if len(lines) != height || !strings.HasPrefix(lines[height-1], "Pane is dead") || lines[height-2] != "" ||
		lines[height-3] != "error: sandbox stopped" || lines[height-4] != "line 60" || lines[0] != fmt.Sprintf("line %d", 60-(height-4)) {
		t.Fatalf("dead pane shows:\n%s", shown())
	}
	if all := tm(t, socket, "capture-pane", "-p", "-S", "-", "-t", pane); !strings.Contains(all, "line 1\n") {
		t.Fatalf("history lost the output:\n%s", all)
	}
	// Made smaller, as docking does, the pane keeps showing them, each on
	// its own line.
	tm(t, socket, "resize-window", "-t", id, "-y", "8", "-x", "60")
	if got := strings.Split(shown(), "\n"); len(got) != 8 || got[5] != "line 60" || got[6] != "error: sandbox stopped" || !strings.HasPrefix(got[7], "Pane is dead") {
		t.Fatalf("made smaller, the pane shows:\n%s", shown())
	}
}

// Claude redraws its whole screen on every resize, after clearing it, and
// tmux pushes each screen it clears into the history (scroll-on-clear). An
// agent resized a few times, docked and undocked, then cut off, has only
// its last screen drawn again, not a stack of the earlier ones (#130).
func TestLastScreenDrawsOnlyTheLastOfClaudesRedraws(t *testing.T) {
	socket := testutil.TmuxSocket(t)
	c := Client{Run: proc.Exec{}, Socket: socket}
	dir := t.TempDir()
	if err := c.EnsureSession(dir); err != nil {
		t.Fatal(err)
	}
	next := filepath.Join(dir, "next")
	redrawn := filepath.Join(dir, "redrawn")
	// Screen k, drawn as Claude draws it: cleared, the conversation at the
	// top, the footer on the bottom row. Each screen is drawn once the test
	// has resized the pane; after the third, the reset and sbx's last word,
	// then what LastScreen gives.
	id, err := c.NewWindow("a", dir, map[string]string{"id": "x1", "name": "a"}, []string{"sh", "-c", `
		for k in 1 2 3; do
			while [ ! -e "$1.$k" ]; do sleep 0.05; done
			printf '\033[2J\033[H\n Claude Code\n\n> say yo\n\n* Yo\033[999;1Hfooter %s' "$k"
		done
		while [ ! -e "$1.4" ]; do sleep 0.05; done
		printf '\033c'; echo 'error: sandbox stopped'
		while [ ! -e "$2" ]; do sleep 0.05; done; cat "$2"; exit 1`, "sh", next, redrawn})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(id); err != nil {
		t.Fatal(err)
	}
	pane := tm(t, socket, "display-message", "-p", "-t", id, "#{pane_id}")
	shown := func() string { return tm(t, socket, "capture-pane", "-p", "-t", pane) }
	for k, height := range []int{30, 20, 16} {
		tm(t, socket, "resize-window", "-t", id, "-y", strconv.Itoa(height), "-x", "60")
		if err := os.WriteFile(fmt.Sprintf("%s.%d", next, k+1), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		eventually(t, "a redraw", func() bool { return strings.Contains(shown(), fmt.Sprintf("footer %d", k+1)) })
	}
	if err := os.WriteFile(next+".4", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the reset", func() bool { return strings.HasPrefix(shown(), "error: sandbox stopped") })
	s, err := c.LastScreen(pane)
	if err != nil || s == "" {
		t.Fatalf("%q %v", s, err)
	}
	if err := os.WriteFile(redrawn+".tmp", []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(redrawn+".tmp", redrawn); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the pane to die", func() bool { return tm(t, socket, "display-message", "-p", "-t", pane, "#{pane_dead}") == "1" })
	// The dead pane shows the last screen once, its blank rows folded, and
	// sbx's last word; the history still holds the earlier screens.
	got := shown()
	if strings.Count(got, "Claude Code") != 1 || strings.Contains(got, "footer 1") || strings.Contains(got, "footer 2") ||
		!strings.Contains(got, "* Yo\n\nfooter 3\nerror: sandbox stopped\n\nPane is dead") {
		t.Fatalf("dead pane shows:\n%s", got)
	}
	if all := tm(t, socket, "capture-pane", "-p", "-S", "-", "-t", pane); !strings.Contains(all, "footer 1") || !strings.Contains(all, "footer 2") {
		t.Fatalf("history lost the earlier screens:\n%s", all)
	}
}

// An agent's pane runs its session through hq's session program, when there
// is one, both when it starts and when it is relaunched.
func TestAgentPanesRunTheSessionThroughHqsSessionProgram(t *testing.T) {
	socket := testutil.TmuxSocket(t)
	c := Client{Run: proc.Exec{}, Socket: socket, Session: []string{"sh", "-c", `printf 'session:'; exec "$@"`, "sh"}}
	dir := t.TempDir()
	if err := c.EnsureSession(dir); err != nil {
		t.Fatal(err)
	}
	id, err := c.NewWindow("a", dir, map[string]string{"id": "x1", "name": "a"}, []string{"echo", "first", "run"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(id); err != nil {
		t.Fatal(err)
	}
	pane := tm(t, socket, "display-message", "-p", "-t", id, "#{pane_id}")
	shown := func() string { return tm(t, socket, "capture-pane", "-p", "-S", "-", "-t", pane) }
	eventually(t, "the session", func() bool { return strings.Contains(shown(), "session:first run") })
	if err := c.Respawn(pane, dir, []string{"echo", "relaunched"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the relaunched session", func() bool { return strings.Contains(shown(), "session:relaunched") })
}

func TestKeepFirstLetsTheFirstRecordWinAcrossRacingWriters(t *testing.T) {
	c := Client{Run: proc.Exec{}, Socket: testutil.TmuxSocket(t)}
	dir := t.TempDir()
	if err := c.EnsureSession(dir); err != nil {
		t.Fatal(err)
	}
	id, err := c.NewWindow("a", dir, map[string]string{"id": "x1"}, []string{"sleep", "60"})
	if err != nil {
		t.Fatal(err)
	}
	// Writers in separate tmux processes, as hq ls and the list are, each
	// with the moment it saw, all for the same report (#100). Every one
	// gets back the record that won, and it stays.
	for round := range 5 {
		key := strconv.Itoa(1000 + round)
		const writers = 8
		got := make([]string, writers)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := range writers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				got[i], _ = c.KeepFirst(id, "turnend", key+" ", fmt.Sprintf("%s %d User declined to answer questions", key, 100+i))
			}()
		}
		close(start)
		wg.Wait()
		ws, _ := c.Windows()
		stored := ws[len(ws)-1].Options["turnend"]
		if !strings.HasPrefix(stored, key+" 1") || !strings.HasSuffix(stored, " User declined to answer questions") {
			t.Fatalf("round %d: stored %q", round, stored)
		}
		for i, g := range got {
			if g != stored {
				t.Fatalf("round %d: writer %d got %q, stored %q", round, i, g, stored)
			}
		}
		// A later writer for the same report keeps it too.
		if g, err := c.KeepFirst(id, "turnend", key+" ", key+" 999 Interrupted"); err != nil || g != stored {
			t.Fatalf("round %d: later writer %q %v", round, g, err)
		}
	}
	// A record for another report (another prefix) is replaced; quotes and
	// spaces survive; a prefix that is not plain is refused.
	if g, err := c.KeepFirst(id, "endseen", "1.5 ", `1.5 "a b" $x`); err != nil || g != `1.5 "a b" $x` {
		t.Fatalf("new: %q %v", g, err)
	}
	if g, err := c.KeepFirst(id, "endseen", "2.5 ", "2.5 7"); err != nil || g != "2.5 7" {
		t.Fatalf("replaced: %q %v", g, err)
	}
	if _, err := c.KeepFirst(id, "endseen", "2* ", "2 7"); err == nil {
		t.Fatal("a glob prefix was taken")
	}
}

// hq send types a message into Claude's prompt box as a paste: bracketed,
// since Claude asks for that, its lines kept, then Enter; no buffer stays.
func TestPasteTypesTextAsABracketedPasteAndSubmitPressesEnter(t *testing.T) {
	c := Client{Run: proc.Exec{}, Socket: testutil.TmuxSocket(t)}
	dir := t.TempDir()
	if err := c.EnsureSession(dir); err != nil {
		t.Fatal(err)
	}
	got := filepath.Join(dir, "got")
	// A program that asks for bracketed paste, as Claude does, and records
	// what reaches it, a line at a time.
	id, err := c.NewWindow("w", dir, nil, []string{"sh", "-c", `stty -echo; printf '\033[?2004h'; echo ready; while IFS= read -r l; do printf '%s\n' "$l" >>"$0"; done`, got})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(id); err != nil {
		t.Fatal(err)
	}
	ws, _ := c.Windows()
	pane := ws[len(ws)-1].Pane
	eventually(t, "the program", func() bool {
		s, _ := c.Screens([]string{pane})
		return strings.Contains(s[pane], "ready")
	})
	// Longer than one tmux command carries, lines well within a terminal's.
	var lines []string
	for i := 0; len(strings.Join(lines, "\n")) < 3*pasteChunk; i++ {
		lines = append(lines, fmt.Sprintf("line %03d żółw %s", i, strings.Repeat("-", 80)))
	}
	text := strings.Join(lines, "\n")
	if err := c.Paste(pane, text); err != nil {
		t.Fatal(err)
	}
	if err := c.Submit(pane); err != nil {
		t.Fatal(err)
	}
	want := "\x1b[200~" + text + "\x1b[201~\n"
	var b []byte
	if !waitFor(func() bool { b, _ = os.ReadFile(got); return len(b) >= len(want) }) {
		t.Fatalf("got %d bytes of %d", len(b), len(want))
	}
	if string(b) != want {
		t.Fatalf("got %q", b)
	}
	if out, _ := c.tmux("list-buffers", "-F", "#{buffer_name}"); strings.Contains(string(out), "hq-send") {
		t.Fatalf("buffers left: %q", out)
	}
	if err := c.Paste("%999", "x"); err == nil {
		t.Fatal("a pane that is gone is an error")
	}
	if out, _ := c.tmux("list-buffers", "-F", "#{buffer_name}"); strings.Contains(string(out), "hq-send") {
		t.Fatalf("buffers left after a failure: %q", out)
	}
}
