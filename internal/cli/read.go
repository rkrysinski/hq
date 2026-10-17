package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/state"
)

// readView is one agent as hq read --json prints it: what hq ls --json has,
// and the agent in full (spec §4.1).
type readView struct {
	lsRow
	Worktree string `json:"worktree"`
	// Reply is a page of the last reply, from Offset: the reply in full
	// when it fits one; LAST is one line of it.
	Reply     string `json:"reply"`
	ReplySize int    `json:"reply_size"` // the whole reply's size, in bytes
	// NextOffset is where the reply's next page starts, for hq read
	// --offset; null on its last page.
	NextOffset *int       `json:"next_offset"`
	Asks       *state.Ask `json:"asks"` // what the agent asks while question or needs input, else null
	// NextSince is the moment of the look, for hq wait --since: what
	// changes after it is returned by hq wait. hq read --since takes it
	// too, to tell whether the reply's next page is of the same reply.
	NextSince time.Time `json:"next_since"`
	offset    int       // where the page starts in the reply
	full      string    // the whole reply
	// answerable says hq answer takes an answer to its dialog.
	answerable bool
}

const readUsage = "hq read NAME [--offset N --since TIME] [--json]"

// readPage is the most of a reply hq read gives at once, in bytes (spec
// §4.1): a reply that is longer comes in pages, each read with --offset.
const readPage = 32 << 10

// readArgs are hq read's arguments.
type readArgs struct {
	name   string
	asJSON bool
	offset int
	since  time.Time // zero without --since
}

func parseRead(args []string, now time.Time) (readArgs, error) {
	var r readArgs
	for i := 0; i < len(args); i++ {
		a := args[i]
		flag, value, inline := strings.Cut(a, "=")
		switch {
		case a == "--json":
			r.asJSON = true
		case flag == "--offset" || flag == "--since":
			if !inline {
				if i+1 == len(args) {
					return r, usageErr("%s needs a value (usage: %s)", flag, readUsage)
				}
				i++
				value = args[i]
			}
			var err error
			if flag == "--since" {
				r.since, err = parseSince(value, now)
			} else if r.offset, err = strconv.Atoi(value); err != nil || r.offset < 0 {
				err = usageErr("--offset '%s' is not a place in the reply: the next_offset of the read before", value)
			}
			if err != nil {
				return r, err
			}
		case r.name == "" && !strings.HasPrefix(a, "-"):
			r.name = a
		default:
			return r, usageErr("usage: %s", readUsage)
		}
	}
	if r.name == "" {
		return r, usageErr("usage: %s", readUsage)
	}
	if r.offset > 0 && r.since.IsZero() {
		return r, usageErr("--offset needs --since: the next_since of the read that gave the offset, so its page is of the same reply")
	}
	return r, nil
}

func runRead(env Env, d deps, args []string) error {
	at := d.now()
	r, err := parseRead(args, at)
	if err != nil {
		return err
	}
	as, err := gatherAgents(d)
	if err != nil {
		return err
	}
	a, ok := agent.Find(as, r.name)
	if !ok {
		return notFoundErr("no agent '%s' (see hq ls)", r.name)
	}
	det := d.readDetail(a.RepoPath, a.ID)
	if r.offset > 0 && replyChanged(det.ReplyAt, r.since) {
		return usageErr("%s's reply changed since %s: read it again from its start, without --offset", a.Name, r.since.Format(time.RFC3339Nano))
	}
	v, err := newReadView(newLsRow(d, a, d.now()), det, a.Dialog(), worktree(d, a), r.offset)
	if err != nil {
		return err
	}
	v.answerable = answerable(a, det.Ask) == nil
	v.NextSince = lookedAt(at)
	if r.asJSON {
		return writeJSON(env.Stdout, v)
	}
	return v.write(env.Stdout)
}

// replyChanged reports whether a reply written at replyAt is newer than
// the look whose next_since is since: a look sees what was written before
// it began, waitSettle after its next_since (lookedAt), so a reply written
// later is another one than the look's.
func replyChanged(replyAt, since time.Time) bool {
	return replyAt.After(since.Add(waitSettle))
}

// newReadView joins the agent as hq ls shows it with what its state files
// hold in full, its reply from offset. What it asks follows its state: what
// the dialog it is at asks, as its dialog verdict says (the detail's for a
// dialog its hooks reported, the screen's for one of Claude's own), the
// end of its reply, where the question is, while it asks one.
func newReadView(row lsRow, det state.Detail, dialog agent.Dialog, worktree string, offset int) (readView, error) {
	page, next, err := replyPage(det.Reply, offset)
	if err != nil {
		return readView{}, err
	}
	v := readView{lsRow: row, Worktree: worktree, Reply: page, ReplySize: len(det.Reply), NextOffset: next, offset: offset, full: det.Reply}
	switch {
	case dialog.Open():
		v.Asks = dialog.Asks(det.Ask)
	case row.State == state.Question:
		if q := lastParagraph(det.Reply); q != "" {
			v.Asks = &state.Ask{Message: q}
		}
	}
	return v, nil
}

// replyPage is the page of reply that starts at offset: at most readPage
// bytes, ending where a character begins, and where the next page starts
// (nil after the last). An offset is where a page starts, as a page before
// gave it.
func replyPage(reply string, offset int) (page string, next *int, err error) {
	if offset > 0 && (offset >= len(reply) || !utf8.RuneStart(reply[offset])) {
		return "", nil, usageErr("--offset %d is not where a page of the reply starts (it has %d bytes): pass the next_offset of the read before", offset, len(reply))
	}
	end := offset + readPage
	if end >= len(reply) {
		return reply[offset:], nil, nil
	}
	for end > offset && !utf8.RuneStart(reply[end]) {
		end--
	}
	if end == offset { // no character starts in the page: never a page without progress
		end = offset + readPage
	}
	return reply[offset:end], &end, nil
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
	// What it left running itself, each on one line, cut as LAST is (#78).
	for _, t := range v.OwnTasks {
		field("own task:", t.Type+"  "+truncate(state.Clean(t.Text), lastWidth))
	}
	field("pending", strconv.Itoa(v.Pending)) // messages hq send left for it
	// LAST says more than the reply when the turn ended without one (an
	// interrupt), a dialog is open, or the session ended.
	if v.Last != state.Clean(v.full) {
		field("last", orDash(v.Last))
	}
	if a := v.Asks; a != nil {
		if v.Dialog == "" {
			b.WriteString("\nasks\n")
		} else {
			b.WriteString("\n")
			field("asks", "dialog "+v.Dialog)
		}
		writeAsk(&b, a)
		if v.answerable {
			b.WriteString("\n")
			field("answer", answerHint(v.Name, v.Dialog, a))
		}
	}
	switch {
	case v.Reply == "":
		b.WriteString("\nreply     none\n")
	case len(v.Reply) == v.ReplySize:
		b.WriteString("\nreply\n" + indent(v.Reply) + "\n")
	default:
		// One page of a longer reply: which bytes, and how to read on.
		fmt.Fprintf(&b, "\nreply     bytes %d-%d of %d\n%s\n", v.offset, v.offset+len(v.Reply)-1, v.ReplySize, indent(v.Reply))
		if v.NextOffset != nil {
			fmt.Fprintf(&b, "\nnext      hq read %s --offset %d --since %s\n", v.Name, *v.NextOffset, v.NextSince.Format(time.RFC3339Nano))
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// answerHint is the hq answer command for the question dialog a, with a
// place for each answer: N for a question that takes one option, N,N for
// one that takes several.
func answerHint(name, dialog string, a *state.Ask) string {
	line := "hq answer " + name + " --dialog " + dialog
	for _, q := range a.Questions {
		if q.MultiSelect {
			line += " N,N"
		} else {
			line += " N"
		}
	}
	return line
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
