// Package dash is the list program: the top pane of the dashboard, which
// shows every agent, refreshes on its own and owns the list's keys (spec §6,
// design §3.1, §3.8, §5.1). It knows nothing of tmux or sbx; the Source it is
// given reaches them.
package dash

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/state"
)

// Source is what the list program reads and drives.
type Source struct {
	// Agents collects the agents from tmux and the state files; running is
	// the latest answer of sbx, nil while unknown (design §5.1).
	Agents func(running map[string]bool) ([]agent.Agent, error)
	// Running asks sbx which sandboxes run; it may take seconds.
	Running func() (map[string]bool, error)
	Now     func() time.Time
	// UpdateHint is "vX.Y.Z available - hq update" when a newer release
	// exists, else empty; it asks GitHub at most once a day.
	UpdateHint func() string
	// Layout re-applies the list pane's height to the window's (§6.1).
	Layout func()
	// Footer shows the key hints on the dashboard's status line.
	Footer func([]Hint)
}

// Hint is one key and what it does, as the footer shows it.
type Hint struct{ Key, Label string }

// Timing of the refresh loop (design §5.1).
const (
	tickEvery   = 250 * time.Millisecond
	sbxEvery    = 4 // ticks
	updateEvery = time.Hour
)

// Rows is how many agents the list shows: 6, or 3 in a window below 24
// lines (spec §6.1).
func Rows(windowHeight int) int {
	if windowHeight < 24 {
		return 3
	}
	return 6
}

// Height is the list pane's height for a window: the header, a blank line,
// the column header, the rows and the scroll hint line.
func Height(windowHeight int) int { return Rows(windowHeight) + 4 }

type (
	tickMsg   struct{}
	agentsMsg struct {
		agents []agent.Agent
		err    error
	}
	runningMsg struct {
		running map[string]bool
		err     error
	}
	hintMsg struct{ text string }
	hourMsg struct{}
)

// Model is the list program's state.
type Model struct {
	src     Source
	agents  []agent.Agent
	running map[string]bool
	err     error
	hint    string // update hint

	width, height int
	ticks         int
	polling       bool      // an sbx poll is under way
	polled        time.Time // when sbx last answered
}

// New is the list program before its first refresh.
func New(src Source) Model { return Model{src: src, width: 80, height: Height(24)} }

// Keys of this milestone's list, in footer order.
var hints = []Hint{{"r", "refresh"}, {"q", "quit"}}

func (m Model) Init() tea.Cmd {
	m.src.Footer(hints)
	return tea.Batch(m.collect(), m.poll(), m.checkUpdate(), tick())
}

func tick() tea.Cmd { return tea.Tick(tickEvery, func(time.Time) tea.Msg { return tickMsg{} }) }

func (m Model) collect() tea.Cmd {
	running := m.running
	return func() tea.Msg {
		as, err := m.src.Agents(running)
		return agentsMsg{as, err}
	}
}

func (m *Model) poll() tea.Cmd {
	if m.polling {
		return nil
	}
	m.polling = true
	return func() tea.Msg {
		r, err := m.src.Running()
		return runningMsg{r, err}
	}
}

func (m Model) checkUpdate() tea.Cmd {
	return func() tea.Msg { return hintMsg{m.src.UpdateHint()} }
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.src.Layout()
	case tickMsg:
		m.ticks++
		cmds := []tea.Cmd{m.collect(), tick()}
		if m.ticks%sbxEvery == 0 {
			cmds = append(cmds, m.poll())
		}
		return m, tea.Batch(cmds...)
	case agentsMsg:
		m.err = msg.err
		if msg.err == nil {
			agent.SortAttention(msg.agents)
			m.agents = msg.agents
		}
	case runningMsg:
		m.polling = false
		// A failed or slow sbx leaves the states to tmux and the hooks.
		m.running = msg.running
		if msg.err == nil {
			m.polled = m.src.Now()
		}
		return m, m.collect()
	case hintMsg:
		m.hint = msg.text
		return m, tea.Tick(updateEvery, func(time.Time) tea.Msg { return hourMsg{} })
	case hourMsg:
		return m, m.checkUpdate()
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "r":
			return m, tea.Batch(m.collect(), m.poll())
		}
	}
	return m, nil
}

// Colours of the mocks, degrading on terminals with fewer colours.
var (
	cTitle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#A78BFA")).Bold(true)
	cDim     = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))
	cText    = lipgloss.NewStyle().Foreground(lipgloss.Color("#D1D5DB"))
	cName    = lipgloss.NewStyle().Foreground(lipgloss.Color("#F3F4F6")).Bold(true)
	cErr     = lipgloss.NewStyle().Foreground(lipgloss.Color("#F87171"))
	cByState = map[string]lipgloss.Style{
		state.NeedsInput: lipgloss.NewStyle().Foreground(lipgloss.Color("#F5A524")),
		state.Question:   lipgloss.NewStyle().Foreground(lipgloss.Color("#F5A524")),
		state.Done:       lipgloss.NewStyle().Foreground(lipgloss.Color("#34D399")),
		state.Working:    lipgloss.NewStyle().Foreground(lipgloss.Color("#60A5FA")),
		state.Starting:   lipgloss.NewStyle().Foreground(lipgloss.Color("#9CA3AF")),
		state.Ended:      lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280")),
	}
)

const (
	margin    = "  "
	gap       = "  "
	emptyText = "no agents yet - press n to start one, or run: hq new NAME [DIR] [PROMPT]"
)

func (m Model) View() string {
	rows := max(1, m.height-4)
	lines := []string{m.header(), ""}
	cols := m.columns()
	lines = append(lines, cols.header())
	switch {
	case len(m.agents) == 0:
		lines = append(lines, "", center(cDim.Render(emptyText), ansi.StringWidth(emptyText), m.width))
	default:
		now := m.src.Now()
		for _, a := range m.agents[:min(rows, len(m.agents))] {
			lines = append(lines, cols.row(a, now))
		}
	}
	for len(lines) < rows+3 {
		lines = append(lines, "")
	}
	lines = append(lines, m.footerLine(rows))
	return strings.Join(lines, "\n")
}

// header is `hq  N agents · X need you · Y done · Z working` on the left, the
// update hint after it, and the clock with the age of sbx's answer on the
// right (§6.1).
func (m Model) header() string {
	var need, done, working int
	for _, a := range m.agents {
		switch {
		case a.NeedsYou():
			need++
		case a.State == state.Done:
			done++
		case a.State == state.Working:
			working++
		}
	}
	parts := []string{plural(len(m.agents), "agent")}
	for _, p := range []struct {
		n     int
		label string
	}{{need, "need you"}, {done, "done"}, {working, "working"}} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", p.n, p.label))
		}
	}
	left := cTitle.Render("hq") + "  " + cText.Render(strings.Join(parts, " · "))
	if m.hint != "" {
		left += "  " + cDim.Render(m.hint)
	}
	now := m.src.Now()
	right := now.Format("Mon 15:04")
	if !m.polled.IsZero() {
		right += "  ⟳ " + agent.Age(now.Sub(m.polled).Round(time.Second))
	}
	right = cDim.Render(right)
	pad := m.width - len(margin) - lipgloss.Width(left) - lipgloss.Width(right) - len(margin)
	return margin + left + strings.Repeat(" ", max(1, pad)) + right
}

// footerLine is the last line: an error from tmux, or the scroll hint when
// more agents exist than rows.
func (m Model) footerLine(rows int) string {
	if m.err != nil {
		return margin + cErr.Render(ansi.Truncate("hq: "+m.err.Error(), m.width-4, "…"))
	}
	if len(m.agents) <= rows {
		return ""
	}
	s := fmt.Sprintf("%d of %d  ▾ %d more", rows, len(m.agents), len(m.agents)-rows)
	return strings.Repeat(" ", max(0, m.width-ansi.StringWidth(s)-len(margin))) + cDim.Render(s)
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func center(s string, w, width int) string {
	return strings.Repeat(" ", max(0, (width-w)/2)) + s
}

// columns are the widths of TAB, REPO, BRANCH, STATE and AGE; LAST takes the
// rest of the line.
type columns struct{ tab, repo, branch, state, age, last int }

func (m Model) columns() columns {
	c := columns{tab: 6, repo: 10, branch: 12, state: ansi.StringWidth("● needs input"), age: 4}
	for _, a := range m.agents {
		c.tab = max(c.tab, ansi.StringWidth(a.Name))
		c.repo = max(c.repo, ansi.StringWidth(a.Repo()))
		c.branch = max(c.branch, ansi.StringWidth(a.Branch))
	}
	c.tab, c.repo, c.branch = min(c.tab, 16), min(c.repo, 24), min(c.branch, 28)
	used := len(margin) + c.tab + c.repo + c.branch + c.state + c.age + 5*len(gap) + len(margin)
	c.last = max(0, m.width-used)
	return c
}

func (c columns) header() string {
	// The sorted column is brighter (spec §6.2).
	return margin + cDim.Render(strings.Join([]string{fit("TAB", c.tab), fit("REPO", c.repo), fit("BRANCH", c.branch)}, gap)+gap) +
		cText.Render(fit("STATE ▾", c.state)) + cDim.Render(gap+strings.Join([]string{fit("AGE", c.age), fit("LAST", c.last)}, gap))
}

func (c columns) row(a agent.Agent, now time.Time) string {
	st, ok := cByState[a.State]
	if !ok {
		st = cText
	}
	last := a.Last
	if last == "" && a.State == state.Ended {
		last = "[session ended]"
	}
	lastStyle := cText
	if a.State == state.Ended {
		lastStyle = cDim
	}
	cells := []string{
		cName.Render(fit(a.Name, c.tab)),
		cText.Render(fit(a.Repo(), c.repo)),
		cText.Render(fit(orDash(a.Branch), c.branch)),
		st.Render(fit("● "+a.State, c.state)),
		cDim.Render(fit(agent.Age(max(0, now.Sub(a.Since))), c.age)),
		lastStyle.Render(fit(orDash(last), c.last)),
	}
	return margin + strings.Join(cells, gap)
}

// fit cuts s to w cells, marking the cut, and pads it to w.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = ansi.Truncate(s, w, "…")
	return s + strings.Repeat(" ", w-ansi.StringWidth(s))
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// Run runs the list program on the terminal until q.
func Run(src Source) error {
	_, err := tea.NewProgram(New(src), tea.WithAltScreen()).Run()
	return err
}
