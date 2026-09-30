// Package desktop adds hq's MCP server to Claude Desktop's configuration
// file (hq mcp install, spec §11, design §3.12), keeping everything else in
// it as it was.
package desktop

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"time"
)

// Name is hq's key among Claude Desktop's MCP servers.
const Name = "hq"

// servers is the key of the MCP servers in Claude Desktop's configuration.
const servers = "mcpServers"

// Server is how Claude Desktop starts an MCP server.
type Server struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env,omitempty"`
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

// Result is what Install did: the outcome, and the copy of the file as it
// was before, when it changed an existing file.
type Result struct {
	Outcome Outcome
	Backup  string
}

// ErrNoFolder is Install finding no folder for the configuration file:
// Claude Desktop creates it on its first start, so it is not installed, or
// keeps its configuration elsewhere.
var ErrNoFolder = errors.New("Claude Desktop's folder is not there")

// Install sets hq up as s in the configuration file at path, created when
// missing in Claude Desktop's folder; the folder itself is never created
// (ErrNoFolder). It writes only when something changes: first a copy of the file
// next to it (claude_desktop_config.json.hq-backup-<time>), then the new
// file under a temporary name, moved into place so the file is never half
// written. A link is followed, so a configuration kept elsewhere is edited
// there. Any error leaves the file as it was.
func Install(path string, s Server, now time.Time) (Result, error) {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	fi, err := os.Stat(path)
	var old []byte
	mode := os.FileMode(0o600)
	switch {
	case errors.Is(err, os.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
		if dir, err := os.Stat(filepath.Dir(path)); err != nil || !dir.IsDir() {
			return Result{}, ErrNoFolder
		}
	case err != nil:
		return Result{}, err
	case !fi.Mode().IsRegular():
		return Result{}, fmt.Errorf("%s is not a regular file", path)
	default:
		if old, err = os.ReadFile(path); err != nil {
			return Result{}, err
		}
		mode = fi.Mode().Perm()
	}
	out, outcome, err := Merge(old, s)
	if err != nil || outcome == Unchanged {
		return Result{Outcome: outcome}, err
	}
	r := Result{Outcome: outcome}
	if fi != nil {
		r.Backup = path + ".hq-backup-" + now.Format("20060102-150405")
		if err := writeNew(r.Backup, old, mode); err != nil {
			return Result{}, err
		}
	}
	return r, replace(path, out, mode)
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

// replace writes b to path through a temporary file in the same directory
// moved over it.
func replace(path string, b []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".hq-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
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
		return err
	}
	return os.Rename(tmp.Name(), path)
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
