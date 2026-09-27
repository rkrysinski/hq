package cli

import (
	"fmt"

	"github.com/rkrysinski/hq/internal/iterm"
)

// itermProfileCommand is the hidden command install.sh runs after putting
// the binary in place, so the profile has one source (design §3.9).
const itermProfileCommand = "__iterm-profile"

func runItermProfile(env Env, d deps, args []string) error {
	if len(args) != 0 {
		return usageErr("usage: hq %s", itermProfileCommand)
	}
	addProfile(env, d)
	return nil
}

// addProfile adds the iTerm2 profile hq (Option as Esc+) and says so when
// it wrote it. Without it only the Alt chords are lost, so a failure is a
// warning, never the end of an install or update.
func addProfile(env Env, d deps) {
	wrote, err := d.itermProfile()
	switch {
	case err != nil:
		fmt.Fprintf(env.Stderr, "hq: could not add the iTerm2 profile %s (Option as Alt in the dashboard): %v\n", iterm.Name, err)
	case wrote:
		fmt.Fprintf(env.Stdout, "added the iTerm2 profile %s (Option as Esc+) for the dashboard\n", iterm.Name)
	}
}
