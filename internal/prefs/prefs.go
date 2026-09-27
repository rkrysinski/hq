// Package prefs keeps hq's per-user preferences file (design §6): what must
// survive a tmux restart, such as the last update check.
package prefs

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	other         map[string]json.RawMessage
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

// Save writes the file, creating its directory.
func Save(path string, p Prefs) error {
	all := map[string]any{}
	for k, v := range p.other {
		all[k] = v
	}
	delete(all, "update_checked")
	delete(all, "latest_release")
	if p.UpdateChecked != 0 {
		all["update_checked"] = p.UpdateChecked
	}
	if p.LatestRelease != "" {
		all["latest_release"] = p.LatestRelease
	}
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
