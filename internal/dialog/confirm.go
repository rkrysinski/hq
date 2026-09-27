package dialog

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// ConfirmWidth and ConfirmHeight are a yes/no dialog's popup size with its
// frame.
const (
	ConfirmWidth  = 60
	ConfirmHeight = 10
)

// Action is what a yes/no dialog does on Yes.
type Action func() error

type doneMsg struct{ err error }

// Confirm is a yes/no dialog (spec §6.6): a question, a note under it, `No ⏎`
// (the default) and `Yes`. y and n answer too.
type Confirm struct {
	title, question, note string
	busyText              string // shown while the action runs
	action                Action
	yes                   bool // Yes has the focus
	busy                  bool
	err                   string
	width                 int
	// Done is true once the action succeeded; false when the answer was no.
	Done bool
}

// KillDialog asks `Kill NAME (branch)?` and runs kill on Yes.
func KillDialog(name, branch string, kill Action) Confirm {
	return Confirm{
		title:    "Kill agent",
		question: "Kill " + name + " (" + branch + ")?",
		note:     "Ends the Claude session; the sandbox stays.",
		busyText: "killing " + name + "…",
		action:   kill,
		width:    ConfirmWidth - 2,
	}
}

func (m Confirm) Init() tea.Cmd { return nil }

func (m Confirm) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case doneMsg:
		m.busy = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.Done = true
		return m, tea.Quit
	case tea.KeyMsg:
		if m.busy {
			return m, nil
		}
		switch msg.String() {
		case "esc", "ctrl+c", "n", "N":
			return m, tea.Quit
		case "y", "Y":
			return m.run()
		case "enter":
			if m.yes {
				return m.run()
			}
			return m, tea.Quit
		case "tab", "shift+tab", "left", "right":
			m.yes = !m.yes
		}
	}
	return m, nil
}

// run runs the action; the dialog waits for the outcome.
func (m Confirm) run() (tea.Model, tea.Cmd) {
	m.busy, m.err = true, ""
	action := m.action
	return m, func() tea.Msg { return doneMsg{action()} }
}

func (m Confirm) View() string {
	w := max(m.width, 30)
	lines := append(titleLines(m.title, w), "",
		centered(cText.Render(ansi.Truncate(m.question, w-2, "…")), w), "")
	// The line under the question: the note, what runs, or why it failed.
	switch {
	case m.busy:
		lines = append(lines, centered(cDim.Render(ansi.Truncate(m.busyText, w-2, "…")), w))
	case m.err != "":
		lines = append(lines, centered(cErr.Render(ansi.Truncate(m.err, w-2, "…")), w))
	default:
		lines = append(lines, centered(cDim.Render(ansi.Truncate(m.note, w-2, "…")), w))
	}
	no, yes := cDefault, cButton
	if m.yes {
		no, yes = cButton, cDefault
	}
	lines = append(lines, "", centered(no.Render("No ⏎")+"   "+yes.Render("Yes"), w))
	return strings.Join(lines, "\n")
}

// titleLines are a dialog's first lines: the title centered with × at the
// right, and a rule under it.
func titleLines(title string, w int) []string {
	left := (w - ansi.StringWidth(title)) / 2
	return []string{
		strings.Repeat(" ", left) + cTitle.Render(title) + strings.Repeat(" ", max(1, w-left-ansi.StringWidth(title)-2)) + cDim.Render("×"),
		" " + cDim.Render(strings.Repeat("─", w-2)),
	}
}

// centered centers a rendered line in w cells.
func centered(s string, w int) string {
	return strings.Repeat(" ", max(0, (w-lipgloss.Width(s))/2)) + s
}
