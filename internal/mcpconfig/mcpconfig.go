// Package mcpconfig adds hq's MCP server to an MCP client's configuration
// file, Claude Desktop's or Claude Code's (hq mcp install, spec §11, design
// §3.12), keeping everything else in it as it was.
package mcpconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"time"
)

// Name is hq's key among the client's MCP servers.
const Name = "hq"

// servers is the key of the MCP servers in the client's configuration.
const servers = "mcpServers"

// Server is how the client starts an MCP server: Claude Code's entry also
// says its type, as claude mcp add writes it, and how long, in
// milliseconds, a tool call may run.
type Server struct {
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env,omitempty"`
	Timeout int               `json:"timeout,omitempty"`
}

// Outcome is what an install did to the configuration.
type Outcome int

const (
	Unchanged Outcome = iota // hq was set up as it is
	Added                    // hq was not set up
	Updated                  // hq was set up differently (another path or PATH)
)

// Merge returns config with hq set up as s under mcpServers. Every other key
// and server keeps its value and its place; hq's entry keeps its place when
// it is there. An empty config is an empty object. It refuses a config it
// cannot edit safely: not JSON, not an object, a key twice, or mcpServers
// not an object. When hq is set up as s already, it returns config as it is.
func Merge(config []byte, s Server) ([]byte, Outcome, error) {
	if len(bytes.TrimSpace(config)) == 0 {
		config = []byte("{}")
	}
	top, err := parseObject(config)
	if err != nil {
		return nil, Unchanged, err
	}
	entry, err := json.Marshal(s)
	if err != nil {
		return nil, Unchanged, err
	}
	var list object
	if raw, ok := top.get(servers); ok && string(bytes.TrimSpace(raw)) != "null" {
		if list, err = parseObject(raw); err != nil {
			return nil, Unchanged, fmt.Errorf("%s: %w", servers, err)
		}
	}
	outcome := Added
	if old, ok := list.get(Name); ok {
		if same(old, entry) {
			return config, Unchanged, nil
		}
		outcome = Updated
	}
	list = list.set(Name, entry)
	top = top.set(servers, list.bytes())
	var out bytes.Buffer
	if err := json.Indent(&out, top.bytes(), "", "  "); err != nil {
		return nil, Unchanged, err
	}
	out.WriteByte('\n')
	return out.Bytes(), outcome, nil
}

// same reports whether two JSON values are equal as values.
func same(a, b []byte) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}

// Snippet is hq's entry as it goes into the configuration, for the user to
// add by hand.
func Snippet(s Server) string {
	b, _ := json.MarshalIndent(map[string]map[string]Server{servers: {Name: s}}, "", "  ")
	return string(b) + "\n"
}

// AddJSON is the claude command that adds hq's entry to Claude Code's
// user-scope servers, for the user to run by hand.
func AddJSON(s Server) string {
	b, _ := json.Marshal(s)
	return "claude mcp add-json --scope user " + Name + " '" + strings.ReplaceAll(string(b), "'", `'\''`) + "'\n"
}

// Result is what Install did: the outcome, and the copy of the file as it
// was before, when it changed an existing file.
type Result struct {
	Outcome Outcome
	Backup  string
}

// ErrNoFolder is Install finding no folder for the configuration file:
// Claude Desktop creates it on its first start, so it is not installed, or
// keeps its configuration elsewhere.
var ErrNoFolder = errors.New("the configuration's folder is not there")

// ErrNoFile is Install finding no configuration file it may not create:
// Claude Code writes it on its first start, so it was never run there, or
// keeps its configuration elsewhere.
var ErrNoFile = errors.New("the configuration file is not there")

// File is a client's configuration file and how the client keeps it.
type File struct {
	Path string
	// Create lets Install create the file when it is missing, in its
	// folder (Claude Desktop's); otherwise a missing file is ErrNoFile
	// (Claude Code's).
	Create bool
	// Lock says the client writes the file holding the lock directory
	// Path.lock, as proper-lockfile takes it (Claude Code's): Install holds
	// it too while it reads and writes the file.
	Lock bool
}

var (
	// lockStale is how long a lock directory may go without its time
	// refreshed before it is taken for abandoned: proper-lockfile's stale,
	// which Claude Code keeps; a live holder refreshes it every 5 s.
	lockStale = 10 * time.Second
	// lockWait is how long Install waits for a lock a live holder keeps:
	// Claude Code holds it for one save.
	lockWait = 10 * time.Second
	// beforeCommit runs before Install looks whether the file changed
	// while it merged; the tests change the file there.
	beforeCommit = func() {}
)

// mergeTries is how often Install merges, when the file keeps changing
// while it does.
const mergeTries = 5

// Install sets hq up as s in the configuration file f. A missing file is
// created, when f allows it, in its folder (Claude Desktop's), which itself
// is never created (ErrNoFolder); otherwise it is ErrNoFile. It writes only
// when something changes: the new file under a temporary name, a copy of
// the file as it was next to it (FILE.hq-backup-<time>), then, when the
// file is still as it was read, the new file moved into place, so it is
// never half written; a file that changed meanwhile is merged again, so no
// other writer's change is lost. A link is followed, so a configuration
// kept elsewhere is edited there. Any error leaves the file as it was.
func Install(f File, s Server, now time.Time) (Result, error) {
	if f.Lock {
		unlock, err := lock(f.Path + ".lock")
		switch {
		case (errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)) && f.Create:
			return Result{}, ErrNoFolder
		case errors.Is(err, os.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
			return Result{}, ErrNoFile
		case err != nil:
			return Result{}, err
		}
		defer unlock()
	}
	path := f.Path
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	for try := 1; ; try++ {
		r, changed, err := install(path, f.Create, s, now)
		if !changed {
			return r, err
		}
		if try == mergeTries {
			return Result{}, fmt.Errorf("%s kept changing while hq merged it", path)
		}
	}
}

// install is one merge of Install's: changed when the file changed between
// its read and its move, which leaves the file to the other writer.
func install(path string, create bool, s Server, now time.Time) (Result, bool, error) {
	fi, err := os.Stat(path)
	var old []byte
	mode := os.FileMode(0o600)
	switch {
	case (errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)) && !create:
		return Result{}, false, ErrNoFile
	case errors.Is(err, os.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
		if dir, err := os.Stat(filepath.Dir(path)); err != nil || !dir.IsDir() {
			return Result{}, false, ErrNoFolder
		}
	case err != nil:
		return Result{}, false, err
	case !fi.Mode().IsRegular():
		return Result{}, false, fmt.Errorf("%s is not a regular file", path)
	default:
		if old, err = os.ReadFile(path); err != nil {
			return Result{}, false, err
		}
		mode = fi.Mode().Perm()
	}
	out, outcome, err := Merge(old, s)
	if err != nil || outcome == Unchanged {
		return Result{Outcome: outcome}, false, err
	}
	tmp, err := writeTemp(path, out, mode)
	if err != nil {
		return Result{}, false, err
	}
	defer os.Remove(tmp)
	r := Result{Outcome: outcome}
	if fi != nil {
		r.Backup = path + ".hq-backup-" + now.Format("20060102-150405")
		if err := writeNew(r.Backup, old, mode); err != nil {
			return Result{}, false, err
		}
	}
	beforeCommit()
	if cur, err := os.ReadFile(path); (err == nil) != (fi != nil) || !bytes.Equal(cur, old) {
		if r.Backup != "" {
			os.Remove(r.Backup)
		}
		return Result{}, true, nil
	}
	return r, false, os.Rename(tmp, path)
}

// lock takes the lock directory dir as proper-lockfile does: it makes it,
// takes one left unrefreshed for lockStale for abandoned and makes it anew,
// and waits up to lockWait for a live holder. The function it returns
// gives the lock up.
func lock(dir string) (func(), error) {
	deadline := time.Now().Add(lockWait)
	for {
		err := os.Mkdir(dir, 0o777)
		if err == nil {
			return func() { os.Remove(dir) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if fi, err := os.Stat(dir); err == nil && time.Since(fi.ModTime()) > lockStale {
			os.Remove(dir)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%s is held: the client is saving the file; try again", dir)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// writeNew writes a file that must not exist yet.
func writeNew(path string, b []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// writeTemp writes b to a temporary file in path's directory, to be moved
// over path, and returns its name.
func writeTemp(path string, b []byte, mode os.FileMode) (string, error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".hq-*.json")
	if err != nil {
		return "", err
	}
	_, err = tmp.Write(b)
	if err == nil {
		err = tmp.Chmod(mode)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

// object is a JSON object's members in their order, values as they were
// written.
type object []member

type member struct {
	key   string
	value json.RawMessage
}

func (o object) get(key string) (json.RawMessage, bool) {
	for _, m := range o {
		if m.key == key {
			return m.value, true
		}
	}
	return nil, false
}

// set replaces key's value in its place, or adds it at the end.
func (o object) set(key string, value []byte) object {
	for i, m := range o {
		if m.key == key {
			o[i].value = value
			return o
		}
	}
	return append(o, member{key, value})
}

func (o object) bytes() []byte {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, m := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(m.key)
		b.Write(k)
		b.WriteByte(':')
		b.Write(m.value)
	}
	b.WriteByte('}')
	return b.Bytes()
}

// parseObject reads a JSON object, refusing anything else and a key given
// twice, whose meaning differs between readers. Once b is known to be one
// valid JSON value, reading it cannot fail.
func parseObject(b []byte) (object, error) {
	if !json.Valid(b) {
		return nil, errors.New("not valid JSON")
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	if t, _ := dec.Token(); t != json.Delim('{') {
		return nil, errors.New("not a JSON object")
	}
	var o object
	for dec.More() {
		t, _ := dec.Token()
		key := t.(string)
		if _, dup := o.get(key); dup {
			return nil, fmt.Errorf("%q is given twice", key)
		}
		var v json.RawMessage
		_ = dec.Decode(&v)
		o = append(o, member{key, v})
	}
	return o, nil
}
