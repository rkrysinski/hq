package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/state"
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

// attentionRank orders states for hq ls: ended last (the full attention
// order of spec §6.2 comes with #20).
func attentionRank(s string) int {
	if s == state.Ended {
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
	as := withStates(d, agent.FromWindows(ws))
	sortAttention(as)
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
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", r.Name, r.Repo, dash(r.Branch), r.State, agent.Age(time.Duration(r.AgeSeconds)*time.Second), dash(truncate(r.Last, lastWidth)))
	}
	return tw.Flush()
}

// withStates adds each agent's reported state (design §3.4).
func withStates(d deps, as []agent.Agent) []agent.Agent {
	for i := range as {
		as[i].Apply(d.readState(as[i].RepoPath, as[i].ID))
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

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
