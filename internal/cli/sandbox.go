package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/sbx"
)

const sandboxUsage = "usage: hq sandbox rm|restart REPO [-y]"

func runSandbox(env Env, d deps, args []string) error {
	rest, yes, err := parseYes(args)
	if err != nil || len(rest) != 2 || (rest[0] != "rm" && rest[0] != "restart") {
		return usageErr(sandboxUsage)
	}
	as, err := agents(d)
	if err != nil {
		return err
	}
	sb, err := resolveSandbox(d, rest[1])
	if err != nil {
		return err
	}
	var mine []agent.Agent
	for _, a := range as {
		if a.Sandbox == sb.Name {
			mine = append(mine, a)
		}
	}
	if rest[0] == "rm" {
		return sandboxRm(env, d, sb, mine, yes)
	}
	return sandboxRestart(env, d, sb, mine)
}

// resolveSandbox finds the sandbox of REPO: a directory in the repository
// (resolved as in hq new) or the repository's name as hq ls shows it.
func resolveSandbox(d deps, repoArg string) (sbx.Sandbox, error) {
	all, err := d.sbx.List()
	if err != nil {
		return sbx.Sandbox{}, sbxErr(err)
	}
	dir := repoArg
	if !filepath.IsAbs(dir) {
		cwd, err := d.getwd()
		if err != nil {
			return sbx.Sandbox{}, envErr("%v", err)
		}
		dir = filepath.Join(cwd, dir)
	}
	if d.isDir(dir) {
		root, ok := d.repoRoot(dir)
		if !ok {
			return sbx.Sandbox{}, notFoundErr("%s is not in a git repository", dir)
		}
		if s, ok := sbx.ByWorkspace(all, root, d.samePath); ok {
			return s, nil
		}
		return sbx.Sandbox{}, notFoundErr("no sandbox for %s (hq new creates it)", filepath.Base(root))
	}
	var found []sbx.Sandbox
	for _, s := range all {
		if len(s.Workspaces) > 0 && filepath.Base(s.Workspaces[0]) == repoArg {
			found = append(found, s)
		}
	}
	switch len(found) {
	case 0:
		return sbx.Sandbox{}, notFoundErr("no sandbox for '%s' (see hq ls)", repoArg)
	case 1:
		return found[0], nil
	}
	return sbx.Sandbox{}, usageErr("'%s' names %d repositories; give the repository's directory", repoArg, len(found))
}

func sandboxRm(env Env, d deps, sb sbx.Sandbox, mine []agent.Agent, yes bool) error {
	var running []string
	for _, a := range mine {
		if a.State == agent.Running {
			running = append(running, a.Name)
		}
	}
	if len(running) > 0 {
		return usageErr("agents of %s are running (%s); end them first with hq kill", sb.Name, strings.Join(running, ", "))
	}
	if !yes {
		if ok, err := confirm(env, d, fmt.Sprintf("Remove the sandbox %s and its state?", sb.Name)); !ok || err != nil {
			return err
		}
	}
	if err := d.sbx.Remove(sb.Name); err != nil {
		return sbxErr(err)
	}
	fmt.Fprintf(env.Stdout, "removed the sandbox %s\n", sb.Name)
	return nil
}

// sandboxRestart stops and starts the sandbox, then relaunches each of its
// agents under the same name with a new id (design §3.6). Without the agent
// state of M2 there is no Claude session id to resume, so they start fresh.
func sandboxRestart(env Env, d deps, sb sbx.Sandbox, mine []agent.Agent) error {
	// Stopping ends every session in the sandbox; their windows go next.
	if err := d.sbx.Stop(sb.Name); err != nil {
		return sbxErr(err)
	}
	if err := removeAgents(d, mine); err != nil {
		return err
	}
	// sbx has no start command; running anything in a sandbox starts it.
	if err := d.sbx.Exec(sb.Name, "true"); err != nil {
		return sbxErr(err)
	}
	var names []string
	for _, a := range mine {
		if err := startAgent(d, a.Name, a.RepoPath, sb.Name, ""); err != nil {
			return err
		}
		names = append(names, a.Name)
	}
	if len(names) == 0 {
		fmt.Fprintf(env.Stdout, "restarted the sandbox %s\n", sb.Name)
		return nil
	}
	fmt.Fprintf(env.Stdout, "restarted the sandbox %s; relaunched %s\n", sb.Name, strings.Join(names, ", "))
	return nil
}
