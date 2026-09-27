// Package agent is hq's row model: one agent as hq ls and, later, the
// dashboard show it (design §3.8, §6).
package agent

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
)

// Agent is one agent: its home window and what hq stored on it.
type Agent struct {
	Window   string    `json:"-"`
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	RepoPath string    `json:"repo_path"`
	Sandbox  string    `json:"sandbox"`
	Started  time.Time `json:"started"`
	Alive    bool      `json:"-"` // the home window's process runs
	State    string    `json:"state"`
	Since    time.Time `json:"since"`
	Branch   string    `json:"branch"`
	Last     string    `json:"last"`
}

// Repo is the repository's display name.
func (a Agent) Repo() string { return filepath.Base(a.RepoPath) }

var nameRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ValidName reports whether name is a valid agent name (spec §4.2).
func ValidName(name string) bool { return len(name) <= 32 && nameRE.MatchString(name) }

// NewID returns a fresh agent id.
func NewID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// FromWindows returns the agents among hq's windows, in window order.
// Windows without an hq id (the dashboard's own window) are not agents.
func FromWindows(ws []tmux.Window) []Agent {
	var as []Agent
	for _, w := range ws {
		o := w.Options
		if o["id"] == "" {
			continue
		}
		a := Agent{Window: w.ID, ID: o["id"], Name: o["name"], RepoPath: o["repo"], Sandbox: o["sandbox"], Alive: !w.PaneDead, State: state.Starting}
		if a.Name == "" {
			a.Name = w.Name
		}
		if s, err := strconv.ParseInt(o["started"], 10, 64); err == nil {
			a.Started = time.Unix(s, 0)
		}
		a.Since = a.Started
		if w.PaneDead {
			a.State = state.Ended
		}
		as = append(as, a)
	}
	return as
}

// Apply adds what the agent's state file reports (ok false: nothing yet).
// A dead pane stays ended whatever the file says, counted from the agent's
// last report (tmux does not say when the pane died); the file still gives
// the last known message.
func (a *Agent) Apply(r state.Report, ok bool) {
	if !ok {
		return
	}
	if a.Alive {
		a.State = r.State
	}
	if a.Alive || r.Since.After(a.Since) {
		a.Since = r.Since
	}
	a.Last = r.Last
}

// Find returns the agent named name.
func Find(as []Agent, name string) (Agent, bool) {
	for _, a := range as {
		if a.Name == name {
			return a, true
		}
	}
	return Agent{}, false
}

// Age formats a duration as spec §5 shows it: 3s, 2m, 1h, 2d.
func Age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
