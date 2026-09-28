package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/state"
)

// readView is one agent as hq read --json prints it: what hq ls --json has,
// and the agent in full (spec §4.1).
type readView struct {
	lsRow
	Worktree string     `json:"worktree"`
	Reply    string     `json:"reply"` // the last reply in full; LAST is one line of it
	Asks     *state.Ask `json:"asks"`  // what the agent asks while question or needs input, else null
}

func runRead(env Env, d deps, args []string) error {
	name, asJSON := "", false
	for _, a := range args {
		switch {
		case a == "--json":
			asJSON = true
		case name == "" && !strings.HasPrefix(a, "-"):
			name = a
		default:
			return usageErr("usage: hq read NAME [--json]")
		}
	}
	if name == "" {
		return usageErr("usage: hq read NAME [--json]")
	}
	as, err := look(d)
	if err != nil {
		return err
	}
	a, ok := agent.Find(as, name)
	if !ok {
		return notFoundErr("no agent '%s' (see hq ls)", name)
	}
	v := newReadView(newLsRow(d, a, d.now()), d.readDetail(a.RepoPath, a.ID), worktree(d, a))
	if asJSON {
		return writeJSON(env.Stdout, v)
	}
	return v.write(env.Stdout)
}

// newReadView joins the agent as hq ls shows it with what its state files
// hold in full. What it asks follows its state: an open dialog's questions
// or permission while it needs input, the end of its reply, where the
// question is, while it asks one.
func newReadView(row lsRow, det state.Detail, worktree string) readView {
	v := readView{lsRow: row, Worktree: worktree, Reply: det.Reply}
	switch row.State {
	case state.NeedsInput:
		v.Asks = det.Ask
	case state.Question:
		if q := lastParagraph(det.Reply); q != "" {
			v.Asks = &state.Ask{Message: q}
		}
	}
	return v
}

// lastParagraph is the text after the last blank line.
func lastParagraph(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "\n\n"); i >= 0 {
		s = strings.TrimSpace(s[i+2:])
	}
	return s
}

// write prints the view for a person: the agent's facts, then what it
// asks and its last reply, each in full.
func (v readView) write(w io.Writer) error {
	var b strings.Builder
	field := func(k, val string) { fmt.Fprintf(&b, "%-10s%s\n", k, val) }
	field("name", v.Name)
	field("repo", v.Repo+"  "+v.RepoPath)
	field("branch", orDash(v.Branch))
	field("worktree", v.Worktree)
	field("sandbox", v.Sandbox)
	field("state", v.State+" "+agent.Age(time.Duration(v.AgeSeconds)*time.Second))
	field("pending", strconv.Itoa(v.Pending)) // messages hq send left for it
	// LAST says more than the reply when the turn ended without one (an
	// interrupt), a dialog is open, or the session ended.
	if v.Last != state.Clean(v.Reply) {
		field("last", orDash(v.Last))
	}
	if a := v.Asks; a != nil {
		b.WriteString("\nasks\n")
		writeAsk(&b, a)
	}
	if v.Reply == "" {
		b.WriteString("\nreply     none\n")
	} else {
		b.WriteString("\nreply\n" + indent(v.Reply) + "\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// writeAsk prints what the agent asks: each question with its numbered
// options, a permission's tool and what it would run, or the message.
func writeAsk(b *strings.Builder, a *state.Ask) {
	for _, q := range a.Questions {
		line := q.Question
		if q.Header != "" {
			line = q.Header + ": " + line
		}
		if q.MultiSelect {
			line += " (any of)"
		}
		b.WriteString("  " + line + "\n")
		for i, o := range q.Options {
			opt := o.Label
			if o.Description != "" {
				opt += " - " + o.Description
			}
			fmt.Fprintf(b, "    %d. %s\n", i+1, opt)
		}
	}
	if len(a.Questions) > 0 {
		return
	}
	if a.Tool != "" {
		what, desc := a.Command, a.Description
		if what == "" {
			what, desc = desc, ""
		}
		b.WriteString("  " + strings.TrimSuffix(a.Tool+": "+what, ": ") + "\n")
		if desc != "" {
			b.WriteString("  " + desc + "\n")
		}
		return
	}
	b.WriteString(indent(a.Message) + "\n")
}

// indent sets text off by two spaces, blank lines left blank.
func indent(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = "  " + l
		}
	}
	return strings.Join(lines, "\n")
}
