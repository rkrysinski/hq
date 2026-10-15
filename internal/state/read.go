package state

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"syscall"
)

// maxFile caps what hq reads from a state file (design §7.3): a bound
// against a planted file, not a limit on what an agent may say, so a long
// reply is read whole (#64).
const maxFile = 16 << 20

// tooLargeError is the error of a regular file over maxFile: it is no state hq
// reads, but the agent's report all the same, which hq says (TooLarge).
type tooLargeError struct{ size int64 }

func (e tooLargeError) Error() string { return fmt.Sprintf("state file of %d bytes", e.size) }

// TooLarge is what hq shows of a report too large to read: as the last
// message and as the reply (spec §4.1, design §7.3).
func TooLarge(size int64) string {
	return fmt.Sprintf("[report too large to read: %.1f MiB]", float64(size)/(1<<20))
}

var idRE = regexp.MustCompile(`^[0-9a-f]{1,64}$`)

// Dir is where agents of the repository at root report their state.
func Dir(root string) string { return filepath.Join(root, ".git", "hq", "agents") }

// keptSuffix names, beside an agent's state file, the latest event the hook
// kept without restarting the agent's time (Report.Kept).
const keptSuffix = ".on"

// owedSuffix names, beside an agent's state file, the hook's own list of
// the background work it follows: the tasks Claude still owes a turn for,
// and the shell commands the agent's subagents started (hookAwk).
const owedSuffix = ".owe"

// supervisedSuffix names, beside an agent's state file, the hook's mark
// that the agent's turn is a supervised turn: every prompt of it came from
// the supervisor, so its end notifies nobody (spec §5, design §3.5).
const supervisedSuffix = ".sup"

// announcedSuffix names, beside an agent's state file, hq's word to the
// hook that the agent's next prompt is the supervisor's (Announce).
const announcedSuffix = ".next"

// Read returns the report of agent id of the repository at root; ok is false
// until the agent's first event. It parses the agent's state files once
// per change: while none of them changed in size or modification time it
// returns the report it parsed last, so the list's refresh pays nothing
// for a long reply (design §5.1, #64).
func Read(root, id string) (r Report, ok bool) {
	if !idRE.MatchString(id) {
		return Report{}, false
	}
	path := filepath.Join(Dir(root), id)
	// Taken before the files are read: a file that changes meanwhile
	// changes the signature again, so the next look reads it anew.
	sig := signature(path)
	parsed.Lock()
	c, hit := parsed.m[path]
	parsed.Unlock()
	if hit && c.sig == sig {
		return c.r, c.ok
	}
	r, ok = read(path)
	parsed.Lock()
	parsed.m[path] = parsedReport{sig: sig, r: r, ok: ok}
	parsed.Unlock()
	return r, ok
}

// stateSuffixes are the files a report is parsed from: the state file
// itself, then those beside it.
var stateSuffixes = [...]string{"", ".stop", ".prev", keptSuffix, owedSuffix}

// fileSig is what tells that a file changed: its type, size and
// modification time, as lstat gives them; zero when there is no file.
type fileSig struct {
	mode fs.FileMode
	size int64
	mod  int64 // Unix nanoseconds
}

// parsedReport is a report with the signature of the files it came from.
type parsedReport struct {
	sig [len(stateSuffixes)]fileSig
	r   Report
	ok  bool
}

// parsed keeps each agent's latest report, by the path of its state file.
var parsed = struct {
	sync.Mutex
	m map[string]parsedReport
}{m: map[string]parsedReport{}}

// signature is the signature of each of the files a report of the state
// file path is parsed from.
func signature(path string) (sig [len(stateSuffixes)]fileSig) {
	for i, s := range stateSuffixes {
		if info, err := os.Lstat(path + s); err == nil {
			sig[i] = fileSig{info.Mode().Type(), info.Size(), info.ModTime().UnixNano()}
		}
	}
	return sig
}

// read parses the report of the state file path (Read). A state file too
// large to read is the agent's report all the same: its turn ended with a
// reply too long to keep, the only report of that size an agent gives, so
// it is done, saying so, never starting with nothing (#64).
func read(path string) (r Report, ok bool) {
	latest, info, err := readData(path)
	var big tooLargeError
	switch {
	case errors.As(err, &big):
		return Report{State: Done, Last: TooLarge(big.size), Since: info.ModTime(), Latest: info.ModTime()}, true
	case err != nil:
		return Report{}, false
	}
	lastStop, _, err := readData(path + ".stop")
	prev, _, _ := readData(path + ".prev")
	r = Parse(latest, lastStop, prev)
	if errors.As(err, &big) && r.Last == "" {
		r.Last = TooLarge(big.size)
	}
	r.Since = info.ModTime()
	r.Latest = r.Since
	if kept, info, err := readData(path + keptSuffix); err == nil {
		owed, _, _ := readData(path + owedSuffix)
		r = r.Kept(latest, kept, owed, info.ModTime())
	}
	return r, true
}

// Remove deletes the state files and the inbox of agent id of the repository
// at root, once the agent is gone (design §3.4); files already gone are no
// error. Only that
// agent's files are touched, never another agent's: agents of other tmux
// servers report to the same directory.
func Remove(root, id string) error {
	if !idRE.MatchString(id) {
		return errors.New("not an agent id")
	}
	path := filepath.Join(Dir(root), id)
	parsed.Lock()
	delete(parsed.m, path)
	parsed.Unlock()
	var errs []error
	for _, suffix := range append(stateSuffixes[:], supervisedSuffix, announcedSuffix) {
		if err := os.Remove(path + suffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	// Messages still waiting go with the agent (#135).
	if err := removeInbox(root, id); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// readData reads a regular file of at most maxFile bytes, never following a
// symbolic link, so a link planted in the repository cannot make hq read
// and show another file. A larger regular file is a tooLargeError, with
// its information.
func readData(path string) ([]byte, os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, errors.New("not a state file")
	}
	if info.Size() > maxFile {
		return nil, info, tooLargeError{info.Size()}
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	if info, err = f.Stat(); err != nil || !info.Mode().IsRegular() {
		return nil, nil, errors.New("not a state file")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxFile+1))
	if err != nil {
		return nil, nil, err
	}
	if len(data) > maxFile {
		return nil, info, tooLargeError{max(info.Size(), int64(len(data)))}
	}
	return data, info, nil
}
