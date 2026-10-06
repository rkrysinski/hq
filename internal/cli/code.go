package cli

import (
	"errors"
	"fmt"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/proc"
)

func runCode(env Env, d deps, args []string) error {
	if len(args) != 1 {
		return usageErr("usage: hq code NAME")
	}
	dir, err := openCode(d, args[0])
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "opened %s in VS Code\n", dir)
	return nil
}

// openCode opens VS Code on the agent's worktree (spec §4.1 hq code, §6.4
// code) and returns the directory it opened.
func openCode(d deps, name string) (string, error) {
	if err := checkTmux(d.tmux); err != nil {
		return "", err
	}
	ws, err := d.tmux.Windows()
	if err != nil {
		return "", tmuxErr(err)
	}
	// With its state, for the worktree.
	a, ok := agent.Find(agent.Collect(ws, d.readState, agent.Sandboxes{}), name)
	if !ok {
		return "", notFoundErr("no agent '%s' (see hq ls)", name)
	}
	dir := worktree(d, a)
	if err := d.editor(dir); err != nil {
		var pe *proc.Error
		if errors.As(err, &pe) && pe.NotFound {
			return "", envErr("VS Code's code command not found; in VS Code run \"Shell Command: Install 'code' command in PATH\"")
		}
		return "", envErr("%v", err)
	}
	return dir, nil
}

// worktree is the top of the work tree the agent's Claude works in, as hq
// sees it; the repository while that is not known yet, or when Claude works
// outside the repository.
func worktree(d deps, a agent.Agent) string {
	if a.Worktree == "" {
		return a.RepoPath
	}
	cwd, err := d.fromSbx(a.Worktree)
	if err != nil {
		return a.RepoPath
	}
	top, ok := d.worktreeTop(cwd)
	if !ok {
		return a.RepoPath
	}
	if root, ok := d.repoRoot(top); !ok || !d.samePath(root, a.RepoPath) {
		return a.RepoPath
	}
	return top
}
