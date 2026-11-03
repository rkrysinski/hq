// Package kills keeps hq's trace of the agents it killed (#147): which
// agent, when, and who asked for it, so an agent that is gone can be
// explained. The trace is a file of the user's, beside the preferences
// file, outside every sandbox: no agent can write it (design §6).
package kills

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Path is the trace: $XDG_CONFIG_HOME/hq/kills.log, with ~/.config as the
// default, as the preferences file.
func Path(getenv func(string) string) string {
	dir := getenv("XDG_CONFIG_HOME")
	if dir == "" {
		dir = filepath.Join(getenv("HOME"), ".config")
	}
	return filepath.Join(dir, "hq", "kills.log")
}

// The ways a kill comes to hq.
const (
	ByCLI       = "cli"       // hq kill or hq stop on the command line
	ByDashboard = "dashboard" // the dashboard's Kill dialog
	ByMCP       = "mcp"       // the kill tool of hq mcp
)

// Kill is one agent hq killed.
type Kill struct {
	At      time.Time `json:"at"`
	Name    string    `json:"name"`
	ID      string    `json:"id"`
	Repo    string    `json:"repo"` // the repository's directory
	Sandbox string    `json:"sandbox"`
	By      string    `json:"by"` // ByCLI, ByDashboard or ByMCP
	// Command is the command line's: kill or stop.
	Command string `json:"command,omitempty"`
	// Client is hq mcp's client as hq mcp takes it (claude-desktop,
	// claude-code, sbx), App the name and version the client gave itself.
	Client string `json:"client,omitempty"`
	App    string `json:"app,omitempty"`
	// Caller is the agent that called the tool, when hq knows it.
	Caller string `json:"caller,omitempty"`
}

// maxSize is how large the trace grows before its older half goes: a line
// is a few hundred bytes, so it keeps hundreds of kills.
const maxSize = 128 << 10

// Append adds k to the trace at path, under a lock, so hq processes killing
// at once never lose each other's lines; past maxSize the oldest lines go.
func Append(path string, k Kill) error {
	line, err := json.Marshal(k)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if len(data)+len(line)+1 <= maxSize {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return err
		}
		_, err = f.Write(append(line, '\n'))
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		return err
	}
	return rewrite(path, append(keepNewest(data, maxSize/2), append(line, '\n')...))
}

// keepNewest is the whole lines at the end of data that fit in max bytes.
func keepNewest(data []byte, max int) []byte {
	if len(data) <= max {
		return data
	}
	tail := data[len(data)-max:]
	if data[len(data)-max-1] == '\n' {
		return tail
	}
	if i := bytes.IndexByte(tail, '\n'); i >= 0 {
		return tail[i+1:]
	}
	return nil
}

// rewrite replaces the file at path with data: written to a new file beside
// it and renamed over it, so a reader never sees half a file.
func rewrite(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".kills-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Read is the trace at path, oldest first; none when there is no trace. A
// line hq cannot read is skipped.
func Read(path string) ([]Kill, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Kill
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(nil, maxSize)
	for sc.Scan() {
		var k Kill
		if json.Unmarshal(sc.Bytes(), &k) == nil && k.Name != "" {
			out = append(out, k)
		}
	}
	return out, nil
}
