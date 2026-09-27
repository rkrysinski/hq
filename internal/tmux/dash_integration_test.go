//go:build integration

package tmux

import (
	"os/exec"
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
