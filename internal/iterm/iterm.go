// Package iterm holds what hq needs from iTerm2 so that Option works as Alt
// in the dashboard with no user setup (design §3.7, §3.11): a dynamic
// profile named hq with Option as Esc+, and the sequence that gives it to
// the one tab hq attaches in.
package iterm

import (
	"bytes"
	"os"
	"path/filepath"
)

// Name is the profile's name, the one SetProfile asks for.
const Name = "hq"

// profile is the dynamic profile. Without a parent it inherits the user's
// default profile, so the tab keeps its look; only the Option keys change
// (2 is Esc+).
const profile = `{
  "Profiles": [
    {
      "Name": "hq",
      "Guid": "hq-dashboard",
      "Option Key Sends": 2,
      "Right Option Key Sends": 2
    }
  ]
}
`

// Profile is the profile file's content.
func Profile() []byte { return []byte(profile) }

// supportDir is iTerm2's own directory under the home directory.
func supportDir(home string) string {
	return filepath.Join(home, "Library", "Application Support", "iTerm2")
}

// ProfilePath is where the profile goes: iTerm2 loads every file in its
// DynamicProfiles directory, and removing the file removes the profile.
func ProfilePath(home string) string {
	return filepath.Join(supportDir(home), "DynamicProfiles", "hq.json")
}

// present reports whether iTerm2 is on this Mac: the application in one of
// the Applications folders, or the support directory it makes at first
// start.
func present(home string, root string) bool {
	for _, p := range []string{
		filepath.Join(root, "Applications", "iTerm.app"),
		filepath.Join(home, "Applications", "iTerm.app"),
		supportDir(home),
	} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// Install writes the profile on macOS when iTerm2 is present and reports
// whether it wrote it (new or changed); elsewhere, or without iTerm2, it
// writes nothing. It changes no other profile, and running it again leaves
// the one file as it is.
// root is the file system's root ("/"), a directory in tests.
func Install(goos, home, root string) (bool, error) {
	if goos != "darwin" || home == "" || !present(home, root) {
		return false, nil
	}
	path := ProfilePath(home)
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, Profile()) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	// Written aside and moved in, so iTerm2 never loads half a file; it
	// skips hidden files.
	tmp := filepath.Join(filepath.Dir(path), ".hq.json.new")
	if err := os.WriteFile(tmp, Profile(), 0o644); err != nil {
		return false, err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return false, err
	}
	return true, nil
}

// SetProfile is the sequence that switches the current tab alone to the
// profile, sent just before attaching; empty when the terminal is not
// iTerm2, so other terminals and WSL get nothing. TERM_PROGRAM names the
// terminal; LC_TERMINAL counts only without it (through ssh), since
// programs started from iTerm2, such as VS Code, pass it on to their own
// terminals.
func SetProfile(getenv func(string) string) string {
	term := getenv("TERM_PROGRAM")
	if term != "iTerm.app" && (term != "" || getenv("LC_TERMINAL") != "iTerm2") {
		return ""
	}
	return "\x1b]1337;SetProfile=" + Name + "\a"
}
