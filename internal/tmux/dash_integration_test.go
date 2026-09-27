//go:build integration

package tmux

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
	if got := tm(t, socket, "list-panes", "-t", d.Window, "-F", "#{pane_id}"); got != d.List+"\n"+d.Slot {
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
	if got := tm(t, socket, "display-message", "-p", "-t", d.Slot, "#{T:pane-border-format}"); !strings.Contains(got, "▸ placeholder shell") {
		t.Errorf("slot frame %q", got)
	}
	if got := tm(t, socket, "display-message", "-p", "-t", d.List, "#{T:pane-border-format}"); strings.Contains(got, "▸") {
		t.Errorf("list pane titled %q", got)
	}
	for opt, want := range map[string]string{"set-titles": "on", "set-titles-string": "hq"} {
		if got := tm(t, socket, "show-options", "-v", "-t", Session, opt); got != want {
			t.Errorf("%s = %q, want %q", opt, got, want)
		}
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
	if got := tm(t, socket, "list-panes", "-t", d.Window, "-F", "#{pane_id}"); got != d.List+"\n"+d.Slot {
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

func TestSessionFromBeforeTheDashboardKeepsItsShellAsTheSlot(t *testing.T) {
	c, socket := dashClient(t)
	tm(t, socket, "new-session", "-d", "-s", Session, "-n", Session, "sh")
	old := tm(t, socket, "display-message", "-p", "-t", Session+":", "#{pane_id}")
	d, err := c.Dashboard(t.TempDir(), listStub)
	if err != nil || d.Slot != old || !d.Started {
		t.Fatalf("%+v %v, want the old pane %s as the slot", d, err, old)
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
	if n := tm(t, socket, "display-message", "-p", "-t", d.Window, "#{window_name} #{window_panes}"); n != "hq 2" {
		t.Fatalf("dashboard window %q", n)
	}
}

func TestListPaneRespawnFitAndFooter(t *testing.T) {
	c, socket := dashClient(t)
	d, err := c.Dashboard(t.TempDir(), listStub)
	if err != nil {
		t.Fatal(err)
	}
	if h, err := c.WindowHeight(d.List); err != nil || h < 20 {
		t.Fatalf("window height %d %v", h, err)
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
	if got := tm(t, socket, "display-message", "-p", "-t", ap, "#{T:pane-border-format}"); !strings.Contains(got, "▸ a · main · claude-x") {
		t.Errorf("frame %q", got)
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
	if got := tm(t, socket, "list-panes", "-t", d.Window, "-F", "#{pane_id}"); got != d.List+"\n"+d.Slot {
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
	tm(t, term, "send-keys", "M-j")
	tm(t, term, "send-keys", "M-k")
	eventually(t, "the chord commands", func() bool { return logged() == "new next "+socket+"\nnew previous "+socket })

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
