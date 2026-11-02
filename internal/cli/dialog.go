package cli

import (
	"io"
	"path/filepath"
	"strings"
	"sync"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/dialog"
	"github.com/rkrysinski/hq/internal/sbx"
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
	m := dialog.NewAgentDialog(tildeDir(args[0], d.getenv("HOME")), dialogStart(d, cwd))
	return d.runDialog(m.WithSetups(d.loadPrefs().SetupNames(), dialogSandbox(d, cwd)))
}

// hasSetups reports whether the preferences define a setup besides the
// default, so the New agent dialog has its setup field.
func hasSetups(d deps) bool { return len(d.loadPrefs().SetupNames()) > 1 }

// dialogSandbox is the dialog's Sandbox: the repository's sandbox of the
// setup, as hq new finds it, for DIR as typed; for one not created yet the
// name it will get. sbx is asked once per dialog; when it cannot answer,
// or DIR is no repository, there is no name.
func dialogSandbox(d deps, cwd string) dialog.Sandbox {
	var once sync.Once
	var all []sbx.Sandbox
	var listErr error
	return func(setup, dir string) (string, bool) {
		root, ok := d.repoRoot(expandDir(dir, d.getenv("HOME"), cwd))
		if !ok {
			return "", false
		}
		once.Do(func() { all, listErr = d.sbx.List() })
		if listErr != nil {
			return "", false
		}
		if s, ok := sbx.Find(all, root, setup, d.samePath); ok {
			return s.Name, true
		}
		if name := sbx.SandboxName(setup, filepath.Base(root)); name != "" {
			return name, false
		}
		return sbx.DefaultName(filepath.Base(root)), false
	}
}

// dialogStart is the dialog's Start: hq new NAME DIR PROMPT --setup SETUP,
// with DIR as typed (~ and relative to cwd allowed), then the new agent
// docked with the keys in its session. A failure to dock leaves the agent
// started.
func dialogStart(d deps, cwd string) dialog.Start {
	return func(name, dir, prompt, setup string) error {
		if _, _, err := createAgent(io.Discard, d, newArgs{name: name, dir: expandDir(dir, d.getenv("HOME"), cwd), prompt: prompt, setup: setup}); err != nil {
			return err
		}
		// The user's own new agent takes the slot, and the cursor with it
		// (spec S2, §6.3); an agent started outside the dashboard does not.
		_ = dock(d, name)
		return nil
	}
}

// killDialogCommand is the hidden command the Kill dialog's popup runs,
// with the agent's name.
const killDialogCommand = "__kill-dialog"

// runKillDialog runs the Kill dialog: on Yes it ends the agent as hq kill
// does and closes (spec §6.6, S6).
func runKillDialog(_ Env, d deps, args []string) error {
	if len(args) != 1 {
		return usageErr("usage: hq %s NAME", killDialogCommand)
	}
	if err := checkTmux(d.tmux); err != nil {
		return err
	}
	ws, err := d.tmux.Windows()
	if err != nil {
		return tmuxErr(err)
	}
	// With its state, for the branch.
	a, ok := agent.Find(agent.Collect(ws, d.readState, agent.Sandboxes{}), args[0])
	if !ok {
		return notFoundErr("no agent '%s' (see hq ls)", args[0])
	}
	branch := a.Branch
	if branch == "" {
		branch = a.Repo()
	}
	return d.runDialog(dialog.KillDialog(a.Name, branch, func() error { return endAgents(d, []agent.Agent{a}) }))
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
