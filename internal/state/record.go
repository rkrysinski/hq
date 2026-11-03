package state

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// recordSuffix names, beside an agent's state files, hq's record of the
// agent (WriteRecord).
const recordSuffix = ".hq"

// maxRecord caps what hq reads from a record: one is a few dozen bytes.
const maxRecord = 4 << 10

// Record is hq's own record of an agent, kept beside its state files: what
// its home window holds that hq needs to bring the agent back when the
// window is gone with its tmux server (hq restore, design §3.3, #140).
type Record struct {
	ID      string `json:"-"`       // the agent's id, the record's name
	Name    string `json:"name"`    // the agent's name
	Sandbox string `json:"sandbox"` // the sandbox it runs in
	// Server is the tmux server its window is on: the -L socket name hq
	// ran with, "" for the default server.
	Server string `json:"server,omitempty"`
}

// WriteRecord keeps r as the record of agent id of the repository at root,
// replacing the one there was.
func WriteRecord(root, id string, r Record) error {
	if !idRE.MatchString(id) {
		return errors.New("not an agent id")
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	dir := Dir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// The sandbox writes in this directory too: a file of a new name,
	// renamed into place, never writes through a link planted at the
	// record's own name (design §7.3).
	f, err := os.CreateTemp(dir, "."+id+".*")
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), filepath.Join(dir, id+recordSuffix))
	}
	if err != nil {
		os.Remove(f.Name())
	}
	return err
}

// Records are the records of the agents of the repository at root, by id.
// Only a regular file named for an id that holds a record with a name and
// a sandbox counts: the sandbox writes in the same directory, so anything
// else there is skipped (design §7.3). The caller still checks what a
// record names before it acts on it.
func Records(root string) ([]Record, error) {
	entries, err := os.ReadDir(Dir(root))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Record
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), recordSuffix)
		if !ok || !idRE.MatchString(id) {
			continue
		}
		data, info, err := readData(filepath.Join(Dir(root), e.Name()))
		if err != nil || info.Size() > maxRecord {
			continue
		}
		var r Record
		if json.Unmarshal(data, &r) != nil || r.Name == "" || r.Sandbox == "" {
			continue
		}
		r.ID = id
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
