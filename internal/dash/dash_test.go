package dash

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/rkrysinski/hq/internal/agent"
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
	}
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
	f := &fakeSource{view: ViewAll, agents: []agent.Agent{{
		Name: "a-very-long-agent-name-indeed", RepoPath: "/w/r", Branch: "feat/" + strings.Repeat("b", 40),
		State: state.Working, Since: now, Last: strings.Repeat("words ", 40),
	}}}
	ls := lines(started(f, 100, 10))
	row := ls[3]
	if w := ansi.StringWidth(row); w > 100 {
		t.Errorf("row %d cells wide in 100: %q", w, row)
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
	m := started(f, 100, 10)
	if got := fmt.Sprint(f.footer); got != "[{↑↓ select} {⏎ open session below} {s sort: attention} {a view: all} {r refresh} {q quit}]" {
		t.Errorf("footer %s", got)
	}
	update(m, tea.WindowSizeMsg{Width: 90, Height: 12})
	if f.layouts != 2 {
		t.Errorf("%d layouts after two sizes", f.layouts)
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

func TestRowsFollowTheWindowHeight(t *testing.T) {
	for _, tc := range []struct{ window, rows, height int }{{50, 6, 10}, {24, 6, 10}, {23, 3, 7}} {
		if Rows(tc.window) != tc.rows || Height(tc.window) != tc.height {
			t.Errorf("window %d: %d rows in %d lines", tc.window, Rows(tc.window), Height(tc.window))
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
	if got := f.footer[3]; got != (Hint{"a", "view: attention"}) {
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
		if got := f.footer[2]; got != (Hint{"s", "sort: " + tc.sort}) {
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
	m = key(key(key(m, "j"), "down"), "k")
	if m.cursor != "ask" {
		t.Fatalf("after j down k: %q", m.cursor)
	}
	m = key(key(m, "up"), "k")
	if m.cursor != "perm" || m.cursorRow != 0 {
		t.Fatalf("above the top: %q %d", m.cursor, m.cursorRow)
	}
	m = key(key(key(key(key(key(m, "j"), "j"), "j"), "j"), "j"), "j")
	if m.cursor != "end" {
		t.Fatalf("below the bottom: %q", m.cursor)
	}

	// w1 asks a question: rows reshuffle, the cursor stays on its agent.
	m = key(key(m, "k"), "k") // on done
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
	m = key(key(m, "j"), "k") // no rows: nothing to move to
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
		m = key(m, "j")
	}
	ls := lines(m)
	if m.offset != 1 || !strings.HasSuffix(ls[9], "6 of 8  ▴ 1 more  ▾ 1 more") || !strings.Contains(ls[8], "a6") || !strings.Contains(ls[3], "a1") {
		t.Fatalf("offset %d:\n%s", m.offset, strings.Join(ls, "\n"))
	}
	m = key(m, "j")
	if ls = lines(m); !strings.HasSuffix(ls[9], "6 of 8  ▴ 2 more") {
		t.Errorf("at the bottom: %q", ls[9])
	}
	for range 7 {
		m = key(m, "k")
	}
	if m.offset != 0 || m.cursor != "a0" {
		t.Errorf("back at the top: offset %d cursor %q", m.offset, m.cursor)
	}
	// A smaller window keeps the cursor in sight.
	m = key(key(key(key(key(m, "j"), "j"), "j"), "j"), "j") // a5
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 7})
	if m.offset != 3 {
		t.Errorf("3 rows with the cursor on a5: offset %d", m.offset)
	}
}

func TestCursorRowHasABackground(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	f := &fakeSource{agents: team(), view: ViewAll}
	m := key(started(f, 120, 10), "j")
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
	m = key(m, "j")
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
	m := key(key(started(f, 120, 10), "j"), "enter") // perm ask done w1 end
	if len(f.docked) != 1 || f.docked[0] != "ask" {
		t.Fatalf("docked %v, want ask", f.docked)
	}
	if !m.rows[1].Docked {
		t.Fatalf("the list does not know ask is docked: %+v", m.rows[1])
	}
	// The cursor moves on; the docked row keeps its outline.
	m = key(m, "j")
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
