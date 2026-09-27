//go:build integration

package tmux

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/rkrysinski/hq/internal/proc"
	"github.com/rkrysinski/hq/internal/testutil"
)

// listStub stands in for the list program: it shows the socket it was given.
var listStub = []string{"sh", "-c", `printf 'list:%s\n' "$HQ_TMUX_SOCKET"; sleep 30`}

func tm(t *testing.T, socket string, args ...string) string {
	t.Helper()
	out, err := exec.Command("tmux", append([]string{"-L", socket}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// framed lists the dashboard's panes other than the margins beside the
// slot, top to bottom: the list, then the slot.
func framed(t *testing.T, socket, win string) string {
	t.Helper()
	var ids []string
	for _, l := range strings.Split(tm(t, socket, "list-panes", "-t", win, "-F", "#{pane_id} #{@hq_role}"), "\n") {
		if id, role, _ := strings.Cut(l, " "); role != roleSpacer {
			ids = append(ids, id)
		}
	}
	return strings.Join(ids, "\n")
}

// shownTitle is the title the row above the slot shows, its styles left out.
func shownTitle(t *testing.T, socket, list string) string {
	t.Helper()
	got := tm(t, socket, "display-message", "-p", "-t", list, "#{T:pane-border-format}")
	return strings.TrimSpace(regexp.MustCompile(`#\[[^]]*\]`).ReplaceAllString(got, ""))
}

func dashClient(t *testing.T) (Client, string) {
	socket := testutil.TmuxSocket(t)
	return Client{Run: proc.Exec{}, Socket: socket}, socket
}

func TestDashboardHasTheListOnTopAndThePlaceholderBelow(t *testing.T) {
	c, socket := dashClient(t)
	d, err := c.Dashboard(t.TempDir(), listStub)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Started || d.List == "" || d.Slot == "" || d.List == d.Slot {
		t.Fatalf("%+v", d)
	}
	if got := framed(t, socket, d.Window); got != d.List+"\n"+d.Slot {
		t.Fatalf("panes top to bottom %q, want list then slot", got)
	}
	if tm(t, socket, "show-options", "-wv", "-t", d.Window, "@hq_dash") != "1" {
		t.Error("window not marked as the dashboard")
	}
	eventually(t, "list program to start", func() bool {
		return strings.Contains(tm(t, socket, "capture-pane", "-p", "-t", d.List), "list:"+socket)
	})
	eventually(t, "placeholder hint", func() bool {
		return strings.Contains(tm(t, socket, "capture-pane", "-p", "-t", d.Slot), PlaceholderHint)
	})
	if got := shownTitle(t, socket, d.List); got != "▸ placeholder" {
		t.Errorf("slot title %q", got)
	}
	if got := tm(t, socket, "display-message", "-p", "-t", d.Slot, "#{T:pane-border-format}"); got != "" {
		t.Errorf("the slot's own status row shows %q, want the empty margin", got)
	}
	for opt, want := range map[string]string{"set-titles": "on", "set-titles-string": "hq - agents", "mouse": "on"} {
		if got := tm(t, socket, "show-options", "-v", "-t", Session, opt); got != want {
			t.Errorf("%s = %q, want %q", opt, got, want)
		}
	}
	// Other sessions keep the server's mouse setting (text selection there
	// is unchanged).
	if got := tm(t, socket, "show-options", "-gv", "mouse"); got != "off" {
		t.Errorf("global mouse %q, want off", got)
	}
}

func TestStatusLineIsTheFooterAlone(t *testing.T) {
	c, socket := dashClient(t)
	dir := t.TempDir()
	if _, err := c.Dashboard(dir, listStub); err != nil {
		t.Fatal(err)
	}
	if _, err := c.NewWindow("agent-a", dir, map[string]string{"id": "x1"}, []string{"sleep", "30"}); err != nil {
		t.Fatal(err)
	}
	if err := c.SetFooter("keys here"); err != nil {
		t.Fatal(err)
	}
	// A second session on the same server keeps tmux's defaults.
	tm(t, socket, "new-session", "-d", "-s", "other")
	status := func(target string) string {
		return tm(t, socket, "display-message", "-p", "-t", target, "#{E:status-format[0]}")
	}
	if got := status(Session + ":"); got != "keys here" {
		t.Errorf("hq's status line %q, want the footer alone", got)
	}
	if got := status(Session + ":agent-a"); got != "keys here" {
		t.Errorf("with an agent window current: %q", got)
	}
	if got := status("other:"); strings.Contains(got, "keys here") {
		t.Errorf("another session shows hq's footer: %q", got)
	}
}

func TestDashboardIsFoundAgainAndRepairedWhenAPaneIsGone(t *testing.T) {
	c, socket := dashClient(t)
	dir := t.TempDir()
	first, err := c.Dashboard(dir, listStub)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.MarkList(first.List, 4242); err != nil {
		t.Fatal(err)
	}
	again, _ := c.Dashboard(dir, listStub)
	if again.Started || again.Window != first.Window || again.List != first.List || again.Slot != first.Slot || again.ListPID != 4242 {
		t.Fatalf("again %+v, first %+v", again, first)
	}
	_ = c.MarkList(first.List, 0)
	if again, _ = c.Dashboard(dir, listStub); again.ListPID != 0 {
		t.Fatalf("pid %d after clearing", again.ListPID)
	}

	tm(t, socket, "kill-pane", "-t", first.List)
	d, err := c.Dashboard(dir, listStub)
	if err != nil || !d.Started || d.Slot != first.Slot || d.List == first.List {
		t.Fatalf("list pane not made again: %+v %v", d, err)
	}
	if got := framed(t, socket, d.Window); got != d.List+"\n"+d.Slot {
		t.Fatalf("panes %q, want the list above the slot", got)
	}

	tm(t, socket, "kill-pane", "-t", d.Slot)
	e, err := c.Dashboard(dir, listStub)
	if err != nil || e.Started || e.List != d.List || e.Slot == d.Slot {
		t.Fatalf("slot not made again: %+v %v", e, err)
	}
	eventually(t, "new placeholder", func() bool {
		return strings.Contains(tm(t, socket, "capture-pane", "-p", "-t", e.Slot), PlaceholderHint)
	})
}

func TestSessionFromBeforeTheDashboardGetsItsPaneAsThePlaceholder(t *testing.T) {
	c, socket := dashClient(t)
	tm(t, socket, "new-session", "-d", "-s", Session, "-n", Session, "sh")
	old := tm(t, socket, "display-message", "-p", "-t", Session+":", "#{pane_id}")
	d, err := c.Dashboard(t.TempDir(), listStub)
	if err != nil || d.Slot != old || !d.Started {
		t.Fatalf("%+v %v, want the old pane %s as the slot", d, err, old)
	}
	eventually(t, "the placeholder in place of the shell", func() bool {
		return strings.Contains(tm(t, socket, "capture-pane", "-p", "-t", d.Slot), PlaceholderHint)
	})
}

// slotStub stands in for hq's placeholder program: it says its hint and
// where it runs, and waits.
var slotStub = []string{"sh", "-c", `printf 'slot:%s %s\n' "$HQ_TMUX_SOCKET" "$1"; exec sleep 30`, "sh"}

func TestThePlaceholderIsHqsProgramAndOutlivesItsEnd(t *testing.T) {
	c, socket := dashClient(t)
	c.Placeholder = slotStub
	d, err := c.Dashboard(t.TempDir(), listStub)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "the placeholder program with its hint", func() bool {
		return strings.Contains(tm(t, socket, "capture-pane", "-p", "-t", d.Slot), "slot:"+socket+" "+PlaceholderHint)
	})
	if got := tm(t, socket, "display-message", "-p", "-t", d.Slot, "#{@hq_placeholder} #{remain-on-exit}"); got != "program on" {
		t.Fatalf("placeholder options %q", got)
	}
	// A placeholder whose program ended stays, and is started again.
	tm(t, socket, "respawn-pane", "-k", "-t", d.Slot, "true")
	eventually(t, "the placeholder to end", func() bool {
		return tm(t, socket, "display-message", "-p", "-t", d.Slot, "#{pane_dead}") == "1"
	})
	again, err := c.Dashboard(t.TempDir(), listStub)
	if err != nil || again.Slot != d.Slot {
		t.Fatalf("%+v %v", again, err)
	}
	eventually(t, "the placeholder again", func() bool {
		return tm(t, socket, "display-message", "-p", "-t", d.Slot, "#{pane_dead}") == "0" &&
			strings.Contains(tm(t, socket, "capture-pane", "-p", "-t", d.Slot), "slot:")
	})
}

func TestAnOlderHqsPlaceholderShellIsReplaced(t *testing.T) {
	c, socket := dashClient(t)
	dir := t.TempDir()
	d, err := c.Dashboard(dir, listStub)
	if err != nil {
		t.Fatal(err)
	}
	// What an older hq left: a shell in the slot, framed "placeholder shell",
	// waiting in the home window of the docked agent.
	tm(t, socket, "respawn-pane", "-k", "-t", d.Slot, "sh")
	tm(t, socket, "set-option", "-p", "-u", "-t", d.Slot, "@hq_placeholder")
	tm(t, socket, "set-option", "-p", "-u", "-t", d.Slot, "remain-on-exit")
	tm(t, socket, "set-option", "-p", "-t", d.Slot, "@hq_title", "placeholder shell")
	w, _ := agentWindow(t, c, "a", dir, "sleep", "30")
	if err := c.Dock(w, "a"); err != nil {
		t.Fatal(err)
	}
	c.Placeholder = slotStub
	if _, err := c.Dashboard(dir, listStub); err != nil {
		t.Fatal(err)
	}
	if got := tm(t, socket, "display-message", "-p", "-t", d.Slot, "#{window_id} #{@hq_placeholder} #{remain-on-exit} #{@hq_title}"); got != w+" program on placeholder" {
		t.Fatalf("placeholder %q, want it in a's home window, hq's program", got)
	}
	eventually(t, "the placeholder program", func() bool {
		return strings.Contains(tm(t, socket, "capture-pane", "-p", "-t", d.Slot), "slot:"+socket+" "+PlaceholderHint)
	})
	if ws := mustWindows(t, c); !ws["a"].Docked {
		t.Fatalf("a no longer docked: %+v", ws["a"])
	}
}

func TestFocusListPutsTheKeysOnTheList(t *testing.T) {
	c, socket := dashClient(t)
	d, err := c.Dashboard(t.TempDir(), listStub)
	if err != nil {
		t.Fatal(err)
	}
	tm(t, socket, "select-pane", "-t", d.Slot)
	if err := c.FocusList(); err != nil {
		t.Fatal(err)
	}
	if got := tm(t, socket, "display-message", "-p", "-t", d.Window, "#{pane_id}"); got != d.List {
		t.Fatalf("keys on %s, want the list %s", got, d.List)
	}
	tm(t, socket, "kill-window", "-t", d.Window)
	if err := c.EnsureSession(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := c.FocusList(); err != ErrNoDashboard {
		t.Fatalf("without a list: %v", err)
	}
}

func TestDashboardIsMadeWhenOnlyAgentWindowsRemain(t *testing.T) {
	c, socket := dashClient(t)
	tm(t, socket, "new-session", "-d", "-s", Session, "-n", "a", "sleep 30")
	tm(t, socket, "set-option", "-w", "-t", Session+":a", "@hq_id", "x1")
	d, err := c.Dashboard(t.TempDir(), listStub)
	if err != nil {
		t.Fatal(err)
	}
	if n := tm(t, socket, "display-message", "-p", "-t", d.Window, "#{window_name} #{window_panes}"); n != "hq 4" {
		t.Fatalf("dashboard window %q", n)
	}
}

func TestListPaneRespawnFitAndFooter(t *testing.T) {
	c, socket := dashClient(t)
	d, err := c.Dashboard(t.TempDir(), listStub)
	if err != nil {
		t.Fatal(err)
	}
	// The terminal's height counts hq's status line on top of the window.
	win, _ := strconv.Atoi(tm(t, socket, "display-message", "-p", "-t", d.List, "#{window_height}"))
	if h, err := c.TerminalHeight(d.List); err != nil || win < 20 || h != win+1 {
		t.Fatalf("terminal height %d %v, window %d", h, err, win)
	}
	if err := c.ResizeHeight(d.List, 7); err != nil {
		t.Fatal(err)
	}
	if got := tm(t, socket, "display-message", "-p", "-t", d.List, "#{pane_height}"); got != "7" {
		t.Errorf("list height %s, want 7", got)
	}
	if err := c.SetFooter("#[bold]q#[default] quit"); err != nil {
		t.Fatal(err)
	}
	if got := tm(t, socket, "show-options", "-v", "-t", Session, "status-left"); got != "#[bold]q#[default] quit" {
		t.Errorf("footer %q", got)
	}
	if err := c.RespawnList(d.List, []string{"sh", "-c", "echo again; sleep 30"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "list program again", func() bool {
		return strings.Contains(tm(t, socket, "capture-pane", "-p", "-t", d.List), "again")
	})
}

func TestSessionValuesSurviveTheListProgram(t *testing.T) {
	c, _ := dashClient(t)
	if _, err := c.Dashboard(t.TempDir(), listStub); err != nil {
		t.Fatal(err)
	}
	if v, err := c.SessionValue("cursor"); err != nil || v != "" {
		t.Fatalf("unset: %q %v", v, err)
	}
	if err := c.SetSessionValue("cursor", "bok-17"); err != nil {
		t.Fatal(err)
	}
	if v, err := c.SessionValue("cursor"); err != nil || v != "bok-17" {
		t.Fatalf("%q %v", v, err)
	}
}

// agentWindow starts an agent window running argv, as hq new does.
func agentWindow(t *testing.T, c Client, name, dir string, argv ...string) (window, pane string) {
	t.Helper()
	id, err := c.NewWindow(name, dir, map[string]string{"id": "id-" + name, "name": name}, argv)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(id); err != nil {
		t.Fatal(err)
	}
	for _, w := range mustWindows(t, c) {
		if w.ID == id {
			return id, w.Pane
		}
	}
	t.Fatalf("window %s not listed", id)
	return "", ""
}

func mustWindows(t *testing.T, c Client) map[string]Window {
	t.Helper()
	ws, err := c.Windows()
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]Window{}
	for _, w := range ws {
		m[w.ID] = w
		if w.Options["name"] != "" {
			m[w.Options["name"]] = w
		}
	}
	return m
}

func TestDockSwapsAgentsIntoTheSlotAndBackHome(t *testing.T) {
	c, socket := dashClient(t)
	dir := t.TempDir()
	d, err := c.Dashboard(dir, listStub)
	if err != nil {
		t.Fatal(err)
	}
	aw, ap := agentWindow(t, c, "a", dir, "sh", "-c", "echo a-was-here; sleep 30")
	bw, bp := agentWindow(t, c, "b", dir, "sh", "-c", "echo b-was-here; sleep 30")
	eventually(t, "a's output", func() bool {
		return strings.Contains(tm(t, socket, "capture-pane", "-p", "-t", ap), "a-was-here")
	})
	inWindow := func(p string) string { return tm(t, socket, "display-message", "-p", "-t", p, "#{window_id}") }

	if err := c.Dock(aw, "a · main · claude-x"); err != nil {
		t.Fatal(err)
	}
	if inWindow(ap) != d.Window || inWindow(d.Slot) != aw {
		t.Fatalf("a's pane in %s, placeholder in %s", inWindow(ap), inWindow(d.Slot))
	}
	if got := tm(t, socket, "display-message", "-p", "-t", d.Window, "#{pane_id}"); got != ap {
		t.Errorf("keys on %s, want a's pane %s", got, ap)
	}
	if got := shownTitle(t, socket, d.List); got != "▸ a · main · claude-x" {
		t.Errorf("slot title %q", got)
	}
	ws := mustWindows(t, c)
	if a := ws["a"]; !a.Docked || a.Pane != ap || a.PaneDead || a.Title != "a · main · claude-x" || ws["b"].Docked {
		t.Fatalf("a %+v, b %+v", a, ws["b"])
	}

	// Docking b sends a home, still running, its output intact.
	if err := c.Dock(bw, "b · - · claude-x"); err != nil {
		t.Fatal(err)
	}
	if inWindow(ap) != aw || inWindow(bp) != d.Window || inWindow(d.Slot) != bw {
		t.Fatalf("a in %s, b in %s, placeholder in %s", inWindow(ap), inWindow(bp), inWindow(d.Slot))
	}
	if out := tm(t, socket, "capture-pane", "-p", "-t", ap); !strings.Contains(out, "a-was-here") {
		t.Errorf("a's scrollback after going home:\n%s", out)
	}
	if ws := mustWindows(t, c); ws["a"].Docked || ws["a"].PaneDead || !ws["b"].Docked {
		t.Fatalf("a %+v, b %+v", ws["a"], ws["b"])
	}

	// Docking the docked agent again changes nothing.
	if err := c.Dock(bw, "b · feat · claude-x"); err != nil || inWindow(bp) != d.Window {
		t.Fatalf("again: %v, b in %s", err, inWindow(bp))
	}
	if mustWindows(t, c)["b"].Title != "b · feat · claude-x" {
		t.Error("title not updated")
	}

	// Removing the docked agent's window takes its own pane and gives the
	// slot the placeholder, keys on the list.
	if err := c.KillWindow(bw); err != nil {
		t.Fatal(err)
	}
	if inWindow(d.Slot) != d.Window {
		t.Fatalf("placeholder in %s, want the dashboard", inWindow(d.Slot))
	}
	if strings.Contains(tm(t, socket, "list-panes", "-a", "-F", "#{pane_id}")+"\n", bp+"\n") {
		t.Fatal("b's pane outlived its window")
	}
	if got := framed(t, socket, d.Window); got != d.List+"\n"+d.Slot {
		t.Fatalf("dashboard panes %q", got)
	}
	if got := tm(t, socket, "display-message", "-p", "-t", d.Window, "#{pane_id}"); got != d.List {
		t.Errorf("keys on %s, want the list", got)
	}
	eventually(t, "the killed hint", func() bool {
		return strings.Contains(tm(t, socket, "capture-pane", "-p", "-t", d.Slot), KilledHint("b"))
	})
	if ws := mustWindows(t, c); ws["a"].Docked || ws["a"].PaneDead || len(ws) != 3 { // the dashboard by id, a by id and name
		t.Fatalf("windows %+v", ws)
	}
}

func TestDockedAgentThatEndsStaysReadable(t *testing.T) {
	c, socket := dashClient(t)
	dir := t.TempDir()
	if _, err := c.Dashboard(dir, listStub); err != nil {
		t.Fatal(err)
	}
	w, p := agentWindow(t, c, "e", dir, "sh", "-c", "read _; echo last-words")
	if err := c.Dock(w, "e"); err != nil {
		t.Fatal(err)
	}
	tm(t, socket, "send-keys", "-t", p, "Enter")
	eventually(t, "docked pane to die", func() bool { return mustWindows(t, c)["e"].PaneDead })
	if out := tm(t, socket, "capture-pane", "-p", "-t", p); !strings.Contains(out, "last-words") {
		t.Errorf("ended agent's output:\n%s", out)
	}
	if e := mustWindows(t, c)["e"]; !e.Docked || e.Pane != p {
		t.Errorf("%+v", e)
	}
}

func TestRespawnRelaunchesAnEndedDockedAgentInPlace(t *testing.T) {
	c, socket := dashClient(t)
	dir := t.TempDir()
	d, err := c.Dashboard(dir, listStub)
	if err != nil {
		t.Fatal(err)
	}
	w, p := agentWindow(t, c, "r", dir, "sh", "-c", "read _; echo first-session")
	if err := c.Dock(w, "r · feat · claude-x"); err != nil {
		t.Fatal(err)
	}
	tm(t, socket, "send-keys", "-t", p, "Enter")
	eventually(t, "the first session to end", func() bool { return mustWindows(t, c)["r"].PaneDead })

	other := t.TempDir()
	if err := c.Respawn(p, other, []string{"sh", "-c", `echo second-session; sleep 30`}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the second session", func() bool {
		return strings.Contains(tm(t, socket, "capture-pane", "-p", "-S", "-", "-t", p), "second-session")
	})
	// The same pane, still docked, framed as before, and still the agent's
	// own: it stays readable when it ends again.
	r := mustWindows(t, c)["r"]
	if !r.Docked || r.Pane != p || r.PaneDead || r.Title != "r · feat · claude-x" {
		t.Fatalf("after respawn %+v", r)
	}
	if got := tm(t, socket, "display-message", "-p", "-t", p, "#{window_id}"); got != d.Window {
		t.Fatalf("the pane moved to %s", got)
	}
	for opt, want := range map[string]string{"remain-on-exit": "on", "alternate-screen": "off", "@hq_agent": mustWindows(t, c)["r"].Options["id"]} {
		if got := tm(t, socket, "show-options", "-p", "-v", "-t", p, opt); got != want {
			t.Errorf("%s = %q, want %q", opt, got, want)
		}
	}
	if err := c.Respawn("%999", dir, []string{"true"}); err == nil {
		t.Error("respawning a pane that is gone succeeded")
	}
}

func TestDockWithoutADashboardIsAnError(t *testing.T) {
	c, _ := dashClient(t)
	dir := t.TempDir()
	if err := c.EnsureSession(dir); err != nil {
		t.Fatal(err)
	}
	w, _ := agentWindow(t, c, "a", dir, "sleep", "30")
	tm(t, c.Socket, "kill-window", "-t", Session+":"+Session)
	if err := c.Dock(w, "a"); err != ErrNoDashboard {
		t.Fatalf("err %v", err)
	}
	if err := c.Dock("@999", "x"); err == nil {
		t.Fatal("docked a window that does not exist")
	}
}

func TestPopupRunsCenteredOverTheClientAndReturnsWhenItCloses(t *testing.T) {
	c, socket := dashClient(t)
	d, err := c.Dashboard(t.TempDir(), listStub)
	if err != nil {
		t.Fatal(err)
	}
	// A terminal attached to hq's session: popups need a client.
	term := testutil.TmuxSocket(t)
	tm(t, term, "new-session", "-d", "-x", "100", "-y", "30", "env", "-u", "TMUX", "tmux", "-L", socket, "attach", "-t", Session)
	screen := func() string { return tm(t, term, "capture-pane", "-p") }
	eventually(t, "the client to attach", func() bool { return strings.Contains(screen(), "list:") })

	dir := t.TempDir()
	done := make(chan error, 1)
	go func() {
		done <- c.Popup(d.List, dir, 40, 8, []string{"sh", "-c", `printf 'popup %s %s\n' "$HQ_TMUX_SOCKET" "$(basename "$(pwd -P)")"; read _`})
	}()
	eventually(t, "the popup", func() bool {
		return strings.Contains(screen(), "popup "+socket+" "+filepath.Base(dir))
	})
	lines := strings.Split(screen(), "\n")
	for i, l := range lines {
		if strings.Contains(l, "popup ") {
			// Centered: as much room left and right of the 40 cells.
			r := []rune(l)
			left := strings.IndexRune(l, '│')
			left = len([]rune(l[:max(0, left)]))
			if right := 100 - left - 40; left < 0 || len(r) < left+40 || r[left+39] != '│' || left-right > 2 || right-left > 2 {
				t.Errorf("popup not centered:\n%s", strings.Join(lines, "\n"))
			}
			if !strings.Contains(lines[i-1], "╭") {
				t.Errorf("no rounded frame above:\n%s", strings.Join(lines, "\n"))
			}
		}
	}
	select {
	case err := <-done:
		t.Fatalf("Popup returned while open: %v", err)
	default:
	}
	tm(t, term, "send-keys", "Enter")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(screen(), "popup ") {
		t.Fatalf("popup still shown:\n%s", screen())
	}
}

func TestANewAgentsMarkerIsStoredOnItsWindow(t *testing.T) {
	c, _ := dashClient(t)
	if err := c.EnsureSession(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	id, err := c.NewWindow("a", t.TempDir(), map[string]string{"id": "x", "new": "1"}, []string{"sleep", "30"})
	if err != nil {
		t.Fatal(err)
	}
	if w := mustWindows(t, c)[id]; w.Options["new"] != "1" {
		t.Fatalf("%+v", w)
	}
}

func TestChordsWorkInHqsSessionAloneAndKeepOtherBindings(t *testing.T) {
	c, socket := dashClient(t)
	d, err := c.Dashboard(t.TempDir(), listStub)
	if err != nil {
		t.Fatal(err)
	}
	// The user's own binding of Alt+k, and another session showing its keys.
	tm(t, socket, "bind-key", "-n", "M-k", "send-keys", "user-k")
	tm(t, socket, "new-session", "-d", "-s", "other", "cat", "-v")
	// The chord command logs what it was given; its path needs quoting.
	dir := filepath.Join(t.TempDir(), "it's #1")
	log := filepath.Join(dir, "log")
	chord := func(tag string) []string {
		return []string{"sh", "-c", `mkdir -p "$(dirname "$0")"; echo "` + tag + ` $1 $HQ_TMUX_SOCKET" >> "$0"`, log}
	}
	if err := c.BindChords(chord("old"), "chord hints"); err != nil {
		t.Fatal(err)
	}
	// hq moved: bound again, the bindings stay and name the new command.
	if err := c.BindChords(chord("new"), "chord hints"); err != nil {
		t.Fatal(err)
	}
	keys := tm(t, socket, "list-keys", "-T", "root")
	if n := strings.Count(keys, "M-k "); n != 1 || !strings.Contains(keys, `"send-keys user-k"`) {
		t.Fatalf("Alt+k bound %d times or the user's binding lost:\n%s", n, keys)
	}

	term := testutil.TmuxSocket(t)
	tm(t, term, "new-session", "-d", "-x", "100", "-y", "30", "env", "-u", "TMUX", "tmux", "-L", socket, "attach", "-t", Session)
	screen := func() string { return tm(t, term, "capture-pane", "-p") }
	eventually(t, "the client to attach", func() bool { return strings.Contains(screen(), "list:") })
	logged := func() string {
		b, _ := os.ReadFile(log)
		return strings.TrimSpace(string(b))
	}
	// Chords run in the background, so each is awaited before the next;
	// sent together they may log in either order.
	want := ""
	for _, c := range []struct{ key, action string }{{"M-j", "next"}, {"M-k", "previous"}} {
		tm(t, term, "send-keys", c.key)
		want = strings.TrimSpace(want + "\nnew " + c.action + " " + socket)
		if !waitFor(func() bool { return logged() == want }) {
			t.Fatalf("after %s the chord log is %q, want %q", c.key, logged(), want)
		}
	}

	// Alt+l toggles the keys between list and slot; the footer follows.
	status := func() string {
		return tm(t, socket, "display-message", "-p", "-t", Session+":", "#{E:status-format[0]}")
	}
	active := func() string { return tm(t, socket, "display-message", "-p", "-t", Session+":", "#{pane_id}") }
	tm(t, socket, "select-pane", "-t", d.List)
	tm(t, socket, "set-option", "-t", Session, "status-left", "list keys")
	if got := status(); got != "list keys" {
		t.Errorf("footer with the keys in the list: %q", got)
	}
	tm(t, term, "send-keys", "M-l")
	eventually(t, "the keys in the slot", func() bool { return active() == d.Slot })
	if got := status(); got != "chord hints" {
		t.Errorf("footer with the keys in the slot: %q", got)
	}
	tm(t, term, "send-keys", "M-l")
	eventually(t, "the keys in the list", func() bool { return active() == d.List })

	// Another session: Alt+j reaches the pane, Alt+k the user's binding.
	tm(t, socket, "switch-client", "-t", "other")
	tm(t, term, "send-keys", "M-j")
	tm(t, term, "send-keys", "M-k")
	eventually(t, "the keys in the other session", func() bool { return strings.Contains(screen(), "^[juser-k") })
	if got := logged(); strings.Count(got, "\n") != 1 {
		t.Errorf("a chord ran in another session: %q", got)
	}
}

func TestMessageShowsOnTheStatusLine(t *testing.T) {
	c, _ := dashClient(t)
	if _, err := c.Dashboard(t.TempDir(), listStub); err != nil {
		t.Fatal(err)
	}
	// Without a client there is nothing to show it on; it is not an error.
	if err := c.Message("no agent needs you #1"); err != nil {
		t.Fatal(err)
	}
}

func TestShowAttachedNamesTheClientsTerminalAndShowsItTheWindow(t *testing.T) {
	c, socket := dashClient(t)
	d, err := c.Dashboard(t.TempDir(), listStub)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := agentWindow(t, c, "a", t.TempDir())
	if ttys, err := c.ShowAttached(d.Window); err != nil || len(ttys) != 0 {
		t.Fatalf("no client: %q %v", ttys, err)
	}
	term := testutil.TmuxSocket(t)
	tm(t, term, "new-session", "-d", "-x", "100", "-y", "30", "env", "-u", "TMUX", "tmux", "-L", socket, "attach", "-t", Session)
	eventually(t, "the client to attach", func() bool { return tm(t, socket, "list-clients", "-t", Session) != "" })
	tm(t, socket, "select-window", "-t", other)
	ttys, err := c.ShowAttached(d.Window)
	want := tm(t, term, "display-message", "-p", "#{pane_tty}")
	if err != nil || len(ttys) != 1 || ttys[0] != want {
		t.Fatalf("clients %q %v, want [%s]", ttys, err, want)
	}
	if got := tm(t, socket, "display-message", "-p", "-t", Session, "#{window_id}"); got != d.Window {
		t.Fatalf("window shown %s, want %s", got, d.Window)
	}
}

func TestTheTitleIsHqForHqsSessionAlone(t *testing.T) {
	c, socket := dashClient(t)
	if _, err := c.Dashboard(t.TempDir(), listStub); err != nil {
		t.Fatal(err)
	}
	tm(t, socket, "new-session", "-d", "-s", "other")
	if got := tm(t, socket, "show-options", "-gv", "set-titles"); got != "off" {
		t.Errorf("global set-titles %q", got)
	}
	if got := tm(t, socket, "show-options", "-v", "-t", "other", "set-titles"); got != "" {
		t.Errorf("other session's set-titles %q", got)
	}
}

func TestLeaveDetachesAPlainTerminalAndSendsASwitchedClientBack(t *testing.T) {
	c, socket := dashClient(t)
	d, err := c.Dashboard(t.TempDir(), listStub)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Leave("bye"); err != nil {
		t.Fatalf("no client: %v", err)
	}
	// A plain terminal attached to hq's session, and a client of another
	// session of this server that switched to hq's.
	tm(t, socket, "new-session", "-d", "-s", "work")
	plain, switched := testutil.TmuxSocket(t), testutil.TmuxSocket(t)
	tm(t, plain, "new-session", "-d", "-x", "100", "-y", "30", "env", "-u", "TMUX", "sh", "-c",
		"tmux -L "+socket+" attach -t "+Session+"; echo back-in-the-shell; sleep 30")
	tm(t, switched, "new-session", "-d", "-x", "100", "-y", "30", "env", "-u", "TMUX", "tmux", "-L", socket, "attach", "-t", "work")
	eventually(t, "both clients", func() bool {
		return len(strings.Fields(tm(t, socket, "list-clients", "-F", "#{client_name}"))) == 2
	})
	for _, l := range strings.Split(tm(t, socket, "list-clients", "-F", "#{client_name} #{session_name}"), "\n") {
		if name, s, _ := strings.Cut(l, " "); s == "work" {
			tm(t, socket, "switch-client", "-c", name, "-t", Session)
		}
	}
	eventually(t, "both clients on hq", func() bool {
		return len(strings.Fields(tm(t, socket, "list-clients", "-t", Session, "-F", "#{client_name}"))) == 2
	})
	if err := c.Leave("hq closed"); err != nil {
		t.Fatal(err)
	}
	if got := tm(t, socket, "list-clients", "-F", "#{session_name}"); got != "work" {
		t.Fatalf("clients now on %q, want the switched one back on work", got)
	}
	eventually(t, "the plain terminal back in its shell", func() bool {
		return strings.Contains(tm(t, plain, "capture-pane", "-p"), "back-in-the-shell")
	})
	// hq's session and its panes are untouched.
	if got := framed(t, socket, d.Window); got != d.List+"\n"+d.Slot {
		t.Fatalf("dashboard panes %q", got)
	}
}

// geometry is each dashboard pane's role (the slot's is blank or slot),
// left edge, top and width.
func geometry(t *testing.T, socket, win string) []string {
	t.Helper()
	return strings.Split(tm(t, socket, "list-panes", "-t", win, "-F", "#{@hq_role}:#{pane_left},#{pane_top},#{pane_width}"), "\n")
}

func TestTheSlotSitsInADarkerAreaFramedByTheSurround(t *testing.T) {
	c, socket := dashClient(t)
	dir := t.TempDir()
	d, err := c.Dashboard(dir, listStub)
	if err != nil {
		t.Fatal(err)
	}
	w, _ := strconv.Atoi(tm(t, socket, "display-message", "-p", "-t", d.Window, "#{window_width}"))
	top := tm(t, socket, "display-message", "-p", "-t", d.Slot, "#{pane_top}")
	// The list from the window's first row across its width; below it the
	// slot, one column of margin and one of border each side.
	want := []string{
		"list:0,0," + strconv.Itoa(w),
		"spacer:0," + top + ",1",
		"slot:2," + top + "," + strconv.Itoa(w-4),
		"spacer:" + strconv.Itoa(w-1) + "," + top + ",1",
	}
	if got := geometry(t, socket, d.Window); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("panes %v, want %v", got, want)
	}
	// The title row is the list's bottom status, as wide as the slot.
	if got := tm(t, socket, "show-options", "-wv", "-t", d.Window, "pane-border-status"); got != "bottom" {
		t.Errorf("pane-border-status %q", got)
	}
	// The slot's colour is the window's, whatever pane is docked; the
	// list and the margins have the surround's; borders are invisible.
	for opt, want := range map[string]string{"window-style": "bg=#111317", "window-active-style": "bg=#111317",
		"pane-border-style": "fg=#16181d,bg=#16181d", "pane-active-border-style": "fg=#16181d,bg=#16181d"} {
		if got := tm(t, socket, "show-options", "-wv", "-t", d.Window, opt); !strings.EqualFold(got, want) {
			t.Errorf("%s = %q, want %q", opt, got, want)
		}
	}
	for _, l := range strings.Split(tm(t, socket, "list-panes", "-t", d.Window, "-F", "#{pane_id} #{@hq_role}"), "\n") {
		id, role, _ := strings.Cut(l, " ")
		got := tm(t, socket, "show-options", "-pv", "-t", id, "window-style")
		if want := "bg=#16181d"; role == "slot" && got != "" || role != "slot" && !strings.EqualFold(got, want) {
			t.Errorf("%s pane's window-style %q", role, got)
		}
	}
	if got := tm(t, socket, "show-options", "-v", "-t", Session, "status-style"); !strings.Contains(strings.ToLower(got), "bg=#16181d") {
		t.Errorf("status-style %q", got)
	}

	// A docked agent takes the slot's place between the margins.
	aw, ap := agentWindow(t, c, "a", dir, "sleep", "30")
	if err := c.Dock(aw, "a"); err != nil {
		t.Fatal(err)
	}
	if got := tm(t, socket, "display-message", "-p", "-t", ap, "#{pane_left} #{pane_width}"); got != "2 "+strconv.Itoa(w-4) {
		t.Errorf("docked pane at %q", got)
	}
	if got := framed(t, socket, d.Window); got != d.List+"\n"+ap {
		t.Errorf("dashboard panes %q", got)
	}

	// A wider terminal widens the slot, the margins set back to one column.
	tm(t, socket, "resize-window", "-t", d.Window, "-x", strconv.Itoa(w+7))
	if err := c.KeepMargins(d.List); err != nil {
		t.Fatal(err)
	}
	if got := tm(t, socket, "display-message", "-p", "-t", ap, "#{pane_left} #{pane_width}"); got != "2 "+strconv.Itoa(w+3) {
		t.Errorf("after a resize the docked pane is at %q", got)
	}
}

func TestMissingMarginsOrListAreMadeAgainAroundTheSlot(t *testing.T) {
	c, socket := dashClient(t)
	dir := t.TempDir()
	d, err := c.Dashboard(dir, listStub)
	if err != nil {
		t.Fatal(err)
	}
	w, _ := strconv.Atoi(tm(t, socket, "display-message", "-p", "-t", d.Window, "#{window_width}"))
	wide := strconv.Itoa(w)
	for _, l := range strings.Split(tm(t, socket, "list-panes", "-t", d.Window, "-F", "#{pane_id} #{@hq_role}"), "\n") {
		if id, role, _ := strings.Cut(l, " "); role == roleSpacer {
			tm(t, socket, "kill-pane", "-t", id)
			break
		}
	}
	again, err := c.Dashboard(dir, listStub)
	if err != nil || again.Slot != d.Slot || again.List != d.List {
		t.Fatalf("%+v %v", again, err)
	}
	top := tm(t, socket, "display-message", "-p", "-t", d.Slot, "#{pane_top}")
	want := "list:0,0," + wide + " spacer:0," + top + ",1 slot:2," + top + "," + strconv.Itoa(w-4) + " spacer:" + strconv.Itoa(w-1) + "," + top + ",1"
	if got := strings.Join(geometry(t, socket, d.Window), " "); got != want {
		t.Fatalf("with a margin gone, made again as %s, want %s", got, want)
	}
	tm(t, socket, "kill-pane", "-t", d.List)
	again, err = c.Dashboard(dir, listStub)
	if err != nil || !again.Started {
		t.Fatalf("%+v %v", again, err)
	}
	if got := geometry(t, socket, d.Window)[0]; got != "list:0,0,"+wide {
		t.Fatalf("list made again at %s, want across the top", got)
	}
}

func TestAltLFromAnOlderHqIsBroughtUpToDate(t *testing.T) {
	c, socket := dashClient(t)
	if _, err := c.Dashboard(t.TempDir(), listStub); err != nil {
		t.Fatal(err)
	}
	// What an older hq bound, over the user's own binding of Alt+l.
	tm(t, socket, "bind-key", "-n", "M-l", "if-shell", "-F", "#{==:#{session_name},hq}", "select-pane -t :.+", `send-keys "user $HOME"`)
	if err := c.BindChords([]string{"true"}, "hints"); err != nil {
		t.Fatal(err)
	}
	cmd, ours := rootBinding(tm(t, socket, "list-keys", "-T", "root"), "M-l")
	if !ours || !strings.Contains(cmd, "{bottom}") || strings.Contains(cmd, ":.+") || !strings.Contains(cmd, "user") {
		t.Fatalf("Alt+l bound as %q", cmd)
	}
	// Bound once more, it stays as it is.
	if err := c.BindChords([]string{"true"}, "hints"); err != nil {
		t.Fatal(err)
	}
	if again, _ := rootBinding(tm(t, socket, "list-keys", "-T", "root"), "M-l"); again != cmd {
		t.Fatalf("Alt+l bound again as %q, was %q", again, cmd)
	}
}

// backgrounds is the background of each cell of each line of a capture
// with escape sequences (capture-pane -e -N), as the SGR parameters that
// set it; a line's sequences carry on from the line before.
func backgrounds(capture string) [][]string {
	var rows [][]string
	bg := ""
	for _, line := range strings.Split(capture, "\n") {
		rows = append(rows, lineBackgrounds(line, &bg))
	}
	return rows
}

func lineBackgrounds(line string, bg *string) []string {
	var bgs []string
	for i := 0; i < len(line); {
		if strings.HasPrefix(line[i:], "\x1b[") {
			end := strings.IndexByte(line[i:], 'm')
			ps := strings.Split(line[i+2:i+end], ";")
			for j := 0; j < len(ps); j++ {
				switch {
				case ps[j] == "" || ps[j] == "0" || ps[j] == "49":
					*bg = ""
				case (ps[j] == "38" || ps[j] == "48") && j+1 < len(ps) && ps[j+1] == "5":
					if ps[j] == "48" {
						*bg = strings.Join(ps[j:j+3], ";")
					}
					j += 2
				case (ps[j] == "38" || ps[j] == "48") && j+1 < len(ps) && ps[j+1] == "2":
					if ps[j] == "48" {
						*bg = strings.Join(ps[j:j+5], ";")
					}
					j += 4
				case len(ps[j]) == 2 && ps[j][0] == '4', len(ps[j]) == 3 && strings.HasPrefix(ps[j], "10"):
					*bg = ps[j]
				}
			}
			i += end + 1
			continue
		}
		_, size := utf8.DecodeRuneInString(line[i:])
		bgs = append(bgs, *bg)
		i += size
	}
	return bgs
}

func TestTheSlotAndItsTitleRowMakeOneDarkerAreaOnScreen(t *testing.T) {
	c, socket := dashClient(t)
	d, err := c.Dashboard(t.TempDir(), listStub)
	if err != nil {
		t.Fatal(err)
	}
	term := testutil.TmuxSocket(t)
	tm(t, term, "new-session", "-d", "-x", "60", "-y", "20", "env", "-u", "TMUX", "tmux", "-L", socket, "attach", "-t", Session)
	eventually(t, "the client to attach", func() bool {
		return strings.Contains(tm(t, term, "capture-pane", "-p"), "▸ placeholder")
	})
	capture := tm(t, term, "capture-pane", "-p", "-e", "-N")
	rows := backgrounds(capture)
	top, _ := strconv.Atoi(tm(t, socket, "display-message", "-p", "-t", d.Slot, "#{pane_top}"))
	list, title, slot := rows[0], rows[top-1], rows[top]
	if len(title) < 60 || len(slot) < 60 {
		t.Fatalf("rows of %d and %d cells:\n%s", len(title), len(slot), capture)
	}
	surround, dark := list[0], slot[2]
	if surround == dark {
		t.Fatalf("the slot has the list's background %q", dark)
	}
	// Two columns of surround each side; between them, the title row and
	// the slot's rows alike.
	for x := 0; x < 60; x++ {
		want := dark
		if x < 2 || x >= 58 {
			want = surround
		}
		if title[x] != want || slot[x] != want {
			t.Errorf("column %d: title row %q, slot %q, want %q", x, title[x], slot[x], want)
		}
	}
}
