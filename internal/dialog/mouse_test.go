package dialog

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// clickMsg is a left click at x, y.
func clickMsg(x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
}

// clicked feeds a click to the New agent dialog, running what it starts;
// it reports whether the dialog closed.
func clicked(m NewAgent, x, y int) (NewAgent, bool) {
	var msg tea.Msg = clickMsg(x, y)
	for msg != nil {
		tm, cmd := m.Update(msg)
		m, msg = tm.(NewAgent), result(cmd)
		if _, ok := msg.(tea.QuitMsg); ok {
			return m, true
		}
	}
	return m, false
}

// find is the line and column of text in a dialog's view; the line is the
// last one holding it.
func find(t *testing.T, v, text string) (x, y int) {
	t.Helper()
	ls := strings.Split(ansi.Strip(v), "\n")
	for y := len(ls) - 1; y >= 0; y-- {
		if i := strings.Index(ls[y], text); i >= 0 {
			return ansi.StringWidth(ls[y][:i]), y
		}
	}
	t.Fatalf("no %q in\n%s", text, ansi.Strip(v))
	return 0, 0
}

func TestAClickOnAFieldFocusesIt(t *testing.T) {
	m := NewAgentDialog("~/w/app", (&starts{}).start)
	// The prompt box's top and bottom lines, its label and its inside.
	_, y := find(t, m.View(), "prompt")
	for _, at := range [][2]int{{30, y - 1}, {2, y}, {40, y}, {30, y + 1}} {
		m.setFocus(Name)
		if m, _ = clicked(m, at[0], at[1]); m.focus != Prompt {
			t.Errorf("click at %v: focus %d", at, m.focus)
		}
	}
	// Typing goes to the field clicked.
	m, _ = clicked(m, 30, y-4)
	m, _ = press(m, "x")
	if m.focus != Dir || m.inputs[Dir].Value() != "~/w/appx" {
		t.Fatalf("focus %d, dir %q", m.focus, m.inputs[Dir].Value())
	}
}

func TestAClickOnStartStartsAndOnCancelOrTheCrossCloses(t *testing.T) {
	s := &starts{}
	m, _ := press(NewAgentDialog("~/w/app", s.start), "a", "b")
	x, y := find(t, m.View(), "Start ⏎")
	if _, quit := clicked(m, x-2, y); !quit || strings.Join(s.calls, ",") != "ab|~/w/app|" {
		t.Fatalf("Start: quit %v, started %v", quit, s.calls)
	}
	s.calls = nil
	x, y = find(t, m.View(), "Cancel")
	if _, quit := clicked(m, x+len("Cancel")+1, y); !quit || len(s.calls) != 0 {
		t.Fatalf("Cancel: quit %v, started %v", quit, s.calls)
	}
	x, _ = find(t, m.View(), "×")
	if _, quit := clicked(m, x, 0); !quit || len(s.calls) != 0 {
		t.Fatalf("×: quit %v, started %v", quit, s.calls)
	}
}

func TestClicksElsewhereInTheDialogDoNothing(t *testing.T) {
	s := &starts{}
	m, _ := press(NewAgentDialog("~/w/app", s.start), "a")
	x, y := find(t, m.View(), "Start ⏎")
	_, hint := find(t, m.View(), hintText)
	for _, at := range [][2]int{{x - 4, y}, {x + ansi.StringWidth("Start ⏎") + 3, y}, {10, 0}, {10, 1}, {10, hint}, {x, y - 1}} {
		if n, quit := clicked(m, at[0], at[1]); quit || n.focus != Name || len(s.calls) != 0 {
			t.Errorf("click at %v: quit %v, focus %d, started %v", at, quit, n.focus, s.calls)
		}
	}
	// Nor do the other buttons, or a click while the agent starts.
	tm, _ := m.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonRight})
	if len(s.calls) != 0 || tm.(NewAgent).busy {
		t.Fatalf("right click started %v", s.calls)
	}
	m.busy = true
	if _, quit := clicked(m, x, y); quit || len(s.calls) != 0 {
		t.Fatalf("while starting: quit %v, started %v", quit, s.calls)
	}
}

// clickedConfirm feeds a click to a yes/no dialog, running its action.
func clickedConfirm(m Confirm, x, y int) (Confirm, bool) {
	var msg tea.Msg = clickMsg(x, y)
	for msg != nil {
		tm, cmd := m.Update(msg)
		m, msg = tm.(Confirm), nil
		if cmd != nil {
			msg = cmd()
		}
		if _, ok := msg.(tea.QuitMsg); ok {
			return m, true
		}
	}
	return m, false
}

func TestTheKillDialogsButtonsAndCrossAreClickable(t *testing.T) {
	k := &kills{}
	m := KillDialog("bok-17", "fix/17", k.kill)
	x, y := find(t, m.View(), "No ⏎")
	if _, quit := clickedConfirm(m, x, y); !quit || k.n != 0 {
		t.Fatalf("No: quit %v, kills %d", quit, k.n)
	}
	x, _ = find(t, m.View(), "×")
	if _, quit := clickedConfirm(m, x+1, 0); !quit || k.n != 0 {
		t.Fatalf("×: quit %v, kills %d", quit, k.n)
	}
	for _, at := range [][2]int{{10, 0}, {10, 2}, {2, y}} {
		if _, quit := clickedConfirm(m, at[0], at[1]); quit || k.n != 0 {
			t.Fatalf("click at %v: quit %v, kills %d", at, quit, k.n)
		}
	}
	x, y = find(t, m.View(), "Yes")
	if _, quit := clickedConfirm(m, x+len("Yes")+1, y); !quit || k.n != 1 {
		t.Fatalf("Yes: quit %v, kills %d", quit, k.n)
	}
	// While killing, clicks are ignored.
	m.busy = true
	if _, quit := clickedConfirm(m, x, y); quit || k.n != 1 {
		t.Fatalf("while killing: quit %v, kills %d", quit, k.n)
	}
}
