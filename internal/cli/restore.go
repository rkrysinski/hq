package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/sbx"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
)

const restoreUsage = "usage: hq restore [REPO]"

// server is the tmux server hq runs with: the -L socket name, "" for the
// default server (design §3.2).
func server(d deps) string { return d.getenv("HQ_TMUX_SOCKET") }

// keepRecord keeps hq's record of a beside its state files, for hq restore
// (design §3.3, #140).
func keepRecord(d deps, a agent.Agent) error {
	if err := d.writeRecord(a.RepoPath, a.ID, state.Record{Name: a.Name, Sandbox: a.Sandbox, Server: server(d)}); err != nil {
		return envErr("could not keep a record of %s in %s: %v", a.Name, state.Dir(a.RepoPath), err)
	}
	return nil
}

// knownRepositories are the repositories hq knows: the primary workspace
// of each sandbox, each once.
func knownRepositories(d deps, all []sbx.Sandbox) []string {
	var roots []string
	for _, s := range all {
		if len(s.Workspaces) > 0 && !slices.ContainsFunc(roots, func(r string) bool { return d.samePath(r, s.Workspaces[0]) }) {
			roots = append(roots, s.Workspaces[0])
		}
	}
	return roots
}

// lostAgents are the agents of the repositories at roots that this tmux
// server lost (#140): hq keeps a record of each, made on this server, but
// no window has its id. A record whose name a window holds now was
// replaced by that agent and is no lost agent; of two lost agents of one
// name, the first counts. They come by repository, each one's by name. A record's name is checked as hq new checks
// one, since the sandbox writes in the same directory (design §7.3).
func lostAgents(d deps, ws []tmux.Window, roots []string) []agent.Agent {
	live := agent.FromWindows(ws)
	var lost []agent.Agent
	var seen []string
	for _, root := range roots {
		if slices.ContainsFunc(seen, func(r string) bool { return d.samePath(r, root) }) {
			continue
		}
		seen = append(seen, root)
		records, _ := d.records(root)
		slices.SortStableFunc(records, func(a, b state.Record) int { return strings.Compare(a.Name, b.Name) })
		for _, r := range records {
			taken := func(a agent.Agent) bool { return a.ID == r.ID || a.Name == r.Name }
			if r.Server != server(d) || checkName(r.Name) != nil || slices.ContainsFunc(live, taken) || slices.ContainsFunc(lost, taken) {
				continue
			}
			lost = append(lost, agent.Agent{ID: r.ID, Name: r.Name, RepoPath: root, Sandbox: r.Sandbox})
		}
	}
	return lost
}

func runRestore(env Env, d deps, args []string) error {
	if len(args) > 1 || len(args) == 1 && strings.HasPrefix(args[0], "-") {
		return usageErr(restoreUsage)
	}
	if err := checkTmux(d.tmux); err != nil {
		return err
	}
	all, err := d.sbx.List()
	if err != nil {
		return sbxErr(err)
	}
	roots := knownRepositories(d, all)
	if len(args) == 1 {
		if roots, err = repositoryOf(d, all, args[0]); err != nil {
			return err
		}
	}
	ws, err := d.tmux.Windows()
	if err != nil {
		return tmuxErr(err)
	}
	lost := lostAgents(d, ws, roots)
	if len(lost) == 0 {
		fmt.Fprintln(env.Stdout, "nothing to restore")
		return nil
	}
	plans := planRestore(d, all, lost)
	if err := endLost(d, all, plans); err != nil {
		return err
	}
	restored, err := restoreLost(d, plans)
	for _, a := range restored {
		fmt.Fprintf(env.Stdout, "restored %s in %s (sandbox %s)\n", a.Name, a.Repo(), a.Sandbox)
	}
	reportNotRestored(env.Stdout, plans)
	reloadNotice(env.Stdout, d, restored)
	return err
}

// repositoryOf is the repository REPO names, as for hq sandbox: a
// directory in it, or its name as hq ls shows it, among the repositories
// hq knows.
func repositoryOf(d deps, all []sbx.Sandbox, repo string) ([]string, error) {
	dir := repo
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
		return []string{root}, nil
	}
	switch roots := repositoriesNamed(d, all, repo); len(roots) {
	case 0:
		return nil, notFoundErr("no repository '%s' (see hq ls)", repo)
	case 1:
		return roots, nil
	default:
		return nil, usageErr("'%s' names %d repositories; give the repository's directory", repo, len(roots))
	}
}

// restorePlan is what hq restore does with a lost agent: relaunch it,
// continuing the Claude session resume, or, when why says why it cannot,
// report it.
type restorePlan struct {
	a      agent.Agent
	resume string
	why    string
}

// planRestore decides, for each lost agent, whether hq can bring it back
// (#140): its sandbox must be one of its repository's, its worktree must
// still be there, and it must have a conversation to continue, as hq
// sandbox restart takes it from its latest report.
func planRestore(d deps, all []sbx.Sandbox, lost []agent.Agent) []restorePlan {
	var plans []restorePlan
	for _, a := range lost {
		p := restorePlan{a: a}
		r, ok := d.readState(a.RepoPath, a.ID)
		switch {
		case !slices.ContainsFunc(sbx.OfRepository(all, a.RepoPath, d.samePath), func(s sbx.Sandbox) bool { return s.Name == a.Sandbox }):
			p.why = "its sandbox " + a.Sandbox + " is gone"
		case ok && r.Cwd != "" && worktreeGone(d, r.Cwd):
			dir, _ := d.fromSbx(r.Cwd)
			p.why = "its worktree " + dir + " is gone"
		case !ok || !sessionID.MatchString(r.SessionID):
			p.why = "it has no conversation to continue"
		default:
			p.resume = r.SessionID
		}
		plans = append(plans, p)
	}
	return plans
}

// worktreeGone reports whether the directory Claude worked in, as the
// sandbox sees it, is no longer there; unknown when hq cannot tell.
func worktreeGone(d deps, cwd string) bool {
	dir, err := d.fromSbx(cwd)
	return err == nil && !d.isDir(dir)
}

// endLost ends the sessions of the lost agents to restore that still run
// in their sandbox, and waits for them as hq kill does: tmux going takes
// only the host side of sbx run, and Claude runs on in the sandbox, which
// would write the same state files as the restored session. A sandbox sbx
// lists as not running is left alone: sbx exec would start it.
func endLost(d deps, all []sbx.Sandbox, plans []restorePlan) error {
	var orphans []agent.Agent
	for _, p := range plans {
		running := slices.ContainsFunc(all, func(s sbx.Sandbox) bool { return s.Name == p.a.Sandbox && s.Running() })
		if p.why != "" || !running {
			continue
		}
		// No match (the session already ended) fails, and is not an error.
		if d.sbx.Exec(p.a.Sandbox, "pkill", "-TERM", "-f", sessionPattern(p.a.ID)) == nil {
			orphans = append(orphans, p.a)
		}
	}
	return waitEnded(d, nil, orphans)
}

// restoreLost relaunches the lost agents hq can bring back in new home
// windows, under their names and ids, as hq sandbox restart relaunches an
// agent (relaunchAgent), and returns them.
func restoreLost(d deps, plans []restorePlan) ([]agent.Agent, error) {
	var restored []agent.Agent
	for _, p := range plans {
		if p.why != "" {
			continue
		}
		_ = d.withdraw(p.a.RepoPath, p.a.ID)
		if err := launchAgent(d, p.a, p.resume, "", false); err != nil {
			return restored, err
		}
		restored = append(restored, p.a)
	}
	return restored, nil
}

// agentNames are the agents' names, in order.
func agentNames(as []agent.Agent) []string {
	var names []string
	for _, a := range as {
		names = append(names, a.Name)
	}
	return names
}

// reportNotRestored says of each lost agent hq could not bring back why,
// and how to forget it.
func reportNotRestored(out io.Writer, plans []restorePlan) {
	for _, p := range plans {
		if p.why != "" {
			fmt.Fprintf(out, "not restored %s in %s: %s (hq kill %s forgets it)\n", p.a.Name, p.a.Repo(), p.why, p.a.Name)
		}
	}
}
