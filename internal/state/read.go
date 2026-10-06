package state

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
)

// maxFile caps what hq reads from a state file (design §7.3).
const maxFile = 64 << 10

var idRE = regexp.MustCompile(`^[0-9a-f]{1,64}$`)

// Dir is where agents of the repository at root report their state.
func Dir(root string) string { return filepath.Join(root, ".git", "hq", "agents") }

// keptSuffix names, beside an agent's state file, the latest event the hook
// kept without restarting the agent's time (Report.Kept).
const keptSuffix = ".on"

// owedSuffix names, beside an agent's state file, the hook's list of the
// background work it follows: the tasks Claude still owes a turn for, and
// the shell commands subagents started (hookAwk).
const owedSuffix = ".owe"

// Read returns the report of agent id of the repository at root; ok is false
// until the agent's first event.
func Read(root, id string) (r Report, ok bool) {
	if !idRE.MatchString(id) {
		return Report{}, false
	}
	path := filepath.Join(Dir(root), id)
	latest, info, err := readData(path)
	if err != nil {
		return Report{}, false
	}
	lastStop, _, _ := readData(path + ".stop")
	prev, _, _ := readData(path + ".prev")
	r = Parse(latest, lastStop, prev)
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
	var errs []error
	for _, p := range []string{path, path + ".stop", path + ".prev", path + keptSuffix, path + owedSuffix} {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
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
// and show another file.
func readData(path string) ([]byte, os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxFile {
		return nil, nil, errors.New("not a state file")
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
	if err != nil || len(data) > maxFile {
		return nil, nil, errors.New("not a state file")
	}
	return data, info, nil
}
