package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/state"
)

// lsRow is one agent as hq ls --json prints it.
type lsRow struct {
	Name     string `json:"name"`
	Repo     string `json:"repo"`
	RepoPath string `json:"repo_path"`
	Branch   string `json:"branch"`
	State    string `json:"state"`
	// StateSeq is the number of the change that put it in its state, or
	// moved it to another dialog, and CompletionSeq the number of the change
	// that ended its last completed turn (ADR 0019); null when there is no
	// such event (spec §4.1).
	StateSeq      *int64    `json:"state_seq"`
	CompletionSeq *int64    `json:"completion_seq"`
	Since         time.Time `json:"since"`
	AgeSeconds    int64     `json:"age_seconds"`
	Last          string    `json:"last"`
	Sandbox       string    `json:"sandbox"`
	// Setup is the setup its sandbox was created for, read off the
	// sandbox's name (#135).
	Setup   string `json:"setup"`
	Pending int    `json:"pending"` // messages waiting for it (hq send)
	// Dialog is the open dialog's id while it needs input, which hq answer
	// names it by; absent otherwise.
	Dialog string `json:"dialog,omitempty"`
	// OwnTasks are its own tasks at the turn end its done or question comes
	// from (#78), in full; [] when none, never null.
	OwnTasks []state.Task `json:"own_tasks"`
	Asks     *state.Ask   `json:"asks"` // what the agent asks while question or needs input, else null
}

const lsUsage = "hq ls [NAME...] [--json]"

// runLs lists the agents named, every agent when none is (spec §4.1,
// #291). The look it lists is the whole look: messages wait for every
// agent of it are typed in, and with --json it is covered as a whole, so
// its cursor stands for every agent, those named among them; the rows are
// filtered after that, and a name no agent of it has is the look's refusal
// (agent.Seen.Named), with nothing listed.
func runLs(env Env, d deps, args []string) error {
	asJSON := false
	var names []string
	f := flagsOf("ls")
	for _, a := range args {
		switch {
		case f.is(a, "--json"):
			asJSON = true
		case strings.HasPrefix(a, "-"):
			return unknownOption(a, lsUsage)
		case !slices.Contains(names, a):
			names = append(names, a)
		}
	}
	seen, err := gatherAgents(d)
	if err != nil {
		return err
	}
	var cursor int64
	if asJSON {
		if seen, cursor, err = d.looker().Cover(seen); err != nil {
			return lookErr(err)
		}
	}
	as := seen.Agents
	if len(names) > 0 {
		as = nil
		for _, n := range names {
			a, err := seen.Named(n)
			if err != nil {
				return refusalErr(err)
			}
			as = append(as, a)
		}
	}
	agent.SortAttention(as)
	rows := lsRows(d, as)
	if asJSON {
		return writeJSON(env.Stdout, waitOutput{Agents: rows, NextSince: cursorText(cursor)})
	}
	return writeTable(env.Stdout, rows)
}

// lsRows are the agents as hq ls shows them now, in the order given, with
// how many messages wait for each: the rows of a list (hq ls, hq wait, hq
// send --wait), each with its last cut to listLastWidth (#291).
func lsRows(d deps, as []agent.Agent) []lsRow {
	now := d.now()
	rows := make([]lsRow, 0, len(as))
	for _, a := range as {
		r := newLsRow(d, a, now)
		r.Last = truncate(r.Last, listLastWidth)
		rows = append(rows, r)
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
		// LAST starts with the count of its own tasks, which --json lists.
		last := agent.OwnPrefix(r.OwnTasks) + orDash(r.Last)
		// An outdated agent's state, and so its age, is unknown.
		age := "-"
		if r.State != state.Outdated {
			age = agent.Age(time.Duration(r.AgeSeconds) * time.Second)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", r.Name, r.Repo, orDash(r.Branch), r.State, age, truncate(last, lastWidth))
	}
	return tw.Flush()
}

// newLsRow is agent a as hq ls --json shows it at now, with how many
// messages wait for it and what it asks. Only an agent that may ask has
// its detail read.
func newLsRow(d deps, a agent.Agent, now time.Time) lsRow {
	var det state.Detail
	if a.Dialog().Open() || a.State == state.Question {
		det = a.Detail()
	}
	return lsRowWithDetail(d, a, now, det)
}

// lsRowWithDetail is newLsRow with the detail of a's report given (det).
func lsRowWithDetail(d deps, a agent.Agent, now time.Time, det state.Detail) lsRow {
	stateSeq, completionSeq := seqs(a)
	return lsRow{
		Name: a.Name, Repo: a.Repo(), RepoPath: a.RepoPath, Branch: a.Branch, State: a.State,
		StateSeq: stateSeq, CompletionSeq: completionSeq, Since: a.Since.UTC(), AgeSeconds: max(0, int64(now.Sub(a.Since).Seconds())), Last: a.Last, Sandbox: a.Sandbox,
		Setup:   a.Setup(),
		Pending: d.pending(a.RepoPath, a.ID), Dialog: a.Dialog().ID(), OwnTasks: append([]state.Task{}, a.OwnTasks...), Asks: asks(a, det),
	}
}

// asks is what agent a asks, its report's detail det: what the dialog it is
// at asks, as its dialog verdict says (the detail's for a dialog its hooks
// reported, the screen's for one of Claude's own), the end of its reply,
// where the question is, while it asks one; nil otherwise.
func asks(a agent.Agent, det state.Detail) *state.Ask {
	switch dialog := a.Dialog(); {
	case dialog.Open():
		return dialog.Asks(det.Ask)
	case a.State == state.Question:
		if q := lastParagraph(det.Reply); q != "" {
			return &state.Ask{Message: q}
		}
	}
	return nil
}

// gatherAgents gathers the agents as hq ls shows them, once tmux is known
// to be fit: their look (agent.Shown), with messages left waiting for
// agents at their prompt typed in (deliverWaiting).
func gatherAgents(d deps) (agent.Seen, error) {
	if err := checkTmux(d.tmux); err != nil {
		return agent.Seen{}, err
	}
	seen, err := d.looker().Look(agent.Shown)
	if err != nil {
		return agent.Seen{}, lookErr(err)
	}
	deliverWaiting(d, seen.Agents)
	return seen, nil
}

// runningSandboxes asks sbx which sandboxes run, for hq kill
// (agent.Looker.AskSbx); the answer says nothing when sbx fails, does not
// answer in time or is too old.
func runningSandboxes(d deps) agent.Sandboxes {
	running, _ := d.looker().AskSbx()
	return running
}

// lastWidth is how much of the last message hq ls shows in its table.
const lastWidth = 60

// listLastWidth is how much of the last message a row of a list carries
// (lsRows), in JSON too; hq read gives it whole (#291).
const listLastWidth = 120

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
