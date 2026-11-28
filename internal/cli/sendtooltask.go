package cli

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/state"
)

// A sendToolTask is a wait task of the send tool (waitTask): a send with
// wait whose answer has not come within its hold, waiting on for the
// answer to its one message (#364). What turn_ended and not_taken say is
// of that message, not of any change of its agent, so its id carries the
// agent, the message, the cursor the send took before it left the message
// and the deadline, signed as a wait's (taskid). Any hq mcp answers for it
// by taking a look, and none holds it.
type sendToolTask struct {
	// taskHeader holds the cursor the send took before it left the
	// message: the message's taking is read off the agent's events
	// numbered above it (takingOf); and the call's timeout as its deadline.
	taskHeader
	// agent is the agent the message was left for, its id and name alone,
	// as a wait task's covered agents (waitToolTask): one killed since
	// comes back ended with what the trace of kills says of it (traced).
	agent agent.Agent
	// msg is the name the message was left under, and command says it
	// starts with a slash command: an empty box once hq typed it in and no
	// turn started is no_turn, where for any other message it is a stall.
	msg     string
	command bool
	// taken says the call saw the message taken: what a journal removed
	// since, as hq kill removes it, can no longer say.
	taken bool
	// delivery is how the message went, the line the call made of it.
	delivery string
}

// sendToolTaskKind starts the id of every wait task of the send tool: its
// kind and version.
const sendToolTaskKind = "s1"

// terms are what t's task id carries, which taskid.Sign signs: its header
// (taskHeader.fields), its flags (t when the message was taken, c when it
// starts with a slash command), the agent's name, = and its id, the
// message's name and the delivery (deliveryTerm), between colons, which
// neither a name nor an id holds. Claude Code prints the id into the
// supervisor's context with the handle and again with the result, so each
// term is as short as it can be: the message's name is how the journal
// names its delivery (takingOf), the agent's name is the result's, and the
// delivery is the call's line, which no look can tell again.
func (t sendToolTask) terms() string {
	flags := ""
	if t.taken {
		flags += "t"
	}
	if t.command {
		flags += "c"
	}
	return strings.Join(append(t.fields(), flags, t.agent.Name+"="+t.agent.ID, t.msg, deliveryTerm(t.delivery, t.agent.Name)), ":")
}

// deliveryLines are the lines of hq send's delivery by the codes a send's
// wait task's id carries them by (deliveryTerm): a code never changes its
// line, since an id outlives the hq that made it.
var deliveryLines = map[string]string{
	"p": typedLine, "t": tookLine, "h": hooksLine, "x": hooksTextLine, "f": followsTmpl, "b": boxBusyLine, "o": ownDialogLine,
	"d": dialogLine, "D": dialogNowLine, "w": workingLine, "W": workingNowLine, "e": endedLine, "s": startingLine,
}

// deliveryTerm is how a send's wait task's id carries delivery, the line
// of a message left for the agent named name: the code of its line in
// deliveryLines, followed by a when asText added its words; any other line
// whole, in base64url after = (it holds colons). It is a character or two
// where the line takes 40 to 180.
func deliveryTerm(delivery, name string) string {
	line, words := strings.CutSuffix(delivery, asTextWords)
	for code, tmpl := range deliveryLines {
		switch {
		case fmt.Sprintf(tmpl, name) != line:
		case words:
			return code + "a"
		default:
			return code
		}
	}
	return "=" + base64.RawURLEncoding.EncodeToString([]byte(delivery))
}

// deliveryOf is the line that deliveryTerm gave term for, of the agent
// named name, or false for a term it gives for none.
func deliveryOf(term, name string) (string, bool) {
	if whole, ok := strings.CutPrefix(term, "="); ok {
		line, err := base64.RawURLEncoding.DecodeString(whole)
		return string(line), err == nil
	}
	code, words := strings.CutSuffix(term, "a")
	tmpl, ok := deliveryLines[code]
	line := fmt.Sprintf(tmpl, name)
	if words {
		line += asTextWords
	}
	return line, ok
}

// parseSendToolTask is the send tool's task whose id carries the terms f,
// or false for terms no such task's id carries.
func parseSendToolTask(f []string) (sendToolTask, bool) {
	h, ok := parseTaskHeader(f, sendToolTaskKind, 8)
	if !ok {
		return sendToolTask{}, false
	}
	name, id, named := strings.Cut(f[5], "=")
	delivery, known := deliveryOf(f[7], name)
	flags, command := strings.CutSuffix(f[4], "c")
	t := sendToolTask{taskHeader: h, agent: agent.Agent{ID: id, Name: name}, msg: f[6], command: command, taken: flags == "t", delivery: delivery}
	return t, (flags == "" || flags == "t") && named && id != "" && checkName(name) == nil && t.msg != "" && known
}

// look takes a look for t as the call takes each look (waitedSend.wait),
// delivering what waits as each look does, its own message too until its
// deadline, and prints the send's result to out, as the send tool prints
// it, once it ends (waitedSend.endAt): its agent stopped after taking the
// message, stopped or was killed before taking it, its message stalled or
// a slash command started no turn (waitedSend.stalled), or the deadline
// passed. done says that it ended, or failed as the call would: stalled,
// tmux gone or a look failing; the task has then completed.
//
// A look cannot measure the unbroken stretch of stallBound a stall takes,
// as the call's looks do (stallClock), so it reads it off the journal: the
// condition the call measures (stallKey) did not hold at the handoff, the
// call's last look, so it holds since the latest of the handoff, the event
// that brought the agent to its prompt (cameToPrompt) and, for a message
// typed in, the delivery: a look of the agent elsewhere in between would
// have moved one of them on.
func (t sendToolTask) look(out io.Writer, d deps) (done bool, err error) {
	w := waitedSend{agent: t.agent, msg: t.msg, slashCommand: t.command, sent: t.cursor}
	var end sendEnd
	err = lookFor(d, func(s agent.Seen, next int64, _ bool) (bool, error) {
		as := s.Agents
		if t.passed(d.now()) {
			// At its timeout a waited send types its message in no more
			// (#170, decision 6).
			as = othersThan(as, t.agent.ID)
		}
		deliverWaiting(d, as)
		var events sentEvents
		w.readOn(d, &events)
		tk := takingOf(events.events, t.msg)
		if len(events.events) == 0 {
			tk.taken = t.taken
		}
		now := d.now()
		w.agent = traced(d, []agent.Agent{t.agent}, s.Agents)[0]
		var a agent.Agent
		if end, a, done = w.endAt(d, s.Agents, tk, next, now); done {
			return false, nil
		}
		if key, origin := stallKey(a, tk, t.created); key != "" && now.Sub(latest(origin, t.created, cameToPrompt(a, events.events))) >= stallBound {
			var err error
			end, err = w.stalled(d, a, tk, next)
			done = true
			return false, err
		}
		if t.passed(now) {
			end, done = sendEnd{outcome: endTimeout, next: next}, true
		}
		return false, nil
	})
	if err != nil {
		return true, err
	}
	if !done {
		return false, nil
	}
	return true, printSendWait(out, d, t.agent.Name, t.delivery, true, end)
}

// cameToPrompt is when agent a, at its prompt, came there, as far as its
// events numbered above a send's cursor tell: the moment it entered its
// state, or the latest turn end among them when that is later, as for an
// agent on background work, which stays working past the turn end that
// left it at its prompt (state.Reduced.Entered).
func cameToPrompt(a agent.Agent, events []state.Event) time.Time {
	at := a.Entered()
	for _, e := range events {
		if e.Kind == state.KindTurnEnd && e.Aside == "" {
			at = latest(at, e.Time)
		}
	}
	return at
}

// latest is the latest of moments.
func latest(at time.Time, more ...time.Time) time.Time {
	for _, m := range more {
		if m.After(at) {
			at = m
		}
	}
	return at
}

// sendOrHandOff is hq send --wait with args for a call of the send tool
// that may go on as a task (mcptasks.Offered), args holding the call's
// timeout, 0 for none: what it returns within waitHold, or within its
// timeout when that is shorter, as runSend prints it; or, when the hold
// passes with no end and no stall to tell, nothing, the call going on as a
// send's wait task (#364), until its timeout when it has one; or the error
// of a key hq can neither read nor make, the message left as it is.
func sendOrHandOff(ctx context.Context, env Env, d deps, args []string) error {
	return send(env, d, args, func(t sendToolTask) error {
		if err := handOff(ctx, d, t); err != nil {
			return fmt.Errorf("the message is left, but the wait for its answer cannot go on as a task: %w", err)
		}
		return nil
	})
}
