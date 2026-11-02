package cli

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/sbx"
)

const sandboxUsage = "usage: hq sandbox rm|restart REPO|SANDBOX [--setup NAME|--all-setups] [-y]"

// sandboxArgs is hq sandbox's arguments after parsing: setup "" is the
// default setup's sandbox, all every sandbox of the repository.
type sandboxArgs struct {
	cmd, repo, setup string
	all, yes         bool
}

func parseSandbox(args []string) (sandboxArgs, error) {
	var s sandboxArgs
	var plain []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		v, isSetup := strings.CutPrefix(a, "--setup=")
		if a == "--setup" {
			isSetup, v = true, ""
			if i+1 < len(args) {
				i++
				v = args[i]
			}
		}
		switch {
		case isSetup && v == "":
			return s, usageErr("--setup needs a setup's name (%s)", sandboxUsage)
		case isSetup:
			s.setup = v
		case a == "--all-setups":
			s.all = true
		case a == "-y" || a == "--yes":
			s.yes = true
		case strings.HasPrefix(a, "-"):
			return s, usageErr(sandboxUsage)
		default:
			plain = append(plain, a)
		}
	}
	if len(plain) != 2 || (plain[0] != "rm" && plain[0] != "restart") {
		return s, usageErr(sandboxUsage)
	}
	if s.all && s.setup != "" {
		return s, usageErr("--setup and --all-setups exclude each other (%s)", sandboxUsage)
	}
	s.cmd, s.repo = plain[0], plain[1]
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
	return sandboxRestart(env, d, sbs, mine, s.yes)
}

// resolveSandboxes finds the sandboxes REPO and the flags name, for a
// directory in the repository (resolved as in hq new) or the repository's
// name as hq ls shows it: its default sandbox, the sandbox of --setup, or
// with --all-setups every one it has (sbx.OfRepository); without a flag
// REPO may also be a sandbox's own name as sbx ls shows it, whatever its
// setup (#113, #135, #136).
func resolveSandboxes(d deps, a sandboxArgs) ([]sbx.Sandbox, error) {
	all, err := d.sbx.List()
	if err != nil {
		return nil, sbxErr(err)
	}
	setup := a.setup
	if setup == "" {
		setup = sbx.DefaultSetup
	}
	pick := func(root string) []sbx.Sandbox {
		if a.all {
			return sbx.OfRepository(all, root, d.samePath)
		}
		if s, ok := sbx.Find(all, root, setup, d.samePath); ok {
			return []sbx.Sandbox{s}
		}
		return nil
	}
	dir := a.repo
	if !filepath.IsAbs(dir) {
		cwd, err := d.getwd()
		if err != nil {
			return nil, envErr("%v", err)
		}
		dir = filepath.Join(cwd, dir)
	}
	if d.isDir(dir) {
		root, ok := d.repoRoot(dir)
		if !ok {
			return nil, notFoundErr("%s is not in a git repository", dir)
		}
		if sbs := pick(root); len(sbs) > 0 {
			return sbs, nil
		}
		return nil, noSandboxErr(filepath.Base(root), a.setup)
	}
	// The repositories of that name, each once, and what each has of them.
	var repos []string
	var found [][]sbx.Sandbox
	for _, s := range all {
		if len(s.Workspaces) == 0 || filepath.Base(s.Workspaces[0]) != a.repo || slices.ContainsFunc(repos, func(r string) bool { return d.samePath(r, s.Workspaces[0]) }) {
			continue
		}
		repos = append(repos, s.Workspaces[0])
		if sbs := pick(s.Workspaces[0]); len(sbs) > 0 {
			found = append(found, sbs)
		}
	}
	flag := a.setup != "" || a.all
	named := slices.IndexFunc(all, func(s sbx.Sandbox) bool { return s.Name == a.repo })
	switch {
	case named >= 0 && !flag && !slices.ContainsFunc(found, func(sbs []sbx.Sandbox) bool { return sbs[0].Name == a.repo }):
		found = append(found, []sbx.Sandbox{all[named]})
	case named >= 0 && len(repos) == 0 && a.all:
		return nil, usageErr("--all-setups takes a repository, and %s is a sandbox's name", a.repo)
	case named >= 0 && len(repos) == 0:
		return nil, usageErr("--setup takes a repository, and %s is a sandbox's name", a.repo)
	}
	switch {
	case len(found) == 1:
		return found[0], nil
	case len(found) > 1 && a.all:
		return nil, usageErr("'%s' names %d repositories; give the repository's directory", a.repo, len(found))
	case len(found) > 1:
		return nil, usageErr("'%s' names %d repositories; give the repository's directory or its sandbox's name", a.repo, len(found))
	case len(repos) > 0 && a.setup != "":
		return nil, noSandboxErr(a.repo, a.setup)
	}
	return nil, notFoundErr("no sandbox for '%s' (see hq ls)", a.repo)
}

// noSandboxErr says the repository in directory dir has no sandbox of
// setup ("" being the default, or with --all-setups any) and which hq new
// creates it.
func noSandboxErr(dir, setup string) error {
	if setup == "" {
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
	for _, sb := range sbs {
		if err := d.sbx.Remove(sb.Name); err != nil {
			return sbxErr(err)
		}
	}
	fmt.Fprintf(env.Stdout, "removed %s\n", sandboxNames(sbs))
	return nil
}

// restartCommand is the hq sandbox restart that relaunches a: its
// repository's, with its setup unless that is the default (#136).
func restartCommand(a agent.Agent) string {
	cmd := "hq sandbox restart " + a.Repo()
	if setup := sbx.SetupOf(a.Sandbox, a.Repo()); setup != sbx.DefaultSetup {
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
// reported stays until they report anew.
func sandboxRestart(env Env, d deps, sbs []sbx.Sandbox, mine []agent.Agent, yes bool) error {
	var names []string
	for _, a := range mine {
		names = append(names, a.Name)
	}
	if !yes {
		q := fmt.Sprintf("Restart %s?", sandboxNames(sbs))
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
		fmt.Fprintf(env.Stdout, "restarted %s\n", sandboxNames(sbs))
		return nil
	}
	fmt.Fprintf(env.Stdout, "restarted %s; relaunched %s\n", sandboxNames(sbs), strings.Join(names, ", "))
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
