// Package journal keeps the agents' journals on the host (ADR 0018, design
// §3.4): one per agent, in hq's directory under the user's state
// directory, out of every sandbox's reach. The hook in the sandbox leaves
// each event as a file of its own beside the repository (the sandbox
// stream); the first hq process to see new event files appends them to the
// agent's journal in the order of their names and removes them. Every
// append takes the one lock of the state directory and gives each event
// the next number of its one counter: host-wide, never reset, never used
// twice.
package journal

import (
	"errors"
	"path/filepath"

	"github.com/rkrysinski/hq/internal/state"
)

// Store is the seam to the journals (design §7.2): ingest, append, read.
// Files is the store; journaltest.Memory its in-memory fake.
type Store interface {
	// Ingest appends the event files the hook of agent id of the
	// repository at root left to the agent's journal, in the order of
	// their names, and removes them; each is appended exactly once,
	// whatever processes ingest at once or die while they do.
	Ingest(root, id string) error
	// Append numbers evidence (of the screen, tmux and sbx) and events of
	// hq's own and appends them to the journal of agent id, as they are
	// numbered, under the lock: each that the journal shows to be a
	// duplicate, or overtaken by a later report, is marked so (Aside) and
	// set aside, so of processes appending the same evidence at once the
	// first wins.
	Append(id string, events ...state.Event) ([]state.Event, error)
	// AppendWhileWindowStands appends evidence a look saw, or an event of
	// hq's own other than the launch (#318), to the journal of agent id as
	// Append does, but to a journal that is not there (never begun, or
	// removed) only when stands, asked under the lock, says the agent's
	// home window still stands; else it writes nothing and returns ErrGone.
	// hq kill kills the window before it removes the journal, so a look or
	// a command that falls between the steps of a kill writes no journal
	// back.
	AppendWhileWindowStands(id string, stands func() bool, events ...state.Event) ([]state.Event, error)
	// Read is the journal of agent id, read under the lock.
	Read(id string) (Journal, error)
	// ReadFrom is the journal of agent id from position from of a Journal
	// read before (its Next), read under the lock: only the events
	// appended since, so a long-running program folds only those (design
	// §5.1). A position the journal no longer reaches (it was removed) reads
	// it from the start, and From says so.
	ReadFrom(id string, from int64) (Journal, error)
	// Remove deletes the journal of agent id and the event files its hook
	// left in the repository at root, once the agent is gone.
	Remove(root, id string) error
	// IDs are the ids of the agents that have a journal, in their order.
	IDs() ([]string, error)
	// Through is what the counter holds, read under the lock: every event
	// numbered up to it, of any agent, is in its journal (Journal.Through).
	// The cursor of a look or of an hq command that acts starts from it
	// (ADR 0019).
	Through() (int64, error)
}

// ErrGone says evidence, or an event of hq's own, was not appended
// (Store.AppendWhileWindowStands): the agent's journal is not there and its
// home window is gone.
var ErrGone = errors.New("the agent's journal and home window are gone")

// Home is where the agent of the journal events was last launched (its
// latest launch that stands): the repository, which holds its record,
// and the tmux server, which holds its home window. ok is false for a
// journal that holds no such launch.
func Home(events []state.Event) (repo, tmux string, ok bool) {
	for i := len(events) - 1; i >= 0; i-- {
		if e := events[i]; e.Source == state.SourceHQ && e.Kind == state.KindLaunched && e.Aside == "" && e.Facts.Repo != "" {
			return e.Facts.Repo, e.Facts.Tmux, true
		}
	}
	return "", "", false
}

// Journal is an agent's journal as Read gives it.
type Journal struct {
	// Events are the agent's events in the order they were appended, their
	// numbers rising.
	Events []state.Event
	// Reply is what the journal keeps of the latest turn end (Number 0
	// when there is none); read from a position, only when that turn end
	// is among Events.
	Reply state.Reply
	// Ask is what the journal keeps of the latest dialog the hook
	// reported: what it asks, for hq read (Number 0 when there is none);
	// read from a position, only when that report is among Events.
	Ask state.Asked
	// From is the position Events were read from, and Next the position
	// after them, for the next ReadFrom.
	From, Next int64
	// Through is a number such that every event numbered up to it, of any
	// agent, is in its journal: what the counter held when Events were read.
	Through int64
}

// Dir is hq's directory under the user's state directory:
// $XDG_STATE_HOME/hq, with ~/.local/state as the default; "" when neither
// is known (a relative XDG_STATE_HOME is ignored, as the XDG rules say).
func Dir(getenv func(string) string) string {
	dir := getenv("XDG_STATE_HOME")
	if !filepath.IsAbs(dir) {
		home := getenv("HOME")
		if !filepath.IsAbs(home) {
			return ""
		}
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "hq")
}
