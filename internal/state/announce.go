package state

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Announce tells the hook of agent id of the repository at root that the
// agent's next prompt is the supervisor's: hq mcp is about to give it one,
// as its first prompt or typed in as a message. The hook takes the word
// with that prompt, once; a prompt without it is the user's (design §3.5).
func Announce(root, id string) error {
	if !idRE.MatchString(id) {
		return errors.New("not an agent id")
	}
	dir := Dir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// The sandbox writes in this directory too: a file of a new name,
	// renamed into place, never writes through a link planted at the
	// word's own name (design §7.3).
	f, err := os.CreateTemp(dir, "."+id+".*")
	if err != nil {
		return err
	}
	f.Close()
	if err := os.Rename(f.Name(), filepath.Join(dir, id+announcedSuffix)); err != nil {
		os.Remove(f.Name())
		return err
	}
	return nil
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
