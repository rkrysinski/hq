package cli

import (
	"io"
	"path/filepath"
	"strings"

	"github.com/rkrysinski/hq/internal/dialog"
)

// newDialogCommand is the hidden command the New agent dialog's popup runs
// (design §3.8), with the dir to prefill.
const newDialogCommand = "__new-dialog"

// runNewDialog runs the New agent dialog: it starts the agent as hq new does
// and closes, or shows what is wrong under the field (spec §6.6).
func runNewDialog(_ Env, d deps, args []string) error {
	if len(args) != 1 {
		return usageErr("usage: hq %s DIR", newDialogCommand)
	}
	cwd, err := d.getwd()
	if err != nil {
		return envErr("%v", err)
	}
	return d.runDialog(dialog.NewAgentDialog(tildeDir(args[0], d.getenv("HOME")), dialogStart(d, cwd)))
}

// dialogStart is the dialog's Start: hq new NAME DIR PROMPT, with DIR as
// typed (~ and relative to cwd allowed).
func dialogStart(d deps, cwd string) dialog.Start {
	return func(name, dir, prompt string) error {
		_, _, err := createAgent(io.Discard, d, newArgs{name: name, dir: expandDir(dir, d.getenv("HOME"), cwd), prompt: prompt})
		return err
	}
}

// tildeDir shows a directory under home as ~/..., as the mocks do.
func tildeDir(dir, home string) string {
	if home != "" && (dir == home || strings.HasPrefix(dir, home+"/")) {
		return "~" + dir[len(home):]
	}
	return dir
}

// expandDir turns the dialog's dir into a path: ~ is home, a relative path
// is under cwd, empty is cwd.
func expandDir(dir, home, cwd string) string {
	switch {
	case dir == "~" || strings.HasPrefix(dir, "~/"):
		return filepath.Join(home, dir[1:])
	case !filepath.IsAbs(dir):
		return filepath.Join(cwd, dir)
	}
	return filepath.Clean(dir)
}
