// Package dash is the list program: the top pane of the dashboard, which
// shows every agent, refreshes on its own and owns the list's keys (spec §6,
// design §3.1, §3.8, §5.1). It knows nothing of tmux or sbx; the Source it is
// given reaches them.
package dash

import (
	"fmt"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/gh"
	"github.com/rkrysinski/hq/internal/state"
)

// Source is what the list program reads and drives.
type Source struct {
	// Agents collects the agents from tmux and the state files; running is
	// the latest answer of sbx, which says nothing while unknown (design §5.1).
	Agents func(running agent.Sandboxes) ([]agent.Agent, error)
	// Running asks sbx which sandboxes run; it may take seconds.
	Running func() (agent.Sandboxes, error)
	Now     func() time.Time
	// UpdateHint is "vX.Y.Z available - hq update" when a newer release
	// exists, else empty; it asks GitHub at most once a day.
	UpdateHint func() string
	// Layout re-applies the list pane's height to the terminal's (§6.1).
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
	// Dock docks the agent named name below the list, keys in its session
	// (spec §6.4 open).
	Dock func(name string) error
	// Show docks the agent named name below the list, keys staying on the
	// list: the slot follows the cursor (spec §6.3).
	Show func(name string) error
	// Code opens VS Code on the agent's worktree (spec §6.4 code).
	Code func(name string) error
	// NewAgent opens the New agent dialog over the dashboard, dir
	// prefilled (empty: where hq was started), and returns when it closes
	// (spec §6.6).
	NewAgent func(dir string) error
	// Kill opens the Kill dialog on the agent named name, which ends it on
	// Yes, and returns when it closes (spec §6.4 kill, §6.6).
	Kill func(name string) error
	// PullRequests asks GitHub for a repository's pull requests, the
	// newest per branch; it may take seconds (design §5.2).
	PullRequests func(repo string) (map[string]gh.PR, error)
	// Browse opens a pull request in the browser (spec §6.4 pr).
	Browse func(url string) error
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

// filterRows keeps the agents the view shows: in the attention view those
// needing the user, the new ones and the docked one, whatever its state, so
// the list always shows the agent on screen (spec §6.2, ADR 0006).
func filterRows(as []agent.Agent, view string) []agent.Agent {
	rows := make([]agent.Agent, 0, len(as))
	for _, a := range as {
		if view == ViewAll || a.NeedsYou() || a.New || a.Docked {
			rows = append(rows, a)
		}
	}
	return rows
}

// Arrange is the rows of a sort and view, as the list shows them; unknown
// modes are the defaults.
func Arrange(as []agent.Agent, sort, view string) []agent.Agent {
	if view != ViewAll {
		view = ViewAttention
	}
	rows := filterRows(as, view)
	sortRows(rows, sort)
	return rows
}

// Neighbour is the agent Alt+j (step 1) or Alt+k (step -1) docks: the row
// after or before the docked one. Every view shows the docked agent; while
// none is shown (nothing is docked), the cursor stands where it left, so
// Alt+j docks the cursor row and Alt+k the one above it (design §3.7).
func Neighbour(rows []agent.Agent, docked, cursor string, step int) (string, bool) {
	at := func(name string) int {
		for i, a := range rows {
			if a.Name == name {
				return i
			}
		}
		return -1
	}
	i := at(docked)
	switch {
	case i >= 0:
		i += step
	case at(cursor) >= 0:
		i = at(cursor)
		if step < 0 {
			i--
		}
	case step < 0:
		i = len(rows) - 1
	default:
		i = 0
	}
	if i < 0 || i >= len(rows) {
		return "", false
	}
	return rows[i].Name, true
}

// EntryDock is the agent docked as the dashboard is entered (spec S1): with
// nothing docked, the cursor row the list comes back with, the cursor's
// agent when the view shows it, else the first row (§6.3). Something
// docked stays docked, and with no row shown the placeholder stays.
func EntryDock(as []agent.Agent, sort, view, cursor string) (string, bool) {
	for _, a := range as {
		if a.Docked {
			return "", false
		}
	}
	rows := Arrange(as, sort, view)
	if len(rows) == 0 {
		return "", false
	}
	for _, a := range rows {
		if a.Name == cursor {
			return cursor, true
		}
	}
	return rows[0].Name, true
}

// FirstNeedingYou is the agent Alt+a docks: the first needing the user in
// attention order, never the one docked now, whose answer may not be
// reported yet (design §3.7).
func FirstNeedingYou(as []agent.Agent, docked string) (string, bool) {
	for _, a := range Arrange(as, SortAttention, ViewAttention) {
		if a.NeedsYou() && a.Name != docked {
			return a.Name, true
		}
	}
	return "", false
}

// ChordHints name the Alt chords; the footer shows them while the keys are
// in the session below (spec §6.5).
var ChordHints = []Hint{{"alt+j/k", "dock next/previous"}, {"alt+a", "dock first needing you"}, {"alt+l", "list"}, {"alt+n", "new"}}

// Hint is one key and what it does, as the footer shows it.
type Hint struct{ Key, Label string }

// Timing of the refresh loop (design §5.1).
const (
	tickEvery   = 250 * time.Millisecond
	sbxEvery    = 4 // ticks
	updateEvery = time.Hour
	prEvery     = time.Minute
	// errFor is how long a row action's error shows, unless a key clears it
	// first; then the scroll hint is back.
	errFor = 5 * time.Second
	// restFor is how long the cursor rests on a row, moved by the user,
	// before its agent is docked (spec §6.3): long enough that holding an
	// arrow key, which repeats every 30-90 ms on macOS and Windows by
	// default, docks only the row it stops on; short enough to read as at
	// once (design §3.1).
	restFor = 150 * time.Millisecond
	// sbxStale is how old sbx's last answer may be before the header says
	// `sbx ?` (design §7.1). A poll starts at most 1 s after the last answer
	// and sbx ls may take up to its 5 s timeout, so an sbx that answers,
	// however slowly, leaves at most 6 s between answers; 8 s means at
	// least one poll failed or timed out.
	sbxStale = 8 * time.Second
)

// Rows is how many agents the list shows: 6, or 3 in a terminal below 24
// rows (spec §6.1). It counts the terminal's rows, the status line
// included, so the classic 80x24 terminal shows 6.
func Rows(terminalHeight int) int {
	if terminalHeight < 24 {
		return 3
	}
	return 6
}

// Height is the list pane's height in a terminal: the header, a blank line,
// the column header, the rows and the scroll hint line.
func Height(terminalHeight int) int { return Rows(terminalHeight) + 4 }

type (
	tickMsg   struct{}
	agentsMsg struct {
		agents []agent.Agent
		err    error
		seq    int // the collection's number, counted as they are asked for
	}
	restMsg    struct{ seq int }   // the cursor moved by the user rested for restFor
	dockedMsg  struct{ err error } // the list's own dock finished
	runningMsg struct {
		running agent.Sandboxes
		err     error
	}
	hintMsg    struct{ text string }
	hourMsg    struct{}
	actedMsg   struct{ err error } // a row action (dock, code) finished
	closedMsg  struct{ err error } // a dialog closed
	errGoneMsg struct{ seq int }   // the row action's error seq has shown long enough
	prTickMsg  struct{}
	prsMsg     struct {
		repo string
		prs  map[string]gh.PR
		err  error
	}
)

// Model is the list program's state.
type Model struct {
	src     Source
	agents  []agent.Agent // every agent, as collected
	rows    []agent.Agent // the agents the view shows, in sort order
	running agent.Sandboxes
	err     error
	actErr  error // why the last row action failed, for errFor or until the next key
	errSeq  int   // counts the row actions' errors, so an older timer clears no newer one
	// dialog is true from n or k until the dialog closes: the keys typed before
	// tmux shows the popup are not the list's either (spec §6.6).
	dialog bool
	hint   string // update hint
	// search is the name typed after /, while searching is true (§6.3).
	search    string
	searching bool
	// seen are the new agents the list has welcomed and the agents it
	// found at its start, by id; nil before the first refresh (S2).
	seen map[string]bool
	// docked is the agent docked at the last refresh, or the one the list
	// itself docks: when another is docked, by a chord, hq go or the New
	// agent dialog, the cursor goes to it, so the two stay the same row
	// (design §3.7, spec §6.3).
	docked string
	// rests counts the user's cursor moves: the one a restMsg carries docks
	// only when no move came after it.
	rests int
	// collects counts the collections asked for. While the list's own
	// docks run (owning counts them), and until a collection asked after
	// the last finished (ownFrom) answers, the refreshes may still show the
	// agent docked before, and are not taken for a dock made elsewhere.
	collects int
	owning   int
	ownFrom  int

	sort, view string

	// prs are the pull requests of each repository shown, by branch; a
	// repository is asked when it first shows, every minute, and when one
	// of its agents turns done. states are the agents' states at the last
	// refresh, by id, to see that (design §5.2).
	prs    map[string]map[string]gh.PR
	states map[string]string

	// The cursor follows its agent through re-sorts; when the agent leaves
	// the view, it stays at the same place, the nearest row (§6.3).
	cursor    string // the cursor row's agent
	cursorRow int    // its index in rows, -1 when there are none
	offset    int    // the first row shown
	// place is the row the cursor takes when its agent leaves the rows: the
	// cursor row, except while its agent has just ended, which moves the
	// row to the bottom a moment before a kill removes it (#82). live is
	// when the list last saw each agent not ended, by name.
	place int
	live  map[string]time.Time

	width, height int
	ticks         int
	polling       bool      // an sbx poll is under way
	polled        time.Time // when sbx last answered
	began         time.Time // when the list program started
}

// New is the list program before its first refresh, in the modes the user
// left.
func New(src Source) Model {
	m := Model{src: src, width: 80, height: Height(24), sort: SortAttention, view: ViewAttention, cursorRow: -1, place: -1, began: src.Now()}
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

// hints are the keys of the list, in footer order: the sort shown is the
// current one, the view the one a switches to (mocks). While searching the
// footer is the search: what is typed, then ` · ` and its matches, Enter and
// Esc (spec §6.3: `/bo · 1 match: bok-17`).
func (m Model) hints() []Hint {
	if m.searching {
		return []Hint{{"/" + m.search, "· " + m.matchText()}, {"⏎", "open"}, {"esc", "cancel"}}
	}
	other := ViewAll
	if m.view == ViewAll {
		other = ViewAttention
	}
	hs := []Hint{{"↑↓ /name", "select"}, {"⏎", "open session below"}, {"n", "new"}, {"k", "kill"}, {"c", "code"}, {"p", "pr"}, {"s", "sort: " + m.sort}, {"a", "view: " + other}, {"r", "refresh"}, {"q", "quit"}}
	if HintsWidth(hs) <= m.width {
		return hs
	}
	hs[1].Label = "open" // so q quit still fits 120 columns
	if HintsWidth(hs) <= m.width {
		return hs
	}
	// Narrow (mock frame S12): the strip's actions, then new, the
	// search and quit; the other keys still work. Narrower still, hints go
	// from before q quit until the rest fits.
	hs = []Hint{{"⏎", "open"}, {"c", "code"}, {"p", "pr"}, {"k", "kill"}, {"n", "new"}, {"/", "name"}, {"q", "quit"}}
	for len(hs) > 1 && HintsWidth(hs) > m.width {
		hs = append(hs[:len(hs)-2], hs[len(hs)-1])
	}
	return hs
}

// HintsWidth is how many cells the footer takes: a space, then the hints
// two spaces apart.
func HintsWidth(hs []Hint) int {
	w := 1
	for i, h := range hs {
		w += ansi.StringWidth(h.Key) + 1 + ansi.StringWidth(h.Label)
		if i > 0 {
			w += 2
		}
	}
	return w
}

// visible is how many rows the pane shows.
func (m Model) visible() int { return max(1, m.height-4) }

// arrange filters and sorts the agents into rows and puts the cursor back on
// its agent; when the agent is gone, the cursor takes its place, the row
// after it or, when it was the last, the one before (#82).
func (m *Model) arrange() {
	m.rows = Arrange(m.agents, m.sort, m.view)
	for i, a := range m.rows {
		if a.Name == m.cursor {
			place := m.place
			m.moveTo(i)
			if m.justEnded(a) {
				m.place = place
			}
			return
		}
	}
	row := min(m.place, len(m.rows)-1)
	if row < 0 && len(m.rows) > 0 {
		row = 0
	}
	// Not the user's move: a dock waiting for the cursor to rest on the
	// agent that went is off (S6: a kill leaves the placeholder).
	m.rests++
	m.moveTo(row)
}

// endedGrace is how long after the list last saw an agent not ended its
// ended row still keeps the cursor's place: a kill ends the session a moment
// before it removes the row, hq stop one agent after another.
const endedGrace = 10 * time.Second

// justEnded reports whether a is ended and the list saw it not ended in the
// last endedGrace.
func (m Model) justEnded(a agent.Agent) bool {
	t, ok := m.live[a.Name]
	return a.State == state.Ended && ok && m.src.Now().Sub(t) < endedGrace
}

// seeLive notes when each agent was last seen not ended, forgetting the
// agents gone.
func (m *Model) seeLive() {
	now, live := m.src.Now(), map[string]time.Time{}
	for _, a := range m.agents {
		if a.State != state.Ended {
			live[a.Name] = now
		} else if t, ok := m.live[a.Name]; ok {
			live[a.Name] = t
		}
	}
	m.live = live
}

// rowsTop is the line of the first row: the header, a blank line and the
// column names come before it.
const rowsTop = 3

// mouse handles a click (spec §6.3, §6.4): on a row it moves the cursor
// there; on the cursor row it fires the strip item under it, as its key
// does, and elsewhere does nothing. The wheel moves the cursor as the
// arrows do.
func (m Model) mouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	switch {
	case msg.Button == tea.MouseButtonWheelUp && msg.Action == tea.MouseActionPress:
		return m.update(tea.KeyMsg{Type: tea.KeyUp})
	case msg.Button == tea.MouseButtonWheelDown && msg.Action == tea.MouseActionPress:
		return m.update(tea.KeyMsg{Type: tea.KeyDown})
	case msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress:
		return m, nil
	}
	i := m.offset + msg.Y - rowsTop
	if msg.Y < rowsTop || i >= min(len(m.rows), m.offset+m.visible()) {
		return m, nil
	}
	m.actErr = nil
	if i != m.cursorRow {
		m.moveTo(i)
		return m, m.rest()
	}
	_, pr := m.pr(m.rows[i])
	switch k := chipAt(pr, m.width >= wideFrom, msg.X, m.width); k {
	case "":
		return m, nil
	case "enter":
		return m.update(tea.KeyMsg{Type: tea.KeyEnter})
	default:
		return m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
	}
}

// moveTo puts the cursor on row i, which becomes its place, and scrolls to
// keep it in sight. With no rows the cursor keeps its agent's name, so it
// returns to that agent when it shows again (as when the list starts before
// its first refresh).
func (m *Model) moveTo(i int) {
	m.place = i
	m.show(i)
}

// show puts the cursor on row i and scrolls to keep it in sight, leaving its
// place.
func (m *Model) show(i int) {
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
	return tea.Batch(m.collect(), m.poll(), m.checkUpdate(), tick(), prTick())
}

func prTick() tea.Cmd { return tea.Tick(prEvery, func(time.Time) tea.Msg { return prTickMsg{} }) }

// askPRs asks for the pull requests of repos in the background.
func (m Model) askPRs(repos []string) tea.Cmd {
	var cmds []tea.Cmd
	for _, r := range repos {
		ask := m.src.PullRequests
		cmds = append(cmds, func() tea.Msg {
			prs, err := ask(r)
			return prsMsg{r, prs, err}
		})
	}
	return tea.Batch(cmds...)
}

// prsDue are the repositories to ask about after a refresh: those showing
// for the first time and those with an agent that just turned done.
func (m *Model) prsDue() []string {
	if m.prs == nil {
		m.prs, m.states = map[string]map[string]gh.PR{}, map[string]string{}
	}
	var due []string
	add := func(r string) {
		if !slices.Contains(due, r) {
			due = append(due, r)
		}
	}
	for _, a := range m.agents {
		if _, ok := m.prs[a.RepoPath]; !ok {
			m.prs[a.RepoPath] = nil // asked
			add(a.RepoPath)
		}
		if was, ok := m.states[a.ID]; ok && was != state.Done && a.State == state.Done {
			add(a.RepoPath)
		}
		m.states[a.ID] = a.State
	}
	return due
}

// pr is the pull request of a row's branch, when the map has one.
func (m Model) pr(a agent.Agent) (gh.PR, bool) {
	p, ok := m.prs[a.RepoPath][a.Branch]
	return p, ok && a.Branch != ""
}

func tick() tea.Cmd { return tea.Tick(tickEvery, func(time.Time) tea.Msg { return tickMsg{} }) }

func (m *Model) collect() tea.Cmd {
	running := m.running
	m.collects++
	seq, agents := m.collects, m.src.Agents
	return func() tea.Msg {
		as, err := agents(running)
		return agentsMsg{as, err, seq}
	}
}

// rest notes that the user moved the cursor, and asks to be told once it
// has rested there for restFor; each move starts the wait again.
func (m *Model) rest() tea.Cmd {
	m.rests++
	seq := m.rests
	return tea.Tick(restFor, func(time.Time) tea.Msg { return restMsg{seq} })
}

// ownDock docks name through dock, noting it as the list's own, so the
// cursor stays where the user put it while refreshes catch up.
func (m *Model) ownDock(name string, dock func(string) error) tea.Cmd {
	m.docked, m.ownFrom = name, 0
	m.owning++
	return func() tea.Msg { return dockedMsg{dock(name)} }
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
		m.src.Footer(m.hints())
		m.show(m.cursorRow)
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
			m.seeLive()
			if m.owning == 0 && msg.seq >= m.ownFrom {
				m.followDock()
			}
			dock := m.welcome()
			m.arrange()
			return m, tea.Batch(dock, m.askPRs(m.prsDue()))
		}
	case prTickMsg:
		var repos []string
		for _, a := range m.agents {
			if !slices.Contains(repos, a.RepoPath) {
				repos = append(repos, a.RepoPath)
			}
		}
		return m, tea.Batch(m.askPRs(repos), prTick())
	case prsMsg:
		// gh missing, logged out or offline: no pr, and no error (§7.1).
		if msg.err != nil {
			msg.prs = nil
		}
		if m.prs != nil {
			m.prs[msg.repo] = msg.prs
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
	case actedMsg:
		gone := m.failed(msg.err)
		return m, tea.Batch(gone, m.collect())
	case dockedMsg:
		gone := m.failed(msg.err)
		refresh := m.collect()
		if m.owning--; m.owning == 0 {
			m.ownFrom = m.collects
		}
		return m, tea.Batch(gone, refresh)
	case restMsg:
		// The cursor rests where the user moved it: its agent takes the
		// slot, the keys staying on the list (spec §6.3).
		if msg.seq == m.rests && m.cursorRow >= 0 && m.cursor != m.docked {
			return m, m.ownDock(m.cursor, m.src.Show)
		}
	case closedMsg:
		m.dialog = false
		gone := m.failed(msg.err)
		return m, tea.Batch(gone, m.collect())
	case errGoneMsg:
		if msg.seq == m.errSeq {
			m.actErr = nil
		}
	case tea.MouseMsg:
		if m.dialog || m.searching {
			return m, nil
		}
		return m.mouse(msg)
	case tea.KeyMsg:
		// Bubble Tea delivers runes that arrive together, typed fast or
		// pasted, as one key message ("/c-"): they are the same keys one
		// after another, so a k among them opens the dialog and the rest
		// are ignored, as if typed.
		if msg.Type == tea.KeyRunes && len(msg.Runes) > 1 {
			var cmds []tea.Cmd
			for _, r := range msg.Runes {
				var cmd tea.Cmd
				m, cmd = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}, Alt: msg.Alt})
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)
		}
		if m.dialog {
			return m, nil
		}
		m.actErr = nil
		if m.searching {
			return m.searchKey(msg)
		}
		switch msg.String() {
		case "/":
			m.searching, m.search = true, ""
			m.src.Footer(m.hints())
		case "enter":
			if m.cursorRow >= 0 {
				m.rests++ // docked now, keys and all: no dock at rest
				return m, m.ownDock(m.cursor, m.src.Dock)
			}
		case "c":
			if m.cursorRow >= 0 {
				name, code := m.cursor, m.src.Code
				return m, func() tea.Msg { return actedMsg{code(name)} }
			}
		case "p":
			if m.cursorRow >= 0 {
				a := m.rows[m.cursorRow]
				p, ok := m.pr(a)
				if !ok {
					gone := m.failed(fmt.Errorf("no pull request for %s", orDash(a.Branch)))
					return m, gone
				}
				browse := m.src.Browse
				return m, func() tea.Msg { return actedMsg{browse(p.URL)} }
			}
		case "n":
			dir, open := "", m.src.NewAgent
			if m.cursorRow >= 0 {
				dir = m.rows[m.cursorRow].RepoPath
			}
			m.dialog = true
			return m, func() tea.Msg { return closedMsg{open(dir)} }
		case "k":
			if m.cursorRow >= 0 {
				// The row after this one takes the cursor when it goes,
				// even when it ended moments ago.
				m.place = m.cursorRow
				name, kill := m.cursor, m.src.Kill
				m.dialog = true
				return m, func() tea.Msg { return closedMsg{kill(name)} }
			}
		case "q", "ctrl+c":
			return m, tea.Quit
		case "r":
			return m, tea.Batch(m.collect(), m.poll())
		case "down":
			if m.cursorRow+1 < len(m.rows) {
				m.moveTo(m.cursorRow + 1)
				return m, m.rest()
			}
		case "up":
			if m.cursorRow > 0 {
				m.moveTo(m.cursorRow - 1)
				return m, m.rest()
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

// failed shows why a row action failed, err nil being none, and clears it
// after errFor unless a newer error or a key replaced it by then.
func (m *Model) failed(err error) tea.Cmd {
	m.actErr = err
	if err == nil {
		return nil
	}
	m.errSeq++
	seq := m.errSeq
	return tea.Tick(errFor, func(time.Time) tea.Msg { return errGoneMsg{seq} })
}

// searchKey handles a key while searching: letters extend the name and
// move the cursor to the first match, Enter docks it, Esc ends the search.
func (m Model) searchKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.searching = false
	case tea.KeyEnter:
		m.searching = false
		if ms := m.matches(); len(ms) > 0 {
			m.rests++ // docked now, keys and all: no dock at rest
			cmd = m.ownDock(ms[0], m.src.Dock)
		}
	case tea.KeyBackspace:
		if r := []rune(m.search); len(r) > 0 {
			m.search = string(r[:len(r)-1])
		}
	case tea.KeyRunes, tea.KeySpace:
		m.search += string(msg.Runes)
		if ms := m.matches(); len(ms) > 0 && ms[0] != m.cursor {
			for i, a := range m.rows {
				if a.Name == ms[0] {
					m.moveTo(i)
					cmd = m.rest()
				}
			}
		}
	}
	m.src.Footer(m.hints())
	return m, cmd
}

// matches are the names of the rows shown that start with the search,
// ignoring case, in row order.
func (m Model) matches() []string {
	if m.search == "" {
		return nil
	}
	var out []string
	for _, a := range m.rows {
		if strings.HasPrefix(strings.ToLower(a.Name), strings.ToLower(m.search)) {
			out = append(out, a.Name)
		}
	}
	return out
}

// matchText tells the matches in the footer.
func (m Model) matchText() string {
	ms := m.matches()
	switch {
	case m.search == "":
		return "type a name"
	case len(ms) == 0:
		return "no match"
	case len(ms) == 1:
		return "1 match: " + ms[0]
	}
	return fmt.Sprintf("%d matches: %s", len(ms), strings.Join(ms, " "))
}

// welcome docks an agent started since the last refresh, from the dialog or
// hq new in any shell, when nothing is docked, and gives it the cursor, so a
// fresh sandbox's login happens in front of the user (S2). With an agent
// docked, one started elsewhere takes neither the slot nor the cursor: they
// move only as the user moves them (spec §6.3); the New agent dialog docks
// its own agent. Agents running when the list starts are not new to it. An
// agent is welcomed once, when first seen new.
func (m *Model) welcome() tea.Cmd {
	first := m.seen == nil
	if first {
		m.seen = map[string]bool{}
	}
	var fresh string
	docked := false
	for _, a := range m.agents {
		docked = docked || a.Docked
		if first || a.New && !m.seen[a.ID] {
			if !first {
				fresh = a.Name
			}
			m.seen[a.ID] = true
		}
	}
	if fresh == "" || docked || m.owning > 0 {
		return nil
	}
	m.cursor = fresh
	return m.ownDock(fresh, m.src.Dock)
}

// followDock moves the cursor to an agent docked since the last refresh, or
// docked as the list starts, so cursor and slot are the same row.
func (m *Model) followDock() {
	docked := ""
	for _, a := range m.agents {
		if a.Docked {
			docked = a.Name
		}
	}
	if docked != "" && (docked != m.docked || m.seen == nil) {
		m.cursor = docked
	}
	m.docked = docked
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
	cCursor  = lipgloss.Color("#2A2E37")                                 // the cursor row's background
	cOutline = lipgloss.NewStyle().Foreground(lipgloss.Color("#A78BFA")) // the docked row's outline
	cNew     = lipgloss.NewStyle().Foreground(lipgloss.Color("#A78BFA")) // the new marker
	// The action strip's chips: the key in the accent, the label bright,
	// on a background a step lighter than the cursor row's.
	cChip     = lipgloss.NewStyle().Foreground(lipgloss.Color("#D1D5DB")).Background(lipgloss.Color("#3A3F4B"))
	cChipKey  = cChip.Foreground(lipgloss.Color("#A78BFA")).Bold(true)
	cStripGap = lipgloss.NewStyle().Background(cCursor)
	cByState  = map[string]lipgloss.Style{
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
			a := m.rows[i]
			if i != m.cursorRow {
				lines = append(lines, cols.row(a, now, false, ""))
				continue
			}
			_, pr := m.pr(a)
			lines = append(lines, cols.row(a, now, true, strip(pr, m.width >= wideFrom)))
		}
		if hidden := len(m.agents) - len(m.rows); hidden > 0 && len(m.rows) <= v {
			lines = append(lines, margin+cDim.Render(fmt.Sprintf("nothing else needs you · %s hidden · press a to show all", plural(hidden, "more agent"))))
		}
	}
	for len(lines) < v+3 {
		lines = append(lines, "")
	}
	lines = append(lines, m.footerLine(v))
	// Nothing wraps, whatever the width (S12).
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, m.width, "")
	}
	return strings.Join(lines, "\n")
}

// header is `hq  N agents · X need you · Y done · Z working` on the left, the
// update hint after it, and the clock on the right, with `sbx ?` after it
// while sbx does not answer (§6.1, design §7.1).
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
	now := m.src.Now()
	right := now.Format("Mon 15:04")
	if m.sbxDown(now) {
		right += "  sbx ?"
	}
	right = cDim.Render(right)
	// The clock and sbx ? stay; the left keeps what fits, giving up the
	// update hint, the view, then the counts from the last (S12).
	hint, view := m.hint, "view: "+m.view
	room := m.width - 2*len(margin) - lipgloss.Width(right) - 1
	left := func() string {
		l := cTitle.Render("hq") + "  " + cText.Render(strings.Join(parts, " · "))
		for _, s := range []string{view, hint} {
			if s != "" {
				l += "  " + cDim.Render(s)
			}
		}
		return l
	}
	for _, drop := range []func(){func() { hint = "" }, func() { view = "" }, func() { parts = parts[:max(1, len(parts)-1)] }, func() { parts = parts[:max(1, len(parts)-1)] }, func() { parts = parts[:1] }} {
		if lipgloss.Width(left()) <= room {
			break
		}
		drop()
	}
	l := left()
	pad := m.width - len(margin) - lipgloss.Width(l) - lipgloss.Width(right) - len(margin)
	return margin + l + strings.Repeat(" ", max(1, pad)) + right
}

// sbxDown is whether sbx's last answer, or the start when it has not
// answered yet, is older than sbxStale.
func (m Model) sbxDown(now time.Time) bool {
	last := m.polled
	if last.IsZero() {
		last = m.began
	}
	return now.Sub(last) > sbxStale
}

// footerLine is the last line: an error from tmux, or the scroll hint when
// more rows exist than fit.
func (m Model) footerLine(v int) string {
	for _, err := range []error{m.err, m.actErr} {
		if err != nil {
			return margin + cErr.Render(ansi.Truncate("hq: "+err.Error(), m.width-4, "…"))
		}
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

// Widths where the layout changes (spec §6.1, §6.4, S12): from wideFrom
// columns the rows show LAST and the strip its labels; below, LAST goes and
// the strip is glyphs; below narrowFrom REPO goes too.
const (
	wideFrom   = 100
	narrowFrom = 80
	minLast    = 32 // LAST shown is at least this wide: the strip with labels covers it alone
)

// columns are the widths of TAB, REPO, BRANCH, STATE and AGE; LAST takes the
// rest of the line. A column of width 0 is not shown.
type columns struct{ tab, repo, branch, state, age, last int }

func (m Model) columns() columns {
	c := columns{tab: 6, repo: 10, branch: 12, state: ansi.StringWidth("● needs input"), age: 4}
	for _, a := range m.agents {
		c.tab = max(c.tab, ansi.StringWidth(a.Name)+len(newMark)*boolInt(a.New))
		c.repo = max(c.repo, ansi.StringWidth(a.Repo()))
		c.branch = max(c.branch, ansi.StringWidth(a.Branch))
	}
	c.tab, c.repo, c.branch = min(c.tab, 16), min(c.repo, 24), min(c.branch, 28)
	if m.width < narrowFrom {
		c.repo = 0
	}
	wide := m.width >= wideFrom
	room := func() int {
		used, n := 0, 0
		for _, w := range []int{c.tab, c.repo, c.branch, c.state, c.age} {
			if w > 0 {
				used, n = used+w, n+1
			}
		}
		if wide {
			used, n = used+minLast, n+1
		}
		return m.width - 2*len(margin) - used - (n-1)*len(gap)
	}
	// Too wide: the branch gives way first, then the repository, then the
	// name, each down to what still reads; fit cuts them with ….
	for _, col := range []*int{&c.branch, &c.repo, &c.tab} {
		if over := -room(); over > 0 && *col > 0 {
			*col = max(min(*col, 6), *col-over)
		}
	}
	// The last column takes the rest, so rows, the docked outline and the
	// strip reach the right edge.
	if wide {
		c.last = minLast + max(0, room())
	} else {
		c.age += max(0, room())
	}
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
	var cells []string
	for _, col := range c.shown() {
		cells = append(cells, cell(col.name, col.w))
	}
	return margin + strings.Join(cells, cDim.Render(gap))
}

type column struct {
	name string
	w    int
}

// shown are the columns with a width, in order.
func (c columns) shown() []column {
	var cs []column
	for _, col := range []column{{"TAB", c.tab}, {"REPO", c.repo}, {"BRANCH", c.branch}, {"STATE", c.state}, {"AGE", c.age}, {"LAST", c.last}} {
		if col.w > 0 {
			cs = append(cs, col)
		}
	}
	return cs
}

// chip is one action of the strip: the key it stands for and how it is
// drawn.
type chip struct {
	key, text string
}

// chips are the cursor row's actions, pr only with a pull request (spec
// §6.4, the mock's all view); below wideFrom columns, glyphs alone
// (mock frame S12).
func chips(pr, wide bool) []chip {
	// key pressed, key shown, glyph when narrow, label
	items := [][4]string{{"enter", "⏎", "⏎", "open"}, {"c", "c", "c", "code"}, {"p", "p", "p", "pr"}, {"k", "k", "✕", "kill"}}
	var cs []chip
	for _, it := range items {
		if it[0] == "p" && !pr {
			continue
		}
		if !wide {
			cs = append(cs, chip{it[0], cChipKey.Render(" " + it[2] + " ")})
			continue
		}
		cs = append(cs, chip{it[0], cChipKey.Render(" "+it[1]) + cChip.Render(" "+it[3]+" ")})
	}
	return cs
}

// strip draws the chips a space apart.
func strip(pr, wide bool) string {
	var ts []string
	for _, c := range chips(pr, wide) {
		ts = append(ts, c.text)
	}
	return strings.Join(ts, cStripGap.Render(" "))
}

// chipAt is the key of the chip at column x of the cursor row, whose strip
// ends a margin from the right edge; "" between chips or off the strip.
func chipAt(pr, wide bool, x, width int) string {
	at := width - len(margin) - ansi.StringWidth(strip(pr, wide))
	for _, c := range chips(pr, wide) {
		w := ansi.StringWidth(c.text)
		if x >= at && x < at+w {
			return c.key
		}
		at += w + 1
	}
	return ""
}

// row is one agent; the cursor row has a background and the action strip
// drawn over the tail of its content, the docked row an outline, drawn as
// bars at both ends so it takes no extra lines (spec §6.1, §6.4); design
// 3.1 says why the design's full box is not drawn.
func (c columns) row(a agent.Agent, now time.Time, cursor bool, strip string) string {
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
	lastStyle := cText
	if a.State == state.Ended {
		lastStyle = cDim
	}
	plain := lipgloss.NewStyle()
	name := paint(cName, fit(a.Name, c.tab))
	if a.New {
		n := ansi.Truncate(a.Name, max(0, c.tab-len(newMark)), "…")
		name = paint(cName, n) + paint(cNew, fit(newMark, c.tab-ansi.StringWidth(n)))
	}
	var cells []string
	for _, col := range c.shown() {
		switch col.name {
		case "TAB":
			cells = append(cells, name)
		case "REPO":
			cells = append(cells, paint(cText, fit(a.Repo(), col.w)))
		case "BRANCH":
			cells = append(cells, paint(cText, fit(orDash(a.Branch), col.w)))
		case "STATE":
			cells = append(cells, paint(st, fit("● "+a.State, col.w)))
		case "AGE":
			cells = append(cells, paint(cDim, fit(agent.Age(max(0, now.Sub(a.Since))), col.w)))
		case "LAST":
			cells = append(cells, paint(lastStyle, fit(orDash(a.Last), col.w)))
		}
	}
	left, right := paint(plain, margin), paint(plain, margin)
	if a.Docked {
		left, right = paint(cOutline, "│")+paint(plain, " "), paint(plain, " ")+paint(cOutline, "│")
	}
	body := left + strings.Join(cells, paint(plain, gap))
	if strip != "" {
		// Columns stay put: the strip covers the tail, a space before it.
		w := ansi.StringWidth(body) - ansi.StringWidth(strip) - 1
		cut := ansi.Truncate(body, max(0, w), "")
		body = cut + paint(plain, strings.Repeat(" ", max(0, w-ansi.StringWidth(cut))+1)) + strip
	}
	return body + right
}

// newMark follows the name of an agent new since its start (S2).
const newMark = " new"

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
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
	_, err := tea.NewProgram(New(src), tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	return err
}
