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

	"github.com/rkrysinski/hq/internal/prefs"
)

// Width and Height are the popup's size with its frame, which tmux draws
// (and shrinks to fit a smaller window); HeightWithSetups is the New agent
// dialog's with its setup field.
const (
	Width            = 76
	Height           = 20
	HeightWithSetups = Height + 3
)

// The New agent dialog's fields: its text fields, then the setup field,
// shown between dir and prompt when there are setups to pick (spec §6.6).
const (
	Name = iota
	Dir
	Prompt
	Setup
)

// FieldError is a failed start that belongs under one field (a duplicate
// name, a dir that is not a repository); the dialog stays open showing it.
type FieldError struct {
	Field int
	Err   error
}

func (e FieldError) Error() string { return e.Err.Error() }
func (e FieldError) Unwrap() error { return e.Err }

// Start starts an agent as hq new does, on the setup picked ("" when the
// dialog has no setup field: hq new's default).
type Start func(name, dir, prompt, setup string) error

// Sandbox is the sandbox an agent of the setup started in dir would run in,
// and whether it exists yet; "" when dir is no repository.
type Sandbox func(setup, dir string) (name string, exists bool)

// Focus positions after the fields.
const (
	startButton = Setup + 1 + iota
	cancelButton
)

var labels = []string{"name", "dir", "prompt"}

// hintText is the line under the fields (frame S2), hintSetups the line
// with the setup field (frame S2b).
const (
	hintText   = "dir defaults to the cursor row's repo · Tab next field · Esc cancel"
	hintSetups = "dir defaults to the cursor row's repo · ↑↓ setup · Tab next · Esc cancel"
)

type startedMsg struct{ err error }

// noteMsg is the sandbox an agent of setup started in dir would run in.
type noteMsg struct {
	setup, dir, name string
	exists           bool
}

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
	// setups are those to pick from, none without the setup field; chosen
	// is the one picked; note names the sandbox it runs in (sandbox).
	setups  []string
	chosen  int
	sandbox Sandbox
	note    string
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

// WithSetups is the dialog with a setup field picking among setups, the
// default chosen, its note asking sandbox; with fewer than two setups (only
// the default) it is the dialog as it is.
func (m NewAgent) WithSetups(setups []string, sandbox Sandbox) NewAgent {
	if len(setups) < 2 {
		return m
	}
	m.setups, m.sandbox = setups, sandbox
	for i, s := range setups {
		if s == prefs.DefaultSetup {
			m.chosen = i
		}
	}
	return m
}

// order is the focus order: the fields as shown, then the buttons.
func (m NewAgent) order() []int {
	if m.setups == nil {
		return []int{Name, Dir, Prompt, startButton, cancelButton}
	}
	return []int{Name, Dir, Setup, Prompt, startButton, cancelButton}
}

// step moves the focus d places along the focus order, round.
func (m *NewAgent) step(d int) tea.Cmd {
	o := m.order()
	for i, f := range o {
		if f == m.focus {
			return m.setFocus(o[(i+d+len(o))%len(o)])
		}
	}
	return nil
}

// choose picks the setup d places on, round, and asks for its sandbox.
func (m *NewAgent) choose(d int) tea.Cmd {
	m.chosen = (m.chosen + d + len(m.setups)) % len(m.setups)
	return m.askNote()
}

// askNote asks for the sandbox of the chosen setup and the dir typed; the
// answer is shown only while both are still those (sbx is slow to ask).
func (m NewAgent) askNote() tea.Cmd {
	if m.setups == nil || m.sandbox == nil {
		return nil
	}
	setup, dir, sandbox := m.setups[m.chosen], strings.TrimSpace(m.inputs[Dir].Value()), m.sandbox
	return func() tea.Msg {
		name, exists := sandbox(setup, dir)
		return noteMsg{setup: setup, dir: dir, name: name, exists: exists}
	}
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

func (m NewAgent) Init() tea.Cmd { return tea.Batch(textinput.Blink, m.askNote()) }

func (m NewAgent) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	case noteMsg:
		if m.setups != nil && msg.setup == m.setups[m.chosen] && msg.dir == strings.TrimSpace(m.inputs[Dir].Value()) {
			m.note = ""
			if msg.name != "" {
				m.note = "sandbox " + msg.name
				if !msg.exists {
					m.note += " (new)"
				}
			}
		}
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
		case "tab":
			return m, m.step(1)
		case "shift+tab":
			return m, m.step(-1)
		case "down", "up":
			d := keyStep[msg.String()]
			if m.focus == Setup {
				return m, m.choose(d)
			}
			return m, m.step(d)
		case "left", "right":
			d := keyStep[msg.String()]
			switch {
			case m.focus >= startButton:
				return m, m.setFocus(startButton + cancelButton - m.focus)
			case m.focus == Setup:
				return m, m.step(d)
			}
		}
	}
	if m.focus < len(m.inputs) {
		dir := m.inputs[Dir].Value()
		var cmd tea.Cmd
		m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
		if m.inputs[Dir].Value() != dir {
			return m, tea.Batch(cmd, m.askNote())
		}
		return m, cmd
	}
	return m, nil
}

// keyStep is how far an arrow key moves the focus or the setup chosen:
// down and right one on, up and left one back.
var keyStep = map[string]int{"down": 1, "right": 1, "up": -1, "left": -1}

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
	for f, top := range tops {
		if y >= top && y < top+3 && x < w {
			cmd := m.setFocus(f)
			if f == Setup && y == top+1 {
				if i := m.optionAt(x); i >= 0 && i != m.chosen {
					m.chosen = i
					return m, m.askNote()
				}
			}
			return m, cmd
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
	setup := ""
	if m.setups != nil {
		setup = m.setups[m.chosen]
	}
	return m, func() tea.Msg { return startedMsg{start(name, dir, prompt, setup)} }
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
func (m NewAgent) layout() ([]string, map[int]int) {
	w := m.w()
	boxW := w - labelWidth - 1
	lines := append(titleLines("New agent", w), "")
	tops := map[int]int{}
	for _, f := range m.order() {
		if f >= startButton {
			break
		}
		tops[f] = len(lines)
		border := cLine
		if f == m.focus {
			border = cAccent
		}
		var content, label string
		if f == Setup {
			content, label = m.setupLine(boxW-4), "setup"
		} else {
			in := m.inputs[f]
			in.Width = boxW - 5
			content, label = in.View(), labels[f]
		}
		box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).Padding(0, 1).Width(boxW - 2).Render(content)
		lines = append(lines, strings.Split(lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(labelWidth).Render("\n"+cDim.Render(" "+label)), box), "\n")...)
		if f < len(m.errs) && m.errs[f] != "" {
			lines = append(lines, wrap(m.errs[f], labelWidth+1, boxW)...)
		}
	}
	hint := hintText
	if m.setups != nil {
		hint = hintSetups
	}
	if m.busy {
		lines = append(lines, " "+cDim.Render(ansi.Truncate("starting "+strings.TrimSpace(m.inputs[Name].Value())+"…", w-2, "…")))
	} else {
		lines = append(lines, " "+cDim.Render(ansi.Truncate(hint, w-2, "…")))
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

// optionGap is the space between two setups of the setup field.
const optionGap = "  "

// setupLine is the inside of the setup field, w cells: the setups, the one
// chosen marked, and at the right the note naming its sandbox (frame S2b).
func (m NewAgent) setupLine(w int) string {
	var opts []string
	for i, s := range m.setups {
		if i == m.chosen {
			opts = append(opts, lipgloss.NewStyle().Foreground(cAccent).Render("●")+" "+cText.Render(s))
		} else {
			opts = append(opts, cDim.Render("○ "+s))
		}
	}
	line := ansi.Truncate(strings.Join(opts, optionGap), w, "…")
	room := w - ansi.StringWidth(line) - len(optionGap)
	if m.note == "" || room < len("sandbox …") {
		return line
	}
	note := ansi.Truncate(m.note, room, "…")
	return line + strings.Repeat(" ", w-ansi.StringWidth(line)-ansi.StringWidth(note)) + cDim.Render(note)
}

// optionAt is the setup at column x of the setup field's middle line; -1
// for none.
func (m NewAgent) optionAt(x int) int {
	at := labelWidth + 2 // the box's border and padding
	for i, s := range m.setups {
		end := at + ansi.StringWidth("○ "+s)
		if x >= at && x < end {
			return i
		}
		at = end + len(optionGap)
	}
	return -1
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
