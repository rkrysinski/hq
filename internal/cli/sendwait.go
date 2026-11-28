package cli

import (
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/state"
)

// stallBound is how long a message that an agent at its prompt could take
// may go without starting a turn before hq send --wait calls it stalled
// (#170, decision 3): the box not free that long with the message waiting,
// or no prompt reported that long after hq typed it in. It never runs
// while the agent works, is starting or is at a dialog.
const stallBound = 5 * time.Second

// stallText is stallBound as the tools' texts give it.
var stallText = fmt.Sprintf("%d seconds", int(stallBound.Seconds()))

// The ways a hq send --wait ends but a stall, as --json names them (#170).
const (
	// The agent stopped after taking the message: the end of the turn
	// that took it, as hq wait returns it.
	endTurn = "turn_ended"
	// The agent waits for the user, or has ended, before it took the
	// message.
	endNotTaken = "not_taken"
	// A slash command hq typed in left the box and started no turn.
	endNoTurn = "no_turn"
	// The timeout passed first.
	endTimeout = "timeout"
)

// Where a message not taken is, for endNotTaken.
const (
	msgWaits = "waiting" // in the inbox, delivered as without --wait
	msgKept  = "kept"    // the agent ended; delivered once it is relaunched
	msgGone  = "gone"    // the agent was killed; the message went with it
)

// sendWaitOutput is what hq send --wait --json prints: how the message went,
// how the wait ended (and where a message not taken is), the agent's row
// as hq wait --json gives it (none at the timeout), and the cursor of the
// look that returned it (ADR 0019), still that one agent's (decision 9).
type sendWaitOutput struct {
	Delivery  string  `json:"delivery"`
	Outcome   string  `json:"outcome"`
	Message   string  `json:"message,omitempty"`
	Agents    []lsRow `json:"agents"`
	NextSince string  `json:"next_since"`
}

// taking is what an agent's events after a send say of its message: taken
// once a delivery names it (the hook's, or hq's before it types it in) with
// no withdrawal of that delivery after it (the typing failed and the
// message went back into the inbox). A held message is not taken until a
// later look types it in (#173).
type taking struct {
	taken bool
	byHQ  bool      // hq typed it in as a prompt; else the hook handed it over
	at    time.Time // when the delivery was made
	// delivery is the number of that delivery, and prompt that of the
	// first prompt Claude reported after it that is not its own wake-up
	// for background work (none: 0): the turn a typed message started.
	delivery, prompt int64
}

// takingOf reads the taking of the message named name off events, an
// agent's events in their order.
func takingOf(events []state.Event, name string) taking {
	var t taking
	for _, e := range events {
		switch {
		case e.Aside != "":
		case e.Kind == state.KindDelivery && namesIt(e.Facts.Messages, name):
			t = taking{taken: true, byHQ: e.Source == state.SourceHQ, at: e.Time, delivery: e.Number}
		case e.Kind == state.KindWithdrawn && e.Facts.Withdrawn == state.KindDelivery && t.taken && namesIt(e.Facts.Messages, name):
			t = taking{}
		case e.Kind == state.KindPrompt && !e.Facts.Wake && t.taken && t.prompt == 0 && e.Number > t.delivery:
			t.prompt = e.Number
		}
	}
	return t
}

// namesIt reports whether the message named name is among those named.
func namesIt(named []string, name string) bool {
	return slices.ContainsFunc(named, func(n string) bool { return state.SameMessage(n, name) })
}

// settled says how agent a, as a look whose cursor is through shows it,
// ends a hq send --wait whose message's taking is t, if it does: in one of
// the states hq wait returns, by a change the look's cursor stands for
// (decision 7). A change at or below the delivery came before the message
// was taken and is never returned; for a message hq typed in, neither is
// one before the turn it started, such as the end of a turn Claude woke
// itself for, unless the agent needs the user or has ended (a slash command
// that opened a dialog of Claude Code's own). An agent with no number
// (ended with a journal hq can no longer read) is returned as hq wait's
// gone returns it (returnable).
func settled(a agent.Agent, t taking, through int64) (string, bool) {
	seq, numbered, ok := returnable(a, through)
	if !ok {
		return "", false
	}
	stopped := a.State == state.NeedsInput || a.State == state.Ended
	switch {
	case !t.taken:
		return endNotTaken, stopped
	case !numbered:
		return endTurn, true
	case !t.byHQ:
		return endTurn, seq > t.delivery
	case t.prompt > 0:
		return endTurn, seq > t.prompt
	default:
		return endTurn, stopped && seq > t.delivery
	}
}

// stallClock measures how long the condition key has held without a break:
// "" for none, which pauses it, so the bound never runs while the agent
// works, is starting or is at a dialog. A new key counts from the origin
// given with it (the moment the message could first be taken), or from now
// when it follows a pause.
type stallClock struct {
	// key is the condition the clock measures: keyWaiting, or that hq
	// typed the message in with a delivery of a given number.
	key    string
	from   time.Time
	paused bool
}

// keyWaiting is the stall clock's condition of a message that waits for an
// agent at its prompt, not taken.
const keyWaiting = "waiting"

// lasted is how long, at now, the condition key has held: since origin when
// it is new, since now after a pause; 0 for no condition.
func (c *stallClock) lasted(key string, origin, now time.Time) time.Duration {
	switch {
	case key == "":
		c.key, c.paused = "", true
		return 0
	case key != c.key:
		c.key, c.from = key, origin
		if c.paused {
			c.from = now
		}
		c.paused = false
	}
	return now.Sub(c.from)
}

// waitedSend is a hq send --wait after its message was left and the line
// that says how it goes was made (#170).
type waitedSend struct {
	agent        agent.Agent // as the send found it last
	msg          string      // the name the message was left under
	slashCommand bool        // it starts with a slash command
	sent         int64       // the cursor before it was left
	ready        time.Time   // when the send began to deliver it
	start        time.Time   // when the call started, for timeout
	timeout      time.Duration
	// hold, when not zero, is when the send may go on as a wait task
	// (sendToolTask): at the first look at or past it with no stall to
	// tell (#364).
	hold time.Time
}

// sendEnd is how a waited send ended: its outcome, where a message not
// taken is, the agent's row (none at the timeout) and the look's cursor.
type sendEnd struct {
	outcome, message string
	agents           []agent.Agent
	next             int64
	// toTask says it ended with no outcome, its hold passed: it goes on as
	// a wait task, and taken says whether the message was taken by then.
	toTask, taken bool
}

// wait looks at the agents as hq wait does until the send ends in one of
// its five ways (#170): the agent stopped after taking the message, it
// waits for the user or has ended before taking it, a slash command hq
// typed in started no turn, the message stalled (an error, ExitStalled), or
// the timeout. Meanwhile it types the message in itself as soon as the
// agent waits at its prompt with an empty box at rest, and leaves the
// others' messages to deliverWaiting, as hq wait does; it never types the
// message in again and takes nothing back. With a hold, it ends at the
// first look at or past it whose stall clock does not run, to go on as a
// wait task (#364): a stall, and a slash command that started no turn, are
// told within stallBound and stay the call's, also when that runs past the
// hold.
func (w waitedSend) wait(d deps) (sendEnd, error) {
	var (
		events sentEvents
		clock  stallClock
		end    sendEnd
	)
	k := d.looker()
	err := k.Loop(func(s agent.Seen, _ bool) (bool, error) {
		next := k.Cursor(s) // taken before what waits is typed in, as hq wait takes it
		deliverWaiting(d, othersThan(s.Agents, w.agent.ID))
		w.readOn(d, &events)
		t := takingOf(events.events, w.msg)
		now := d.now()
		var (
			a     agent.Agent
			ended bool
		)
		if end, a, ended = w.endAt(d, s.Agents, t, next, now); ended {
			return true, nil
		}
		w.agent = a
		key, origin := stallKey(a, t, w.ready)
		late := w.timeout > 0 && !now.Before(w.start.Add(w.timeout))
		if key == keyWaiting && !late && boxAtRest(d, a, 0) {
			// Typed in now, by the send that left it: deliverWaiting
			// would wait waitingAfter, beyond the bound. When nothing was
			// there to take (another process took it, and its delivery is
			// not in the journal yet), the bound and the timeout run on.
			before := d.pending(a.RepoPath, a.ID)
			if _, err := typeIn(d, a, 0, w.msg, w.slashCommand); err != nil {
				// The messages are back in the inbox, as a plain send leaves
				// them: the call fails as one does.
				return true, err
			}
			if d.pending(a.RepoPath, a.ID) < before {
				return false, nil
			}
		}
		if clock.lasted(key, origin, now) >= stallBound {
			var err error
			end, err = w.stalled(d, a, t, next)
			return true, err
		}
		switch {
		case late:
			end = sendEnd{outcome: endTimeout, next: next}
			return true, nil
		case !w.hold.IsZero() && key == "" && !now.Before(w.hold):
			end = sendEnd{next: next, toTask: true, taken: t.taken}
			return true, nil
		}
		return false, nil
	})
	return end, lookErr(err)
}

// sentEvents are the events of a waited send's agent numbered above the
// cursor the send took before it left the message (waitedSend.sent), as
// far as its journal has been read (readOn).
type sentEvents struct {
	events []state.Event
	pos    int64 // where the next read of the journal starts
}

// readOn reads on the events of w's agent numbered above its cursor into
// e, from where e's last read ended: the call reads on at each look, and a
// send's wait task reads them whole at each look, from a new sentEvents.
// A journal that cannot be read leaves e as it was.
func (w waitedSend) readOn(d deps, e *sentEvents) {
	j, err := d.journal.ReadFrom(w.agent.ID, e.pos)
	if err != nil {
		return
	}
	if j.From != e.pos {
		e.events = nil
	}
	e.events = append(e.events, above(j.Events, w.sent)...)
	e.pos = j.Next
}

// stallKey is the condition the stall clock of a waited send measures at a
// look that shows its agent as a, the message's taking being t, and the
// moment it counts from when it is new (stallClock): keyWaiting while the
// message waits, not taken, from ready; that hq typed it in, with the
// delivery's number, while Claude has reported no prompt since, from the
// delivery's time; "" for none, while the agent is not at its prompt
// (atPrompt) or the message was taken otherwise (#170, decision 3).
func stallKey(a agent.Agent, t taking, ready time.Time) (key string, origin time.Time) {
	switch {
	case !atPrompt(a):
	case !t.taken:
		key, origin = keyWaiting, ready
	case t.byHQ && t.prompt == 0:
		key, origin = fmt.Sprint("typed ", t.delivery), t.at
	}
	return key, origin
}

// stalled is how the waited send ends once the stall clock of its message
// has run stallBound, at a look whose cursor is next that shows its agent
// as a, the message's taking being t (#170, decisions 4 and 5): stalled
// with the message still in the inbox, or gone from it with no delivery of
// it in the journal; typed in, the box decides: text in it is a prompt not
// sent (stalled), and an empty or gone box is a slash command that started
// no turn (no_turn) or any other message that left the box with no turn
// (stalled). The call asks it of its own looks, and a send's wait task of
// the journal (sendToolTask.look).
func (w waitedSend) stalled(d deps, a agent.Agent, t taking, next int64) (sendEnd, error) {
	switch box := boxOf(d, a); {
	case !t.taken && d.pending(a.RepoPath, a.ID) > 0:
		return sendEnd{}, stalledErr(a, "it still waits in "+a.Name+"'s inbox, and nothing was typed in")
	case !t.taken:
		// Taken by another process with no delivery of it in the journal,
		// or a journal hq cannot read: where it went is not known.
		return sendEnd{}, stalledErr(a, "it is no longer in "+a.Name+"'s inbox, and no delivery of it is in "+a.Name+"'s journal")
	case box != "":
		return sendEnd{}, stalledErr(a, "it is typed into "+a.Name+"'s prompt box and not sent")
	case w.slashCommand:
		return sendEnd{outcome: endNoTurn, agents: []agent.Agent{a}, next: next}, nil
	default:
		return sendEnd{}, stalledErr(a, "it left "+a.Name+"'s prompt box and started no turn")
	}
}

// endAt is how the waited send ends by what a look whose agents are as and
// whose cursor is next shows of its agent at now, the message's taking
// being t, if it does: killed since it was last seen, gone with its inbox
// (gone), as the end of the turn that took the message or with the message
// gone with it; or settled, its row with where a message not taken is.
// Otherwise it gives the agent as the look shows it. The call asks it at
// each look, and a send's wait task at each poll (#364).
func (w waitedSend) endAt(d deps, as []agent.Agent, t taking, next int64, now time.Time) (sendEnd, agent.Agent, bool) {
	i := slices.IndexFunc(as, func(b agent.Agent) bool { return b.ID == w.agent.ID })
	if i < 0 {
		end := sendEnd{outcome: endTurn, agents: []agent.Agent{w.agent.Gone(now)}, next: next}
		if !t.taken {
			end.outcome, end.message = endNotTaken, msgGone
		}
		return end, agent.Agent{}, true
	}
	a := as[i]
	outcome, ok := settled(a, t, next)
	if !ok {
		return sendEnd{}, a, false
	}
	end := sendEnd{outcome: outcome, agents: []agent.Agent{a}, next: next}
	if outcome == endNotTaken {
		end.message = notTakenWhere(d, a)
	}
	return end, a, true
}

// othersThan are the agents as but the one whose id is id.
func othersThan(as []agent.Agent, id string) []agent.Agent {
	return slices.DeleteFunc(slices.Clone(as), func(b agent.Agent) bool { return b.ID == id })
}

// above are the events numbered above n.
func above(events []state.Event, n int64) []state.Event {
	var out []state.Event
	for _, e := range events {
		if e.Number > n {
			out = append(out, e)
		}
	}
	return out
}

// boxOf is what agent a's prompt box holds, Claude's faint hints aside: ""
// when it is empty or gone, as once Claude has taken a prompt.
func boxOf(d deps, a agent.Agent) string {
	styled, err := d.tmux.StyledScreen(a.Pane)
	if err != nil {
		return ""
	}
	return state.BoxText(state.WithoutHints(styled))
}

// notTakenWhere is where the message of agent a, which has not taken it,
// is: waiting while it needs input, and once it has ended kept for its
// relaunch while its inbox holds messages, else gone. Nothing takes a
// message out of an ended agent's inbox but a kill, which removes it whole,
// so a message not taken is still there while any is.
func notTakenWhere(d deps, a agent.Agent) string {
	switch {
	case a.State != state.Ended:
		return msgWaits
	case d.pending(a.RepoPath, a.ID) > 0:
		return msgKept
	default:
		return msgGone
	}
}

// stalledErr is the failure of a stalled message (decisions 4 and 5),
// saying where the message is.
func stalledErr(a agent.Agent, where string) error {
	return &Error{Code: ExitStalled, Msg: fmt.Sprintf("stalled: %s did not take the message: %s; see hq read %s before sending again", a.Name, where, a.Name)}
}

// printSendWait prints how a waited send ended, after the line that said
// how the message goes (printed already, but with --json): a line for a
// message not taken and for a command that started no turn, then the
// agent's row and the cursor as hq wait prints them.
func printSendWait(out io.Writer, d deps, name, how string, asJSON bool, e sendEnd) error {
	rows := lsRows(d, e.agents)
	if asJSON {
		return writeJSON(out, sendWaitOutput{Delivery: how, Outcome: e.outcome, Message: e.message, Agents: rows, NextSince: cursorText(e.next)})
	}
	switch {
	case e.outcome == endNoTurn:
		fmt.Fprintf(out, "no turn: the slash command left %s's prompt box and started no turn\n", name)
	case e.message == msgWaits:
		fmt.Fprintf(out, "not taken: %s needs input before it took the message, which still waits for it\n", name)
	case e.message == msgKept:
		fmt.Fprintf(out, "not taken: %s has ended before it took the message, which is kept for its relaunch\n", name)
	case e.message == msgGone:
		fmt.Fprintf(out, "not taken: %s has ended before it took the message, which went with it\n", name)
	}
	return printWait(out, waitArgs{names: []string{name}}, rows, e.next)
}
