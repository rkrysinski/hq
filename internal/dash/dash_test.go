package dash

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

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
	if !strings.HasPrefix(ls[0], "  hq  0 agents") || !strings.HasSuffix(ls[0], "Sat 14:32  ⟳ 0s") {
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
	f := &fakeSource{agents: []agent.Agent{
		ag("idm", state.Working, 3*time.Second, "Running mvn verify ..."),
		ag("spike", state.Ended, time.Hour, ""),
		ag("43", state.Done, 11*time.Minute, "PR #58 opened"),
		ag("42", state.NeedsInput, 2*time.Minute, "Which Entra tenant?"),
		ag("bok", state.Question, 6*time.Minute, "Changelog too?"),
	}}
	ls := lines(started(f, 140, 10))
	if want := "  hq  5 agents · 2 need you · 1 done · 1 working"; !strings.HasPrefix(ls[0], want) {
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
	if !strings.HasPrefix(h, "  hq  1 agent · 1 done  v1.2.0 available - hq update") {
		t.Fatalf("header %q", h)
	}
}

func TestMoreAgentsThanRowsScrollWithAHint(t *testing.T) {
	var as []agent.Agent
	for i := range 8 {
		as = append(as, ag(fmt.Sprint("a", i), state.Working, time.Duration(i)*time.Minute, ""))
	}
	f := &fakeSource{agents: as}
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
	f := &fakeSource{agents: []agent.Agent{{
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
	f := &fakeSource{agents: []agent.Agent{ag("a", state.Working, 0, "")}}
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
	if len(f.footer) != 2 || f.footer[0] != (Hint{"r", "refresh"}) || f.footer[1] != (Hint{"q", "quit"}) {
		t.Errorf("footer %v", f.footer)
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
