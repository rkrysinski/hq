package cli

import (
	"encoding/json"
	"fmt"
	"io"
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
	Pending    int       `json:"pending"` // messages waiting for it (hq send)
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
	// A working agent's screen at rest may be a turn the user rewound,
	// which it is once it stays so a while (design §3.4): look again then,
	// so hq ls tells the turn's end at once, like the list.
	if resting(as) {
		d.sleep(agent.RestDelay)
		if as, err = collect(d); err != nil {
			return err
		}
	}
	agent.SortAttention(as)
	rows := lsRows(as, d.now())
	if asJSON {
		return writeJSON(env.Stdout, rows)
	}
	return writeTable(env.Stdout, rows)
}

// lsRows are the agents as hq ls shows them at now, in the order given.
func lsRows(as []agent.Agent, now time.Time) []lsRow {
	rows := make([]lsRow, 0, len(as))
	for _, a := range as {
		rows = append(rows, lsRow{
			Name: a.Name, Repo: a.Repo(), RepoPath: a.RepoPath, Branch: a.Branch, State: a.State,
			Since: a.Since.UTC(), AgeSeconds: max(0, int64(now.Sub(a.Since).Seconds())), Last: a.Last, Sandbox: a.Sandbox,
			Pending: d.pending(a.RepoPath, a.ID),
		})
	}
	return rows
}

// writeJSON prints v as indented JSON.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// writeTable prints rows as hq ls's columns; nothing when there are none.
func writeTable(w io.Writer, rows []lsRow) error {
	if len(rows) == 0 {
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
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
	return collectWith(d, runningSandboxes(d))
}

// collectWith is collect with sbx's answer given: which sandboxes run, nil
// when sbx could not say.
func collectWith(d deps, running map[string]bool) ([]agent.Agent, error) {
	ws, err := d.tmux.Windows()
	if err != nil {
		return nil, tmuxErr(err)
	}
	return seeEnds(d, settle(d, agent.Collect(ws, d.readState, running))), nil
}

// runningSandboxes asks sbx which sandboxes run; nil when it fails or does
// not answer in time.
func runningSandboxes(d deps) map[string]bool {
	sbs, err := d.pollSandboxes()
	if err != nil {
		return nil
	}
	running := map[string]bool{}
	for _, s := range sbs {
		running[s.Name] = s.Running()
	}
	return running
}

// settle looks at the screens of agents whose turn the user may have ended,
// which no hook reports, and records when hq first saw it on their windows
// (design §3.4): the turn's end, or the screen at rest of a turn the user
// may have rewound. When tmux cannot show the screens, the hooks' states
// stand.
func settle(d deps, as []agent.Agent) []agent.Agent {
	now := d.now()
	var panes []string
	for i := range as {
		if as[i].Unsettled(now) && !as[i].Recall() {
			panes = append(panes, as[i].Pane)
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
			keep(d, &as[i], as[i].Settle(s, now))
		}
	}
	return as
}

// keep stores what hq saw on the agent's window, unless another hq process
// stored its record of the same thing first, which the agent then shows,
// so that every process shows the same moment (design §3.4).
func keep(d deps, a *agent.Agent, r agent.Record) {
	if r.Value == "" {
		return
	}
	if stored, err := d.tmux.KeepFirst(a.Window, r.Option, r.Key, r.Value); err == nil {
		a.Keep(r, stored)
	}
}

// resting reports whether a working agent's screen is at rest, not yet for
// long enough to be a turn the user rewound.
func resting(as []agent.Agent) bool {
	for _, a := range as {
		if a.Resting() {
			return true
		}
	}
	return false
}

// seeEnds records on their windows when hq first saw ended the agents
// whose end nothing else dates (design §3.4), so hq ls and the list agree.
func seeEnds(d deps, as []agent.Agent) []agent.Agent {
	now := d.now()
	for i := range as {
		keep(d, &as[i], as[i].SeeEnd(now))
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
