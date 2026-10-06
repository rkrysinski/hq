package tmux

import (
	"errors"
	"testing"
)

// broken is a tmux that fails every command, as one whose server went away.
type broken struct{}

func (broken) Run(string, ...string) ([]byte, error) { return nil, errors.New("tmux: lost server") }

// What hq asks of tmux fails when tmux fails: no command goes on as if it
// had worked (#42).
func TestEveryCommandReportsTmuxsFailure(t *testing.T) {
	c := Client{Run: broken{}}
	calls := map[string]func() error{
		"Version":         func() error { _, err := c.Version(); return err },
		"NewWindow":       func() error { _, err := c.NewWindow("a", "/w", nil, []string{"true"}); return err },
		"SetOption":       func() error { return c.SetOption("@1", "new", "1") },
		"KeepFirst":       func() error { _, err := c.KeepFirst("@1", "turnend", "1", "1 x"); return err },
		"Screens":         func() error { _, err := c.Screens([]string{"%1"}); return err },
		"Paste":           func() error { return c.Paste("%1", "hi") },
		"StyledScreen":    func() error { _, err := c.StyledScreen("%1"); return err },
		"Submit":          func() error { return c.Submit("%1") },
		"Start":           func() error { return c.Start("@1") },
		"Respawn":         func() error { return c.Respawn("%1", "/w", []string{"true"}) },
		"KillWindow":      func() error { return c.KillWindow("@1") },
		"SocketPath":      func() error { _, err := c.SocketPath(); return err },
		"Enter":           func() error { return c.Enter("@1") },
		"ShowAttached":    func() error { _, err := c.ShowAttached("@1"); return err },
		"Leave":           func() error { return c.Leave("bye") },
		"Attach":          func() error { return c.Attach("@1", &term{}) },
		"LastScreen":      func() error { _, err := c.LastScreen("%1"); return err },
		"EnsureSession":   func() error { return c.EnsureSession("/w") },
		"BindChords":      func() error { return c.BindChords([]string{"hq"}, "hints") },
		"Message":         func() error { return c.Message("hi") },
		"Dashboard":       func() error { _, err := c.Dashboard("/w", []string{"hq"}); return err },
		"FocusList":       func() error { return c.FocusList() },
		"RespawnList":     func() error { return c.RespawnList("%1", []string{"hq"}) },
		"TerminalHeight":  func() error { _, err := c.TerminalHeight("%1"); return err },
		"ResizeHeight":    func() error { return c.ResizeHeight("%1", 5) },
		"KeepMargins":     func() error { return c.KeepMargins("%1") },
		"SetFooter":       func() error { return c.SetFooter("keys") },
		"MarkList":        func() error { return c.MarkList("%1", 42) },
		"SessionValue":    func() error { _, err := c.SessionValue("filter"); return err },
		"SetSessionValue": func() error { return c.SetSessionValue("filter", "x") },
		"Dock":            func() error { return c.Dock("@1", "a") },
		"Show":            func() error { return c.Show("@1", "a") },
		"FitHomes":        func() error { return c.FitHomes() },
		"SetTitle":        func() error { return c.SetTitle("%1", "a") },
		"Popup":           func() error { return c.Popup("%1", "/w", 40, 8, []string{"true"}) },
	}
	for name, call := range calls {
		if err := call(); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if got := (CommandTooLong{Over: 3}).Error(); got != "tmux command 3 bytes too long" {
		t.Errorf("%q", got)
	}
}
