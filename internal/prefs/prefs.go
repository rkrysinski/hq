// Package prefs keeps hq's per-user preferences file (design §6): what must
// survive a tmux restart, such as the last update check.
package prefs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// Path is the preferences file: $XDG_CONFIG_HOME/hq/preferences.json, with
// ~/.config as the default on both platforms.
func Path(getenv func(string) string) string {
	dir := getenv("XDG_CONFIG_HOME")
	if dir == "" {
		dir = filepath.Join(getenv("HOME"), ".config")
	}
	return filepath.Join(dir, "hq", "preferences.json")
}

// Prefs is the file's content. Fields hq does not know (written by a newer
// version) are kept.
type Prefs struct {
	UpdateChecked int64  `json:"update_checked,omitempty"` // unix seconds
	LatestRelease string `json:"latest_release,omitempty"`
	// Sort and View are the dashboard's modes as the user left them (spec
	// §6.2); empty is the default.
	Sort string `json:"sort,omitempty"`
	View string `json:"view,omitempty"`
	// Sandbox is what hq creates a repository's sandbox with: the default
	// setup. Sandboxes are the other setups, by name (#135). The user
	// writes them and hq only reads them: Save keeps them as the file has
	// them.
	Sandbox   Sandbox            `json:"sandbox"`
	Sandboxes map[string]Sandbox `json:"sandboxes"`
	other     map[string]json.RawMessage
	// badSetups is what is wrong with the setups as the file holds them,
	// which every Setup returns; nil when they read.
	badSetups error
}

// Sandbox is a setup: the image (sbx create --template), the MCP servers
// registered with sbx mcp add (--static-mcp), and the model and its
// provider (sbx run --model, --provider), which only a setup under
// "sandboxes" has. Empty is sbx's defaults.
type Sandbox struct {
	Template  string   `json:"template,omitempty"`
	StaticMCP []string `json:"staticMcp,omitempty"`
	Model     string   `json:"model,omitempty"`
	Provider  string   `json:"provider,omitempty"`
}

// DefaultSetup is the setup of the "sandbox" object, the one an agent
// started without a setup gets, whose sandbox has sbx's own name. It is
// hq's one name for it.
const DefaultSetup = "default"

// ErrDefaultUnderSandboxes refuses "default" as a setup under "sandboxes":
// it would be a second default.
var ErrDefaultUnderSandboxes = errors.New(`"default" under "sandboxes" is not a setup of its own: the default setup is the "sandbox" object; move its settings there or give it another name`)

// FormError is a setting of the setups the file holds in a form hq cannot
// read: At is where, as "sandboxes.local.model", Want what hq reads there
// and Is what the file has.
type FormError struct{ At, Want, Is string }

func (e FormError) Error() string { return fmt.Sprintf("%q should be %s, not %s", e.At, e.Want, e.Is) }

// InFile reports whether err, from Setup, is about what the file holds,
// so the caller names the file with it, rather than about the name asked
// for.
func InFile(err error) bool {
	var fe FormError
	return errors.Is(err, ErrDefaultUnderSandboxes) || errors.As(err, &fe)
}

// Setup is the setup of that name: the "sandbox" object for "default",
// without a model or provider, else its object under "sandboxes". An
// unknown name is refused naming the setups there are, and so is any name
// while "sandboxes" holds "default" or the setups are in a form hq cannot
// read (FormError).
func (p Prefs) Setup(name string) (Sandbox, error) {
	if p.badSetups != nil {
		return Sandbox{}, p.badSetups
	}
	if _, ok := p.Sandboxes[DefaultSetup]; ok {
		return Sandbox{}, ErrDefaultUnderSandboxes
	}
	if name == DefaultSetup {
		return Sandbox{Template: p.Sandbox.Template, StaticMCP: p.Sandbox.StaticMCP}, nil
	}
	s, ok := p.Sandboxes[name]
	if !ok {
		return Sandbox{}, fmt.Errorf("unknown setup '%s'; setups: %s", name, strings.Join(p.SetupNames(), ", "))
	}
	return s, nil
}

// SetupNames are the setups the file defines: default first, then the
// others in alphabetical order.
func (p Prefs) SetupNames() []string {
	var names []string
	for n := range p.Sandboxes {
		if n != DefaultSetup {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return append([]string{DefaultSetup}, names...)
}

// Load reads the file; a missing or unreadable file gives empty preferences.
// Setups in a form hq cannot read are kept as the FormError Setup returns,
// whatever else the file holds in a wrong form.
func Load(path string) Prefs {
	var p Prefs
	data, err := os.ReadFile(path)
	if err != nil {
		return p
	}
	_ = json.Unmarshal(data, &p)
	_ = json.Unmarshal(data, &p.other)
	p.badSetups = setupsForm(data)
	return p
}

// setupsForm is what is wrong with the form of the setups in the file
// (FormError), nil when they read or the file is no JSON object. The path
// it names is built here, the same whatever Go's json reports; setups are
// looked at in name order, so the first wrong one is always the same.
func setupsForm(data []byte) error {
	var top map[string]json.RawMessage
	if json.Unmarshal(data, &top) != nil {
		return nil
	}
	if raw, ok := top["sandbox"]; ok {
		if err := settingsForm("sandbox", raw); err != nil {
			return err
		}
	}
	raw, ok := top["sandboxes"]
	if !ok {
		return nil
	}
	var setups map[string]json.RawMessage
	if err := json.Unmarshal(raw, &setups); err != nil {
		return formError("sandboxes", "an object of setups by name", err)
	}
	names := make([]string, 0, len(setups))
	for n := range setups {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := settingsForm("sandboxes."+n, setups[n]); err != nil {
			return err
		}
	}
	return nil
}

// settingsForm is what is wrong with the form of one setup's settings at
// at, nil when they read; settings hq does not know are not looked at.
func settingsForm(at string, raw json.RawMessage) error {
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(raw, &settings); err != nil {
		return formError(at, "an object of settings", err)
	}
	for _, key := range []string{"template", "model", "provider"} {
		var s string
		if v, ok := settings[key]; ok {
			if err := json.Unmarshal(v, &s); err != nil {
				return formError(at+"."+key, "a string", err)
			}
		}
	}
	v, ok := settings["staticMcp"]
	if !ok {
		return nil
	}
	var list []json.RawMessage
	if err := json.Unmarshal(v, &list); err != nil {
		return formError(at+".staticMcp", "a list of names", err)
	}
	for i, item := range list {
		var s string
		if err := json.Unmarshal(item, &s); err != nil {
			return formError(fmt.Sprintf("%s.staticMcp[%d]", at, i), "a string", err)
		}
	}
	return nil
}

// formError is the FormError for the value at at, which json could not
// read as want; the value is read whole, so json's own path is not needed.
func formError(at, want string, err error) error {
	var te *json.UnmarshalTypeError
	if !errors.As(err, &te) {
		return FormError{At: at, Want: want, Is: "something else"}
	}
	return FormError{At: at, Want: want, Is: formFound(te.Value)}
}

// formFound is a JSON value of the kind json reports (string, number,
// array, object, bool).
func formFound(kind string) string {
	switch kind {
	case "array":
		return "a list"
	case "object":
		return "an object"
	case "bool":
		return "true or false"
	case "number", "string":
		return "a " + kind
	}
	return kind
}

// Save writes the file, creating its directory. The file is written next to
// it and renamed over it, so a reader never sees half a file.
func Save(path string, p Prefs) error {
	all := map[string]any{}
	for k, v := range p.other {
		all[k] = v
	}
	for k, v := range map[string]any{"update_checked": p.UpdateChecked, "latest_release": p.LatestRelease, "sort": p.Sort, "view": p.View} {
		delete(all, k)
		if v != int64(0) && v != "" {
			all[k] = v
		}
	}
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".preferences-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
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

// Update changes the file under a lock, so hq processes changing it at once
// (the list keeping its modes, the update check, hq update) never lose each
// other's changes: change gets the file as it is and returns false to leave
// it unchanged. The lock is an flock on the file's .lock beside it, let go
// when the process ends, however it ends.
func Update(path string, change func(*Prefs) bool) error {
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
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	p := Load(path)
	if !change(&p) {
		return nil
	}
	return Save(path, p)
}
