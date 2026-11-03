package cli

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/prefs"
	"github.com/rkrysinski/hq/internal/proc"
	"github.com/rkrysinski/hq/internal/sbx"
)

const sandboxUsage = "usage: hq sandbox rm|restart REPO|SANDBOX [--setup NAME|--all-setups] [-y]"

// sandboxArgs is hq sandbox's arguments after parsing: setup is the
// setup's sandbox, the default's unless setupGiven; all every sandbox of
// the repository.
type sandboxArgs struct {
	cmd, repo, setup     string
	setupGiven, all, yes bool
}

// flagged reports whether a flag chooses among the repository's sandboxes.
func (s sandboxArgs) flagged() bool { return s.setupGiven || s.all }

func parseSandbox(args []string) (sandboxArgs, error) {
	var s sandboxArgs
	args, setup, given, err := parseSetup(args, sandboxUsage)
	if err != nil {
		return s, err
	}
	s.setup, s.setupGiven = setup, given
	var rest []string
	for _, a := range args {
		if a == "--all-setups" {
			s.all = true
		} else {
			rest = append(rest, a)
		}
	}
	plain, yes, err := parseYes(rest)
	if err != nil || len(plain) != 2 || (plain[0] != "rm" && plain[0] != "restart") {
		return s, usageErr(sandboxUsage)
	}
	if s.all && s.setupGiven {
		return s, usageErr("--setup and --all-setups exclude each other (%s)", sandboxUsage)
	}
	s.cmd, s.repo, s.yes = plain[0], plain[1], yes
	return s, nil
}

func runSandbox(env Env, d deps, args []string) error {
	s, err := parseSandbox(args)
	if err != nil {
		return err
	}
	as, err := agents(d)
	if err != nil {
		return err
	}
	sbs, err := resolveSandboxes(d, s)
	if err != nil {
		return err
	}
	var mine []agent.Agent
	for _, a := range as {
		if slices.ContainsFunc(sbs, func(sb sbx.Sandbox) bool { return a.Sandbox == sb.Name }) {
			mine = append(mine, a)
		}
	}
	if s.cmd == "rm" {
		return sandboxRm(env, d, sbs, mine, s.yes)
	}
	// After a host restart tmux lists none of their agents: those it lost
	// are restored, as hq restore does (#140).
	ws, err := d.tmux.Windows()
	if err != nil {
		return tmuxErr(err)
	}
	var lost []agent.Agent
	for _, a := range lostAgents(d, ws, knownRepositories(d, sbs)) {
		if slices.ContainsFunc(sbs, func(sb sbx.Sandbox) bool { return a.Sandbox == sb.Name }) {
			lost = append(lost, a)
		}
	}
	return sandboxRestart(env, d, sbs, mine, lost, s.yes)
}

// resolveSandboxes finds the sandboxes REPO and the flags name (#113,
// #135, #136): REPO is a directory in the repository, resolved as in hq
// new, or a name (sandboxesNamed).
func resolveSandboxes(d deps, a sandboxArgs) ([]sbx.Sandbox, error) {
	all, err := d.sbx.List()
	if err != nil {
		return nil, sbxErr(err)
	}
	dir := a.repo
	if !filepath.IsAbs(dir) {
		cwd, err := d.getwd()
		if err != nil {
			return nil, envErr("%v", err)
		}
		dir = filepath.Join(cwd, dir)
	}
	if !d.isDir(dir) {
		return sandboxesNamed(d, all, a)
	}
	root, ok := d.repoRoot(dir)
	if !ok {
		return nil, notFoundErr("%s is not in a git repository", dir)
	}
	if sbs := chosenSandboxes(d, all, root, a); len(sbs) > 0 {
		return sbs, nil
	}
	return nil, noSandboxErr(filepath.Base(root), a.setup)
}

// chosenSandboxes are the sandboxes of the repository at root the flags
// choose: its default sandbox, the sandbox of --setup, or with
// --all-setups every one it has (sbx.OfRepository); none when it has none
// of them.
func chosenSandboxes(d deps, all []sbx.Sandbox, root string, a sandboxArgs) []sbx.Sandbox {
	if a.all {
		return sbx.OfRepository(all, root, d.samePath)
	}
	if s, ok := sbx.Find(all, root, a.setup, d.samePath); ok {
		return []sbx.Sandbox{s}
	}
	return nil
}

// sandboxesNamed finds the sandboxes for REPO as the repository's name as
// hq ls shows it, the flags choosing among each such repository's
// sandboxes; without a flag REPO may also be a sandbox's own name as sbx
// ls shows it, whatever its setup. A name that is more than one
// repository's is refused, asking for the directory.
func sandboxesNamed(d deps, all []sbx.Sandbox, a sandboxArgs) ([]sbx.Sandbox, error) {
	repos := repositoriesNamed(d, all, a.repo)
	var found [][]sbx.Sandbox // per repository that has those chosen
	for _, root := range repos {
		if sbs := chosenSandboxes(d, all, root, a); len(sbs) > 0 {
			found = append(found, sbs)
		}
	}
	if i := slices.IndexFunc(all, func(s sbx.Sandbox) bool { return s.Name == a.repo }); i >= 0 {
		switch {
		case a.all && len(repos) == 0:
			return nil, usageErr("--all-setups takes a repository, and %s is a sandbox's name", a.repo)
		case a.flagged() && len(repos) == 0:
			return nil, usageErr("--setup takes a repository, and %s is a sandbox's name", a.repo)
		case !a.flagged() && !slices.ContainsFunc(found, func(sbs []sbx.Sandbox) bool { return sbs[0].Name == a.repo }):
			found = append(found, []sbx.Sandbox{all[i]})
		}
	}
	switch {
	case len(found) == 1:
		return found[0], nil
	case len(found) > 1 && a.all:
		return nil, usageErr("'%s' names %d repositories; give the repository's directory", a.repo, len(found))
	case len(found) > 1:
		return nil, usageErr("'%s' names %d repositories; give the repository's directory or its sandbox's name", a.repo, len(found))
	case len(repos) > 0 && a.setupGiven:
		return nil, noSandboxErr(a.repo, a.setup)
	}
	return nil, notFoundErr("no sandbox for '%s' (see hq ls)", a.repo)
}

// repositoriesNamed are the repositories in a directory named name that
// have a sandbox, each once: the primary workspaces of the sandboxes.
func repositoriesNamed(d deps, all []sbx.Sandbox, name string) []string {
	var repos []string
	for _, s := range all {
		if len(s.Workspaces) == 0 || filepath.Base(s.Workspaces[0]) != name || slices.ContainsFunc(repos, func(r string) bool { return d.samePath(r, s.Workspaces[0]) }) {
			continue
		}
		repos = append(repos, s.Workspaces[0])
	}
	return repos
}

// noSandboxErr says the repository in directory dir has no sandbox of
// setup (with --all-setups, of any) and which hq new creates it.
func noSandboxErr(dir, setup string) error {
	if setup == prefs.DefaultSetup {
		return notFoundErr("no sandbox for %s (hq new creates it)", dir)
	}
	return notFoundErr("no sandbox of setup '%s' for %s (hq new --setup %s creates it)", setup, dir, setup)
}

// sandboxNames is "the sandbox X" or "the sandboxes X, Y".
func sandboxNames(sbs []sbx.Sandbox) string {
	var names []string
	for _, s := range sbs {
		names = append(names, s.Name)
	}
	if len(names) == 1 {
		return "the sandbox " + names[0]
	}
	return "the sandboxes " + strings.Join(names, ", ")
}

func sandboxRm(env Env, d deps, sbs []sbx.Sandbox, mine []agent.Agent, yes bool) error {
	var running, busy []string
	for _, a := range mine {
		if a.Alive {
			running = append(running, a.Name)
		}
	}
	for _, sb := range sbs {
		if slices.ContainsFunc(mine, func(a agent.Agent) bool { return a.Alive && a.Sandbox == sb.Name }) {
			busy = append(busy, sb.Name)
		}
	}
	if len(running) > 0 {
		return usageErr("agents of %s are running (%s); end them first with hq kill", strings.Join(busy, ", "), strings.Join(running, ", "))
	}
	its := "its state"
	if len(sbs) > 1 {
		its = "their state"
	}
	if !yes {
		if ok, err := confirm(env, d, fmt.Sprintf("Remove %s and %s?", sandboxNames(sbs), its)); !ok || err != nil {
			return err
		}
	}
	if len(sbs) == 1 {
		if err := d.sbx.Remove(sbs[0].Name); err != nil {
			return sbxErr(err)
		}
		forgetLoaded(d, sbs)
		fmt.Fprintf(env.Stdout, "removed %s\n", sandboxNames(sbs))
		return nil
	}
	// Of several, one sbx fails to remove leaves the others to remove; hq
	// says which it removed and which not (#134).
	var removed []sbx.Sandbox
	var failed []string // the sandbox: sbx's error
	for _, sb := range sbs {
		err := d.sbx.Remove(sb.Name)
		var pe *proc.Error
		switch {
		case errors.As(err, &pe) && pe.NotFound:
			return sbxErr(err)
		case err != nil:
			failed = append(failed, sb.Name+": "+sbxErr(err).Error())
		default:
			removed = append(removed, sb)
		}
	}
	if len(removed) > 0 {
		forgetLoaded(d, removed)
		fmt.Fprintf(env.Stdout, "removed %s\n", sandboxNames(removed))
	}
	if len(failed) > 0 {
		return envErr("could not remove %s", strings.Join(failed, "; "))
	}
	return nil
}

// restartCommand is the hq sandbox restart that relaunches a: its
// repository's, with its setup unless that is the default (#136).
func restartCommand(a agent.Agent) string {
	cmd := "hq sandbox restart " + a.Repo()
	if setup := a.Setup(); setup != prefs.DefaultSetup {
		cmd += " --setup " + setup
	}
	return cmd
}

// sessionID is the form of a Claude session id; anything else in a state
// file, which the sandbox writes, is not passed to Claude as an argument.
var sessionID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// sandboxRestart stops and starts the sandboxes, then relaunches each of
// their agents in its own pane, continuing its conversation (design §3.6). The
// agents keep their windows, names and ids throughout, so their rows stay
// (ended, then starting), a docked agent stays docked, and what they last
// reported stays until they report anew. The agents of the sandboxes that
// tmux lost are restored in new windows, as hq restore does (#140).
func sandboxRestart(env Env, d deps, sbs []sbx.Sandbox, mine, lost []agent.Agent, yes bool) error {
	var names []string
	for _, a := range mine {
		names = append(names, a.Name)
	}
	if !yes {
		q := fmt.Sprintf("Restart %s?", sandboxNames(sbs))
		if len(mine) > 0 {
			q += fmt.Sprintf(" This ends and relaunches %s (%s)", plural(len(mine), "agent"), strings.Join(names, ", "))
		}
		if len(lost) > 0 {
			if len(mine) > 0 {
				q += " and restores"
			} else {
				q += " This restores"
			}
			q += fmt.Sprintf(" %s (%s)", plural(len(lost), "lost agent"), strings.Join(agentNames(lost), ", "))
		}
		if len(mine)+len(lost) > 0 {
			q += "."
		}
		if ok, err := confirm(env, d, q); !ok || err != nil {
			return err
		}
	}
	// Each agent continues its conversation: the Claude session of its latest
	// report, read before the agents go. One that never reported starts fresh.
	plans := planRestore(d, sbs, lost)
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
	for _, sb := range sbs {
		if err := d.sbx.Stop(sb.Name); err != nil {
			return sbxErr(err)
		}
	}
	// A relaunched agent keeps its id, so its old session must be done
	// writing its state file before the new one starts; sbx stop usually
	// returns after that.
	if err := waitEnded(d, mine, nil); err != nil {
		return err
	}
	// sbx has no start command; running anything in a sandbox starts it.
	for _, sb := range sbs {
		if err := d.sbx.Exec(sb.Name, "true"); err != nil {
			return sbxErr(err)
		}
	}
	reloaded, notLoaded := reloadHq(d, sbs)
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
	restored, err := restoreLost(d, plans)
	line := "restarted " + sandboxNames(sbs)
	switch {
	case len(reloaded) == 0:
	case len(sbs) == 1:
		line += "; loaded hq into it again"
	default:
		line += "; loaded hq into " + strings.Join(reloaded, ", ") + " again"
	}
	if len(names) > 0 {
		line += "; relaunched " + strings.Join(names, ", ")
	}
	if len(restored) > 0 {
		line += "; restored " + strings.Join(agentNames(restored), ", ")
	}
	fmt.Fprintln(env.Stdout, line)
	reportNotRestored(env.Stdout, plans)
	if err == nil && len(notLoaded) > 0 {
		err = envErr("%s", strings.Join(notLoaded, "; "))
	}
	return err
}

// reloadHq loads hq again into those of the sandboxes, just restarted,
// that hq mcp load loaded it into: sbx loses it with the restart (#141).
// It returns the sandboxes it loaded it into and, for each it could not,
// why and what loads it.
func reloadHq(d deps, sbs []sbx.Sandbox) (loaded, failed []string) {
	recorded := d.loadPrefs().MCPLoaded
	for _, sb := range sbs {
		if !slices.Contains(recorded, sb.Name) {
			continue
		}
		if err := d.sbx.MCPLoad(sbxServer, sb.Name); err != nil {
			failed = append(failed, fmt.Sprintf("could not load hq into %s again (%v); run hq mcp load %s", sb.Name, sbxErr(err), sb.Name))
			continue
		}
		loaded = append(loaded, sb.Name)
	}
	return loaded, failed
}

// forgetLoaded drops removed sandboxes from those hq mcp load loaded hq
// into, so a new sandbox of the same name does not get it. A failure only
// leaves a name that loads nothing until such a sandbox exists.
func forgetLoaded(d deps, removed []sbx.Sandbox) {
	_ = d.updatePrefs(func(p *prefs.Prefs) bool {
		kept := slices.DeleteFunc(slices.Clone(p.MCPLoaded), func(name string) bool {
			return slices.ContainsFunc(removed, func(sb sbx.Sandbox) bool { return sb.Name == name })
		})
		if len(kept) == len(p.MCPLoaded) {
			return false
		}
		p.MCPLoaded = kept
		return true
	})
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
	// An agent an older hq started gets the record hq restore needs.
	return keepRecord(d, a)
}
