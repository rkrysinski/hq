package cli

import (
	"bufio"
	"fmt"
	"strings"
	"time"

	"github.com/rkrysinski/hq/internal/agent"
)

// endWait is how long hq gives Claude sessions to end after SIGTERM before it
// removes their windows anyway.
const endWait = 5 * time.Second

// parseYes splits the -y flag from the other arguments.
func parseYes(args []string) (rest []string, yes bool, err error) {
	for _, a := range args {
		switch {
		case a == "-y" || a == "--yes":
			yes = true
		case strings.HasPrefix(a, "-"):
			return nil, false, fmt.Errorf("unknown option %s", a)
		default:
			rest = append(rest, a)
		}
	}
	return rest, yes, nil
}

// confirm asks question on the terminal; only y or yes means yes (ADR 0004:
// Enter means No). Without a terminal to ask on, hq refuses.
func confirm(env Env, d deps, question string) (bool, error) {
	if !d.canAsk(env.Stdin) {
		return false, usageErr("no terminal to confirm on; add -y to skip the confirmation")
	}
	fmt.Fprintf(env.Stdout, "%s [y/N] ", question)
	line, _ := bufio.NewReader(env.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	}
	return false, nil
}

// agents lists every agent after checking the environment.
func agents(d deps) ([]agent.Agent, error) {
	if err := checkTmux(d.tmux); err != nil {
		return nil, err
	}
	ws, err := d.tmux.Windows()
	if err != nil {
		return nil, tmuxErr(err)
	}
	return agent.FromWindows(ws), nil
}

func runKill(env Env, d deps, args []string) error {
	rest, yes, err := parseYes(args)
	if err != nil || len(rest) != 1 {
		return usageErr("usage: hq kill NAME [-y]")
	}
	name := rest[0]
	as, err := agents(d)
	if err != nil {
		return err
	}
	a, ok := agent.Find(as, name)
	if !ok {
		return notFoundErr("no agent '%s' (see hq ls)", name)
	}
	if !yes {
		if ok, err := confirm(env, d, fmt.Sprintf("Kill %s (%s)?", a.Name, a.Repo())); !ok || err != nil {
			return err
		}
	}
	if err := endAgents(d, []agent.Agent{a}); err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "killed %s; the sandbox stays\n", a.Name)
	return nil
}

func runStop(env Env, d deps, args []string) error {
	rest, yes, err := parseYes(args)
	if err != nil || len(rest) != 0 {
		return usageErr("usage: hq stop [-y]")
	}
	as, err := agents(d)
	if err != nil {
		return err
	}
	if len(as) == 0 {
		fmt.Fprintln(env.Stdout, "no agents")
		return nil
	}
	if !yes {
		if ok, err := confirm(env, d, fmt.Sprintf("End all %s?", plural(len(as), "agent"))); !ok || err != nil {
			return err
		}
	}
	if err := endAgents(d, as); err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "ended %s; sandboxes stay\n", plural(len(as), "agent"))
	return nil
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// endAgents ends the agents' Claude sessions and removes their home windows,
// which frees their names (design §3.3). Closing a window alone would leave
// Claude running inside the sandbox, so each running session first gets
// SIGTERM there, found by the HQ_ID in its settings argument.
func endAgents(d deps, as []agent.Agent) error {
	for _, a := range as {
		if a.Alive {
			// No match (the session already ended) is not an error.
			_ = d.sbx.Exec(a.Sandbox, "pkill", "-TERM", "-f", `HQ_ID":"`+a.ID+`"`)
		}
	}
	return removeAgents(d, as)
}

// removeAgents waits up to endWait for the agents' sessions to exit, then
// removes their home windows and their state files (design §3.4): nothing
// reads those once the window is gone. Only these agents' files go; a file
// that cannot be removed does not stop the removal.
func removeAgents(d deps, as []agent.Agent) error {
	pending := map[string]bool{}
	for _, a := range as {
		if a.Alive {
			pending[a.Window] = true
		}
	}
	for deadline := d.now().Add(endWait); len(pending) > 0 && d.now().Before(deadline); {
		d.sleep(100 * time.Millisecond)
		ws, err := d.tmux.Windows()
		if err != nil {
			return tmuxErr(err)
		}
		live := map[string]bool{}
		for _, w := range ws {
			live[w.ID] = !w.PaneDead
		}
		for id := range pending {
			if !live[id] {
				delete(pending, id)
			}
		}
	}
	ws, err := d.tmux.Windows()
	if err != nil {
		return tmuxErr(err)
	}
	present := map[string]bool{}
	for _, w := range ws {
		present[w.ID] = true
	}
	for _, a := range as {
		if !present[a.Window] {
			continue
		}
		if err := d.tmux.KillWindow(a.Window); err != nil {
			return tmuxErr(err)
		}
	}
	for _, a := range as {
		_ = d.removeState(a.RepoPath, a.ID)
	}
	return nil
}
