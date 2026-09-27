package cli

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/rkrysinski/hq/internal/agent"
)

// lsRow is one agent as hq ls --json prints it.
type lsRow struct {
	Name       string    `json:"name"`
	Repo       string    `json:"repo"`
	RepoPath   string    `json:"repo_path"`
	Branch     string    `json:"branch"`
	State      string    `json:"state"`
	Since      time.Time `json:"since"`
	AgeSeconds int64     `json:"age_seconds"`
	Last       string    `json:"last"`
	Sandbox    string    `json:"sandbox"`
}

func runLs(env Env, d deps, args []string) error {
	asJSON := false
	for _, a := range args {
		switch a {
		case "--json":
			asJSON = true
		default:
			return usageErr("unexpected argument '%s' (usage: hq ls [--json])", a)
		}
	}
	if err := checkTmux(d.tmux); err != nil {
		return err
	}
	as, err := collect(d)
	if err != nil {
		return err
	}
	agent.SortAttention(as)
	now := d.now()
	rows := make([]lsRow, 0, len(as))
	for _, a := range as {
		rows = append(rows, lsRow{
			Name: a.Name, Repo: a.Repo(), RepoPath: a.RepoPath, Branch: a.Branch, State: a.State,
			Since: a.Since.UTC(), AgeSeconds: max(0, int64(now.Sub(a.Since).Seconds())), Last: a.Last, Sandbox: a.Sandbox,
		})
	}
	if asJSON {
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	if len(rows) == 0 {
		return nil
	}
	tw := tabwriter.NewWriter(env.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tREPO\tBRANCH\tSTATE\tAGE\tLAST")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", r.Name, r.Repo, orDash(r.Branch), r.State, agent.Age(time.Duration(r.AgeSeconds)*time.Second), orDash(truncate(r.Last, lastWidth)))
	}
	return tw.Flush()
}

// collect gathers the agents with their state from tmux, the state files and
// sbx (design §5.1). When sbx fails or does not answer in time, the states
// come from tmux and the hooks alone.
func collect(d deps) ([]agent.Agent, error) {
	ws, err := d.tmux.Windows()
	if err != nil {
		return nil, tmuxErr(err)
	}
	var running map[string]bool
	if sbs, err := d.pollSandboxes(); err == nil {
		running = map[string]bool{}
		for _, s := range sbs {
			running[s.Name] = s.Running()
		}
	}
	return settle(d, agent.Collect(ws, d.readState, running)), nil
}

// settle looks at the screens of agents whose turn may have ended with Esc,
// which no hook reports, and records when hq first saw it on their windows
// (design §3.4). When tmux cannot show the screens, the hooks' states stand.
func settle(d deps, as []agent.Agent) []agent.Agent {
	now := d.now()
	var panes []string
	for _, a := range as {
		if a.Unsettled(now) {
			panes = append(panes, a.Pane)
		}
	}
	if len(panes) == 0 {
		return as
	}
	screens, err := d.tmux.Screens(panes)
	if err != nil {
		return as
	}
	for i := range as {
		if s, ok := screens[as[i].Pane]; ok && as[i].Unsettled(now) {
			if record := as[i].Settle(s, now); record != "" {
				_ = d.tmux.SetOption(as[i].Window, "turnend", record)
			}
		}
	}
	return as
}

// lastWidth is how much of the last message hq ls shows; --json has it all.
const lastWidth = 60

// truncate shortens s to n characters, marking the cut with an ellipsis.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
