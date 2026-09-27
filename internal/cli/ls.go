package cli

import (
	"encoding/json"
	"fmt"
	"sort"
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

// attentionRank orders states as spec §6.2's attention sort; M1 knows only
// running (reported like working) and ended.
func attentionRank(state string) int {
	if state == agent.Ended {
		return 1
	}
	return 0
}

// sortAttention sorts agents in attention order, newest first within a state.
func sortAttention(as []agent.Agent) {
	sort.SliceStable(as, func(i, j int) bool {
		ri, rj := attentionRank(as[i].State), attentionRank(as[j].State)
		if ri != rj {
			return ri < rj
		}
		return as[i].Started.After(as[j].Started)
	})
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
	ws, err := d.tmux.Windows()
	if err != nil {
		return tmuxErr(err)
	}
	as := agent.FromWindows(ws)
	sortAttention(as)
	now := d.now()
	rows := make([]lsRow, 0, len(as))
	for _, a := range as {
		rows = append(rows, lsRow{
			Name: a.Name, Repo: a.Repo(), RepoPath: a.RepoPath, Branch: a.Branch, State: a.State,
			Since: a.Started.UTC(), AgeSeconds: int64(now.Sub(a.Started).Seconds()), Last: a.Last, Sandbox: a.Sandbox,
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
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", r.Name, r.Repo, dash(r.Branch), r.State, agent.Age(time.Duration(r.AgeSeconds)*time.Second), dash(r.Last))
	}
	return tw.Flush()
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
