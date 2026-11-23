package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/sbx"
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

func runLs(env Env, d deps, args []string) error {
	asJSON := false
	f := flagsOf("ls")
	for _, a := range args {
		if !f.is(a, "--json") {
			return usageErr("unexpected argument '%s' (usage: hq ls [--json])", a)
		}
		asJSON = true
	}
	seen, err := gatherAgents(d)
	if err != nil {
		return err
	}
	var cursor int64
	if asJSON {
		if seen, cursor, err = seen.Covered(); err != nil {
			return lookErr(err)
		}
	}
	as := seen.Agents
	agent.SortAttention(as)
	rows := lsRows(d, as)
	if asJSON {
		return writeJSON(env.Stdout, waitOutput{Agents: rows, NextSince: cursorText(cursor)})
	}
	return writeTable(env.Stdout, rows)
}

// coverLooks bounds the looks of hq ls --json (agent.CoverLooks).
const coverLooks = agent.CoverLooks

// lsRows are the agents as hq ls shows them now, in the order given, with
// how many messages wait for each.
func lsRows(d deps, as []agent.Agent) []lsRow {
	now := d.now()
	rows := make([]lsRow, 0, len(as))
	for _, a := range as {
		rows = append(rows, newLsRow(d, a, now))
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

// lookNow is a look at the agents in the variant of hq send and hq answer
// (agent.Acting), for the tests that take one this way.
func lookNow(d deps) (looked, error) {
	seen, err := d.looker().Look(agent.Acting)
	return looked{agents: seen.Agents}, lookErr(err)
}

// lookWith is the look of hq wait and hq send --wait, with sbx's answer
// given: which sandboxes run, and when hq asked. It reads the counter
// before it lists the windows, for the look's cursor (looked.cursor).
func lookWith(d deps, running agent.Sandboxes) (looked, error) {
	counter, err := d.journal.Through()
	counted := err == nil
	ws, err := d.tmux.Windows()
	if err != nil {
		return looked{}, tmuxErr(err)
	}
	return looked{agents: d.look(ws, running), windows: ws, counter: counter, counted: counted}, nil
}

// runningSandboxes asks sbx which sandboxes run, noting when it asked; the
// answer says nothing when sbx fails, does not answer in time or is too old.
func runningSandboxes(d deps) agent.Sandboxes {
	running, _ := askSandboxes(d)
	return running
}

// askSandboxes is runningSandboxes, failing only for an sbx too old for hq
// (sbx.TooOldError): an answer, not a failure to give one.
func askSandboxes(d deps) (agent.Sandboxes, error) {
	// The moment before the question: what an agent reports from here on
	// is newer than the answer (#48).
	asked := d.now()
	sbs, err := d.pollSandboxes()
	if err != nil {
		var old *sbx.TooOldError
		if errors.As(err, &old) {
			return agent.Sandboxes{}, err
		}
		return agent.Sandboxes{}, nil
	}
	return agent.SandboxesOf(sbs, asked), nil
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
