// Package journaltest holds the contract every journal store keeps, and
// an in-memory fake that keeps it (design §7.2).
package journaltest

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rkrysinski/hq/internal/journal"
	"github.com/rkrysinski/hq/internal/state"
)

// Memory is the in-memory fake of the journal's store: the event files the
// hooks leave and the journals live in maps, and one mutex stands for the
// lock. Leave puts an event file in place of a hook.
type Memory struct {
	mu       sync.Mutex
	counter  int64
	files    map[string]map[string]file // by root and id, then by name
	journals map[string][]state.Event
	replies  map[string]state.Reply
}

// file is an event file a hook left: its content and its time.
type file struct {
	data []byte
	at   time.Time
}

// NewMemory is an empty fake.
func NewMemory() *Memory {
	return &Memory{files: map[string]map[string]file{}, journals: map[string][]state.Event{}, replies: map[string]state.Reply{}}
}

var _ journal.Store = (*Memory)(nil)

func agentKey(root, id string) string { return root + "\x00" + id }

// Leave leaves an event file named name with data, written at at, for agent
// id of the repository at root, as its hook does.
func (m *Memory) Leave(root, id, name string, data []byte, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := agentKey(root, id)
	if m.files[k] == nil {
		m.files[k] = map[string]file{}
	}
	m.files[k][name] = file{data: data, at: at}
}

// Left are the names of the event files waiting for agent id of the
// repository at root, in their order.
func (m *Memory) Left(root, id string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var names []string
	for n := range m.files[agentKey(root, id)] {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Ingest appends the event files left for agent id, in the order of their
// names, and removes them (journal.Store).
func (m *Memory) Ingest(root, id string) error {
	if !state.IsID(id) {
		return errors.New("not an agent id")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := agentKey(root, id)
	var names []string
	for n := range m.files[k] {
		if state.IsEventFile(n) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		f := m.files[k][n]
		e, r := state.ParseEvent(n, f.data, f.at)
		m.counter++
		e.Number = m.counter
		m.journals[id] = append(m.journals[id], e)
		if r != nil {
			r.Number = e.Number
			m.replies[id] = *r
		}
		delete(m.files[k], n)
	}
	return nil
}

// Append numbers events and appends them to the journal of agent id,
// setting aside what the journal shows to be a duplicate or overtaken
// (journal.Store).
func (m *Memory) Append(id string, events ...state.Event) ([]state.Event, error) {
	if !state.IsID(id) {
		return nil, errors.New("not an agent id")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []state.Event
	for _, e := range events {
		m.counter++
		e.Number = m.counter
		e.Aside = journal.Aside(m.journals[id], e)
		out = append(out, e)
		m.journals[id] = append(m.journals[id], e)
	}
	return out, nil
}

// Read is the journal of agent id (journal.Store).
func (m *Memory) Read(id string) (journal.Journal, error) {
	if !state.IsID(id) {
		return journal.Journal{}, errors.New("not an agent id")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return journal.Journal{
		Events:  append([]state.Event(nil), m.journals[id]...),
		Reply:   m.replies[id],
		Through: m.counter,
	}, nil
}

// Remove deletes the journal of agent id and the event files left for it
// in the repository at root (journal.Store).
func (m *Memory) Remove(root, id string) error {
	if !state.IsID(id) {
		return errors.New("not an agent id")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.files, agentKey(root, id))
	delete(m.journals, id)
	delete(m.replies, id)
	return nil
}

// Agents are the ids that have a journal, for a test to see what is left.
func (m *Memory) Agents() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ids []string
	for id := range m.journals {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Kinds are the kinds of events, joined by spaces, for a test's message.
func Kinds(events []state.Event) string {
	var ks []string
	for _, e := range events {
		ks = append(ks, e.Kind)
	}
	return strings.Join(ks, " ")
}
