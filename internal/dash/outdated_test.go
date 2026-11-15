package dash

import (
	"bytes"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/exp/teatest"
	"github.com/muesli/termenv"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/state"
)

// An outdated agent's row comes first in the attention sort, drawn as a
// row that needs the user (its state in the colour of needs input) and
// counted under need you in the header, with no age, branch - and the
// remedy as its last message (#194). The golden file is the list as the
// program draws it: run the test with -update to write it anew, and look at
// the change before committing it.
func TestAnOutdatedRowIsDrawnAsOneThatNeedsYou(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	f := &fakeSource{agents: []agent.Agent{
		{Name: "w", RepoPath: "/w/lib", State: state.Working, Since: now.Add(-2 * time.Minute), Branch: "fix/w", Last: "on it"},
		{Name: "ask", RepoPath: "/w/app", State: state.NeedsInput, Since: now.Add(-time.Minute), Branch: "feat/ask", Last: "Bash: touch x"},
		{Name: "old", RepoPath: "/w/app", State: state.Outdated, Since: now.Add(-time.Hour), Last: agent.OutdatedLast(2)},
	}}
	tm := teatest.NewTestModel(t, New(f.source()), teatest.WithInitialTermSize(110, 10))
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool { return bytes.Contains(b, []byte("outdated")) }, teatest.WithDuration(5*time.Second))
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m := tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)).(Model)
	teatest.RequireEqualOutput(t, []byte(m.View()))
}
