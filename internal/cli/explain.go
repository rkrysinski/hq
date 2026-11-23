package cli

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/state"
)

// hq explain (spec §4.1, #202) shows the events of an agent's journal its
// present state and its dialog verdict rest on, as the reducer names them,
// or with --all every event of it. It shows no text of the conversation:
// no field of an event that holds a prompt, a reply, what a dialog asks or
// the text of a message is printed, in either form, and the reply the
// journal keeps for hq read is never read here. Where a rule depends on
// text it shows the fact (the reply ends with ?). So its output can go
// into a public issue as it is.

const explainUsage = "hq explain NAME [--all] [--json]"

// explainView is an agent's state explained, as hq explain --json prints
// it.
type explainView struct {
	Name       string    `json:"name"`
	State      string    `json:"state"`
	Since      time.Time `json:"since"`
	AgeSeconds int64     `json:"age_seconds"`
	// RestsOn are the numbers of the events the state rests on, oldest
	// first; [] when none, never null.
	RestsOn []int64        `json:"rests_on"`
	Dialog  explainDialog  `json:"dialog"`
	Events  []explainEvent `json:"events"`
}

// explainDialog is the agent's dialog verdict explained: whether it is at
// a dialog, whose ("" when it was never at one), the events that opened
// and closed it (0: none) and every event it rests on.
type explainDialog struct {
	Open    bool    `json:"open"`
	Whose   string  `json:"whose"`
	Opened  int64   `json:"opened"`
	Closed  int64   `json:"closed"`
	RestsOn []int64 `json:"rests_on"`
}

// explainEvent is one event of the journal as hq explain shows it: its
// number, time, source, kind, what it was in words, the state after it,
// why it was set aside if it was, and the facts the rules read that hold
// no text of the conversation.
type explainEvent struct {
	Number int64        `json:"n"`
	Time   time.Time    `json:"time"`
	Source string       `json:"source"`
	Kind   string       `json:"kind"`
	What   string       `json:"what"`
	what   string       // What without why it was set aside
	State  string       `json:"state"`
	Aside  string       `json:"aside,omitempty"`
	Basis  int64        `json:"basis,omitempty"`
	Facts  explainFacts `json:"facts"`
}

// explainFacts are the facts of an event hq explain shows: state.Facts
// without the conversation's text (Prompt, Text), and without what would
// name the user's work or session (Branch, Cwd, Session) or stands for a
// screen's text (Screen).
type explainFacts struct {
	Hook      string            `json:"hook,omitempty"`
	Whose     string            `json:"whose,omitempty"`
	Withdrawn string            `json:"withdrawn,omitempty"`
	Turn      string            `json:"turn,omitempty"`
	Subagent  bool              `json:"subagent,omitempty"`
	Tool      string            `json:"tool,omitempty"`
	Server    string            `json:"server,omitempty"`
	Start     string            `json:"start,omitempty"`
	Reason    string            `json:"reason,omitempty"`
	Notice    string            `json:"notice,omitempty"`
	From      time.Time         `json:"from,omitzero"`
	Asked     time.Time         `json:"asked,omitzero"`
	Question  bool              `json:"question,omitempty"`
	Tasks     []state.EventTask `json:"tasks,omitempty"`
	Started   string            `json:"started,omitempty"`
	Wake      bool              `json:"wake,omitempty"`
	Woke      string            `json:"woke,omitempty"`
	Messages  []string          `json:"messages,omitempty"`
	Size      int64             `json:"size,omitempty"`
}

// explainArgs are hq explain's arguments.
type explainArgs struct {
	name   string
	all    bool
	asJSON bool
}

func parseExplain(args []string) (explainArgs, error) {
	var x explainArgs
	f := flagsOf("explain")
	for _, a := range args {
		switch {
		case f.is(a, "--all"):
			x.all = true
		case f.is(a, "--json"):
			x.asJSON = true
		case x.name == "" && !strings.HasPrefix(a, "-"):
			x.name = a
		default:
			return x, usageErr("usage: %s", explainUsage)
		}
	}
	if x.name == "" {
		return x, usageErr("usage: %s", explainUsage)
	}
	return x, nil
}

// runExplain takes a look at the agents, as hq ls does, so the journal
// holds what the agent's state now rests on, but delivers no message and
// takes an sbx that fails or is too old as one more failed sbx ls (design
// §3.6): what it explains is the journal, which is on the host. An agent
// tmux lost (lostAgents) is explained from its journal alone.
func runExplain(env Env, d deps, args []string) error {
	x, err := parseExplain(args)
	if err != nil {
		return err
	}
	if err := checkTmux(d.tmux); err != nil {
		return err
	}
	seen, err := d.looker().Look(agent.Explained)
	if err != nil {
		return lookErr(err)
	}
	a, err := seen.Named(x.name)
	if err != nil {
		lost, ok := lostAgent(d, x.name)
		if !ok {
			return refusalErr(err)
		}
		a = lost
		// An outdated agent's files are its older hook's: not ingested.
		if !a.Outdated() {
			_ = d.journal.Ingest(a.RepoPath, a.ID)
		}
	}
	if err := a.Refusal(); err != nil {
		return refusalErr(err)
	}
	j, err := d.journal.Read(a.ID)
	if err != nil {
		return envErr("could not read the journal of %s: %v", a.Name, err)
	}
	v := explain(a.Name, j.Events, j.Reply, d.now(), x.all)
	if x.asJSON {
		return writeJSON(env.Stdout, v)
	}
	return v.write(env.Stdout, d.now().Location(), x.all)
}

// explain is the journal events of the agent named name explained at now:
// the state the reducer folds from them, with reply the journal's reply of
// its latest turn end, and the events it and the dialog verdict rest on,
// or with all every event, each with the state after it.
func explain(name string, events []state.Event, reply state.Reply, now time.Time, all bool) explainView {
	after := make([]string, len(events))
	f := state.Fold{}
	for i, e := range events {
		f = f.With(e)
		after[i] = f.Result(reply, now).State
	}
	r := f.Result(reply, now)
	v := explainView{
		Name: name, State: r.State, Since: r.Since.UTC(), AgeSeconds: max(0, int64(r.Age.Seconds())),
		RestsOn: orNone(r.RestsOn),
		Dialog: explainDialog{
			Open: r.Dialog.Open, Whose: r.Dialog.Whose, Opened: r.Dialog.Opened.Number, Closed: r.Dialog.Closed.Number,
			RestsOn: orNone(r.Dialog.RestsOn),
		},
		Events: []explainEvent{},
	}
	for i, e := range events {
		if all || slices.Contains(r.RestsOn, e.Number) || slices.Contains(r.Dialog.RestsOn, e.Number) {
			v.Events = append(v.Events, explainEventOf(e, after[i]))
		}
	}
	return v
}

// orNone is ns, [] for none.
func orNone(ns []int64) []int64 {
	if ns == nil {
		return []int64{}
	}
	return ns
}

// explainEventOf is event e as hq explain shows it, with the state after
// it. Every word of it is cleaned, since the hook's events come from the
// sandbox.
func explainEventOf(e state.Event, after string) explainEvent {
	f := e.Facts
	tasks := make([]state.EventTask, 0, len(f.Tasks))
	for _, t := range f.Tasks {
		tasks = append(tasks, state.EventTask{ID: state.Clean(t.ID), Type: state.Clean(t.Type), Status: state.Clean(t.Status), Artifact: t.Artifact})
	}
	if len(tasks) == 0 {
		tasks = nil
	}
	var messages []string
	for _, m := range f.Messages {
		messages = append(messages, state.Clean(m))
	}
	return explainEvent{
		Number: e.Number, Time: e.Time.UTC(), Source: state.Clean(e.Source), Kind: state.Clean(e.Kind),
		What: eventWhat(e) + asideWhy(e.Aside), what: eventWhat(e), State: after, Aside: state.Clean(e.Aside), Basis: e.Basis,
		Facts: explainFacts{
			Hook: state.Clean(f.Hook), Whose: state.Clean(f.Whose), Withdrawn: state.Clean(f.Withdrawn), Turn: state.Clean(f.Turn), Subagent: f.Subagent, Tool: state.Clean(f.Tool),
			Server: state.Clean(f.Server), Start: state.Clean(f.Start), Reason: state.Clean(f.Reason),
			Notice: state.Clean(f.Notice), From: f.From.UTC(), Asked: f.Asked.UTC(), Question: f.Question, Tasks: tasks,
			Started: state.Clean(f.Started), Wake: f.Wake, Woke: state.Clean(f.Woke), Messages: messages, Size: f.Size,
		},
	}
}

// eventWhat says in words what event e was, from its kind and the facts
// that hold no text of the conversation.
func eventWhat(e state.Event) string {
	var what string
	switch e.Source {
	case state.SourceHook:
		what = hookWhat(e)
	case state.SourceScreen:
		what = screenWhat[e.Kind]
	case state.SourceTmux:
		if e.Kind == state.KindPaneDied {
			what = "the pane died"
		}
	case state.SourceSbx:
		if e.Kind == state.KindNotRunning {
			what = "sbx says the sandbox does not run"
		}
	case state.SourceHQ:
		what = hqWhat(e)
	}
	if what == "" {
		what = state.Clean(e.Kind)
	}
	return what
}

// asideWhy says why an event set aside for aside was, "" for one that
// stands.
func asideWhy(aside string) string {
	switch aside {
	case "":
		return ""
	case state.AsideDuplicate:
		return " - set aside: another hq process appended it first"
	case state.AsideOvertaken:
		return " - set aside: a later report overtook it"
	}
	return " - set aside: " + state.Clean(aside)
}

// screenWhat says what each kind of event of the screen was.
var screenWhat = map[string]string{
	state.KindTurnEnded:        "a turn the user ended (Esc)",
	state.KindAtRest:           "the screen first seen at rest",
	state.KindToolCut:          "the screen first seen at rest at a tool the user cut short",
	state.KindRewound:          "the turn was rewound: its screen stayed at rest",
	state.KindTurnNotTaken:     "the turn Claude owed does not come: its screen stayed at rest",
	state.KindOwnDialog:        "a dialog of Claude's own",
	state.KindOwnDialogGone:    "the dialog of Claude's own is gone",
	state.KindAtWork:           "Claude at work",
	state.KindTookQueued:       "Claude took the prompt it queued",
	state.KindWokeUp:           "Claude woke up after a turn the user ended",
	state.KindTookSlashCommand: "Claude took the first prompt as a slash command of its own: no turn comes",
}

// hookWhat says what event e of the hook was.
func hookWhat(e state.Event) string {
	f := e.Facts
	var what string
	switch e.Kind {
	case state.KindPrompt:
		what = "prompt"
		if f.Wake {
			what = "Claude's wake-up when background work finished"
			if f.Woke != "" {
				what += ", for task " + state.Clean(f.Woke)
			}
		}
	case state.KindTurnEnd:
		what = "turn end"
		if f.Question {
			what += ", the reply ends with ?"
		}
		var running []string
		for _, t := range f.Tasks {
			if t.Status == "running" {
				task := state.Clean(t.Type) + " " + state.Clean(t.ID)
				if t.Artifact {
					task += " (the artifact's watch)"
				}
				running = append(running, task)
			}
		}
		if len(running) > 0 {
			what += ", running: " + strings.Join(running, ", ")
		}
	case state.KindTurnFailed:
		what = "turn end by an API error"
		if f.Reason != "" {
			what += " (" + state.Clean(f.Reason) + ")"
		}
	case state.KindPermission:
		what = "dialog of tool " + state.Clean(f.Tool)
	case state.KindNotification:
		what = "notification"
		if f.Notice != "" {
			what += " (" + state.Clean(f.Notice) + ")"
		}
	case state.KindElicitation:
		what = "MCP elicitation of server " + state.Clean(f.Server)
	case state.KindElicitationResult:
		what = "MCP elicitation answered"
	case state.KindToolEnd, state.KindToolFail:
		what = "end of tool " + state.Clean(f.Tool)
		if e.Kind == state.KindToolFail {
			what = "failed " + what
		}
		if s := f.Started; s != "" {
			what += ", started " + startedWhat(s)
		}
	case state.KindSessionStart:
		what = "session start (" + state.Clean(f.Start) + ")"
	case state.KindSessionEnd:
		what = "session end (" + state.Clean(f.Reason) + ")"
	case state.KindDelivery:
		what = deliveryWhat(e)
	case state.KindUnreadable:
		what = "an event file hq cannot read, " + strconv.FormatInt(f.Size, 10) + " bytes"
	default:
		return ""
	}
	if f.Subagent {
		what += ", of a subagent"
	}
	return what
}

// startedWhat says what background work a tool started, as the hook's
// list names it: ID, state.SubagentMark and ID for a subagent's,
// state.MonitorMark and ID for a watch of the agent's own.
func startedWhat(s string) string {
	if id, ok := strings.CutPrefix(s, state.SubagentMark); ok {
		return "a subagent's background task " + state.Clean(id)
	}
	if id, ok := strings.CutPrefix(s, state.MonitorMark); ok {
		return "its own watch " + state.Clean(id)
	}
	return "background work " + state.Clean(s)
}

// deliveryWhat says how delivery e delivered how many messages: the hook,
// at the case it delivered at, or hq typing them in.
func deliveryWhat(e state.Event) string {
	n := len(e.Facts.Messages)
	what := "delivery of " + strconv.Itoa(n) + " message"
	if n != 1 {
		what += "s"
	}
	if e.Source == state.SourceHQ {
		return what + ", typed in by hq"
	}
	switch e.Facts.Hook {
	case state.CaseStop:
		return what + ", at a turn end the hook held back"
	case state.CasePrompt:
		return what + ", with the next prompt"
	case state.CaseAnswer:
		return what + ", after a tool's end"
	}
	return what
}

// hqWhat says what event e of hq's own was.
func hqWhat(e state.Event) string {
	f := e.Facts
	switch e.Kind {
	case state.KindLaunched:
		return "launched (" + state.Clean(f.Start) + ")"
	case state.KindRelaunched:
		return "relaunched"
	case state.KindTakenDown:
		return "taken down for a restart"
	case state.KindAnswer:
		if f.Whose == state.DialogOwn {
			return "answered a dialog of Claude's own"
		}
		return "answered its question dialog"
	case state.KindWithdrawn:
		return "took back what it appended as " + state.Clean(f.Withdrawn) + ": it did not reach the agent"
	case state.KindDelivery:
		return deliveryWhat(e)
	}
	return ""
}

// whose says whose a dialog is, in words.
func whose(w string) string {
	switch w {
	case state.DialogHooks:
		return "its hooks'"
	case state.DialogSubagent:
		return "a subagent's"
	case state.DialogOwn:
		return "Claude's own"
	}
	return state.Clean(w)
}

// write prints the explanation for a person, its times in loc: the state
// and the dialog verdict with the events each rests on, or with all every
// event.
func (v explainView) write(w io.Writer, loc *time.Location, all bool) error {
	var b strings.Builder
	field := func(k, val string) { fmt.Fprintf(&b, "%-10s%s\n", k, val) }
	field("name", v.Name)
	st := v.State
	if !v.Since.IsZero() {
		st += " " + agent.Age(time.Duration(v.AgeSeconds)*time.Second) + ", since " + v.Since.In(loc).Format(explainTime)
	}
	field("state", st)
	field("rests on", numbers(v.RestsOn))
	d := v.Dialog
	switch {
	case d.Open:
		field("dialog", fmt.Sprintf("at a dialog, %s, opened at %d", whose(d.Whose), d.Opened))
	case d.Whose != "":
		field("dialog", fmt.Sprintf("at none; the last, %s, opened at %d, closed at %d", whose(d.Whose), d.Opened, d.Closed))
	default:
		field("dialog", "at none, never at one")
	}
	if d.Whose != "" {
		field("rests on", numbers(d.RestsOn))
	}
	byNumber := map[int64]explainEvent{}
	for _, e := range v.Events {
		byNumber[e.Number] = e
	}
	table := func(title string, events []explainEvent) {
		b.WriteString("\n" + title + "\n")
		if len(events) == 0 {
			b.WriteString("  none\n")
			return
		}
		tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  N\tTIME\tSOURCE\tWHAT\tSTATE")
		for _, e := range events {
			what := e.what
			if !e.Facts.From.IsZero() {
				// The earlier moment the event dates the state from.
				what += ", dated from " + e.Facts.From.In(loc).Format(explainTime)
			}
			what += asideWhy(e.Aside)
			fmt.Fprintf(tw, "  %d\t%s\t%s\t%s\t%s\n", e.Number, e.Time.In(loc).Format(explainTime), e.Source, what, e.State)
		}
		_ = tw.Flush()
	}
	pick := func(ns []int64) []explainEvent {
		var es []explainEvent
		for _, n := range ns {
			if e, ok := byNumber[n]; ok {
				es = append(es, e)
			}
		}
		return es
	}
	if all {
		table("every event, oldest first", v.Events)
	} else {
		table("the state rests on, oldest first", pick(v.RestsOn))
		if d.Whose != "" {
			table("the dialog verdict rests on, oldest first", pick(d.RestsOn))
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// explainTime is how hq explain prints a time: to the millisecond, since
// several events may come within a second.
const explainTime = "2006-01-02 15:04:05.000"

// numbers are ns as a line: "1 4 7", "none" for none.
func numbers(ns []int64) string {
	if len(ns) == 0 {
		return "none"
	}
	s := make([]string, len(ns))
	for i, n := range ns {
		s[i] = strconv.FormatInt(n, 10)
	}
	return strings.Join(s, " ")
}
