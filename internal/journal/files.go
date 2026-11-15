package journal

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/rkrysinski/hq/internal/state"
)

// Files is the store: the journals as files in hq's directory under the
// user's state directory (Dir), readable by the user only.
//
//	DIR/lock               the one lock of the state directory (flock)
//	DIR/counter            the last number given to an event
//	DIR/journals/ID        agent ID's events, one JSON line each
//	DIR/journals/ID.reply  the latest turn end's reply
//	DIR/journals/ID.ask    what the latest dialog the hook reported asks
//	DIR/journals/ID.held   what the last append read of the journal (held)
//	DIR/journals/ID.pending the ingest under way (recover)
type Files struct {
	Dir string
}

// The names in the store's directory.
const (
	lockName      = "lock"
	counterName   = "counter"
	journalsName  = "journals"
	replySuffix   = ".reply"
	askSuffix     = ".ask"
	heldSuffix    = ".held"
	pendingSuffix = ".pending"
)

// errNoDir is the error of a store whose directory is not known (Dir).
var errNoDir = errors.New("no state directory: set HOME or XDG_STATE_HOME")

// afterAppend, when set, runs once an ingest has appended its events and
// before it removes their files: the moment a test kills the process at.
var afterAppend func()

// pending is what an ingest records before it appends: the journal's size
// before and after the append, and the event files it appends. An ingest
// cut off by the process's death is finished or undone by the next one
// (recover), so no event file is lost or appended twice.
type pending struct {
	From  int64    `json:"from"`
	To    int64    `json:"to"`
	Files []string `json:"files"`
}

func (s Files) journalPath(id string) string { return filepath.Join(s.Dir, journalsName, id) }

// lock takes the one lock of the state directory, creating the directory
// for the user alone when it is not there; the function it returns lets go.
func (s Files) lock() (func(), error) {
	if s.Dir == "" {
		return nil, errNoDir
	}
	if err := os.MkdirAll(filepath.Join(s.Dir, journalsName), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(s.Dir, lockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// Ingest appends the event files the hook of agent id of the repository at
// root left (Store). It takes the lock only when there are files; under
// it, it first finishes or undoes an ingest a dead process left
// (recover), then lists the files again, so of processes ingesting at once
// each file is appended by the first and seen by no other.
func (s Files) Ingest(root, id string) error {
	if !state.IsID(id) {
		return errors.New("not an agent id")
	}
	dir := state.EventsDir(root, id)
	if len(eventFiles(dir)) == 0 {
		return nil
	}
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	if err := s.recover(dir, id); err != nil {
		return err
	}
	names := eventFiles(dir)
	var events []state.Event
	var reply *state.Reply
	var ask *state.Asked
	turnEnd, dialog := -1, -1
	for _, n := range names {
		e, r, data, ok := readEvent(dir, n)
		if !ok {
			continue
		}
		if e.Kind == state.KindTurnEnd {
			reply, turnEnd = r, len(events)
		}
		if a, ok := state.AskOf(e, data); ok {
			ask, dialog = &state.Asked{Ask: a}, len(events)
		}
		events = append(events, e)
	}
	if len(events) == 0 {
		return nil
	}
	if err := s.number(events); err != nil {
		return err
	}
	if reply != nil {
		reply.Number = events[turnEnd].Number
		if err := writeJSON(s.journalPath(id)+replySuffix, reply); err != nil {
			return err
		}
	}
	if ask != nil {
		ask.Number = events[dialog].Number
		if err := writeJSON(s.journalPath(id)+askSuffix, ask); err != nil {
			return err
		}
	}
	lines, err := encode(events)
	if err != nil {
		return err
	}
	path := s.journalPath(id)
	from, err := repair(path)
	if err != nil {
		return err
	}
	p, err := json.Marshal(pending{From: from, To: from + int64(len(lines)), Files: names})
	if err != nil {
		return err
	}
	if err := writeFile(path+pendingSuffix, p); err != nil {
		return err
	}
	if err := appendAt(path, from, lines); err != nil {
		return err
	}
	if afterAppend != nil {
		afterAppend()
	}
	return finish(dir, path, names)
}

// recover finishes or undoes the ingest a dead process left for agent id
// (pending): when its append is whole, it removes the files that append
// took, which may still be there; when the append was cut off, it cuts the
// journal back, so the files, all still there, are appended anew.
func (s Files) recover(dir, id string) error {
	path := s.journalPath(id)
	data, err := os.ReadFile(path + pendingSuffix)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var p pending
	if json.Unmarshal(data, &p) != nil {
		return os.Remove(path + pendingSuffix)
	}
	if size(path) >= p.To {
		return finish(dir, path, p.Files)
	}
	if err := os.Truncate(path, p.From); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	// What Append kept of the journal may reach beyond the cut.
	if err := os.Remove(path + heldSuffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.Remove(path + pendingSuffix)
}

// finish removes the event files names, appended to the journal at path,
// then the record of the ingest under way; a file it cannot remove keeps
// the record, so the file is never appended again.
func finish(dir, path string, names []string) error {
	var errs []error
	for _, n := range names {
		if err := os.Remove(filepath.Join(dir, n)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	return os.Remove(path + pendingSuffix)
}

// Append numbers events of hq's own and appends them to the journal of
// agent id (Store), each set aside when the journal holds the same
// evidence or a later report (Aside).
func (s Files) Append(id string, events ...state.Event) ([]state.Event, error) {
	if !state.IsID(id) {
		return nil, errors.New("not an agent id")
	}
	if len(events) == 0 {
		return nil, nil
	}
	unlock, err := s.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	path := s.journalPath(id)
	from, err := repair(path)
	if err != nil {
		return nil, err
	}
	h, err := readHeld(path)
	if err != nil {
		return nil, err
	}
	out := h.setAside(events)
	if err := s.number(out); err != nil {
		return nil, err
	}
	lines, err := encode(out)
	if err != nil {
		return nil, err
	}
	if err := appendAt(path, from, lines); err != nil {
		return nil, err
	}
	h.follow(out)
	h.Offset = from + int64(len(lines))
	// One not written leaves the one before, which holds less: the next
	// append reads on from there.
	_ = writeJSON(path+heldSuffix, h)
	return out, nil
}

// held is what Append reads of a journal to set events aside (Aside), kept
// beside it (heldSuffix) so that an append reads only the events appended
// since the one before, however long the journal: up to byte Offset, the
// number of the hook's last event (Basis) and the evidence that stands
// (Evidence).
type held struct {
	Offset   int64    `json:"offset"`
	Basis    int64    `json:"basis"`
	Evidence Evidence `json:"evidence"`
}

// readHeld is what the journal at path holds for Append, read on from what
// is kept beside it; from its start when nothing is, or what is kept goes
// beyond the journal.
func readHeld(path string) (held, error) {
	var h held
	if !readJSON(path+heldSuffix, &h) || h.Offset > size(path) || h.Evidence == nil {
		h = held{Evidence: Evidence{}}
	}
	events, at, next, err := readEventsFrom(path, h.Offset)
	if err != nil {
		return held{}, err
	}
	if at != h.Offset {
		h = held{Evidence: Evidence{}}
	}
	h.follow(events)
	h.Offset = next
	return h, nil
}

// follow adds events of the journal to what it holds.
func (h *held) follow(events []state.Event) {
	for _, e := range events {
		h.Evidence.Add(e)
		if e.Source == state.SourceHook {
			h.Basis = e.Number
		}
	}
}

// setAside is events, to be appended after what the journal holds, each
// marked with why it is set aside, if it is (Aside): against the journal
// and the events before it.
func (h held) setAside(events []state.Event) []state.Event {
	h.Evidence = maps.Clone(h.Evidence)
	out := make([]state.Event, 0, len(events))
	for _, e := range events {
		switch {
		case (e.Source == state.SourceScreen || e.Source == state.SourceSbx) && h.Basis > e.Basis:
			e.Aside = state.AsideOvertaken
		case h.Evidence.Holds(e):
			e.Aside = state.AsideDuplicate
		}
		out = append(out, e)
		h.follow([]state.Event{e})
	}
	return out
}

// Read is the journal of agent id (Store): none for an agent with no
// events yet.
func (s Files) Read(id string) (Journal, error) { return s.ReadFrom(id, 0) }

// ReadFrom is the journal of agent id from position from, a byte offset in
// its file (Store). A line that is no event (cut off when a process died)
// is skipped, and an unfinished last line is left for the next read; the
// reply and the ask count only while they are the latest turn end's and
// the latest dialog's, and from a position only when those are among the
// events read.
func (s Files) ReadFrom(id string, from int64) (Journal, error) {
	if !state.IsID(id) {
		return Journal{}, errors.New("not an agent id")
	}
	unlock, err := s.lock()
	if err != nil {
		return Journal{}, err
	}
	defer unlock()
	var j Journal
	if j.Through, err = readCounter(filepath.Join(s.Dir, counterName)); err != nil {
		return Journal{}, err
	}
	path := s.journalPath(id)
	if j.Events, j.From, j.Next, err = readEventsFrom(path, from); err != nil {
		return Journal{}, err
	}
	if n := latest(j.Events, func(e state.Event) bool { return e.Kind == state.KindTurnEnd }); n > 0 {
		var r state.Reply
		if readJSON(path+replySuffix, &r) && r.Number == n {
			j.Reply = r
		}
	}
	if n := latest(j.Events, func(e state.Event) bool { _, ok := state.AskOf(e, nil); return ok }); n > 0 {
		var a state.Asked
		if readJSON(path+askSuffix, &a) && a.Number == n {
			j.Ask = a
		}
	}
	return j, nil
}

// readEventsFrom are the events of the journal at path from byte offset
// from, none when there is none, with the offset they were read from (0
// when the journal is shorter than from) and the offset after the last
// whole line.
func readEventsFrom(path string, from int64) (events []state.Event, at, next int64, err error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, 0, nil
	}
	if err != nil {
		return nil, 0, 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, 0, 0, err
	}
	if from < 0 || from > info.Size() {
		from = 0
	}
	data := make([]byte, info.Size()-from)
	n, err := f.ReadAt(data, from)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, 0, 0, err
	}
	data = data[:n]
	whole := bytes.LastIndexByte(data, '\n') + 1
	return parseEvents(data[:whole]), from, from + int64(whole), nil
}

// readJSON reads the JSON file at path into v, reporting whether it could.
func readJSON(path string, v any) bool {
	data, err := os.ReadFile(path)
	return err == nil && json.Unmarshal(data, v) == nil
}

// writeJSON replaces the file at path with v as JSON (writeFile).
func writeJSON(path string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return writeFile(path, data)
}

// parseEvents are the events of the journal's lines data; a line that is
// no event is skipped.
func parseEvents(data []byte) []state.Event {
	var events []state.Event
	for _, line := range bytes.Split(data, []byte("\n")) {
		var e state.Event
		if json.Unmarshal(line, &e) == nil && e.Number > 0 {
			events = append(events, e)
		}
	}
	return events
}

// latest is the number of the latest of events that is, 0 when none is.
func latest(events []state.Event, is func(state.Event) bool) int64 {
	for i := len(events) - 1; i >= 0; i-- {
		if is(events[i]) {
			return events[i].Number
		}
	}
	return 0
}

// Remove deletes the journal of agent id and its event files in the
// repository at root (Store); files already gone are no error.
func (s Files) Remove(root, id string) error {
	if !state.IsID(id) {
		return errors.New("not an agent id")
	}
	errs := []error{os.RemoveAll(state.EventsDir(root, id))}
	if s.Dir != "" {
		unlock, err := s.lock()
		if err != nil {
			return errors.Join(append(errs, err)...)
		}
		defer unlock()
		for _, suffix := range []string{"", replySuffix, askSuffix, heldSuffix, pendingSuffix} {
			if err := os.Remove(s.journalPath(id) + suffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// number gives events the next numbers of the counter, under the lock. The
// counter is written before the events are appended: a process that dies
// in between leaves numbers no event has, never one two events have.
func (s Files) number(events []state.Event) error {
	path := filepath.Join(s.Dir, counterName)
	n, err := readCounter(path)
	if err != nil {
		return err
	}
	for i := range events {
		events[i].Number = n + int64(i) + 1
	}
	return writeFile(path, []byte(strconv.FormatInt(n+int64(len(events)), 10)+"\n"))
}

// readCounter is the counter at path: 0 before the first event.
func readCounter(path string) (int64, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("the counter %s holds no number", path)
	}
	return n, nil
}

// eventFiles are the names of the event files in dir, in their order.
func eventFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if state.IsEventFile(e.Name()) && !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}

// readEvent reads the event file name in dir as a state file is read
// (design §7.3): a regular file, never through a link, of at most 16 MiB;
// one hq cannot read is an event that says so, with its size. ok is false
// for a file that is gone; data is what the file holds.
func readEvent(dir, name string) (e state.Event, r *state.Reply, data []byte, ok bool) {
	path := filepath.Join(dir, name)
	info, err := os.Lstat(path)
	if err != nil {
		return state.Event{}, nil, nil, false
	}
	if !info.Mode().IsRegular() {
		return state.UnreadableEvent(name, info.Size(), info.ModTime()), nil, nil, true
	}
	data, read, err := state.ReadFile(path)
	if big, ok := state.TooLargeSize(err); ok {
		return state.UnreadableEvent(name, big, info.ModTime()), nil, nil, true
	}
	if err != nil {
		return state.UnreadableEvent(name, info.Size(), info.ModTime()), nil, nil, true
	}
	e, r = state.ParseEvent(name, data, read.ModTime())
	return e, r, data, true
}

// encode is events as the journal's lines.
func encode(events []state.Event) ([]byte, error) {
	var b bytes.Buffer
	for _, e := range events {
		line, err := json.Marshal(e)
		if err != nil {
			return nil, err
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return b.Bytes(), nil
}

// size is the size of the file at path, 0 when there is none.
func size(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// repair cuts the journal at path back to its last whole line, which a
// process that died while it appended may have left unfinished, and
// returns its size.
func repair(path string) (int64, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer f.Close()
	end, err := f.Seek(0, io.SeekEnd)
	if err != nil || end == 0 {
		return 0, err
	}
	keep := end
	buf := make([]byte, 4096)
	for keep > 0 {
		at := max(0, keep-int64(len(buf)))
		n, err := f.ReadAt(buf[:keep-at], at)
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		if i := bytes.LastIndexByte(buf[:n], '\n'); i >= 0 {
			keep = at + int64(i) + 1
			break
		}
		keep = at
	}
	if keep == end {
		return end, nil
	}
	return keep, f.Truncate(keep)
}

// appendAt writes lines at offset from of the journal at path, created for
// the user alone, in one write.
func appendAt(path string, from int64, lines []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = f.WriteAt(lines, from)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// writeFile replaces the file at path with data, for the user alone:
// written beside it and renamed into place, so no reader sees half of it.
func writeFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		os.Remove(f.Name())
	}
	return err
}
