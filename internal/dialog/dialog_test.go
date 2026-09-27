package dialog

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// starts records the agents the dialog starts, answering with err.
type starts struct {
	calls []string
	err   error
}

func (s *starts) start(name, dir, prompt string) error {
	s.calls = append(s.calls, name+"|"+dir+"|"+prompt)
	return s.err
}

// press feeds keys to the dialog, running what it starts; it reports
// whether the dialog closed.
func press(m NewAgent, keys ...string) (NewAgent, bool) {
	quit := false
	for _, k := range keys {
		var msg tea.Msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		if t, ok := map[string]tea.KeyType{"tab": tea.KeyTab, "shift+tab": tea.KeyShiftTab, "enter": tea.KeyEnter,
			"esc": tea.KeyEsc, "left": tea.KeyLeft, "right": tea.KeyRight, "up": tea.KeyUp, "down": tea.KeyDown}[k]; ok {
			msg = tea.KeyMsg{Type: t}
		}
		for msg != nil {
			tm, cmd := m.Update(msg)
			m, msg = tm.(NewAgent), result(cmd)
			if _, ok := msg.(tea.QuitMsg); ok {
				quit, msg = true, nil
			}
		}
	}
	return m, quit
}

// result is what cmd answers at once: the outcome of a start or a quit; the
// cursor's blinking is not waited for.
func result(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		switch msg.(type) {
		case startedMsg, tea.QuitMsg:
			return msg
		}
	case <-time.After(20 * time.Millisecond):
	}
	return nil
}

func view(m NewAgent) string { return ansi.Strip(m.View()) }

func TestTheDialogLooksLikeTheMock(t *testing.T) {
	s := &starts{}
	v := view(NewAgentDialog("~/w/app", s.start))
	ls := strings.Split(v, "\n")
	if !strings.Contains(ls[0], "New agent") || !strings.HasSuffix(ls[0], "×") {
		t.Errorf("title line %q", ls[0])
	}
	for _, want := range []string{"name", "dir", "prompt", "~/w/app", hintText, "Start ⏎", "Cancel"} {
		if !strings.Contains(v, want) {
			t.Errorf("no %q in\n%s", want, v)
		}
	}
	if len(ls) > Height-2 {
		t.Errorf("%d lines, the popup holds %d:\n%s", len(ls), Height-2, v)
	}
	for _, l := range ls {
		if w := ansi.StringWidth(l); w > Width-2 {
			t.Errorf("line %d wide: %q", w, l)
		}
	}
}

func TestStartStartsTheAgentAndCloses(t *testing.T) {
	s := &starts{}
	m, quit := press(NewAgentDialog("/w/app", s.start), "a", "1", "tab", "tab", "s", "a", "y", " ", "h", "i", "enter")
	if !quit || !m.Started || strings.Join(s.calls, ",") != "a1|/w/app|say hi" {
		t.Fatalf("closed %v, started %v, calls %q", quit, m.Started, s.calls)
	}
}

func TestAnErrorOfAFieldIsShownUnderItAndTheDialogStays(t *testing.T) {
	s := &starts{err: FieldError{Field: Dir, Err: errors.New("/w/plain is not in a git repository")}}
	m, quit := press(NewAgentDialog("/w/plain", s.start), "a", "tab", "tab", "enter")
	if quit || m.focus != Dir {
		t.Fatalf("closed %v, focus %d", quit, m.focus)
	}
	ls := strings.Split(view(m), "\n")
	for i, l := range ls {
		if strings.Contains(l, "/w/plain is not in a git repository") {
			if !strings.Contains(ls[i-2], "/w/plain") || !strings.HasPrefix(l, strings.Repeat(" ", labelWidth)) {
				t.Fatalf("error not under dir:\n%s", view(m))
			}
			return
		}
	}
	t.Fatalf("no error:\n%s", view(m))
}

func TestAnErrorOfNoFieldIsShownAboveTheButtons(t *testing.T) {
	s := &starts{err: errors.New("sbx: daemon not running")}
	m, quit := press(NewAgentDialog("/w/app", s.start), "a", "enter")
	if quit || !strings.Contains(view(m), "sbx: daemon not running") {
		t.Fatalf("closed %v:\n%s", quit, view(m))
	}
	// A new try clears it.
	s.err = nil
	if m, quit = press(m, "enter"); !quit || strings.Contains(view(m), "daemon") {
		t.Fatalf("closed %v:\n%s", quit, view(m))
	}
}

func TestANameIsNeeded(t *testing.T) {
	s := &starts{}
	m, quit := press(NewAgentDialog("/w/app", s.start), "tab", "enter")
	if quit || len(s.calls) != 0 || m.focus != Name || !strings.Contains(view(m), "a name is needed") {
		t.Fatalf("closed %v, calls %q, focus %d", quit, s.calls, m.focus)
	}
}

func TestEscAndCancelCloseWithoutStarting(t *testing.T) {
	for _, keys := range [][]string{
		{"a", "esc"},
		{"a", "tab", "tab", "tab", "tab", "enter"}, // Tab to Cancel
		{"a", "shift+tab", "enter"},                // back from name to Cancel
		{"a", "up", "left", "right", "enter"},      // ←/→ move between the buttons
	} {
		s := &starts{}
		m, quit := press(NewAgentDialog("/w/app", s.start), keys...)
		if !quit || m.Started || len(s.calls) != 0 {
			t.Errorf("%q: closed %v, started %v, calls %q", keys, quit, m.Started, s.calls)
		}
	}
}

func TestTheFocusedButtonIsHighlighted(t *testing.T) {
	s := &starts{}
	m, _ := press(NewAgentDialog("/w/app", s.start), "tab", "tab", "tab", "right")
	if m.focus != cancelButton {
		t.Fatalf("focus %d", m.focus)
	}
	if m, _ = press(m, "left"); m.focus != startButton {
		t.Fatalf("focus %d", m.focus)
	}
	// Enter on Start starts.
	if _, quit := press(m, "enter"); quit {
		t.Fatal("closed without a name")
	}
}

func TestWhileStartingKeysAreIgnored(t *testing.T) {
	s := &starts{}
	m := NewAgentDialog("/w/app", s.start)
	m, _ = press(m, "a")
	tm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // the start runs
	m = tm.(NewAgent)
	if !m.busy || !strings.Contains(view(m), "starting a…") {
		t.Fatalf("busy %v:\n%s", m.busy, view(m))
	}
	if m2, quit := press(m, "esc"); quit || !m2.busy {
		t.Fatal("esc closed the dialog while starting")
	}
}

func TestTheDialogFillsThePopupsWidth(t *testing.T) {
	s := &starts{}
	tm, _ := NewAgentDialog("/w/app", s.start).Update(tea.WindowSizeMsg{Width: 60, Height: 18})
	for _, l := range strings.Split(view(tm.(NewAgent)), "\n") {
		if w := ansi.StringWidth(l); w > 60 {
			t.Fatalf("line %d wide: %q", w, l)
		}
	}
	if tm.(NewAgent).Init() == nil {
		t.Fatal("no cursor blink")
	}
}
