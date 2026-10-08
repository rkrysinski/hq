// Package prefs keeps hq's per-user preferences file (design §6): what must
// survive a tmux restart, such as the last update check.
package prefs

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	// Sandbox is what hq creates a repository's sandbox with. The user
	// writes it and hq only reads it: Save keeps it as the file has it.
	Sandbox Sandbox `json:"sandbox"`
	other   map[string]json.RawMessage
}

// Sandbox is the "sandbox" object: the image (sbx create --template) and
// the MCP servers registered with sbx mcp add (--static-mcp). Empty is sbx's
// defaults.
type Sandbox struct {
	Template  string   `json:"template,omitempty"`
	StaticMCP []string `json:"staticMcp,omitempty"`
}

// Load reads the file; a missing or unreadable file gives empty preferences.
func Load(path string) Prefs {
	var p Prefs
	data, err := os.ReadFile(path)
	if err != nil {
		return p
	}
	_ = json.Unmarshal(data, &p)
	_ = json.Unmarshal(data, &p.other)
	return p
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
