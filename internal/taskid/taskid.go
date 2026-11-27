// Package taskid signs the ids of hq's MCP tasks (ADR 0022). Such an id
// carries the terms of its task, so that any hq process answers for it
// and none keeps it; signed with a key kept in hq's state directory on the
// host, out of every sandbox's reach as the journals are, it is one that
// no one can build from the terms alone, as the MCP Tasks extension
// requires of an id. The id never holds the key.
package taskid

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// keyName is the key's file in hq's state directory, readable by the user
// only.
const keyName = "task-key"

// keySize is how many random bytes the key holds.
const keySize = 32

// sigSize is how many bytes of the HMAC-SHA256 of an id's terms its
// signature holds: 128 bits, which no one guesses, written in 22
// characters, since Claude Code prints the id into the model's context.
const sigSize = 16

// errNoDir is the error of a state directory that is not known.
var errNoDir = errors.New("no state directory: set HOME or XDG_STATE_HOME")

// beforeLink, when set, runs once Key has written a key of its own aside
// and before it puts it into place: the moment a test has another process
// put its key there first.
var beforeLink func()

// Key is the key in hq's state directory dir (journal.Dir), made at its
// first use: a key of random bytes written aside and hard-linked into
// place, so of processes that make it at once the first to link wins,
// none overwrites another's key, and each gets the one in place. The
// directory is made for the user alone when it is not there.
func Key(dir string) ([]byte, error) {
	key, err := ReadKey(dir)
	if !errors.Is(err, fs.ErrNotExist) {
		return key, err
	}
	if err := place(dir); err != nil {
		return nil, err
	}
	return ReadKey(dir)
}

// place makes a key in dir unless another process puts one there first.
func place(dir string) error {
	err := os.MkdirAll(dir, 0o700)
	var f *os.File
	if err == nil {
		// Readable by the user only, as os.CreateTemp makes it.
		f, err = os.CreateTemp(dir, keyName+".*")
	}
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	key := make([]byte, keySize)
	_, _ = rand.Read(key) // never fails, the package says
	_, err = f.Write(key)
	err = errors.Join(err, f.Sync(), f.Close())
	if beforeLink != nil {
		beforeLink()
	}
	if err == nil {
		err = os.Link(f.Name(), filepath.Join(dir, keyName))
	}
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	return err
}

// ReadKey is the key in hq's state directory dir as it stands, making
// nothing: an error that is fs.ErrNotExist when there is none, as when it
// went with the directory.
func ReadKey(dir string) ([]byte, error) {
	if dir == "" {
		return nil, errNoDir
	}
	path := filepath.Join(dir, keyName)
	key, err := os.ReadFile(path)
	if err == nil && len(key) != keySize {
		err = fmt.Errorf("%s is not a key hq made: it holds %d bytes, not %d", path, len(key), keySize)
	}
	return key, err
}

// Sign is the id of terms, signed with key: terms, a colon and their
// signature. terms may hold colons; the signature holds none.
func Sign(key []byte, terms string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(terms))
	return terms + ":" + base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:sigSize])
}

// Verify is the terms of id when id is those terms signed with key, as
// Sign gives it to the character; ok is false for any other id.
func Verify(key []byte, id string) (terms string, ok bool) {
	terms = Terms(id)
	return terms, hmac.Equal([]byte(Sign(key, terms)), []byte(id))
}

// Terms is the terms of an id Sign gave, unchecked: for an id Verify has
// passed. An id with no colon has none.
func Terms(id string) string { return id[:max(strings.LastIndexByte(id, ':'), 0)] }
