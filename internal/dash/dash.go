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
	// Modes are the sort and view the user left; SaveModes keeps them for
	// the next run (spec §6.2).
	Modes     func() (sort, view string)
	SaveModes func(sort, view string)
	// Cursor is the cursor row's agent as the last list program left it;
	// SetCursor keeps it for the next one (design §3.3).
	Cursor    func() string
	SetCursor func(name string)
}

// Sorts, cycled with s, and views, toggled with a (spec §6.2).
const (
	SortAttention = "attention"
	SortRepo      = "repo"
	SortState     = "state"

	ViewAttention = "attention"
	ViewAll       = "all"
)

var sorts = []string{SortAttention, SortRepo, SortState}

// sortRows orders rows as mode says; filtering by view is a separate step
// (design §5.1).
func sortRows(rows []agent.Agent, mode string) {
	switch mode {
	case SortRepo:
		agent.SortRepo(rows)
	case SortState:
		agent.SortState(rows)
	default:
		agent.SortAttention(rows)
	}
}

// filterRows keeps the agents the view shows.
func filterRows(as []agent.Agent, view string) []agent.Agent {
	rows := make([]agent.Agent, 0, len(as))
	for _, a := range as {
		if view == ViewAll || a.NeedsYou() {
			rows = append(rows, a)
		}
	}
	return rows
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
	agents  []agent.Agent // every agent, as collected
	rows    []agent.Agent // the agents the view shows, in sort order
	running map[string]bool
	err     error
	hint    string // update hint

	sort, view string

	// The cursor follows its agent through re-sorts; when the agent leaves
	// the view, it stays at the same place, the nearest row (§6.3).
	cursor    string // the cursor row's agent
	cursorRow int    // its index in rows, -1 when there are none
	offset    int    // the first row shown

	width, height int
	ticks         int
	polling       bool      // an sbx poll is under way
	polled        time.Time // when sbx last answered
}

// New is the list program before its first refresh, in the modes the user
// left.
func New(src Source) Model {
	m := Model{src: src, width: 80, height: Height(24), sort: SortAttention, view: ViewAttention, cursorRow: -1}
	sort, view := src.Modes()
	for _, s := range sorts {
		if sort == s {
			m.sort = s
		}
	}
	if view == ViewAll {
		m.view = ViewAll
	}
	m.cursor = src.Cursor()
	return m
}

// hints are the keys of this milestone's list, in footer order: the sort
// shown is the current one, the view the one a switches to (mocks).
func (m Model) hints() []Hint {
	other := ViewAll
	if m.view == ViewAll {
		other = ViewAttention
	}
	return []Hint{{"↑↓", "select"}, {"s", "sort: " + m.sort}, {"a", "view: " + other}, {"r", "refresh"}, {"q", "quit"}}
}

// visible is how many rows the pane shows.
func (m Model) visible() int { return max(1, m.height-4) }

// arrange filters and sorts the agents into rows and puts the cursor back on
// its agent.
func (m *Model) arrange() {
	m.rows = filterRows(m.agents, m.view)
	sortRows(m.rows, m.sort)
	row := min(m.cursorRow, len(m.rows)-1)
	for i, a := range m.rows {
		if a.Name == m.cursor {
			row = i
		}
	}
	if row < 0 && len(m.rows) > 0 {
		row = 0
	}
	m.moveTo(row)
}

// moveTo puts the cursor on row i and scrolls to keep it in sight. With no
// rows the cursor keeps its agent's name, so it returns to that agent when
// it shows again (as when the list starts before its first refresh).
func (m *Model) moveTo(i int) {
	m.cursorRow = i
	if i < 0 {
		m.offset = 0
		return
	}
	m.cursor = m.rows[i].Name
	v := m.visible()
	m.offset = min(m.offset, i)
	if i >= m.offset+v {
		m.offset = i - v + 1
	}
	m.offset = max(0, min(m.offset, len(m.rows)-v))
}

func (m Model) Init() tea.Cmd {
	m.src.Footer(m.hints())
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
	before := m.cursor
	next, cmd := m.update(msg)
	if next.cursor != before {
		m.src.SetCursor(next.cursor)
	}
	return next, cmd
}

func (m Model) update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.src.Layout()
		m.moveTo(m.cursorRow)
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
			m.agents = msg.agents
			m.arrange()
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
		case "j", "down":
			if m.cursorRow+1 < len(m.rows) {
				m.moveTo(m.cursorRow + 1)
			}
		case "k", "up":
			if m.cursorRow > 0 {
				m.moveTo(m.cursorRow - 1)
			}
		case "s":
			for i, s := range sorts {
				if s == m.sort {
					m.sort = sorts[(i+1)%len(sorts)]
					break
				}
			}
			m.modesChanged()
		case "a":
			m.view = map[string]string{ViewAll: ViewAttention, ViewAttention: ViewAll}[m.view]
			m.modesChanged()
		}
	}
	return m, nil
}

// modesChanged re-arranges the rows, keeps the modes for the next run and
// shows them in the footer.
func (m *Model) modesChanged() {
	m.arrange()
	m.src.SaveModes(m.sort, m.view)
	m.src.Footer(m.hints())
}

// Colours of the mocks, degrading on terminals with fewer colours.
var (
	cTitle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#A78BFA")).Bold(true)
	cDim     = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))
	cText    = lipgloss.NewStyle().Foreground(lipgloss.Color("#D1D5DB"))
	cName    = lipgloss.NewStyle().Foreground(lipgloss.Color("#F3F4F6")).Bold(true)
	cErr     = lipgloss.NewStyle().Foreground(lipgloss.Color("#F87171"))
	cCursor  = lipgloss.Color("#2A2E37") // the cursor row's background
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
	margin      = "  "
	gap         = "  "
	emptyText   = "no agents yet - press n to start one, or run: hq new NAME [DIR] [PROMPT]"
	nothingText = "nothing needs you - press a for all"
)

func (m Model) View() string {
	v := m.visible()
	lines := []string{m.header(), ""}
	cols := m.columns()
	lines = append(lines, cols.header(m.sort))
	switch {
	case len(m.agents) == 0:
		lines = append(lines, "", center(cDim.Render(emptyText), ansi.StringWidth(emptyText), m.width))
	case len(m.rows) == 0:
		lines = append(lines, "", center(cDim.Render(nothingText), ansi.StringWidth(nothingText), m.width))
	default:
		now := m.src.Now()
		end := min(m.offset+v, len(m.rows))
		for i := m.offset; i < end; i++ {
			lines = append(lines, cols.row(m.rows[i], now, i == m.cursorRow))
		}
		if hidden := len(m.agents) - len(m.rows); hidden > 0 && len(m.rows) <= v {
			lines = append(lines, margin+cDim.Render(fmt.Sprintf("nothing else needs you · %s hidden · press a to show all", plural(hidden, "more agent"))))
		}
	}
	for len(lines) < v+3 {
		lines = append(lines, "")
	}
	lines = append(lines, m.footerLine(v))
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
	left := cTitle.Render("hq") + "  " + cText.Render(strings.Join(parts, " · ")) + "  " + cDim.Render("view: "+m.view)
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
// more rows exist than fit.
func (m Model) footerLine(v int) string {
	if m.err != nil {
		return margin + cErr.Render(ansi.Truncate("hq: "+m.err.Error(), m.width-4, "…"))
	}
	if len(m.rows) <= v {
		return ""
	}
	s := fmt.Sprintf("%d of %d", v, len(m.rows))
	if m.offset > 0 {
		s += fmt.Sprintf("  ▴ %d more", m.offset)
	}
	if below := len(m.rows) - m.offset - v; below > 0 {
		s += fmt.Sprintf("  ▾ %d more", below)
	}
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

// header names the columns, marking with ▾ and brighter text the ones the
// rows are sorted by (spec §6.2).
func (c columns) header(sort string) string {
	marked := map[string][]string{SortAttention: {"STATE"}, SortRepo: {"REPO"}, SortState: {"STATE", "TAB"}}[sort]
	cell := func(name string, w int) string {
		for _, k := range marked {
			if k == name {
				return cText.Render(fit(name+" ▾", w))
			}
		}
		return cDim.Render(fit(name, w))
	}
	return margin + strings.Join([]string{cell("TAB", c.tab), cell("REPO", c.repo), cell("BRANCH", c.branch), cell("STATE", c.state), cell("AGE", c.age), cell("LAST", c.last)}, cDim.Render(gap))
}

// row is one agent; the cursor row has a background (spec §6.1).
func (c columns) row(a agent.Agent, now time.Time, cursor bool) string {
	paint := func(st lipgloss.Style, s string) string {
		if cursor {
			st = st.Background(cCursor)
		}
		return st.Render(s)
	}
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
	plain := lipgloss.NewStyle()
	cells := []string{
		paint(cName, fit(a.Name, c.tab)),
		paint(cText, fit(a.Repo(), c.repo)),
		paint(cText, fit(orDash(a.Branch), c.branch)),
		paint(st, fit("● "+a.State, c.state)),
		paint(cDim, fit(agent.Age(max(0, now.Sub(a.Since))), c.age)),
		paint(lastStyle, fit(orDash(last), c.last)),
	}
	return paint(plain, margin) + strings.Join(cells, paint(plain, gap)) + paint(plain, margin)
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
