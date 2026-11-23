package cli

import (
	"encoding/hex"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/journal"
	"github.com/rkrysinski/hq/internal/journal/journaltest"
	"github.com/rkrysinski/hq/internal/state"
)

// hookedJournal is the journals' fake as the commands see it
// (journaltest.Memory): before each ingest, the hook of an agent leaves in
// it what the test says the agent reports last (fakes.states, with what
// it holds in full in fakes.details), as event files of Claude's payloads
// (reportFiles), once for each new report. The fakes' agents have ids
// such as "id-a", which hq never issues: the journal keeps each under a
// shape it takes (journalID).
type hookedJournal struct {
	*journaltest.Memory
	f *fakes
}

var _ journal.Store = hookedJournal{}

// journalID is the id the fakes' journal keeps agent id under: the id
// itself when hq could have issued it, else its bytes in hex.
func journalID(id string) string {
	if state.IsID(id) {
		return id
	}
	return hex.EncodeToString([]byte(id))
}

func (j hookedJournal) Ingest(root, id string) error {
	j.f.leaveReport(root, id)
	return j.Memory.Ingest(root, journalID(id))
}

func (j hookedJournal) Append(id string, events ...state.Event) ([]state.Event, error) {
	if j.f.onAppend != nil {
		j.f.onAppend(id, events)
	}
	return j.Memory.Append(journalID(id), events...)
}

func (j hookedJournal) AppendWhileWindowStands(id string, stands func() bool, events ...state.Event) ([]state.Event, error) {
	if j.f.onAppend != nil {
		j.f.onAppend(id, events)
	}
	return j.Memory.AppendWhileWindowStands(journalID(id), stands, events...)
}

func (j hookedJournal) Through() (int64, error) {
	if j.f.onThrough != nil {
		j.f.onThrough()
	}
	return j.Memory.Through()
}

func (j hookedJournal) Read(id string) (journal.Journal, error) {
	return j.ReadFrom(id, 0)
}

func (j hookedJournal) ReadFrom(id string, from int64) (journal.Journal, error) {
	if j.f.onRead != nil {
		j.f.onRead(id)
	}
	if j.f.unreadable[id] {
		return journal.Journal{}, errors.New("no state directory")
	}
	return j.Memory.ReadFrom(journalID(id), from)
}

// IDs are the agents' ids as hq knows them, the fakes' "id-" ones back
// from their hex (journalID).
func (j hookedJournal) IDs() ([]string, error) {
	ids, err := j.Memory.IDs()
	for i, id := range ids {
		if b, err := hex.DecodeString(id); err == nil && strings.HasPrefix(string(b), "id-") {
			ids[i] = string(b)
		}
	}
	return ids, err
}

func (j hookedJournal) Remove(root, id string) error {
	delete(j.f.left, id)
	return j.Memory.Remove(root, journalID(id))
}

// noJournalOf fails the test unless the store holds no journal of agent id.
func (f *fakes) noJournalOf(t *testing.T, id string) {
	t.Helper()
	ids, err := f.journal.IDs()
	j, rerr := f.journal.Read(journalID(id))
	if err != nil || rerr != nil || slices.Contains(ids, journalID(id)) || len(j.Events) != 0 {
		t.Fatalf("a journal of %s: %v, %s (%v %v)", id, ids, journaltest.Kinds(j.Events), err, rerr)
	}
}

// cursorBefore is the cursor just below the first event of kind in agent
// id's journal: what hq gives as next_since for what that event starts.
func (f *fakes) cursorBefore(t *testing.T, id, kind string) string {
	t.Helper()
	j, err := f.journal.Read(journalID(id))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range j.Events {
		if e.Kind == kind {
			return cursorText(e.Number - 1)
		}
	}
	t.Fatalf("no %s in %s", kind, journaltest.Kinds(j.Events))
	return ""
}

// cursorOf is the number of the last event in agent id's journal.
func (f *fakes) cursorOf(t *testing.T, id string) int64 {
	t.Helper()
	j, err := f.journal.Read(journalID(id))
	if err != nil || len(j.Events) == 0 {
		t.Fatalf("journal of %s: %v", id, err)
	}
	return j.Events[len(j.Events)-1].Number
}

// mustRun runs hq with args, failing unless it exits 0; its output.
func mustRun(t *testing.T, f *fakes, args ...string) string {
	t.Helper()
	code, out, errOut := f.run(args...)
	if code != 0 {
		t.Fatalf("%v: exit %d %q", args, code, errOut)
	}
	return out
}

// leftReport is a report the fakes' hook left for an agent.
type leftReport struct {
	report journaltest.HookState
	detail state.Detail
	ok     bool
}

// leaveReport leaves in agent id's journal, as its hook would, what the
// test says it reports last, when that is new: the event files of the
// report (journaltest.ReportFiles), dated by its time. A report from before the start
// its home window holds is its previous session's: when the journal holds
// no launch yet, hq's launch follows it there, at that start.
func (f *fakes) leaveReport(root, id string) {
	now := leftReport{f.states[id], f.details[id], false}
	_, now.ok = f.states[id]
	if reflect.DeepEqual(f.left[id], now) {
		return
	}
	f.left[id] = now
	if !now.ok {
		return
	}
	f.journal.Report(root, journalID(id), now.report, now.detail)
	started := f.startOf(id)
	if started.IsZero() || !now.report.Since.Before(started) {
		return
	}
	_ = f.journal.Ingest(root, journalID(id))
	j, _ := f.journal.Read(journalID(id))
	for _, e := range j.Events {
		if e.Source == state.SourceHQ && (e.Kind == state.KindLaunched || e.Kind == state.KindRelaunched) {
			return
		}
	}
	_, _ = f.journal.Append(journalID(id), state.Event{Source: state.SourceHQ, Kind: state.KindLaunched, Time: started, Basis: journal.Basis(j.Events)})
}

// seen appends to agent id's journal what a look saw at at, as another hq
// process would have: an event of source and kind with facts, based on
// the hook's last report there.
func (f *fakes) seen(root, id, source, kind string, facts state.Facts, at time.Time) {
	d := f.deps()
	_ = d.journal.Ingest(root, id)
	j, _ := d.journal.Read(id)
	_, _ = f.journal.Append(journalID(id), state.Event{Source: source, Kind: kind, Time: at, Basis: journal.Basis(j.Events), Facts: facts})
}

// eventsOf are the kinds of agent id's events of source, joined by spaces.
func (f *fakes) eventsOf(id, source string) string {
	j, _ := f.journal.Read(journalID(id))
	var es []state.Event
	for _, e := range j.Events {
		if e.Source == source {
			es = append(es, e)
		}
	}
	return journaltest.Kinds(es)
}

// startOf is the start the home window of agent id holds, zero when it has
// none.
func (f *fakes) startOf(id string) time.Time {
	for _, w := range f.tmux.windows {
		if w.Options["id"] == id {
			t, _ := agent.ParseStamp(w.Options["started"])
			return t
		}
	}
	return time.Time{}
}
