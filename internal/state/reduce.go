package state

import (
	"cmp"
	"maps"
	"slices"
	"strings"
	"time"
)

// The reducer (ADR 0018, spec #165): one pure function that folds an
// agent's journal into its state. It does no I/O and takes no clock: the
// time AGE is counted to is passed in and changes nothing else.
//
// This part of it reads the events of the agent's hook. The hook decides
// part of the state inside the sandbox from its own working files (design
// §3.4); the reducer keeps what those files would hold, from the events
// alone, as the hook's cases write them (Facts.At), and reads the state
// off it as Read reads the files: the same state, last message, own tasks
// and dialog verdict after every report (the fixture journals in
// testdata/journals hold the two side by side).

// Mark is an event of the journal as the reducer's result names it: its
// number, from the one count of the state directory, and its time.
type Mark struct {
	Number int64     `json:"n"`
	Time   time.Time `json:"time"`
}

// Reduced is an agent's state as the reducer folds it from the agent's
// journal: what #166's public numbers are taken from (Entered, Changed,
// TurnEnded), what the row shows, and the events each rests on.
type Reduced struct {
	State string
	// Reported says the hook has made a report the state comes from; until
	// then the agent is starting.
	Reported bool
	// Since is the moment the state counts from (AGE), and Age the time
	// from it to the moment passed in.
	Since time.Time
	Age   time.Duration
	// Entered is the event at which the agent last entered its state, by
	// today's rule for entering (design §3.4, "Entered after a moment"):
	// the report its state comes from.
	Entered Mark
	// Changed is the event at which its state, or the dialog it is at,
	// last changed: a report that leaves both as they were does not move it.
	Changed Mark
	// TurnEnded is the number of the event that ended the last completed
	// turn: a turn end the hook reported as done or question; 0 before
	// the first.
	TurnEnded int64
	// Last is the last message, one line, and LastStop the latest turn
	// end's, for which a dialog's text stands in as Last.
	Last     string
	LastStop string
	// OwnTasks are the agent's own tasks while it is done or has a
	// question from the turn end that listed them (#78).
	OwnTasks []Task
	Dialog   DialogVerdict
	Branch   string
	// Worktree is where Claude works, as the sandbox sees it.
	Worktree string
	// Session is Claude's session id.
	Session string
	// Prompt is the prompt the agent works on, as the user sent it.
	Prompt string
	// Latest is the time of its latest report: Since, or the later time of
	// an event that left it working without restarting its time.
	Latest time.Time
	// Background, Owed, Woken, Acted and Queued are as the old reading's
	// Report has them: what the rules on the agent's screen read.
	Background, Owed, Woken, Acted, Queued bool
	// RestsOn are the numbers of the events the state rests on, oldest
	// first.
	RestsOn []int64
}

// DialogVerdict is whether the agent is at a dialog its hooks reported,
// whose, and the events that opened it (GLOSSARY.md, "Dialog verdict").
type DialogVerdict struct {
	Open bool
	// Subagent says one of the agent's subagents opened it.
	Subagent bool
	// Opened is the event that reported it.
	Opened Mark
	// RestsOn are the numbers of the events the verdict rests on, oldest
	// first.
	RestsOn []int64
}

// Fold is a journal folded up to an event: the position a long-running
// program keeps, to fold only the events after it. Its zero value is an
// empty journal. A Fold is a value: With leaves the one it is called on
// as it was.
type Fold struct {
	// file, stop, prev, kept, owed, acted and queued are what the hook's
	// working files hold (records of design §3.4): the state file (ID),
	// the latest turn end (ID.stop), the report before a resumed session
	// (ID.prev), the event kept beside the state file (ID.on), the list
	// of background work (ID.owe), the turn Claude has acted on (ID.acted:
	// the tool's end that marked it) and the queued prompt (ID.queued).
	file, kept *written
	stop, prev *Event
	owed       []string
	acted      *Event
	queued     *Event
	// started are the events that started the background work owed
	// names, by the task's id.
	started   map[string]int64
	changed   Mark
	was       verdictKey
	turnEnded int64
}

// written is what one of the hook's files holds: an event, as at wrote it
// there (a queued prompt the hook hands over is written by a turn end or a
// tool's end).
type written struct {
	Event
	at Mark
}

// verdictKey is what Changed follows: the state and the dialog.
type verdictKey struct {
	state    string
	subagent bool
	dialog   int64
}

// Reduce folds the journal events, with reply the journal's reply of its
// latest turn end, into the agent's state, its age counted to now.
func Reduce(events []Event, reply Reply, now time.Time) Reduced {
	return Fold{}.With(events...).Result(reply, now)
}

// With is the fold of f's journal and events, the events that follow in
// the journal, in its order.
func (f Fold) With(events ...Event) Fold {
	f.owed = slices.Clip(f.owed)
	f.started = maps.Clone(f.started)
	if f.was == (verdictKey{}) {
		f.was.state = Starting
	}
	for _, e := range events {
		if e.Source != SourceHook || e.Kind == KindDelivery {
			continue
		}
		f.add(e)
		r := f.reading(nil)
		key := verdictKey{r.State, r.Dialog.Subagent, r.Dialog.Opened.Number}
		if key != f.was {
			f.was, f.changed = key, Mark{e.Number, e.Time}
		}
		if e.Facts.At == "stop" && f.file.Number == e.Number && (r.State == Done || r.State == Question) {
			f.turnEnded = e.Number
		}
	}
	return f
}

// Result is the agent's state from f, with reply the journal's reply of
// its latest turn end, its age counted to now.
func (f Fold) Result(reply Reply, now time.Time) Reduced {
	r := f.reading(&reply)
	if r.Reported {
		r.Age = now.Sub(r.Since)
	}
	r.Changed, r.TurnEnded = f.changed, f.turnEnded
	return r
}

// add follows event e of the hook through the hook's case that wrote it
// (hookSource): what each of its working files holds afterwards. An event
// of a case the reducer does not know is written to the state file.
func (f *Fold) add(e Event) {
	at := Mark{e.Number, e.Time}
	switch e.Facts.At {
	case "stop":
		f.atStop(e, at)
	case "prompt":
		f.atPrompt(e, at)
	case "answer":
		f.atToolEnd(e, at)
	case "start", "end":
		f.write(e, at)
		f.forget()
	case "resume":
		// The report before a resumed session is kept for its branch,
		// worktree and last message, unless it is itself a resumed start,
		// which keeps the one before it.
		if f.file != nil && !(f.file.Kind == "SessionStart" && f.file.Facts.Start == resumeSource) {
			f.prev = &f.file.Event
		}
		f.write(e, at)
		f.forget()
	default:
		f.write(e, at)
	}
}

// atStop follows a turn end (the hook's stop; a stop it blocked to deliver
// messages leaves no event).
func (f *Fold) atStop(e Event, at Mark) {
	keeps := f.keep(e.Facts.Tasks, e.Facts.Question)
	if f.pending() && f.queued.Facts.Turn == e.Facts.Turn {
		// Claude takes the queued prompt as a turn of its own: the agent
		// works on it from now.
		f.stop, f.kept, f.acted = &e, nil, nil
		f.file = &written{*f.queued, at}
		return
	}
	f.queued, f.acted, f.stop = nil, nil, &e
	if keeps {
		// Background work still runs: kept beside the state file, which
		// it replaces only when the agent was not working (nor at a
		// subagent's dialog).
		w := &written{e, at}
		f.kept = w
		if !f.works() && !f.theirs() {
			f.file = w
		}
		return
	}
	f.kept = nil
	f.file = &written{e, at}
}

// atPrompt follows a prompt: one of the user's, one Claude queues while
// the agent works, or Claude's wake-up after background work finished.
func (f *Fold) atPrompt(e Event, at Mark) {
	queued := false
	if e.Facts.Wake {
		f.wake(e.Facts.Woke)
		if f.works() {
			// The agent works on, since the same moment.
			f.kept = &written{e, at}
			return
		}
	} else {
		f.owed = slices.DeleteFunc(slices.Clone(f.owed), func(l string) bool {
			return strings.HasPrefix(l, heldMark) || l == paidMark
		})
		queued = e.Facts.Turn != "" && f.works() &&
			(e.Facts.Turn == f.file.Facts.Turn || f.acted != nil && e.Facts.Turn == f.acted.Facts.Turn)
	}
	f.kept = nil
	if queued {
		f.queued = &e
	} else {
		f.acted, f.queued = nil, nil
	}
	f.write(e, at)
}

// atToolEnd follows the end of a tool (the hook's answer): the hook leaves
// an event only for one that tells something new.
func (f *Fold) atToolEnd(e Event, at Mark) {
	if !e.Facts.Subagent {
		if f.pending() && e.Facts.Turn != f.queued.Facts.Turn && f.kept == nil {
			// A tool of a turn Claude runs for the queued prompt after an
			// Esc on the turn at work: hand over.
			f.file = &written{*f.queued, at}
		}
		f.acted, f.queued = &e, nil
	}
	if s := e.Facts.Started; s != "" {
		f.owed = append(f.owed, s)
		if f.started == nil {
			f.started = map[string]int64{}
		}
		f.started[strings.TrimLeft(s, subagentMark+monitorMark)] = e.Number
	}
	if f.ask() && f.file.Facts.Tool == e.Facts.Tool || f.form() && f.file.Facts.Server != "" && strings.HasPrefix(e.Facts.Tool, "mcp__"+f.file.Facts.Server+"__") {
		// The tool of the open dialog ran, or a tool of the form's server.
		f.write(e, at)
	}
}

// write makes e the state file's report.
func (f *Fold) write(e Event, at Mark) { f.file = &written{e, at} }

// forget drops what a session's start or end drops.
func (f *Fold) forget() {
	f.kept, f.owed, f.started, f.acted, f.queued = nil, nil, nil, nil, nil
}

// works, ask, form and theirs say what the state file's report is: a
// prompt or a tool's end (the agent works), a dialog, an MCP elicitation's
// form, a dialog one of the agent's subagents opened.
func (f *Fold) works() bool {
	return f.file != nil && (f.file.Kind == "UserPromptSubmit" || f.file.Kind == "PostToolUse" || f.file.Kind == "PostToolUseFailure")
}

func (f *Fold) ask() bool {
	return f.file != nil && (f.file.Kind == "PermissionRequest" || f.file.Kind == "Elicitation")
}

func (f *Fold) form() bool { return f.file != nil && f.file.Kind == "Elicitation" }

func (f *Fold) theirs() bool { return f.ask() && f.file.Facts.Subagent }

// pending says a queued prompt waits in Claude's queue: Claude has not
// taken it at a turn end of the turn it was queued in.
func (f *Fold) pending() bool {
	return f.queued != nil && f.queued.Facts.Turn != "" && (f.stop == nil || f.stop.Facts.Turn != f.queued.Facts.Turn)
}

// The marks of the hook's list of background work beside those of
// subagentMark and monitorMark: a task that has held back a turn end, and
// the line that says Claude has woken for a task since.
const (
	heldMark = "!"
	paidMark = "+"
)

// keep follows a turn end listing tasks through the hook's list of
// background work (hookAwk, K=keep), and reports whether the turn end
// leaves the agent working.
func (f *Fold) keep(tasks []EventTask, question bool) bool {
	paid := false
	theirs, watch, held, was := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, l := range f.owed {
		switch {
		case l == paidMark:
			paid = true
		case strings.HasPrefix(l, subagentMark):
			theirs[l[1:]] = true
		case strings.HasPrefix(l, monitorMark):
			watch[l[1:]] = true
		case strings.HasPrefix(l, heldMark):
			held[l[1:]] = true
		default:
			was[l] = true
		}
	}
	keeps := false
	var owed, mine, watched []string
	for _, t := range tasks {
		switch {
		case t.Status != "running":
		case t.Type != ShellTask && t.Type != MonitorTask:
			owed = append(owed, t.ID)
		case theirs[t.ID]:
			mine = append(mine, t.ID)
		case watch[t.ID]:
			watched = append(watched, t.ID)
		}
	}
	running := map[string]bool{}
	var out []string
	for _, id := range owed {
		running[id], keeps = true, true
		out = append(out, id)
	}
	for _, id := range mine {
		keeps = true
		out = append(out, subagentMark+id)
	}
	for _, id := range watched {
		out = append(out, monitorMark+id)
	}
	for _, l := range f.owed {
		if was[l] && !running[l] {
			keeps = true
			out = append(out, heldMark+l)
		}
	}
	if paid {
		for _, l := range f.owed {
			if id, ok := strings.CutPrefix(l, heldMark); ok && !running[id] && !was[id] {
				keeps = true
				out = append(out, l)
			}
		}
	}
	f.owed = slices.Compact(slices.Sorted(slices.Values(slices.DeleteFunc(out, func(l string) bool {
		return strings.TrimLeft(l, subagentMark+monitorMark+heldMark) == ""
	}))))
	return keeps && !question
}

// wake follows Claude's wake-up for task woke through the hook's list
// (hookAwk, K=wake): the task is paid, and the list says Claude has woken
// for one; a wake-up that names no task forgets them all.
func (f *Fold) wake(woke string) {
	if woke == "" {
		f.owed = nil
		return
	}
	paid := false
	var out []string
	for _, l := range f.owed {
		if l == paidMark || l == woke || l == heldMark+woke {
			paid = true
		} else {
			out = append(out, l)
		}
	}
	if paid {
		out = append(out, paidMark)
	}
	f.owed = out
}

// marked are the ids of the hook's list with mark.
func (f *Fold) marked(mark string) map[string]bool {
	ids := map[string]bool{}
	for _, l := range f.owed {
		if id, ok := strings.CutPrefix(l, mark); ok {
			ids[id] = true
		}
	}
	return ids
}

// message is the reply of turn end e, one line, as reply keeps it for the
// latest turn end; "" for another (or with no reply).
func message(e *Event, reply *Reply) string {
	if e == nil || reply == nil || e.Kind != KindTurnEnd || reply.Number != e.Number {
		return ""
	}
	return Clean(reply.Text)
}

// reading is the state f's files give, as Read reads them; reply is the
// journal's reply of its latest turn end (nil: no texts).
func (f *Fold) reading(reply *Reply) (r Reduced) {
	if f.file == nil {
		return Reduced{State: Starting}
	}
	c, at := f.file.Event, f.file.at
	r = Reduced{Reported: true, Since: at.Time, Latest: at.Time, Entered: at}
	rests := []int64{at.Number, c.Number}
	defer func() { r.RestsOn = sortedNumbers(rests) }()
	if c.Kind == KindUnreadable && c.Facts.Size > maxFile {
		// A report too large to read is a turn end with a reply too long
		// to keep (Read).
		r.State, r.Last = Done, TooLarge(c.Facts.Size)
		return r
	}
	subagent, before := parse(&r, &c, message(f.stop, reply), f.prev)
	if r.Last == "" && f.stop != nil && f.stop.Kind == KindUnreadable && f.stop.Facts.Size > maxFile {
		r.Last = TooLarge(f.stop.Facts.Size)
	}
	if before {
		rests = append(rests, f.prev.Number)
	}
	byKept := false
	if k := f.kept; k != nil {
		switch {
		case k.Kind == KindTurnEnd && (r.State == Working || k.at.Number == at.Number):
			r.State, r.Background = Working, true
			r.Last = message(&k.Event, reply)
			r.LastStop = r.Last
			r.Owed = !f.busy(k.Facts.Tasks)
			r.Latest = laterOf(r.Latest, k.at.Time)
			rests = append(rests, k.at.Number)
			for _, l := range f.owed {
				if n, ok := f.started[strings.TrimLeft(l, subagentMark+monitorMark+heldMark)]; ok {
					rests = append(rests, n)
				}
			}
		case k.Kind == "UserPromptSubmit" && r.State == Working:
			r.Woken = true
			r.Latest = laterOf(r.Latest, k.at.Time)
			rests = append(rests, k.at.Number)
		case k.Kind == KindTurnEnd && r.State == NeedsInput:
			// A dialog reported after a turn that ended with background
			// work running is a subagent's.
			subagent, byKept = true, true
			rests = append(rests, k.at.Number)
		}
	}
	if c.Kind == KindTurnEnd && (r.State == Done || r.State == Question) {
		r.OwnTasks = f.ownTasks(c, reply)
	}
	if f.acted != nil {
		r.Acted = r.State == Working
		if r.Acted {
			rests = append(rests, f.acted.Number)
		}
	}
	if f.queued != nil {
		r.Queued = r.State == Working && f.queued.Facts.Turn != "" && (f.stop == nil || f.queued.Facts.Turn != f.stop.Facts.Turn)
		if r.Queued {
			rests = append(rests, f.queued.Number)
		}
	}
	if r.State == NeedsInput {
		r.Dialog = DialogVerdict{Open: true, Subagent: subagent, Opened: at, RestsOn: []int64{at.Number}}
		if byKept {
			r.Dialog.RestsOn = sortedNumbers([]int64{at.Number, f.kept.at.Number})
		}
	}
	return r
}

// parse reads report c of the state file as Parse reads it, with stop the
// latest turn end's message and prev the report before a resumed session.
// subagent says the report is a dialog one of the agent's subagents
// opened, before that a resumed session took what prev gave.
func parse(r *Reduced, c *Event, stop string, prev *Event) (subagent, before bool) {
	r.State, r.Branch, r.Last, r.LastStop = Starting, c.Facts.Branch, stop, stop
	if c.Kind == KindUnreadable {
		return false, false
	}
	r.Session, r.Worktree = c.Facts.Session, c.Facts.Cwd
	switch c.Kind {
	case "UserPromptSubmit", "PostToolUse", "PostToolUseFailure":
		r.State, r.Prompt, r.Woken = Working, c.Facts.Prompt, c.Facts.Wake
	case "PermissionRequest", "Notification":
		r.State, subagent = NeedsInput, c.Facts.Subagent
		r.Last = cmp.Or(c.Facts.Text, r.Last)
	case "Elicitation":
		r.State = NeedsInput
		r.Last = cmp.Or(c.Facts.Text, c.Facts.Server, r.Last)
	case "ElicitationResult":
		r.State, r.Acted = Working, true
	case KindTurnEnd:
		r.State = Done
		if c.Facts.Question {
			r.State = Question
		}
	case "SessionStart":
		r.State = Done
		if c.Facts.Start == newSource {
			r.Session = ""
		}
		if c.Facts.Start == resumeSource && prev != nil {
			var b Reduced
			if parse(&b, prev, stop, nil); b.State != Starting {
				r.Branch, r.Worktree, r.Last, r.LastStop, before = b.Branch, b.Worktree, b.Last, b.LastStop, true
			}
		}
	case "SessionEnd":
		r.State = Ended
	}
	return subagent, before
}

// busy says turn end tasks lists background work that still runs: a task
// that is neither a shell command nor a watch, or one of those one of the
// agent's subagents started (payload.busy).
func (f *Fold) busy(tasks []EventTask) bool {
	theirs := f.marked(subagentMark)
	for _, t := range tasks {
		if t.Status == "running" && (t.Type != ShellTask && t.Type != MonitorTask || theirs[t.ID]) {
			return true
		}
	}
	return false
}

// ownTasks are the agent's own tasks turn end c lists (payload.ownTasks),
// with their texts from reply.
func (f *Fold) ownTasks(c Event, reply *Reply) []Task {
	theirs, watches := f.marked(subagentMark), f.marked(monitorMark)
	var ts []Task
	for i, t := range c.Facts.Tasks {
		if t.Status != "running" || t.Type != ShellTask && t.Type != MonitorTask || theirs[t.ID] || t.Artifact {
			continue
		}
		var text TaskTexts
		if reply != nil && reply.Number == c.Number && i < len(reply.Tasks) {
			text = reply.Tasks[i]
		}
		kind, s := t.Type, text.Description
		if watches[t.ID] {
			kind = MonitorTask
		}
		if kind == ShellTask {
			s = text.Command
		}
		ts = append(ts, Task{Type: kind, Text: CleanText(s)})
	}
	return ts
}

// laterOf is the later of a and b.
func laterOf(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// sortedNumbers are ns, rising, each once.
func sortedNumbers(ns []int64) []int64 {
	return slices.Compact(slices.Sorted(slices.Values(ns)))
}
