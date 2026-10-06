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

// owedSuffix names, beside an agent's state file, the hook's own list of
// the background tasks Claude still owes a turn for (hookAwk).
const owedSuffix = ".owe"

// supervisedSuffix names, beside an agent's state file, the hook's mark
// that the agent's turn is a supervised turn: every prompt of it came from
// the supervisor, so its end notifies nobody (spec §5, design §3.5).
const supervisedSuffix = ".sup"

// announcedSuffix names, beside an agent's state file, hq's word to the
// hook that the agent's next prompt is the supervisor's (Announce).
const announcedSuffix = ".next"

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
		r = r.Kept(latest, kept, info.ModTime())
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
	for _, p := range []string{path, path + ".stop", path + ".prev", path + keptSuffix, path + owedSuffix, path + supervisedSuffix, path + announcedSuffix} {
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

// Announce tells the hook of agent id of the repository at root that the
// agent's next prompt is the supervisor's: hq mcp is about to give it one,
// as its first prompt or typed in as a message. The hook takes the word
// with that prompt, once; a prompt without it is the user's (design §3.5).
func Announce(root, id string) error {
	if !idRE.MatchString(id) {
		return errors.New("not an agent id")
	}
	if err := os.MkdirAll(Dir(root), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(Dir(root), id+announcedSuffix), nil, 0o644)
}

// Announced reports whether the word Announce left for agent id still
// waits: no prompt has taken it yet.
func Announced(root, id string) bool {
	if !idRE.MatchString(id) {
		return false
	}
	_, err := os.Lstat(filepath.Join(Dir(root), id+announcedSuffix))
	return err == nil
}

// Withdraw takes back what Announce left for agent id, when the prompt it
// was for did not come: the next prompt is the user's again.
func Withdraw(root, id string) error {
	if !idRE.MatchString(id) {
		return errors.New("not an agent id")
	}
	err := os.Remove(filepath.Join(Dir(root), id+announcedSuffix))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
