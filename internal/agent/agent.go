// Package agent is hq's row model: one agent as hq ls and, later, the
// dashboard show it (design §3.8, §6).
package agent

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
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
	Alive    bool      `json:"-"` // the agent's pane's process runs
	Docked   bool      `json:"-"` // its pane is in the dashboard's slot
	New      bool      `json:"-"` // started with hq new, not yet reported (S2)
	ending   bool      // hq is taking the agent down (its sandbox restarting)
	State    string    `json:"state"`
	Since    time.Time `json:"since"`
	Branch   string    `json:"branch"`
	Worktree string    `json:"worktree"` // where Claude works, as the sandbox sees it (for hq code, M4)
	Last     string    `json:"last"`
}

// Repo is the repository's display name.
func (a Agent) Repo() string { return filepath.Base(a.RepoPath) }

var nameRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// MaxName is the longest agent name, in characters (spec §4.2).
const MaxName = 32

// NameChars reports whether name is made of the characters a name may have:
// letters, digits, - and _ (spec §4.2), and has at least one.
func NameChars(name string) bool { return nameRE.MatchString(name) }

// NewID returns a fresh agent id.
func NewID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// Stamp is how hq stores a start time on a home window: Unix seconds with
// milliseconds, fine enough to tell a report of the session before a
// relaunch from one of the relaunched session (see Apply).
func Stamp(t time.Time) string {
	return strconv.FormatFloat(float64(t.UnixMilli())/1000, 'f', 3, 64)
}

// ParseStamp reads a Stamp, or whole Unix seconds as hq stored them before.
func ParseStamp(s string) (time.Time, bool) {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.UnixMilli(int64(math.Round(f * 1000))), true
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
		a := Agent{Window: w.ID, ID: o["id"], Name: o["name"], RepoPath: o["repo"], Sandbox: o["sandbox"], Alive: !w.PaneDead, Docked: w.Docked, ending: o["ending"] != "", State: state.Starting}
		if a.Name == "" {
			a.Name = w.Name
		}
		if t, ok := ParseStamp(o["started"]); ok {
			a.Started = t
		}
		a.Since = a.Started
		// A window marked ending belongs to an agent hq is taking down (its
		// sandbox restarting): ended already, though its pane still runs.
		if w.PaneDead || a.ending {
			a.State = state.Ended
		}
		a.New = o["new"] != "" && a.State == state.Starting
		as = append(as, a)
	}
	return as
}

// EndedLast is the last message of an agent that ended without one: it
// never reported a message before its session went (spec S7, the mocks).
const EndedLast = "[session ended]"

// Collect is the row model's collection, shared by hq ls and the list
// program (design §3.8, §5.1): the agents of the home windows with what
// their state files report, and ended when sbx lists their sandbox as not
// running. running is nil when sbx could not say, which changes nothing.
// An ended agent without a last message has EndedLast, so hq ls and the
// list show the same (design §6).
func Collect(ws []tmux.Window, read func(root, id string) (state.Report, bool), running map[string]bool) []Agent {
	as := FromWindows(ws)
	for i := range as {
		as[i].Apply(read(as[i].RepoPath, as[i].ID))
		if running != nil && !running[as[i].Sandbox] {
			as[i].State = state.Ended
			as[i].New = false
		}
		if as[i].State == state.Ended && as[i].Last == "" {
			as[i].Last = EndedLast
		}
	}
	return as
}

// Apply adds what the agent's state file reports (ok false: nothing yet).
// A dead pane, or one hq is taking down, stays ended whatever the file
// says, counted from the agent's last report (tmux does not say when the
// pane died); the file still gives the last known message. A report older
// than the agent's start is its previous session's (hq sandbox restart
// relaunches an agent under its id, design §3.6): it gives the last known
// message, branch and worktree until the new session reports, but neither
// the state nor its time.
func (a *Agent) Apply(r state.Report, ok bool) {
	if !ok {
		return
	}
	a.Last = r.Last
	a.Branch, a.Worktree = r.Branch, r.Cwd
	if r.Since.Before(a.Started) {
		return
	}
	a.New = false
	if a.Alive && !a.ending {
		a.State = r.State
	}
	if a.Alive || r.Since.After(a.Since) {
		a.Since = r.Since
	}
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
