package cli

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/sbx"
)

const sandboxUsage = "usage: hq sandbox rm|restart REPO|SANDBOX [-y]"

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
	return sandboxRestart(env, d, sb, mine, yes)
}

// resolveSandbox finds the sandbox of REPO: a directory in the repository
// (resolved as in hq new), the repository's name as hq ls shows it, or the
// sandbox's own name as sbx ls shows it (#113).
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
		if s.Name == repoArg || len(s.Workspaces) > 0 && filepath.Base(s.Workspaces[0]) == repoArg {
			found = append(found, s)
		}
	}
	switch len(found) {
	case 0:
		return sbx.Sandbox{}, notFoundErr("no sandbox for '%s' (see hq ls)", repoArg)
	case 1:
		return found[0], nil
	}
	return sbx.Sandbox{}, usageErr("'%s' names %d repositories; give the repository's directory or its sandbox's name", repoArg, len(found))
}

func sandboxRm(env Env, d deps, sb sbx.Sandbox, mine []agent.Agent, yes bool) error {
	var running []string
	for _, a := range mine {
		if a.Alive {
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

// sessionID is the form of a Claude session id; anything else in a state
// file, which the sandbox writes, is not passed to Claude as an argument.
var sessionID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// sandboxRestart stops and starts the sandbox, then relaunches each of its
// agents in its own pane, continuing its conversation (design §3.6). The
// agents keep their windows, names and ids throughout, so their rows stay
// (ended, then starting), a docked agent stays docked, and what they last
// reported stays until they report anew.
func sandboxRestart(env Env, d deps, sb sbx.Sandbox, mine []agent.Agent, yes bool) error {
	var names []string
	for _, a := range mine {
		names = append(names, a.Name)
	}
	if !yes {
		q := fmt.Sprintf("Restart the sandbox %s?", sb.Name)
		if len(mine) > 0 {
			q += fmt.Sprintf(" This ends and relaunches %s (%s).", plural(len(mine), "agent"), strings.Join(names, ", "))
		}
		if ok, err := confirm(env, d, q); !ok || err != nil {
			return err
		}
	}
	// Each agent continues its conversation: the Claude session of its latest
	// report, read before the agents go. One that never reported starts fresh.
	resume := map[string]string{}
	for _, a := range mine {
		if r, ok := d.readState(a.RepoPath, a.ID); ok && sessionID.MatchString(r.SessionID) {
			resume[a.ID] = r.SessionID
		}
	}
	// Stopping ends every session in the sandbox. The agents show ended from
	// now on: sbx lists the sandbox as running until the stop is done, which
	// takes seconds (S9).
	for _, a := range mine {
		if err := d.tmux.SetOption(a.Window, "ending", "1"); err != nil {
			return tmuxErr(err)
		}
	}
	if err := d.sbx.Stop(sb.Name); err != nil {
		return sbxErr(err)
	}
	// A relaunched agent keeps its id, so its old session must be done
	// writing its state file before the new one starts; sbx stop usually
	// returns after that.
	if err := waitEnded(d, mine, nil); err != nil {
		return err
	}
	// sbx has no start command; running anything in a sandbox starts it.
	if err := d.sbx.Exec(sb.Name, "true"); err != nil {
		return sbxErr(err)
	}
	ws, err := d.tmux.Windows()
	if err != nil {
		return tmuxErr(err)
	}
	panes := map[string]string{}
	for _, w := range ws {
		panes[w.ID] = w.Pane
	}
	names = names[:0]
	for _, a := range mine {
		pane, ok := panes[a.Window]
		if !ok {
			continue // killed while the sandbox restarted
		}
		if err := relaunchAgent(d, a, pane, resume[a.ID]); err != nil {
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

// relaunchAgent starts a new Claude session in the agent's own pane,
// wherever the pane is, under the agent's id, resuming the Claude session
// resume when it is set. Its start moves to now: what its state file says
// until the new session reports is the previous session's, which gives the
// branch and the last message but not the state (design §3.6). The window
// stops being ending before it gets its new start: a refresh in between
// would otherwise see the new session ended as it starts, and record that
// (#104).
func relaunchAgent(d deps, a agent.Agent, pane, resume string) error {
	started := agent.Stamp(d.now())
	// The session starts without the prompt hq mcp may have started the
	// agent with: its next prompt is the user's.
	_ = d.withdraw(a.RepoPath, a.ID)
	argv := d.sbx.RunArgv(a.Sandbox, claudeArgs(a.Name, a.ID, resume, "")...)
	if err := d.tmux.Respawn(pane, a.RepoPath, argv); err != nil {
		return tmuxErr(err)
	}
	for _, o := range [][2]string{{"ending", ""}, {"started", started}, {"new", ""}, {"inbox", inboxHooks}} {
		if err := d.tmux.SetOption(a.Window, o[0], o[1]); err != nil {
			return tmuxErr(err)
		}
	}
	return nil
}
