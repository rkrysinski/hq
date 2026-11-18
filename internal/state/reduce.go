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
// It reads the events of the agent's hook first. The hook decides part of
// the state inside the sandbox from its own working files (design §3.4),
// which hq on the host never reads; the reducer keeps what the hook would
// know, from the events alone, as the hook's cases report them (Facts.Hook),
// and reads the hook's state off it (reading). Then it reads what hq saw
// and did (seen.go): the screen, tmux, sbx and hq itself, as the look
// reads them beside the hook's report. After every event of the fixture
// journals in testdata/journals it gives what the journal holds, which the
// look wrote when the journal was last written: a golden of the reducer.

// Ref is an event of the journal as the reducer's result refers to it: its
// number, from the one count of the state directory, and its time.
type Ref struct {
	Number int64     `json:"n"`
	Time   time.Time `json:"time"`
}

// RefOf is the reference to event e.
func RefOf(e Event) Ref { return Ref{e.Number, e.Time} }

// Reduced is an agent's state as the reducer folds it from the agent's
// journal: what #166's public numbers are taken from (Entered, Changed,
// TurnEnded), what the row shows, and the events each rests on.
type Reduced struct {
	State string
	// Reported says the hook has made a report of the agent's session the
	// state comes from (one since its latest launch); until then the agent
	// is starting.
	Reported bool
	// Since is the moment the state counts from (AGE), and Age the time
	// from it to the moment passed in.
	Since time.Time
	Age   time.Duration
	// Entered is the event at which the agent last entered its state: the
	// report its state comes from, or the event that decided it otherwise
	// (a turn end the screen showed, the pane's death, ...). For a decision
	// dated from an earlier moment (a rewound turn, from its screen first
	// seen at rest; an end, from the pane's death) its time is the deciding
	// event's, and Since that earlier moment.
	Entered Ref
	// Changed is the event at which its state, or the dialog it is at,
	// last changed: a report that leaves both as they were does not move it.
	Changed Ref
	// TurnEnded is the number of the event that ended the last completed
	// turn: a turn end the hook reported as done or question, or, for one
	// it reported and kept working for background work, the screen's
	// decision that the turn Claude owed does not come (KindTurnNotTaken);
	// 0 before the first. A turn the user ended at the agent (its line,
	// KindTurnEnded, or rewound, KindRewound) leaves it as it was.
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
	// Background, Owed, Woken, Acted and Queued are what the rules on the
	// agent's screen read: a turn end kept while background work runs,
	// all of it finished, a wake-up Claude gave itself, a tool of the
	// agent's own ended in the turn, a prompt waiting in Claude's queue.
	Background, Owed, Woken, Acted, Queued bool
	// RestsOn are the numbers of the events the state rests on, oldest
	// first.
	RestsOn []int64
	// report is the hook's report the state comes from, which the evidence
	// of the screen is keyed by: the hook's latest noted report, or the
	// event kept beside it when that is later (Latest).
	report Ref
}

// DialogVerdict is whether the agent is at a dialog, whose, since which
// event, and the events that opened and closed it (GLOSSARY.md, "Dialog
// verdict"). While no dialog is open it names the last one the agent was
// at, if any, with the event that closed it.
type DialogVerdict struct {
	Open bool
	// Whose is whose dialog it is: DialogHooks, DialogSubagent or
	// DialogOwn; "" when the agent was never at one.
	Whose string
	// Opened is the event that opened it: the report of the hooks', the
	// screen that first showed one of Claude's own. Its time is when the
	// dialog opened.
	Opened Ref
	// Closed is the event at which it stopped being open: zero while it
	// is.
	Closed Ref
	// Screen is the fingerprint of a dialog of Claude's own, its title and
	// text (OwnFingerprint), as the screen that opened it showed it.
	Screen string
	// RestsOn are the numbers of the events the verdict rests on, oldest
	// first.
	RestsOn []int64
}

// Whose dialog an agent is at, in its dialog verdict: one its hooks
// reported (a question dialog, a permission prompt, Claude's
// Notification), one of its subagents', as their hooks reported it, or one
// of Claude's own, which only its screen shows.
const (
	DialogHooks    = "hooks"
	DialogSubagent = "subagent"
	DialogOwn      = "own"
)

// Fold is a journal folded up to an event: the position a long-running
// program keeps, to fold only the events after it. Its zero value is an
// empty journal. A Fold is a value: With leaves the one it is called on
// as it was.
type Fold struct {
	// report, stop, beside, owed, acted and queued are what the hook knows
	// from its working files (design §3.4): the latest report it took for
	// the state (its note, ID.last), the latest turn end (ID.ended), the
	// event it kept beside the note (marked by ID.on), the list of
	// background work (ID.owe), the turn Claude has acted on (ID.acted:
	// the tool's end that marked it) and the queued prompt (ID.queued).
	// prev is the report before a resumed session, which gives it its
	// branch, worktree and last message.
	report, beside *written
	stop, prev     *Event
	owed           []string
	acted          *Event
	queued         *Event
	// started are the events that started the background work owed
	// names, by the task's id.
	started map[string]int64
	// wakeUp is the latest wake-up of Claude's kept beside a working
	// report: what the agent works on since, after a turn the user ended
	// (seen.go).
	wakeUp Ref
	// seen is what hq saw of the agent and did to it (seen.go), and
	// beforeLaunch what it was before the latest launch, which a launch
	// withdrawn puts back.
	seen         seen
	beforeLaunch *seen
	// dialog is the last dialog verdict with a dialog open, closed at the
	// event that ended it.
	dialog DialogVerdict
	// verdict is the state and dialog the event changed last changed them
	// to (Reduced.Changed), and turnEnded the number of the event that
	// ended the last completed turn.
	verdict   verdictKey
	changed   Ref
	turnEnded int64
}

// written is what one of the hook's files holds: an event, as at wrote it
// there (a queued prompt the hook hands over is written by a turn end or a
// tool's end).
type written struct {
	Event
	at Ref
}

// verdictKey is what Changed follows: the state and the dialog.
type verdictKey struct {
	state  string
	whose  string
	dialog int64
}

// Reduce folds the journal events, with reply the journal's reply of its
// latest turn end, into the agent's state, its age counted to now.
func Reduce(events []Event, reply Reply, now time.Time) Reduced {
	return Fold{}.With(events...).Result(reply, now)
}

// With is the fold of f's journal and events, the events that follow in
// the journal, in its order. An event set aside changes nothing.
func (f Fold) With(events ...Event) Fold {
	f.owed = slices.Clip(f.owed)
	f.started = maps.Clone(f.started)
	f.seen.ends = slices.Clip(f.seen.ends)
	if f.verdict == (verdictKey{}) {
		f.verdict.state = Starting
	}
	for _, e := range events {
		if e.Aside != "" || e.Kind == KindDelivery {
			continue
		}
		stop := false
		if e.Source == SourceHook {
			f.add(e)
			stop = (e.Facts.Hook == CaseStop || e.Facts.Hook == CaseFail) && f.report.Number == e.Number
		} else if !f.see(e) {
			continue
		}
		r := f.look(nil)
		key := verdictKey{r.State, r.Dialog.Whose, r.Dialog.Opened.Number}
		if !r.Dialog.Open {
			key.whose = ""
		}
		if key != f.verdict {
			f.verdict, f.changed = key, RefOf(e)
		}
		if r.Dialog.Open {
			f.dialog = r.Dialog
		} else if f.dialog.Open {
			f.dialog.Open, f.dialog.Closed = false, RefOf(e)
			f.dialog.restOn(e.Number)
		}
		if stop {
			if h := f.reading(nil); h.State == Done || h.State == Question {
				f.turnEnded = e.Number
			}
		}
		if r.notTaken == e.Number && r.State == Done {
			f.turnEnded = e.Number
		}
	}
	return f
}

// Result is the agent's state from f, with reply the journal's reply of
// its latest turn end, its age counted to now.
func (f Fold) Result(reply Reply, now time.Time) Reduced {
	r := f.look(&reply).Reduced
	if !r.Since.IsZero() {
		r.Age = now.Sub(r.Since)
	}
	r.Changed, r.TurnEnded = f.changed, f.turnEnded
	if !r.Dialog.Open {
		r.Dialog = f.dialog
	}
	return r
}

// add follows event e of the hook through the hook's case that wrote it
// (hookSource): what it knows afterwards. An event of a case the reducer
// does not know is taken as its latest report.
func (f *Fold) add(e Event) {
	at := RefOf(e)
	switch e.Facts.Hook {
	case CaseStop:
		f.atStop(e, at)
	case CaseFail:
		f.atFail(e, at)
	case CasePrompt:
		f.atPrompt(e, at)
	case CaseAnswer:
		f.atToolEnd(e, at)
	case CaseStart, CaseEnd:
		f.write(e, at)
		f.forget()
	case CaseResume:
		// The report before a resumed session is kept for its branch,
		// worktree and last message, unless it is itself a resumed start,
		// which keeps the one before it.
		if f.report != nil && !(f.report.Kind == KindSessionStart && f.report.Facts.Start == resumeSource) {
			f.prev = &f.report.Event
		}
		f.write(e, at)
		f.forget()
	default:
		f.write(e, at)
	}
}

// atStop follows a turn end (the hook's stop; a stop it blocked to deliver
// messages leaves no event).
func (f *Fold) atStop(e Event, at Ref) {
	keeps := f.keep(e.Facts.Tasks, e.Facts.Question)
	if f.pending() && f.queued.Facts.Turn == e.Facts.Turn {
		// Claude takes the queued prompt as a turn of its own: the agent
		// works on it from now.
		f.stop, f.beside, f.acted = &e, nil, nil
		f.report = &written{*f.queued, at}
		return
	}
	f.queued, f.acted, f.stop = nil, nil, &e
	if keeps {
		// Background work still runs: kept beside the latest report,
		// which it replaces only when the agent was not working (nor at a
		// subagent's dialog).
		w := &written{e, at}
		f.beside = w
		if !f.works() && !f.theirs() {
			f.report = w
		}
		return
	}
	f.beside = nil
	f.report = &written{e, at}
}

// atFail follows a turn an API error ended (the hook's fail): the latest
// turn end, and the latest report, whatever the turn left; the hook drops
// the marks of a turn as at any prompt or stop it does not keep, and keeps
// its list of background work.
func (f *Fold) atFail(e Event, at Ref) {
	f.beside, f.acted, f.queued, f.stop = nil, nil, nil, &e
	f.write(e, at)
}

// atPrompt follows a prompt: one of the user's, one Claude queues while
// the agent works, or Claude's wake-up after background work finished.
func (f *Fold) atPrompt(e Event, at Ref) {
	queued := false
	if e.Facts.Wake {
		f.wake(e.Facts.Woke)
		if f.works() {
			// The agent works on, since the same moment.
			f.beside, f.wakeUp = &written{e, at}, at
			return
		}
	} else {
		f.owed = slices.DeleteFunc(slices.Clone(f.owed), func(l string) bool {
			return strings.HasPrefix(l, heldMark) || l == paidMark
		})
		queued = e.Facts.Turn != "" && f.works() &&
			(e.Facts.Turn == f.report.Facts.Turn || f.acted != nil && e.Facts.Turn == f.acted.Facts.Turn)
	}
	f.beside = nil
	if queued {
		f.queued = &e
	} else {
		f.acted, f.queued = nil, nil
	}
	f.write(e, at)
}

// atToolEnd follows the end of a tool (the hook's answer): the hook leaves
// an event only for one that tells something new.
func (f *Fold) atToolEnd(e Event, at Ref) {
	if !e.Facts.Subagent {
		if f.pending() && e.Facts.Turn != f.queued.Facts.Turn && f.beside == nil {
			// A tool of a turn Claude runs for the queued prompt after an
			// Esc on the turn at work: hand over.
			f.report = &written{*f.queued, at}
		}
		f.acted, f.queued = &e, nil
	}
	if s := e.Facts.Started; s != "" {
		f.owed = append(f.owed, s)
		if f.started == nil {
			f.started = map[string]int64{}
		}
		f.started[strings.TrimLeft(s, SubagentMark+MonitorMark)] = e.Number
	}
	if f.ask() && f.report.Facts.Tool == e.Facts.Tool || f.form() && f.report.Facts.Server != "" && strings.HasPrefix(e.Facts.Tool, "mcp__"+f.report.Facts.Server+"__") {
		// The tool of the open dialog ran, or a tool of the form's server.
		f.write(e, at)
	}
}

// write makes e the latest report the hook took for the state.
func (f *Fold) write(e Event, at Ref) { f.report = &written{e, at} }

// forget drops what a session's start or end drops.
func (f *Fold) forget() {
	f.beside, f.owed, f.started, f.acted, f.queued = nil, nil, nil, nil, nil
}

// works, ask, form and theirs say what that report is: a
// prompt or a tool's end (the agent works), a dialog, an MCP elicitation's
// form, a dialog one of the agent's subagents opened.
func (f *Fold) works() bool {
	return f.report != nil && (f.report.Kind == KindPrompt || f.report.Kind == KindToolEnd || f.report.Kind == KindToolFail)
}

func (f *Fold) ask() bool {
	return f.report != nil && (f.report.Kind == KindPermission || f.report.Kind == KindElicitation)
}

func (f *Fold) form() bool { return f.report != nil && f.report.Kind == KindElicitation }

func (f *Fold) theirs() bool { return f.ask() && f.report.Facts.Subagent }

// pending says a queued prompt waits in Claude's queue: Claude has not
// taken it at a turn end of the turn it was queued in.
func (f *Fold) pending() bool {
	return f.queued != nil && f.queued.Facts.Turn != "" && (f.stop == nil || f.stop.Facts.Turn != f.queued.Facts.Turn)
}

// The marks of the hook's list of background work beside those of
// SubagentMark and MonitorMark: a task that has held back a turn end, and
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
		case strings.HasPrefix(l, SubagentMark):
			theirs[l[1:]] = true
		case strings.HasPrefix(l, MonitorMark):
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
		case !t.running():
		case !t.shellOrWatch():
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
		out = append(out, SubagentMark+id)
	}
	for _, id := range watched {
		out = append(out, MonitorMark+id)
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
		return strings.TrimLeft(l, SubagentMark+MonitorMark+heldMark) == ""
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
	if e == nil || reply == nil || !EndsTurn(e.Kind) || reply.Number != e.Number {
		return ""
	}
	return Clean(reply.Text)
}

// reading is the state the hook's reports give; reply is the
// journal's reply of its latest turn end (nil: no texts).
func (f *Fold) reading(reply *Reply) (r Reduced) {
	if f.report == nil {
		return Reduced{State: Starting}
	}
	c, at := f.report.Event, f.report.at
	r = Reduced{Reported: true, Since: at.Time, Latest: at.Time, Entered: at, report: at}
	rests := []int64{at.Number, c.Number}
	defer func() { r.restOn(rests...) }()
	if c.Kind == KindUnreadable && c.Facts.Size > maxFile {
		// A report too large to read is a turn end with a reply too long
		// to keep (#64).
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
	if k := f.beside; k != nil {
		switch {
		case k.Kind == KindTurnEnd && (r.State == Working || k.at.Number == at.Number):
			r.State, r.Background = Working, true
			r.Last = message(&k.Event, reply)
			r.LastStop = r.Last
			r.Owed = !f.busy(k.Facts.Tasks)
			r.Latest = laterOf(r.Latest, k.at.Time)
			r.report = laterMark(r.report, k.at)
			rests = append(rests, k.at.Number)
			for _, l := range f.owed {
				if n, ok := f.started[strings.TrimLeft(l, SubagentMark+MonitorMark+heldMark)]; ok {
					rests = append(rests, n)
				}
			}
		case k.Kind == KindPrompt && r.State == Working:
			r.Woken = true
			r.Latest = laterOf(r.Latest, k.at.Time)
			r.report = laterMark(r.report, k.at)
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
		r.Dialog = DialogVerdict{Open: true, Whose: DialogHooks, Opened: at, RestsOn: []int64{at.Number}}
		if subagent {
			r.Dialog.Whose = DialogSubagent
		}
		if byKept {
			r.Dialog.restOn(f.beside.at.Number)
		}
	}
	return r
}

// parse reads the hook's latest report c, with stop the
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
	case KindPrompt, KindToolEnd, KindToolFail:
		r.State, r.Prompt, r.Woken = Working, c.Facts.Prompt, c.Facts.Wake
	case KindPermission, KindNotification:
		r.State, subagent = NeedsInput, c.Facts.Subagent
		r.Last = cmp.Or(c.Facts.Text, r.Last)
	case KindElicitation:
		r.State = NeedsInput
		r.Last = cmp.Or(c.Facts.Text, c.Facts.Server, r.Last)
	case KindElicitationResult:
		r.State, r.Acted = Working, true
	case KindTurnEnd:
		r.State = Done
		if c.Facts.Question {
			r.State = Question
		}
	case KindTurnFailed:
		r.State = Done
	case KindSessionStart:
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
	case KindSessionEnd:
		r.State = Ended
	}
	return subagent, before
}

// busy says turn end tasks lists background work that still runs
// (EventTask.background, payload.busy).
func (f *Fold) busy(tasks []EventTask) bool {
	theirs := f.marked(SubagentMark)
	return slices.ContainsFunc(tasks, func(t EventTask) bool { return t.background(theirs) })
}

// running says the task still runs.
func (t EventTask) running() bool { return t.Status == "running" }

// shellOrWatch says the task is a shell command or a watch (a monitor).
func (t EventTask) shellOrWatch() bool { return t.Type == ShellTask || t.Type == MonitorTask }

// background says the task is background work that still runs: a task
// that is neither a shell command nor a watch, or one of those one of the
// agent's subagents started (theirs, by id).
func (t EventTask) background(theirs map[string]bool) bool {
	return t.running() && (!t.shellOrWatch() || theirs[t.ID])
}

// ownTasks are the agent's own tasks turn end c lists (payload.ownTasks),
// with their texts from reply.
func (f *Fold) ownTasks(c Event, reply *Reply) []Task {
	theirs, watches := f.marked(SubagentMark), f.marked(MonitorMark)
	var ts []Task
	for i, t := range c.Facts.Tasks {
		if !t.running() || t.background(theirs) || t.Artifact {
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

// laterMark is b when it is later than a, else a.
func laterMark(a, b Ref) Ref {
	if b.Time.After(a.Time) {
		return b
	}
	return a
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

// restOn adds the events numbered ns to those the state rests on.
func (r *Reduced) restOn(ns ...int64) {
	r.RestsOn = sortedNumbers(append(slices.Clone(r.RestsOn), ns...))
}

// restOn adds the events numbered ns to those the verdict rests on.
func (v *DialogVerdict) restOn(ns ...int64) {
	v.RestsOn = sortedNumbers(append(slices.Clone(v.RestsOn), ns...))
}
