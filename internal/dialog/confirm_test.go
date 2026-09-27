package dialog

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// kills counts the kills a dialog runs, answering with err.
type kills struct {
	n   int
	err error
}

func (k *kills) kill() error { k.n++; return k.err }

// answer feeds keys to a yes/no dialog, running its action; it reports
// whether the dialog closed.
func answer(m Confirm, keys ...string) (Confirm, bool) {
	quit := false
	for _, k := range keys {
		var msg tea.Msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		if t, ok := map[string]tea.KeyType{"tab": tea.KeyTab, "enter": tea.KeyEnter, "esc": tea.KeyEsc,
			"left": tea.KeyLeft, "right": tea.KeyRight}[k]; ok {
			msg = tea.KeyMsg{Type: t}
		}
		for msg != nil {
			tm, cmd := m.Update(msg)
			m, msg = tm.(Confirm), nil
			if cmd != nil {
				msg = cmd()
			}
			if _, ok := msg.(tea.QuitMsg); ok {
				quit, msg = true, nil
			}
		}
	}
	return m, quit
}

func TestTheKillDialogLooksLikeTheMock(t *testing.T) {
	k := &kills{}
	m := KillDialog("bok-17", "fix/17-release-notes", k.kill)
	if m.Init() != nil {
		t.Fatal("Init does something")
	}
	v := ansi.Strip(m.View())
	ls := strings.Split(v, "\n")
	if !strings.Contains(ls[0], "Kill agent") || !strings.HasSuffix(ls[0], "×") {
		t.Errorf("title line %q", ls[0])
	}
	for _, want := range []string{"Kill bok-17 (fix/17-release-notes)?", "Ends the Claude session; the sandbox stays.", "No ⏎", "Yes"} {
		if !strings.Contains(v, want) {
			t.Errorf("no %q in\n%s", want, v)
		}
	}
	if len(ls) > ConfirmHeight-2 {
		t.Errorf("%d lines, the popup holds %d:\n%s", len(ls), ConfirmHeight-2, v)
	}
	for _, l := range ls {
		if w := ansi.StringWidth(l); w > ConfirmWidth-2 {
			t.Errorf("line %d wide: %q", w, l)
		}
	}
}

func TestNoIsTheDefault(t *testing.T) {
	for _, keys := range [][]string{{"enter"}, {"n"}, {"esc"}, {"tab", "tab", "enter"}, {"right", "left", "enter"}} {
		k := &kills{}
		m, quit := answer(KillDialog("a", "main", k.kill), keys...)
		if !quit || m.Done || k.n != 0 {
			t.Errorf("%q: closed %v, done %v, kills %d", keys, quit, m.Done, k.n)
		}
	}
}

func TestYesKills(t *testing.T) {
	for _, keys := range [][]string{{"y"}, {"Y"}, {"tab", "enter"}, {"right", "enter"}} {
		k := &kills{}
		m, quit := answer(KillDialog("a", "main", k.kill), keys...)
		if !quit || !m.Done || k.n != 1 {
			t.Errorf("%q: closed %v, done %v, kills %d", keys, quit, m.Done, k.n)
		}
	}
}

func TestAFailedKillIsShownAndTheDialogStays(t *testing.T) {
	k := &kills{err: errors.New("tmux: server exited")}
	m, quit := answer(KillDialog("a", "main", k.kill), "y")
	if quit || m.Done || !strings.Contains(ansi.Strip(m.View()), "tmux: server exited") {
		t.Fatalf("closed %v, done %v:\n%s", quit, m.Done, ansi.Strip(m.View()))
	}
	// Another try clears it.
	k.err = nil
	if m, quit = answer(m, "y"); !quit || !m.Done || strings.Contains(ansi.Strip(m.View()), "server exited") {
		t.Fatalf("closed %v, done %v", quit, m.Done)
	}
}

func TestWhileKillingKeysAreIgnored(t *testing.T) {
	k := &kills{}
	tm, _ := KillDialog("a", "main", k.kill).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")}) // the kill runs
	m := tm.(Confirm)
	if !strings.Contains(ansi.Strip(m.View()), "killing a…") {
		t.Fatalf("view:\n%s", ansi.Strip(m.View()))
	}
	if m2, quit := answer(m, "esc"); quit || !m2.busy {
		t.Fatal("esc closed the dialog while killing")
	}
}

func TestTheYesNoDialogFillsThePopupsWidth(t *testing.T) {
	tm, _ := KillDialog(strings.Repeat("x", 80), "main", (&kills{}).kill).Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	for _, l := range strings.Split(ansi.Strip(tm.View()), "\n") {
		if w := ansi.StringWidth(l); w > 40 {
			t.Fatalf("line %d wide: %q", w, l)
		}
	}
}
