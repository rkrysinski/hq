// Package dialog is hq's dialogs: short-lived programs in a tmux popup
// centered over the dashboard, each performing its own action (spec §6.6,
// design §3.8). They know nothing of tmux or sbx; the action they are given
// reaches them.
package dialog

import (
	"errors"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Width and Height are the popup's size with its frame, which tmux draws
// (and shrinks to fit a smaller window).
const (
	Width  = 76
	Height = 20
)

// The New agent dialog's fields, in Tab order.
const (
	Name = iota
	Dir
	Prompt
)

// FieldError is a failed start that belongs under one field (a duplicate
// name, a dir that is not a repository); the dialog stays open showing it.
type FieldError struct {
	Field int
	Err   error
}

func (e FieldError) Error() string { return e.Err.Error() }
func (e FieldError) Unwrap() error { return e.Err }

// Start starts an agent as hq new does.
type Start func(name, dir, prompt string) error

// Focus positions after the fields.
const (
	startButton = Prompt + 1 + iota
	cancelButton
)

var labels = []string{"name", "dir", "prompt"}

// hintText is the line under the fields (mock dash-s2-new).
const hintText = "dir defaults to the cursor row's repo · Tab next field · Esc cancel"

type startedMsg struct{ err error }

// NewAgent is the New agent dialog (spec §6.6): name, dir prefilled, prompt;
// Start or Cancel.
type NewAgent struct {
	start  Start
	inputs []textinput.Model
	errs   []string // under each field
	err    string   // a failure of no field
	focus  int      // a field, startButton or cancelButton
	busy   bool     // the agent is starting
	width  int
	// Started is true once the agent started; false when the dialog was
	// cancelled.
	Started bool
}

// NewAgentDialog is the dialog with dir prefilled.
func NewAgentDialog(dir string, start Start) NewAgent {
	m := NewAgent{start: start, width: Width - 2, errs: make([]string, len(labels))}
	for range labels {
		in := textinput.New()
		in.Prompt = ""
		in.TextStyle = cText
		in.Cursor.Style = cText
		in.CharLimit = 1000
		m.inputs = append(m.inputs, in)
	}
	m.inputs[Dir].SetValue(dir)
	m.setFocus(Name)
	return m
}

// setFocus moves the focus to a field or a button.
func (m *NewAgent) setFocus(i int) tea.Cmd {
	m.focus = i
	var cmd tea.Cmd
	for f := range m.inputs {
		if f == i {
			cmd = m.inputs[f].Focus()
		} else {
			m.inputs[f].Blur()
		}
	}
	return cmd
}

func (m NewAgent) Init() tea.Cmd { return textinput.Blink }

func (m NewAgent) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	case startedMsg:
		m.busy = false
		var fe FieldError
		switch {
		case msg.err == nil:
			m.Started = true
			return m, tea.Quit
		case errors.As(msg.err, &fe):
			m.errs[fe.Field] = fe.Err.Error()
			return m, m.setFocus(fe.Field)
		default:
			m.err = msg.err.Error()
			return m, nil
		}
	case tea.MouseMsg:
		if m.busy || !leftPress(msg) {
			return m, nil
		}
		return m.click(msg.X, msg.Y)
	case tea.KeyMsg:
		if m.busy {
			return m, nil
		}
		switch msg.String() {
		case "esc", "ctrl+c":
			return m, tea.Quit
		case "enter":
			if m.focus == cancelButton {
				return m, tea.Quit
			}
			return m.submit()
		case "tab", "down":
			return m, m.setFocus((m.focus + 1) % (cancelButton + 1))
		case "shift+tab", "up":
			return m, m.setFocus((m.focus + cancelButton) % (cancelButton + 1))
		case "left", "right":
			if m.focus >= startButton {
				return m, m.setFocus(startButton + cancelButton - m.focus)
			}
		}
	}
	if m.focus < startButton {
		var cmd tea.Cmd
		m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
		return m, cmd
	}
	return m, nil
}

// click is a left click at x, y (spec §6.6): × and Cancel close the
// dialog, Start submits it, a field takes the focus; anywhere else does
// nothing.
func (m NewAgent) click(x, y int) (tea.Model, tea.Cmd) {
	w := m.w()
	lines, tops := m.layout()
	switch {
	case onClose(x, y, w):
		return m, tea.Quit
	case y == len(lines)-1:
		switch buttonAt(x, w, "Start ⏎", "Cancel") {
		case 0:
			return m.submit()
		case 1:
			return m, tea.Quit
		}
		return m, nil
	}
	for i, top := range tops {
		if y >= top && y < top+3 && x < w {
			return m, m.setFocus(i)
		}
	}
	return m, nil
}

// submit starts the agent; the dialog waits for the outcome.
func (m NewAgent) submit() (tea.Model, tea.Cmd) {
	for i := range m.errs {
		m.errs[i] = ""
	}
	m.err = ""
	name := strings.TrimSpace(m.inputs[Name].Value())
	if name == "" {
		m.errs[Name] = "a name is needed"
		return m, m.setFocus(Name)
	}
	m.busy = true
	dir, prompt, start := strings.TrimSpace(m.inputs[Dir].Value()), m.inputs[Prompt].Value(), m.start
	return m, func() tea.Msg { return startedMsg{start(name, dir, prompt)} }
}

// Colours of the mocks.
var (
	cTitle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#F3F4F6"))
	cText    = lipgloss.NewStyle().Foreground(lipgloss.Color("#D1D5DB"))
	cDim     = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))
	cErr     = lipgloss.NewStyle().Foreground(lipgloss.Color("#F87171"))
	cAccent  = lipgloss.Color("#A78BFA")
	cLine    = lipgloss.Color("#3F4451")
	cDefault = lipgloss.NewStyle().Background(cAccent).Foreground(lipgloss.Color("#111318")).Bold(true).Padding(0, 2)
	cButton  = lipgloss.NewStyle().Background(lipgloss.Color("#2A2E37")).Foreground(lipgloss.Color("#D1D5DB")).Padding(0, 2)
)

// labelWidth is the width of the field labels with the margin before them.
const labelWidth = 9

// w is the width the dialog draws in.
func (m NewAgent) w() int { return max(m.width, 30) }

func (m NewAgent) View() string {
	lines, _ := m.layout()
	return strings.Join(lines, "\n")
}

// layout is the dialog's lines and the line each field's box starts on.
func (m NewAgent) layout() ([]string, []int) {
	w := m.w()
	boxW := w - labelWidth - 1
	lines := append(titleLines("New agent", w), "")
	var tops []int
	for i, in := range m.inputs {
		tops = append(tops, len(lines))
		border := cLine
		if i == m.focus {
			border = cAccent
		}
		in.Width = boxW - 5
		box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).Padding(0, 1).Width(boxW - 2).Render(in.View())
		label := cDim.Render(" " + labels[i])
		lines = append(lines, strings.Split(lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(labelWidth).Render("\n"+label), box), "\n")...)
		if m.errs[i] != "" {
			lines = append(lines, wrap(m.errs[i], labelWidth+1, boxW)...)
		}
	}
	if m.busy {
		lines = append(lines, " "+cDim.Render(ansi.Truncate("starting "+strings.TrimSpace(m.inputs[Name].Value())+"…", w-2, "…")))
	} else {
		lines = append(lines, " "+cDim.Render(ansi.Truncate(hintText, w-2, "…")))
	}
	if m.err != "" {
		lines = append(lines, wrap(m.err, 1, w-2)...)
	}
	start, cancel := cButton, cButton
	if m.focus == cancelButton {
		cancel = cDefault
	} else {
		start = cDefault
	}
	lines = append(lines, "", buttons(w, start.Render("Start ⏎"), cancel.Render("Cancel")))
	return lines, tops
}

// buttonGap is the space between a dialog's two buttons.
const buttonGap = "   "

// buttons are a dialog's two buttons, centered on its last line.
func buttons(w int, first, second string) string {
	return centered(first+buttonGap+second, w)
}

// buttonAt is which of the two buttons labelled first and second (0 or 1)
// column x of the buttons line falls on; -1 for neither.
func buttonAt(x, w int, first, second string) int {
	// Each button pads its label by two cells a side.
	fw, sw := ansi.StringWidth(first)+4, ansi.StringWidth(second)+4
	at := max(0, (w-fw-len(buttonGap)-sw)/2)
	switch {
	case x >= at && x < at+fw:
		return 0
	case x >= at+fw+len(buttonGap) && x < at+fw+len(buttonGap)+sw:
		return 1
	}
	return -1
}

// leftPress is a click of the left button; other buttons, releases and
// motion do nothing in a dialog.
func leftPress(msg tea.MouseMsg) bool {
	return msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft
}

// wrap renders an error over lines of width w, indented by indent.
func wrap(s string, indent, w int) []string {
	var out []string
	for _, l := range strings.Split(lipgloss.NewStyle().Width(w).Render(s), "\n") {
		out = append(out, strings.Repeat(" ", indent)+cErr.Render(strings.TrimRight(l, " ")))
	}
	return out
}

// Run runs a dialog on the terminal until it closes; it takes the mouse
// (spec §6.6).
func Run(m tea.Model) error {
	_, err := tea.NewProgram(m, tea.WithMouseCellMotion()).Run()
	return err
}
