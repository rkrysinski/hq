package dash

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/gh"
	"github.com/rkrysinski/hq/internal/state"
)

var now = time.Date(2026, 9, 26, 14, 32, 0, 0, time.UTC) // a Saturday

// fakeSource counts what the list program asks for.
type fakeSource struct {
	agents     []agent.Agent
	agentsErr  error
	running    map[string]bool
	runningErr error
	hint       string
	sort, view string   // the modes the user left
	saved      []string // modes kept, "sort/view"
	cursor     string   // the cursor kept on the session
	cursorSets int
	docked     []string // agents docked, in order
	dockErr    error
	dialogs    []string // dirs the New agent dialog opened with
	onDialog   func()   // what the user does in the dialog
	coded      []string // agents VS Code opened on
	codeErr    error
	killed     []string                    // agents the Kill dialog opened on
	onKill     func()                      // what the user answers in it
	prs        map[string]map[string]gh.PR // gh's answer, by repository
	prErr      error
	mu         sync.Mutex
	asked      []string // repositories gh was asked about, in order
	browsed    []string // URLs opened

	collects, polls, layouts int
	seen                     []map[string]bool // running as given to Agents
	footer                   []Hint
}

func (f *fakeSource) source() Source {
	return Source{
		Agents: func(running map[string]bool) ([]agent.Agent, error) {
			f.collects++
			f.seen = append(f.seen, running)
			return append([]agent.Agent(nil), f.agents...), f.agentsErr
		},
		Running: func() (map[string]bool, error) {
			f.polls++
			return f.running, f.runningErr
		},
		Now:        func() time.Time { return now },
		UpdateHint: func() string { return f.hint },
		Layout:     func() { f.layouts++ },
		Footer:     func(h []Hint) { f.footer = h },
		Modes:      func() (string, string) { return f.sort, f.view },
		SaveModes:  func(sort, view string) { f.saved = append(f.saved, sort+"/"+view) },
		Cursor:     func() string { return f.cursor },
		SetCursor:  func(name string) { f.cursor = name; f.cursorSets++ },
		Dock: func(name string) error {
			if f.dockErr != nil {
				return f.dockErr
			}
			f.docked = append(f.docked, name)
			for i := range f.agents {
				f.agents[i].Docked = f.agents[i].Name == name
			}
			return nil
		},
		NewAgent: func(dir string) error {
			f.dialogs = append(f.dialogs, dir)
			if f.onDialog != nil {
				f.onDialog()
			}
			return nil
		},
		Code: func(name string) error {
			f.coded = append(f.coded, name)
			return f.codeErr
		},
		Kill: func(name string) error {
			f.killed = append(f.killed, name)
			if f.onKill != nil {
				f.onKill()
			}
			return nil
		},
		PullRequests: func(repo string) (map[string]gh.PR, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.asked = append(f.asked, repo)
			return f.prs[repo], f.prErr
		},
		Browse: func(url string) error { f.browsed = append(f.browsed, url); return nil },
	}
}

// askedAbout is the repositories gh was asked about since the last call,
// sorted.
func (f *fakeSource) askedAbout() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := append([]string(nil), f.asked...)
	f.asked = nil
	sort.Strings(a)
	return strings.Join(a, " ")
}

// do runs cmd and every command it batches, feeding the messages to m.
// Timers (ticks, the hourly update check) are not waited for.
func do(m Model, cmd tea.Cmd) Model {
	for _, msg := range msgs(cmd) {
		var next tea.Cmd
		var tm tea.Model
		tm, next = m.Update(msg)
		m = tm.(Model)
		m = do(m, next)
	}
	return m
}

func msgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		if b, ok := msg.(tea.BatchMsg); ok {
			var out []tea.Msg
			for _, c := range b {
				out = append(out, msgs(c)...)
			}
			return out
		}
		if msg == nil {
			return nil
		}
		return []tea.Msg{msg}
	case <-time.After(20 * time.Millisecond):
		return nil // a timer
	}
}

func update(m Model, msg tea.Msg) (Model, tea.Cmd) {
	tm, cmd := m.Update(msg)
	return tm.(Model), cmd
}

// started is the list program after its first refresh in a window of w x h.
func started(f *fakeSource, w, h int) Model {
	m := New(f.source())
	m = do(m, m.Init())
	m, cmd := update(m, tea.WindowSizeMsg{Width: w, Height: h})
	return do(m, cmd)
}

func lines(m Model) []string {
	return strings.Split(ansi.Strip(m.View()), "\n")
}

func ag(name, st string, age time.Duration, last string) agent.Agent {
	return agent.Agent{Name: name, RepoPath: "/w/" + name + "-repo", Branch: "feat/" + name, State: st, Since: now.Add(-age), Last: last}
}

func TestEmptyListShowsHowToStart(t *testing.T) {
	f := &fakeSource{running: map[string]bool{}}
	m := started(f, 120, 10)
	ls := lines(m)
	if len(ls) != 10 {
		t.Fatalf("%d lines, want the pane's 10:\n%s", len(ls), strings.Join(ls, "\n"))
	}
	if !strings.HasPrefix(ls[0], "  hq  0 agents  view: attention") || !strings.HasSuffix(ls[0], "Sat 14:32  ⟳ 0s") {
		t.Errorf("header %q", ls[0])
	}
	for _, col := range []string{"TAB", "REPO", "BRANCH", "STATE ▾", "AGE", "LAST"} {
		if !strings.Contains(ls[2], col) {
			t.Errorf("column header %q lacks %s", ls[2], col)
		}
	}
	if !strings.Contains(ls[4], emptyText) || !strings.HasPrefix(ls[4], strings.Repeat(" ", 20)) {
		t.Errorf("empty text %q, want it centred", ls[4])
	}
}

func TestRowsInAttentionOrderWithCountsInTheHeader(t *testing.T) {
	f := &fakeSource{view: ViewAll, agents: []agent.Agent{
		ag("idm", state.Working, 3*time.Second, "Running mvn verify ..."),
		ag("spike", state.Ended, time.Hour, ""),
		ag("43", state.Done, 11*time.Minute, "PR #58 opened"),
		ag("42", state.NeedsInput, 2*time.Minute, "Which Entra tenant?"),
		ag("bok", state.Question, 6*time.Minute, "Changelog too?"),
	}}
	ls := lines(started(f, 140, 10))
	if want := "  hq  5 agents · 2 need you · 1 done · 1 working  view: all"; !strings.HasPrefix(ls[0], want) {
		t.Errorf("header %q, want prefix %q", ls[0], want)
	}
	for i, want := range []string{"42", "bok", "43", "idm", "spike"} {
		if f := strings.Fields(ls[3+i]); len(f) == 0 || f[0] != want {
			t.Errorf("row %d %q, want %s first", i, ls[3+i], want)
		}
	}
	for i, want := range []string{"● needs input  2m", "● question     6m", "● done         11m", "● working      3s", "● ended        1h"} {
		if !strings.Contains(ls[3+i], want) {
			t.Errorf("row %d %q lacks %q", i, ls[3+i], want)
		}
	}
	if !strings.Contains(ls[7], "[session ended]") || !strings.Contains(ls[3], "w/42-repo"[2:]) {
		t.Errorf("rows %q", ls[3:8])
	}
	if strings.TrimSpace(ls[9]) != "" {
		t.Errorf("scroll hint %q with every row shown", ls[9])
	}
}

func TestHeaderLeavesOutZeroCountsAndShowsTheUpdateHint(t *testing.T) {
	f := &fakeSource{agents: []agent.Agent{ag("a", state.Done, 0, "")}, hint: "v1.2.0 available - hq update"}
	h := lines(started(f, 120, 10))[0]
	if !strings.HasPrefix(h, "  hq  1 agent · 1 done  view: attention  v1.2.0 available - hq update") {
		t.Fatalf("header %q", h)
	}
}

func TestMoreAgentsThanRowsScrollWithAHint(t *testing.T) {
	var as []agent.Agent
	for i := range 8 {
		as = append(as, ag(fmt.Sprint("a", i), state.Working, time.Duration(i)*time.Minute, ""))
	}
	f := &fakeSource{agents: as, view: ViewAll}
	ls := lines(started(f, 100, 10))
	if !strings.HasSuffix(ls[9], "6 of 8  ▾ 2 more") || ansi.StringWidth(ls[9]) != 98 {
		t.Errorf("scroll hint %q", ls[9])
	}
	ls = lines(started(f, 100, 7))
	if len(ls) != 7 || !strings.Contains(ls[6], "3 of 8  ▾ 5 more") {
		t.Errorf("small window:\n%s", strings.Join(ls, "\n"))
	}
}

func TestLongCellsAreCutToTheLine(t *testing.T) {
	// The long row is below the cursor, whose strip would cover LAST.
	f := &fakeSource{view: ViewAll, agents: []agent.Agent{ag("cur", state.Working, 0, ""), {
		Name: "a-very-long-agent-name-indeed", RepoPath: "/w/r", Branch: "feat/" + strings.Repeat("b", 40),
		State: state.Working, Since: now.Add(-time.Minute), Last: strings.Repeat("words ", 40),
	}}}
	ls := lines(started(f, 120, 10))
	row := ls[4]
	if w := ansi.StringWidth(row); w > 120 {
		t.Errorf("row %d cells wide in 120: %q", w, row)
	}
	for _, want := range []string{"a-very-long-age…", "feat/bbbbbbbbbbbbbbbbbbbbbb…"} {
		if !strings.Contains(row, want) {
			t.Errorf("row %q lacks %q", row, want)
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(row), "…") {
		t.Errorf("LAST not cut: %q", row)
	}
}

func TestTicksRefreshFromTmuxAndSbxEverySecond(t *testing.T) {
	f := &fakeSource{running: map[string]bool{"claude-x": true}}
	m := started(f, 100, 10)
	collects, polls := f.collects, f.polls
	for range 3 {
		var cmd tea.Cmd
		m, cmd = update(m, tickMsg{})
		m = do(m, cmd)
	}
	if f.collects != collects+3 || f.polls != polls {
		t.Errorf("3 ticks: %d collects %d polls, want 3 and 0", f.collects-collects, f.polls-polls)
	}
	m, cmd := update(m, tickMsg{})
	do(m, cmd)
	if f.polls != polls+1 {
		t.Errorf("4th tick: %d polls, want 1", f.polls-polls)
	}
	if last := f.seen[len(f.seen)-1]; !last["claude-x"] {
		t.Errorf("sbx's answer not passed on: %v", last)
	}
}

func TestOneSbxPollAtATime(t *testing.T) {
	f := &fakeSource{}
	m := New(f.source())
	cmd := m.poll()
	if cmd == nil || m.poll() != nil {
		t.Fatal("a second poll started while one is under way")
	}
	m = do(m, cmd)
	if m.polling || m.poll() == nil {
		t.Fatal("no poll after the answer")
	}
}

func TestFailedSbxKeepsTheClockAndLeavesStatesToTmux(t *testing.T) {
	f := &fakeSource{runningErr: errors.New("sbx hangs")}
	m := started(f, 100, 10)
	if !m.polled.IsZero() || m.running != nil {
		t.Fatalf("polled %v running %v", m.polled, m.running)
	}
	if h := lines(m)[0]; strings.Contains(h, "⟳") {
		t.Errorf("header %q claims an sbx answer", h)
	}
}

func TestTmuxErrorIsShownAndTheRowsKept(t *testing.T) {
	f := &fakeSource{view: ViewAll, agents: []agent.Agent{ag("a", state.Working, 0, "")}}
	m := started(f, 100, 10)
	f.agentsErr = errors.New("tmux gone")
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	m = do(m, cmd)
	ls := lines(m)
	if !strings.Contains(ls[9], "hq: tmux gone") || !strings.Contains(ls[3], "a") {
		t.Fatalf("view:\n%s", strings.Join(ls, "\n"))
	}
}

func TestRRefreshesAtOnce(t *testing.T) {
	f := &fakeSource{}
	m := started(f, 100, 10)
	collects, polls := f.collects, f.polls
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	do(m, cmd)
	if f.collects <= collects || f.polls != polls+1 {
		t.Fatalf("r: %d collects %d polls", f.collects-collects, f.polls-polls)
	}
}

func TestQAndCtrlCQuit(t *testing.T) {
	for _, k := range []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune("q")}, {Type: tea.KeyCtrlC}} {
		_, cmd := update(New((&fakeSource{}).source()), k)
		if cmd == nil {
			t.Fatalf("%v: no command", k)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("%v does not quit", k)
		}
	}
}

func TestStartShowsTheFooterAndEveryResizeReappliesTheLayout(t *testing.T) {
	f := &fakeSource{}
	m := started(f, 130, 10)
	if got := fmt.Sprint(f.footer); got != "[{↑↓ /name select} {⏎ open session below} {n new} {k kill} {c code} {p pr} {s sort: attention} {a view: all} {r refresh} {q quit}]" {
		t.Errorf("footer %s", got)
	}
	// Narrower than the footer: open loses its words, so q quit stays.
	update(m, tea.WindowSizeMsg{Width: 110, Height: 12})
	if f.layouts != 2 {
		t.Errorf("%d layouts after two sizes", f.layouts)
	}
	if f.footer[1] != (Hint{"⏎", "open"}) || HintsWidth(f.footer) > 110 {
		t.Errorf("footer at 110: %v, %d cells", f.footer, HintsWidth(f.footer))
	}
}

func TestUpdateHintIsCheckedAgainHourly(t *testing.T) {
	f := &fakeSource{hint: "v2.0.0 available - hq update"}
	m, cmd := update(New(f.source()), hourMsg{})
	m = do(m, cmd)
	if m.hint != f.hint {
		t.Fatalf("hint %q", m.hint)
	}
}

// The threshold is the terminal's height, status line included: the
// classic 80x24 terminal shows 6 rows, one row less shows 3 (#79).
func TestRowsFollowTheTerminalHeight(t *testing.T) {
	for _, tc := range []struct{ terminal, rows, height int }{{50, 6, 10}, {25, 6, 10}, {24, 6, 10}, {23, 3, 7}, {20, 3, 7}} {
		if Rows(tc.terminal) != tc.rows || Height(tc.terminal) != tc.height {
			t.Errorf("terminal %d: %d rows in %d lines, want %d in %d", tc.terminal, Rows(tc.terminal), Height(tc.terminal), tc.rows, tc.height)
		}
	}
}

func key(m Model, k string) Model {
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	switch k {
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		msg = tea.KeyMsg{Type: tea.KeyUp}
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case "backspace":
		msg = tea.KeyMsg{Type: tea.KeyBackspace}
	}
	m, cmd := update(m, msg)
	return do(m, cmd)
}

func rowNames(m Model) string {
	var n []string
	for _, a := range m.rows {
		n = append(n, a.Name)
	}
	return strings.Join(n, " ")
}

func team() []agent.Agent {
	return []agent.Agent{
		ag("w1", state.Working, time.Minute, "Editing"),
		ag("ask", state.Question, 2*time.Minute, "Go on?"),
		ag("done", state.Done, time.Minute, "PR opened"),
		ag("perm", state.NeedsInput, time.Minute, "Allow?"),
		ag("end", state.Ended, time.Hour, ""),
	}
}

func TestAttentionViewShowsOnlyWhoNeedsYouAndCountsAll(t *testing.T) {
	f := &fakeSource{agents: team()}
	m := started(f, 120, 10)
	ls := lines(m)
	if !strings.HasPrefix(ls[0], "  hq  5 agents · 2 need you · 1 done · 1 working  view: attention") {
		t.Errorf("header %q", ls[0])
	}
	if rowNames(m) != "perm ask" {
		t.Fatalf("rows %q", rowNames(m))
	}
	if want := "  nothing else needs you · 3 more agents hidden · press a to show all"; ls[5] != want {
		t.Errorf("line after the rows %q, want %q", ls[5], want)
	}

	m = key(m, "a")
	if rowNames(m) != "perm ask done w1 end" || !strings.Contains(lines(m)[0], "view: all") {
		t.Fatalf("a: rows %q header %q", rowNames(m), lines(m)[0])
	}
	if len(f.saved) != 1 || f.saved[0] != "attention/all" {
		t.Errorf("kept %v", f.saved)
	}
	if got := f.footer[7]; got != (Hint{"a", "view: attention"}) {
		t.Errorf("footer after a: %v", got)
	}
	m = key(m, "a")
	if rowNames(m) != "perm ask" {
		t.Errorf("a again: %q", rowNames(m))
	}
}

func TestNothingNeedsYouSaysHowToSeeAll(t *testing.T) {
	f := &fakeSource{agents: []agent.Agent{ag("w1", state.Working, 0, "")}}
	ls := lines(started(f, 120, 10))
	if !strings.Contains(ls[4], nothingText) || strings.Contains(strings.Join(ls, "\n"), "w1") {
		t.Fatalf("view:\n%s", strings.Join(ls, "\n"))
	}
}

func TestAttentionViewKeepsTheDockedRowInAnyState(t *testing.T) {
	for _, st := range []string{state.Working, state.Starting, state.Done, state.Ended, state.Question} {
		as := append(team(), ag("dock", st, 30*time.Second, "On screen"))
		as[len(as)-1].Docked = true
		if got, want := names(Arrange(as, SortAttention, ViewAttention)), map[string]string{
			state.Working: "perm ask dock", state.Starting: "perm ask dock", state.Done: "perm ask dock",
			state.Ended: "perm ask dock", state.Question: "perm dock ask",
		}[st]; got != want {
			t.Errorf("docked %s: rows %q, want %q", st, got, want)
		}
	}
	// Drawn with its outline; w1, done and end stay hidden.
	as := team()
	as[0].Docked = true // w1, working
	m := started(&fakeSource{agents: as}, 120, 10)
	if rowNames(m) != "perm ask w1" {
		t.Fatalf("rows %q", rowNames(m))
	}
	ls := lines(m)
	if !strings.HasPrefix(ls[5], "│ w1") || !strings.HasSuffix(ls[5], " │") {
		t.Errorf("docked row %q, want the outline", ls[5])
	}
	if want := "  nothing else needs you · 2 more agents hidden · press a to show all"; ls[6] != want {
		t.Errorf("line after the rows %q, want %q", ls[6], want)
	}
	// The chords go from the docked row as the list shows it.
	if got, _ := Neighbour(Arrange(as, SortAttention, ViewAttention), "w1", "perm", -1); got != "ask" {
		t.Errorf("Alt+k from the docked w1: %q, want ask", got)
	}
}

func TestTheDockedAgentAloneIsShownNotNothingNeedsYou(t *testing.T) {
	a := ag("x", state.Working, 0, "3")
	a.Docked = true
	ls := lines(started(&fakeSource{agents: []agent.Agent{a}}, 120, 10))
	view := strings.Join(ls, "\n")
	if !strings.HasPrefix(ls[3], "│ x") || strings.Contains(view, nothingText) || strings.Contains(view, "hidden") {
		t.Fatalf("view:\n%s", view)
	}
}

func TestSortCyclesAndIsMarkedInTheColumnHeader(t *testing.T) {
	as := []agent.Agent{
		{Name: "b", RepoPath: "/w/zeta", Branch: "feat/b", State: state.Working, Since: now.Add(-time.Hour)},
		{Name: "a", RepoPath: "/w/zeta", Branch: "feat/a", State: state.Working, Since: now},
		{Name: "c", RepoPath: "/w/alpha", Branch: "main", State: state.Done, Since: now},
		{Name: "d", RepoPath: "/w/zeta", Branch: "fix/d", State: state.Question, Since: now},
	}
	f := &fakeSource{agents: as, view: ViewAll}
	m := started(f, 120, 10)
	for _, tc := range []struct{ sort, rows, marked string }{
		{SortAttention, "d c a b", "TAB REPO BRANCH STATE ▾ AGE"},
		{SortRepo, "c d a b", "TAB REPO ▾ BRANCH STATE AGE"},
		{SortState, "d c a b", "TAB ▾ REPO BRANCH STATE ▾ AGE"},
	} {
		if m.sort != tc.sort || rowNames(m) != tc.rows {
			t.Errorf("sort %s: rows %q, want %s %q", m.sort, rowNames(m), tc.sort, tc.rows)
		}
		if got := strings.Join(strings.Fields(lines(m)[2]), " "); !strings.HasPrefix(got, tc.marked) {
			t.Errorf("sort %s: column header %q, want %q", tc.sort, got, tc.marked)
		}
		if got := f.footer[6]; got != (Hint{"s", "sort: " + tc.sort}) {
			t.Errorf("footer %v", got)
		}
		m = key(m, "s")
	}
	if m.sort != SortAttention || strings.Join(f.saved, " ") != "repo/all state/all attention/all" {
		t.Errorf("after a full cycle: %s, kept %v", m.sort, f.saved)
	}
	// S11: agents of one repository are told apart by branch.
	ls := lines(m)
	if !strings.Contains(ls[5], "feat/a") || !strings.Contains(ls[6], "feat/b") {
		t.Errorf("rows %q", ls[3:7])
	}
}

func TestModesStartAsTheUserLeftThem(t *testing.T) {
	m := New((&fakeSource{sort: SortState, view: ViewAll}).source())
	if m.sort != SortState || m.view != ViewAll {
		t.Errorf("modes %s %s", m.sort, m.view)
	}
	m = New((&fakeSource{sort: "age", view: "some"}).source())
	if m.sort != SortAttention || m.view != ViewAttention {
		t.Errorf("unknown modes gave %s %s, want the defaults", m.sort, m.view)
	}
}

func TestCursorMovesAndFollowsItsAgent(t *testing.T) {
	f := &fakeSource{agents: team(), view: ViewAll}
	m := started(f, 120, 10)
	if m.cursor != "perm" {
		t.Fatalf("cursor starts on %q", m.cursor)
	}
	if m = key(m, "j"); m.cursor != "perm" {
		t.Fatalf("j is not a movement key (k is kill): %q", m.cursor)
	}
	m = key(key(key(m, "down"), "down"), "up")
	if m.cursor != "ask" {
		t.Fatalf("after down down up: %q", m.cursor)
	}
	m = key(key(m, "up"), "up")
	if m.cursor != "perm" || m.cursorRow != 0 {
		t.Fatalf("above the top: %q %d", m.cursor, m.cursorRow)
	}
	m = key(key(key(key(key(key(m, "down"), "down"), "down"), "down"), "down"), "down")
	if m.cursor != "end" {
		t.Fatalf("below the bottom: %q", m.cursor)
	}

	// w1 asks a question: rows reshuffle, the cursor stays on its agent.
	m = key(key(m, "up"), "up") // on done
	f.agents[0].State = state.Question
	m = key(m, "r")
	if rowNames(m) != "perm w1 ask done end" || m.cursor != "done" || m.cursorRow != 3 {
		t.Fatalf("rows %q cursor %q at %d", rowNames(m), m.cursor, m.cursorRow)
	}
	cursorLine := lines(m)[3+3]
	if !strings.Contains(cursorLine, "done") {
		t.Errorf("cursor row shows %q", cursorLine)
	}

	// Its agent leaves the view: the cursor takes the nearest row.
	m = key(m, "a") // attention: perm w1 ask
	if m.cursor != "ask" || m.cursorRow != 2 {
		t.Fatalf("after leaving the view: %q at %d", m.cursor, m.cursorRow)
	}
	f.agents = nil
	m = key(m, "r")
	if m.cursor != "ask" || m.cursorRow != -1 {
		t.Fatalf("no rows: %q at %d, want no row and the name kept", m.cursor, m.cursorRow)
	}
	m = key(key(m, "down"), "up") // no rows: nothing to move to
	f.agents = team()
	if m = key(m, "r"); m.cursorRow < 0 || m.rows[m.cursorRow].Name != "ask" {
		t.Fatalf("agent back: %q at %d", m.cursor, m.cursorRow)
	}
}

func TestCursorScrollsTheList(t *testing.T) {
	var as []agent.Agent
	for i := range 8 {
		as = append(as, ag(fmt.Sprint("a", i), state.Working, time.Duration(i)*time.Minute, ""))
	}
	f := &fakeSource{agents: as, view: ViewAll}
	m := started(f, 100, 10)
	for range 6 {
		m = key(m, "down")
	}
	ls := lines(m)
	if m.offset != 1 || !strings.HasSuffix(ls[9], "6 of 8  ▴ 1 more  ▾ 1 more") || !strings.Contains(ls[8], "a6") || !strings.Contains(ls[3], "a1") {
		t.Fatalf("offset %d:\n%s", m.offset, strings.Join(ls, "\n"))
	}
	m = key(m, "down")
	if ls = lines(m); !strings.HasSuffix(ls[9], "6 of 8  ▴ 2 more") {
		t.Errorf("at the bottom: %q", ls[9])
	}
	for range 7 {
		m = key(m, "up")
	}
	if m.offset != 0 || m.cursor != "a0" {
		t.Errorf("back at the top: offset %d cursor %q", m.offset, m.cursor)
	}
	// A smaller window keeps the cursor in sight.
	m = key(key(key(key(key(m, "down"), "down"), "down"), "down"), "down") // a5
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 7})
	if m.offset != 3 {
		t.Errorf("3 rows with the cursor on a5: offset %d", m.offset)
	}
}

func TestCursorRowHasABackground(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	f := &fakeSource{agents: team(), view: ViewAll}
	m := key(started(f, 120, 10), "down")
	rows := strings.Split(m.View(), "\n")[3:5]
	if strings.Contains(rows[0], "48;2;") || !strings.Contains(rows[1], "48;2;") {
		t.Fatalf("background on the wrong row:\n%q\n%q", rows[0], rows[1])
	}
}

func TestCursorIsKeptForTheNextListProgram(t *testing.T) {
	f := &fakeSource{agents: team(), view: ViewAll, cursor: "done"}
	m := started(f, 120, 10)
	if m.cursor != "done" || m.cursorRow != 2 {
		t.Fatalf("cursor %q at %d, want done at 2", m.cursor, m.cursorRow)
	}
	sets := f.cursorSets
	m = key(m, "down")
	if f.cursor != "w1" || f.cursorSets != sets+1 {
		t.Fatalf("kept %q after %d sets", f.cursor, f.cursorSets-sets)
	}
	key(m, "r")
	if f.cursorSets != sets+1 {
		t.Errorf("a refresh that keeps the cursor wrote it again")
	}
}

func TestKeptCursorSurvivesTheSizeArrivingFirst(t *testing.T) {
	f := &fakeSource{agents: team(), view: ViewAll, cursor: "done"}
	var m tea.Model = New(f.source())
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 10})
	m, _ = m.Update(agentsMsg{f.agents, nil})
	if got := m.(Model); got.cursor != "done" || got.cursorRow != 2 || f.cursor != "done" {
		t.Fatalf("cursor %q at %d, kept %q", got.cursor, got.cursorRow, f.cursor)
	}
}

func TestEnterDocksTheCursorRowAndTheListMarksIt(t *testing.T) {
	f := &fakeSource{agents: team(), view: ViewAll}
	m := key(key(started(f, 120, 10), "down"), "enter") // perm ask done w1 end
	if len(f.docked) != 1 || f.docked[0] != "ask" {
		t.Fatalf("docked %v, want ask", f.docked)
	}
	if !m.rows[1].Docked {
		t.Fatalf("the list does not know ask is docked: %+v", m.rows[1])
	}
	// The cursor moves on; the docked row keeps its outline.
	m = key(m, "down")
	ls := lines(m)
	if !strings.HasPrefix(ls[4], "│ ask") || !strings.HasSuffix(ls[4], " │") || strings.Contains(ls[5], "│") {
		t.Fatalf("outline on the wrong row:\n%s\n%s", ls[4], ls[5])
	}
	if m.cursor != "done" {
		t.Errorf("cursor %q", m.cursor)
	}
	if n, o := ansi.StringWidth(ls[4]), ansi.StringWidth(ls[3]); n != o {
		t.Errorf("outlined row is %d wide, others %d", n, o)
	}
}

func TestEnterWithNothingToDockDoesNothing(t *testing.T) {
	f := &fakeSource{}
	key(started(f, 120, 10), "enter")
	if len(f.docked) != 0 {
		t.Fatalf("docked %v", f.docked)
	}
}

func TestAFailedDockIsSaidUntilTheNextKey(t *testing.T) {
	f := &fakeSource{agents: team(), view: ViewAll, dockErr: errors.New("no dashboard window")}
	m := key(started(f, 120, 10), "enter")
	if ls := lines(m); ls[len(ls)-1] != "  hq: no dashboard window" {
		t.Fatalf("footer line %q", ls[len(ls)-1])
	}
	m = key(m, "r")
	if ls := lines(m); strings.Contains(ls[len(ls)-1], "hq:") {
		t.Fatalf("error still shown: %q", ls[len(ls)-1])
	}
}

func TestNOpensTheNewAgentDialogWithTheCursorRowsRepo(t *testing.T) {
	f := &fakeSource{agents: team(), view: ViewAll}
	key(key(started(f, 120, 10), "down"), "n") // perm ask done w1 end
	if len(f.dialogs) != 1 || f.dialogs[0] != "/w/ask-repo" {
		t.Fatalf("dialogs %q", f.dialogs)
	}
	f = &fakeSource{}
	key(started(f, 120, 10), "n")
	if len(f.dialogs) != 1 || f.dialogs[0] != "" {
		t.Fatalf("with no rows: dialogs %q, want where hq started", f.dialogs)
	}
}

// fresh is an agent just started with hq new, before its first report.
func fresh(name string) agent.Agent {
	a := ag(name, state.Starting, 0, "")
	a.ID, a.New = name+"-id", true
	return a
}

func TestANewAgentGetsTheCursorAndIsDockedWhenNothingIs(t *testing.T) {
	f := &fakeSource{agents: team()}
	for i := range f.agents {
		f.agents[i].ID = f.agents[i].Name + "-id"
	}
	m := started(f, 120, 10)
	f.onDialog = func() { f.agents = append(f.agents, fresh("n1")) }
	m = key(m, "n")
	if m.cursor != "n1" || len(f.docked) != 1 || f.docked[0] != "n1" {
		t.Fatalf("cursor %q, docked %v", m.cursor, f.docked)
	}
	// Shown in the attention view while new, with the marker.
	if !strings.Contains(rowNames(m), "n1") {
		t.Fatalf("rows %q", rowNames(m))
	}
	var row string
	for _, l := range lines(m) {
		if strings.Contains(l, "n1") {
			row = l
		}
	}
	if !strings.Contains(row, "n1 new") || !strings.Contains(row, "● starting") {
		t.Fatalf("row %q", row)
	}

	// Another one, from hq new in another shell: the cursor, not the dock.
	f.agents = append(f.agents, fresh("n2"))
	m = key(m, "r")
	if m.cursor != "n2" || len(f.docked) != 1 {
		t.Fatalf("cursor %q, docked %v", m.cursor, f.docked)
	}
	// The cursor stays free to move: the agent is welcomed once.
	m = key(key(m, "up"), "r")
	if m.cursor == "n2" {
		t.Fatal("the cursor went back to n2")
	}

	// Its first report ends the marker, and the attention view lets it go.
	f.agents[len(f.agents)-1].New, f.agents[len(f.agents)-1].State = false, state.Working
	m = key(m, "r")
	if strings.Contains(rowNames(m), "n2") {
		t.Fatalf("rows %q", rowNames(m))
	}
}

func TestAgentsRunningWhenTheListStartsAreNotWelcomed(t *testing.T) {
	f := &fakeSource{agents: []agent.Agent{fresh("a"), fresh("b")}}
	m := started(f, 120, 10)
	if len(f.docked) != 0 || m.cursor != "a" {
		t.Fatalf("docked %v, cursor %q", f.docked, m.cursor)
	}
}

func TestTheNewMarkerFitsALongName(t *testing.T) {
	long := fresh(strings.Repeat("x", 20))
	f := &fakeSource{agents: []agent.Agent{long, ag("b", state.Working, 0, "")}, view: ViewAll}
	ls := lines(started(f, 120, 10))
	if !strings.Contains(ls[4], "… new ") { // after b, which works
		t.Fatalf("row %q", ls[4])
	}
	if a, b := ansi.StringWidth(ls[3]), ansi.StringWidth(ls[4]); a != b {
		t.Fatalf("rows %d and %d wide", a, b)
	}
}

func TestAClosedDialogsErrorIsSaid(t *testing.T) {
	f := &fakeSource{}
	src := f.source()
	src.NewAgent = func(string) error { return errors.New("no hq executable") }
	m := New(src)
	m = do(m, m.Init())
	m = key(m, "n")
	if ls := lines(m); ls[len(ls)-1] != "  hq: no hq executable" {
		t.Fatalf("footer line %q", ls[len(ls)-1])
	}
}

func TestANewAgentThatFirstLooksEndedIsStillWelcomed(t *testing.T) {
	f := &fakeSource{}
	m := started(f, 120, 10)
	// sbx's last answer predates the agent's sandbox: ended for a moment.
	a := fresh("a")
	a.New, a.State = false, state.Ended
	f.agents = []agent.Agent{a}
	m = key(m, "r")
	f.agents[0].New, f.agents[0].State = true, state.Starting
	m = key(m, "r")
	if m.cursor != "a" || len(f.docked) != 1 {
		t.Fatalf("cursor %q, docked %v", m.cursor, f.docked)
	}
}

func TestTheListTakesNoKeysWhileADialogIsOpen(t *testing.T) {
	f := &fakeSource{agents: team(), view: ViewAll}
	m := started(f, 120, 10)
	m, open := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	// Typed before tmux shows the popup: neither q nor Enter reach the list.
	for _, k := range []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune("q")}, {Type: tea.KeyEnter}, {Type: tea.KeyDown}} {
		var cmd tea.Cmd
		if m, cmd = update(m, k); cmd != nil || m.cursorRow != 0 {
			t.Fatalf("%v reached the list", k)
		}
	}
	m = do(m, open) // the dialog closes
	if m = key(m, "down"); m.cursorRow != 1 {
		t.Fatal("keys still ignored after the dialog closed")
	}
}

func TestKOpensTheKillDialogOnTheCursorRow(t *testing.T) {
	f := &fakeSource{agents: team(), view: ViewAll}
	m := key(started(f, 120, 10), "down") // perm ask done w1 end
	// Yes: the agent is gone on the list's next refresh, the cursor takes
	// the nearest row.
	f.onKill = func() { f.agents = remove(f.agents, "ask") }
	m = key(m, "k")
	if strings.Join(f.killed, " ") != "ask" || rowNames(m) != "perm done w1 end" || m.cursor != "done" {
		t.Fatalf("killed %v, rows %q, cursor %q", f.killed, rowNames(m), m.cursor)
	}
	// No: nothing changes.
	f.onKill = nil
	if m = key(m, "k"); len(f.killed) != 2 || rowNames(m) != "perm done w1 end" {
		t.Fatalf("killed %v, rows %q", f.killed, rowNames(m))
	}
	// With no rows there is nothing to kill.
	f = &fakeSource{}
	if key(started(f, 120, 10), "k"); len(f.killed) != 0 {
		t.Fatalf("killed %v", f.killed)
	}
}

// four are working agents in attention order: w0 w1 w2 w3.
func four() []agent.Agent {
	var as []agent.Agent
	for i := range 4 {
		as = append(as, ag(fmt.Sprint("w", i), state.Working, time.Duration(i)*time.Minute, ""))
	}
	return as
}

// onRow moves the cursor down to the row of name.
func onRow(m Model, name string) Model {
	for m.cursor != name && m.cursorRow+1 < len(m.rows) {
		m = key(m, "down")
	}
	return m
}

func TestAKilledAgentsRowGivesTheCursorToTheRowThatTookItsPlace(t *testing.T) {
	// A kill ends the session a moment before it removes the row, so a
	// refresh may see the row ended, at the bottom, before it goes.
	for _, tc := range []struct{ killed, want string }{{"w0", "w1"}, {"w1", "w2"}, {"w3", "w2"}} {
		f := &fakeSource{agents: four(), view: ViewAll}
		m := onRow(started(f, 120, 10), tc.killed)
		i := slices.IndexFunc(f.agents, func(a agent.Agent) bool { return a.Name == tc.killed })
		f.agents[i].State = state.Ended
		if m = key(m, "r"); m.cursor != tc.killed || m.cursorRow != 3 {
			t.Fatalf("%s ended: cursor %q at %d, want it followed to the bottom", tc.killed, m.cursor, m.cursorRow)
		}
		f.agents = remove(f.agents, tc.killed)
		if m = key(m, "r"); m.cursor != tc.want {
			t.Errorf("%s removed: cursor on %q, want %q (rows %s)", tc.killed, m.cursor, tc.want, rowNames(m))
		}
	}
}

func TestKillingARowWithoutAnEndedPhaseGivesTheCursorToTheNextRow(t *testing.T) {
	for _, tc := range []struct{ killed, want string }{{"w0", "w1"}, {"w2", "w3"}, {"w3", "w2"}} {
		f := &fakeSource{agents: four(), view: ViewAll}
		m := onRow(started(f, 120, 10), tc.killed)
		f.onKill = func() { f.agents = remove(f.agents, tc.killed) }
		if m = key(m, "k"); m.cursor != tc.want {
			t.Errorf("%s killed: cursor on %q, want %q", tc.killed, m.cursor, tc.want)
		}
	}
}

func TestKOnAnEndedRowGivesTheCursorToTheRowAfterIt(t *testing.T) {
	// S7: the rows ended by themselves, long before.
	as := four()
	as[2].State, as[3].State = state.Ended, state.Ended // w0 w1 w2 w3
	for _, tc := range []struct{ killed, want string }{{"w2", "w3"}, {"w3", "w2"}} {
		f := &fakeSource{agents: slices.Clone(as), view: ViewAll}
		m := onRow(started(f, 120, 10), tc.killed)
		f.onKill = func() { f.agents = remove(f.agents, tc.killed) }
		if m = key(m, "k"); m.cursor != tc.want {
			t.Errorf("%s killed: cursor on %q, want %q", tc.killed, m.cursor, tc.want)
		}
	}
	// w0 ends by itself as the user looks at it, and is killed at once:
	// the cursor is where the user sees it, at the bottom, so it takes the
	// row before.
	f := &fakeSource{agents: four(), view: ViewAll}
	m := started(f, 120, 10)
	f.agents[0].State = state.Ended
	m = key(m, "r") // w1 w2 w3 w0
	f.onKill = func() { f.agents = remove(f.agents, "w0") }
	if m = key(m, "k"); m.cursor != "w3" {
		t.Errorf("w0 killed from the bottom: cursor on %q, want w3", m.cursor)
	}
}

func TestARowEndedLongAgoKeepsTheCursorWhereItIs(t *testing.T) {
	f := &fakeSource{agents: four(), view: ViewAll}
	m := started(f, 120, 10)
	f.agents[0].State = state.Ended
	m = key(m, "r") // w1 w2 w3 w0, the cursor on w0
	later := now.Add(endedGrace)
	m.src.Now = func() time.Time { return later }
	m = key(m, "r")
	f.agents = remove(f.agents, "w0") // hq kill from a shell
	if m = key(m, "r"); m.cursor != "w3" {
		t.Errorf("cursor on %q, want w3, the row before w0 at the bottom", m.cursor)
	}
}

func remove(as []agent.Agent, name string) []agent.Agent {
	var out []agent.Agent
	for _, a := range as {
		if a.Name != name {
			out = append(out, a)
		}
	}
	return out
}

func TestCOpensTheEditorOnTheCursorRow(t *testing.T) {
	f := &fakeSource{agents: team(), view: ViewAll}
	m := key(key(started(f, 120, 10), "down"), "c") // perm ask done w1 end
	if strings.Join(f.coded, " ") != "ask" {
		t.Fatalf("opened %v", f.coded)
	}
	f.codeErr = errors.New("VS Code's code command not found")
	if ls := lines(key(m, "c")); ls[len(ls)-1] != "  hq: VS Code's code command not found" {
		t.Fatalf("footer line %q", ls[len(ls)-1])
	}
	f = &fakeSource{}
	if key(started(f, 120, 10), "c"); len(f.coded) != 0 {
		t.Fatalf("with no rows: opened %v", f.coded)
	}
}

// typed feeds text to the list one key at a time, as a person types it.
func typed(m Model, text string) Model {
	for _, r := range text {
		m = key(m, string(r))
	}
	return m
}

func TestSlashSelectsByName(t *testing.T) {
	as := []agent.Agent{ag("bok-17", state.Question, 0, ""), ag("42", state.NeedsInput, 0, ""), ag("Bot", state.Working, 0, ""), ag("spike", state.Ended, 0, "")}
	f := &fakeSource{agents: as, view: ViewAll} // 42 bok-17 Bot spike
	m := key(started(f, 120, 10), "/")
	if got := fmt.Sprint(f.footer); got != "[{/ type a name} {⏎ open} {esc cancel}]" {
		t.Fatalf("footer %s", got)
	}
	// S3b: /bo matches two, case-insensitive; /bok one; the cursor follows.
	m = typed(m, "bo")
	if m.cursor != "bok-17" || fmt.Sprint(f.footer[0]) != "{/bo 2 matches: bok-17 Bot}" {
		t.Fatalf("cursor %q footer %v", m.cursor, f.footer)
	}
	m = typed(m, "k")
	if fmt.Sprint(f.footer[0]) != "{/bok 1 match: bok-17}" {
		t.Fatalf("footer %v", f.footer)
	}
	// Letters are the search's: k, n, q type into it.
	m = typed(m, "q")
	if fmt.Sprint(f.footer[0]) != "{/bokq no match}" || m.cursor != "bok-17" || len(f.killed)+len(f.dialogs) != 0 {
		t.Fatalf("footer %v cursor %q", f.footer, m.cursor)
	}
	m = key(m, "backspace")
	m = key(m, "enter")
	if strings.Join(f.docked, " ") != "bok-17" || m.searching || fmt.Sprint(f.footer[0]) != "{↑↓ /name select}" {
		t.Fatalf("docked %v searching %v footer %v", f.docked, m.searching, f.footer)
	}
	// Esc clears; Enter with no match docks nothing.
	m = key(typed(key(m, "/"), "sp"), "esc")
	if m.searching || m.cursor != "spike" || fmt.Sprint(f.footer[0]) != "{↑↓ /name select}" {
		t.Fatalf("after esc: searching %v cursor %q", m.searching, m.cursor)
	}
	key(typed(key(m, "/"), "zz"), "enter")
	if len(f.docked) != 1 {
		t.Fatalf("docked %v", f.docked)
	}
}

func TestSearchLooksAtTheRowsShown(t *testing.T) {
	f := &fakeSource{agents: team()} // attention view: perm ask
	m := typed(key(started(f, 120, 10), "/"), "w")
	if fmt.Sprint(f.footer[0]) != "{/w no match}" || m.cursor != "perm" {
		t.Fatalf("footer %v cursor %q", f.footer, m.cursor)
	}
}

func TestTheCursorRowAloneCarriesTheActionStrip(t *testing.T) {
	f := &fakeSource{agents: team(), view: ViewAll}
	m := started(f, 120, 10)
	ls := lines(m)
	// perm has the cursor: the strip on its tail, right-aligned, without pr.
	if !strings.HasSuffix(ls[3], "⏎ open   c code   k kill   ") || strings.Contains(ls[3], " pr ") {
		t.Fatalf("cursor row %q", ls[3])
	}
	for _, l := range ls[4:8] {
		if strings.Contains(l, "⏎ open") {
			t.Fatalf("strip on another row: %q", l)
		}
	}
	// Columns stay put: the cursor row starts as it would without the strip.
	plain := ansi.Strip(m.columns().row(m.rows[0], now, true, ""))
	if cut := len([]rune(ls[3])) - len([]rune("⏎ open   c code   k kill   ")) - 1; string([]rune(ls[3])[:cut]) != string([]rune(plain)[:cut]) {
		t.Fatalf("columns moved:\n%q\n%q", ls[3], plain)
	}
	if w := ansi.StringWidth(ls[3]); w != 120 {
		t.Fatalf("cursor row %d cells", w)
	}
	// The strip follows the cursor.
	ls = lines(key(m, "down"))
	if strings.Contains(ls[3], "⏎ open") || !strings.Contains(ls[4], "⏎ open") {
		t.Fatalf("strip did not follow:\n%s", strings.Join(ls, "\n"))
	}
}

func TestPrShowsAndOpensWithAPullRequestOnly(t *testing.T) {
	as := team() // perm has the cursor, on feat/perm in /w/perm-repo
	f := &fakeSource{agents: as, view: ViewAll, prs: map[string]map[string]gh.PR{
		"/w/perm-repo": {"feat/perm": {Number: 7, Branch: "feat/perm", URL: "https://github.com/o/r/pull/7"}},
	}}
	m := started(f, 120, 10)
	if ls := lines(m); !strings.HasSuffix(ls[3], "⏎ open   c code   p pr   k kill   ") {
		t.Fatalf("cursor row %q", ls[3])
	}
	key(m, "p")
	if strings.Join(f.browsed, " ") != "https://github.com/o/r/pull/7" {
		t.Fatalf("browsed %q", f.browsed)
	}
	// ask has no pull request: p says so in the footer line, until a key.
	m = key(key(m, "down"), "p")
	if ls := lines(m); !strings.Contains(ls[len(ls)-1], "hq: no pull request for feat/ask") || len(f.browsed) != 1 {
		t.Fatalf("footer line %q, browsed %q", ls[len(ls)-1], f.browsed)
	}
	if ls := lines(key(m, "up")); ls[len(ls)-1] != "" {
		t.Fatalf("footer line kept %q", ls[len(ls)-1])
	}
}

func TestGhFailingOnlyHidesPr(t *testing.T) {
	f := &fakeSource{agents: team(), view: ViewAll, prErr: errors.New("gh: not logged in"),
		prs: map[string]map[string]gh.PR{"/w/perm-repo": {"feat/perm": {Number: 7}}}}
	ls := lines(started(f, 120, 10))
	if strings.Contains(ls[3], " pr ") || ls[len(ls)-1] != "" {
		t.Fatalf("cursor row %q, footer line %q", ls[3], ls[len(ls)-1])
	}
}

func TestPullRequestsAreAskedAtStartEveryMinuteAndOnDone(t *testing.T) {
	as := []agent.Agent{ag("a", state.Working, 0, ""), ag("b", state.Working, 0, ""), ag("c", state.Working, 0, "")}
	as[0].ID, as[1].ID, as[2].ID = "ia", "ib", "ic"
	as[1].RepoPath = as[0].RepoPath // a and b share a repository
	f := &fakeSource{agents: as, view: ViewAll}
	m := started(f, 120, 10)
	if got := f.askedAbout(); got != "/w/a-repo /w/c-repo" {
		t.Fatalf("at start: %q", got)
	}
	m, _ = update(m, agentsMsg{agents: as})
	if got := f.askedAbout(); got != "" {
		t.Fatalf("asked again on a refresh: %q", got)
	}
	// b turns done: its repository is asked at once, once.
	as[1].State = state.Done
	m = do(update(m, agentsMsg{agents: as}))
	m = do(update(m, agentsMsg{agents: as}))
	if got := f.askedAbout(); got != "/w/a-repo" {
		t.Fatalf("on done: %q", got)
	}
	m, cmd := update(m, prTickMsg{})
	for _, msg := range msgs(cmd) {
		if _, ok := msg.(prsMsg); ok {
			m, _ = update(m, msg)
		}
	}
	if got := f.askedAbout(); got != "/w/a-repo /w/c-repo" {
		t.Fatalf("every minute: %q", got)
	}
}

func names(as []agent.Agent) string {
	var ns []string
	for _, a := range as {
		ns = append(ns, a.Name)
	}
	return strings.Join(ns, " ")
}

func TestChordsDockTheRowsAsTheListShowsThem(t *testing.T) {
	rows := Arrange(team(), SortAttention, ViewAll)
	if got := names(rows); got != "perm ask done w1 end" {
		t.Fatalf("rows %s", got)
	}
	if got := names(Arrange(team(), "bogus", "bogus")); got != "perm ask" {
		t.Fatalf("unknown modes: %s", got)
	}
	for _, tc := range []struct {
		docked, cursor string
		step           int
		want           string
	}{
		{"ask", "ask", 1, "done"},
		{"ask", "w1", -1, "perm"},
		{"end", "end", 1, ""},       // the last row: nothing after it
		{"perm", "", -1, ""},        // the first: nothing before it
		{"gone", "done", 1, "done"}, // docked not shown: from the cursor
		{"gone", "done", -1, "ask"},
		{"", "", 1, "perm"}, // nothing to go by: the ends
		{"", "", -1, "end"},
	} {
		got, ok := Neighbour(rows, tc.docked, tc.cursor, tc.step)
		if got != tc.want || ok != (tc.want != "") {
			t.Errorf("docked %q cursor %q step %d: %q %v, want %q", tc.docked, tc.cursor, tc.step, got, ok, tc.want)
		}
	}
	if _, ok := Neighbour(nil, "", "", 1); ok {
		t.Error("no rows gave a neighbour")
	}
}

func TestAltADocksTheFirstNeedingYouButNotTheDockedOne(t *testing.T) {
	for _, tc := range []struct{ docked, want string }{{"", "perm"}, {"w1", "perm"}, {"perm", "ask"}} {
		if got, _ := FirstNeedingYou(team(), tc.docked); got != tc.want {
			t.Errorf("docked %q: %q, want %q", tc.docked, got, tc.want)
		}
	}
	if got, ok := FirstNeedingYou([]agent.Agent{ag("w1", state.Working, 0, ""), ag("x", state.Question, 0, "")}, "x"); ok {
		t.Errorf("only the docked agent needs you: %q", got)
	}
}

func TestTheCursorFollowsAnAgentDockedElsewhere(t *testing.T) {
	as := team()
	as[2].Docked = true // done
	f := &fakeSource{agents: as, view: ViewAll, cursor: "w1"}
	m := started(f, 120, 10)
	if m.cursor != "w1" {
		t.Fatalf("the docked agent took the cursor at start: %q", m.cursor)
	}
	// A chord or hq go docks ask.
	as[2].Docked, as[1].Docked = false, true
	m, _ = update(m, agentsMsg{agents: as})
	if m.cursor != "ask" || f.cursor != "ask" {
		t.Fatalf("cursor %q kept %q, want ask", m.cursor, f.cursor)
	}
	// The cursor moved on by hand stays while ask stays docked.
	m = key(m, "down")
	m, _ = update(m, agentsMsg{agents: as})
	if m.cursor == "ask" {
		t.Fatal("cursor pulled back to the docked agent")
	}
	// Nothing docked (killed): the cursor stays.
	as[1].Docked = false
	before := m.cursor
	if m, _ = update(m, agentsMsg{agents: as}); m.cursor != before {
		t.Fatalf("cursor %q, want %q", m.cursor, before)
	}
}

// narrowTeam is team with one long repository and branch, as in
// dash-s12-narrow.png, and a pull request for the cursor row.
func narrowTeam() *fakeSource {
	as := team()
	as[3].RepoPath, as[3].Branch = "/w/shop-portal", "feat/42-partner-login-with-entra"
	return &fakeSource{agents: as, view: ViewAll, prs: map[string]map[string]gh.PR{"/w/shop-portal": {"feat/42-partner-login-with-entra": {Number: 42, URL: "u"}}}}
}

func TestColumnsGiveWayByWidth(t *testing.T) {
	for _, tc := range []struct {
		width      int
		repo, last bool
		strip      string
	}{
		{120, true, true, "⏎ open   c code   p pr   k kill"},
		{100, true, true, "⏎ open   c code   p pr   k kill"},
		{99, true, false, "⏎   c   p   ✕"},
		{80, true, false, "⏎   c   p   ✕"},
		{79, false, false, "⏎   c   p   ✕"},
		{60, false, false, "⏎   c   p   ✕"},
	} {
		m := started(narrowTeam(), tc.width, 12)
		ls := lines(m)
		head := ls[2]
		if strings.Contains(head, "REPO") != tc.repo || strings.Contains(head, "LAST") != tc.last {
			t.Errorf("%d: header %q, want REPO %v LAST %v", tc.width, head, tc.repo, tc.last)
		}
		for i, l := range ls {
			if w := ansi.StringWidth(l); w > tc.width {
				t.Errorf("%d: line %d is %d wide: %q", tc.width, i, w, l)
			}
		}
		// The cursor row (perm, needing input, first) carries the strip at
		// its end; its columns stay where the header puts them.
		row := ls[3]
		if !strings.HasSuffix(strings.TrimRight(row, " "), tc.strip) {
			t.Errorf("%d: cursor row %q, want the strip %q", tc.width, row, tc.strip)
		}
		// Right-aligned at every width, a margin from the edge (the last
		// chip ends in a space of its own).
		if w := ansi.StringWidth(row); w != tc.width {
			t.Errorf("%d: row %d wide", tc.width, w)
		}
		if w := ansi.StringWidth(strings.TrimRight(row, " ")); w != tc.width-3 {
			t.Errorf("%d: strip ends at %d, want %d", tc.width, w, tc.width-3)
		}
		if at := strings.Index(head, "BRANCH"); !strings.HasPrefix(row[at:], "feat/42") {
			t.Errorf("%d: branch moved:\n%s\n%s", tc.width, head, row)
		}
		if tc.width < 120 && !strings.Contains(row, "…") {
			t.Errorf("%d: long branch not cut with …: %q", tc.width, row)
		}
		// With labels the strip covers LAST alone; glyphs cover the tail
		// of AGE and STATE.
		if tc.last && !strings.Contains(row, "● needs input  1m") {
			t.Errorf("%d: the strip covers STATE or AGE: %q", tc.width, row)
		}
	}
}

func TestTheHeaderKeepsWhatFitsAndTheClock(t *testing.T) {
	f := narrowTeam()
	f.hint = "v9.9.9 available - hq update"
	for _, tc := range []struct {
		width      int
		want, gone []string
	}{
		{160, []string{"5 agents · 2 need you · 1 done · 1 working", "view: all", "v9.9.9"}, nil},
		{100, []string{"5 agents · 2 need you · 1 done · 1 working", "view: all"}, []string{"v9.9.9"}},
		{70, []string{"5 agents · 2 need you · 1 done · 1 working"}, []string{"view:"}},
		{50, []string{"5 agents · 2 need you"}, []string{"done", "view:"}},
	} {
		h := lines(started(f, tc.width, 12))[0]
		for _, s := range tc.want {
			if !strings.Contains(h, s) {
				t.Errorf("%d: header %q lacks %q", tc.width, h, s)
			}
		}
		for _, s := range tc.gone {
			if strings.Contains(h, s) {
				t.Errorf("%d: header %q keeps %q", tc.width, h, s)
			}
		}
		if !strings.HasSuffix(h, "Sat 14:32  ⟳ 0s") {
			t.Errorf("%d: clock gone: %q", tc.width, h)
		}
	}
}

func TestTheFooterKeepsTheLabelsWhenNarrow(t *testing.T) {
	for _, tc := range []struct {
		width int
		want  string
	}{
		{110, "↑↓ /name select  ⏎ open  n new  k kill  c code  p pr  s sort: attention  a view: attention  r refresh  q quit"},
		{109, "⏎ open  c code  p pr  k kill  n new  / name  q quit"},
		{80, "⏎ open  c code  p pr  k kill  n new  / name  q quit"},
		{45, "⏎ open  c code  p pr  k kill  n new  q quit"},
	} {
		f := narrowTeam()
		started(f, tc.width, 12)
		var parts []string
		for _, h := range f.footer {
			parts = append(parts, h.Key+" "+h.Label)
		}
		got := strings.Join(parts, "  ")
		if got != tc.want || HintsWidth(f.footer) > tc.width {
			t.Errorf("%d: footer %q (%d wide), want %q", tc.width, got, HintsWidth(f.footer), tc.want)
		}
	}
}

func TestEveryKeyWorksNarrow(t *testing.T) {
	f := narrowTeam()
	m := started(f, 80, 12)
	m = key(m, "down")
	if m.cursor == "perm" {
		t.Fatal("down did not move")
	}
	m = key(m, "up")
	m = key(m, "p")
	if len(f.browsed) != 1 {
		t.Errorf("p at 80 opened %v", f.browsed)
	}
	m = key(m, "a")
	if m.view != ViewAttention {
		t.Errorf("a at 80: view %s", m.view)
	}
}

// click is a left click at column x of line y.
func click(m Model, x, y int) Model {
	m, cmd := update(m, tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	return do(m, cmd)
}

// at is the column where text starts on line y of the list as shown.
func at(t *testing.T, m Model, y int, text string) int {
	t.Helper()
	l := lines(m)[y]
	i := strings.Index(l, text)
	if i < 0 {
		t.Fatalf("%q not on line %d: %q", text, y, l)
	}
	return ansi.StringWidth(l[:i])
}

func TestAClickOnARowMovesTheCursorThere(t *testing.T) {
	f := &fakeSource{agents: team(), view: ViewAll}
	m := started(f, 120, 10) // perm ask done w1 end, from line 3
	if m = click(m, 10, 5); m.cursor != "done" {
		t.Fatalf("cursor %q, want done", m.cursor)
	}
	// Again on the cursor row, off the strip: nothing happens.
	if m = click(m, 10, 5); m.cursor != "done" || len(f.docked)+len(f.coded)+len(f.killed) != 0 {
		t.Fatalf("cursor %q, docked %v, coded %v, killed %v", m.cursor, f.docked, f.coded, f.killed)
	}
	// The header, the column names and the lines below the rows do nothing.
	for _, y := range []int{0, 1, 2, 8, 9} {
		if m = click(m, 10, y); m.cursor != "done" {
			t.Fatalf("a click on line %d moved the cursor to %q", y, m.cursor)
		}
	}
	// Only a left press counts.
	m, _ = update(m, tea.MouseMsg{X: 10, Y: 3, Action: tea.MouseActionPress, Button: tea.MouseButtonRight})
	m, _ = update(m, tea.MouseMsg{X: 10, Y: 3, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	if m.cursor != "done" {
		t.Fatalf("cursor %q", m.cursor)
	}
}

func TestAClickOnARowScrolledIntoViewTakesThatRow(t *testing.T) {
	f := &fakeSource{agents: team(), view: ViewAll}
	m := started(f, 120, 7) // three rows shown
	m = key(key(key(m, "down"), "down"), "down")
	if m.offset == 0 {
		t.Fatal("the list did not scroll")
	}
	name := strings.Fields(lines(m)[3])[0]
	if m = click(m, 4, 3); m.cursor != name {
		t.Fatalf("cursor %q, want %q", m.cursor, name)
	}
}

func TestAClickOnTheStripFiresItsItem(t *testing.T) {
	for _, width := range []int{120, 80} {
		f := narrowTeam() // perm has the cursor and a pull request
		m := started(f, width, 10)
		glyph := map[string]string{"open": "⏎", "code": " c ", "pr": " p ", "kill": "✕"}
		if width >= wideFrom {
			glyph = map[string]string{"open": "open", "code": "code", "pr": " pr", "kill": "kill"}
		}
		m = click(m, at(t, m, 3, glyph["code"])+1, 3)
		m = click(m, at(t, m, 3, glyph["pr"])+1, 3)
		m = click(m, at(t, m, 3, glyph["kill"]), 3)
		m = click(m, at(t, m, 3, glyph["open"]), 3)
		if strings.Join(f.coded, " ") != "perm" || len(f.browsed) != 1 || strings.Join(f.killed, " ") != "perm" || strings.Join(f.docked, " ") != "perm" {
			t.Errorf("%d: coded %v, browsed %v, killed %v, docked %v", width, f.coded, f.browsed, f.killed, f.docked)
		}
	}
}

func TestTheStripsGapsDoNothing(t *testing.T) {
	f := narrowTeam()
	m := started(f, 120, 10)
	l := lines(m)[3]
	gap := ansi.StringWidth(l[:strings.Index(l, "open ")+len("open ")])
	if chipAt(true, true, gap-1, 120) != "enter" || chipAt(true, true, gap, 120) != "" || chipAt(true, true, gap+1, 120) != "c" {
		t.Fatalf("around the gap at %d: %q %q %q", gap, chipAt(true, true, gap-1, 120), chipAt(true, true, gap, 120), chipAt(true, true, gap+1, 120))
	}
	if chipAt(true, true, 118, 120) != "" {
		t.Fatal("the margin is part of the strip")
	}
}

func TestTheWheelMovesTheCursor(t *testing.T) {
	f := &fakeSource{agents: team(), view: ViewAll}
	m := started(f, 120, 10)
	m, _ = update(m, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	if m.cursor != "ask" {
		t.Fatalf("wheel down: cursor %q", m.cursor)
	}
	m, _ = update(m, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp})
	if m.cursor != "perm" {
		t.Fatalf("wheel up: cursor %q", m.cursor)
	}
}

func TestClicksAreIgnoredWhileADialogIsOpenOrANameIsTyped(t *testing.T) {
	f := &fakeSource{agents: team(), view: ViewAll}
	m := started(f, 120, 10)
	m.dialog = true
	if m = click(m, 10, 5); m.cursor != "perm" {
		t.Fatalf("dialog open: cursor %q", m.cursor)
	}
	m.dialog = false
	m = key(m, "/")
	if m = click(m, 10, 5); m.cursor != "perm" || !m.searching {
		t.Fatalf("searching: cursor %q, searching %v", m.cursor, m.searching)
	}
}
