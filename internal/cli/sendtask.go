package cli

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/mcptasks"
	"github.com/rkrysinski/hq/internal/state"
)

// A sendWaitTask is a send with wait that goes on after its call as a wait
// task (GLOSSARY.md, ADR 0022), waiting for the answer to its one message
// (#364): what turn_ended and not_taken say is of that message, not of any
// change of its agent, so its id carries the agent, the message, the cursor
// the send took before it left the message and the deadline, signed as a
// wait's (taskid). Any hq mcp answers for it by taking a look, and none
// holds it. It tells no stall and no slash command that started no turn:
// those take an unbroken stretch of stallBound, which no look of its own
// measures, and stay the call's (waitedSend.wait).
type sendWaitTask struct {
	// agent is the agent the message was left for, its id and name alone,
	// as a wait task's covered agents (waitTask): one killed since comes
	// back ended with what the trace of kills says of it (traced).
	agent agent.Agent
	// msg is the name the message was left under, and sent the cursor the
	// send took before it left it: the message's taking is read off the
	// agent's events numbered above it (takingOf).
	msg  string
	sent int64
	// taken says the call saw the message taken: what a journal removed
	// since, as hq kill removes it, can no longer say.
	taken bool
	// delivery is how the message went, the line the call made of it.
	delivery string
	// deadline is when it completes as at a timeout, zero for none, and
	// created when the call went on as the task, both to the millisecond.
	deadline, created time.Time
}

// sendWaitTaskKind starts the id of every send's wait task: its kind and
// version.
const sendWaitTaskKind = "s1"

// terms are what t's task id carries, which taskid.Sign signs: its kind,
// cursor, creation and deadline (as a wait task's), t when the message was
// taken, the agent's name, = and its id, the message's name and the
// delivery in base64url (it holds colons), between colons, which neither a
// name nor an id holds.
func (t sendWaitTask) terms() string {
	taken := ""
	if t.taken {
		taken = "t"
	}
	return strings.Join([]string{sendWaitTaskKind, cursorText(t.sent), millis(t.created), millis(t.deadline), taken, t.agent.Name + "=" + t.agent.ID, t.msg, base64.RawURLEncoding.EncodeToString([]byte(t.delivery))}, ":")
}

// parseSendWaitTask is the send's wait task whose id carries terms, or
// false for terms no send's wait task's id carries.
func parseSendWaitTask(terms string) (sendWaitTask, bool) {
	f := strings.Split(terms, ":")
	if len(f) != 8 || f[0] != sendWaitTaskKind {
		return sendWaitTask{}, false
	}
	s, sinceErr := parseSince(f[1], time.Time{})
	created, createdErr := parseMillis(f[2])
	deadline, deadlineErr := parseMillis(f[3])
	name, id, named := strings.Cut(f[5], "=")
	delivery, deliveryErr := base64.RawURLEncoding.DecodeString(f[7])
	t := sendWaitTask{agent: agent.Agent{ID: id, Name: name}, msg: f[6], sent: s.cursor, taken: f[4] == "t", delivery: string(delivery), deadline: deadline, created: created}
	ok := sinceErr == nil && s.isCursor && f[2] != "" && createdErr == nil && deadlineErr == nil && named && id != "" && checkName(name) == nil && t.msg != "" && deliveryErr == nil
	return t, ok
}

// task is t as the MCP server knows it, by its id.
func (t sendWaitTask) task(id string) mcptasks.Task { return taskOf(id, t.created, t.deadline) }

// poll takes a look for the send's wait task t as the call takes each look
// (waitedSend.wait), delivering what waits as each look does, its own
// message too until its deadline, and prints the send's result to out, as
// the send tool prints it, once it ends (waitedSend.endAt): its agent
// stopped after taking the message, stopped or was killed before taking
// it, or the deadline passed. done says that it ended, or failed as the
// call would, tmux gone or a look failing: the task has then completed.
func (t sendWaitTask) poll(out io.Writer, d deps) (done bool, err error) {
	if err := checkTmux(d.tmux); err != nil {
		return true, err
	}
	w := waitedSend{agent: t.agent, msg: t.msg, sent: t.sent}
	var end sendEnd
	k := d.looker()
	err = k.Loop(func(s agent.Seen, _ bool) (bool, error) {
		next := k.Cursor(s) // taken before what waits is typed in
		now := d.now()
		late := !t.deadline.IsZero() && !now.Before(t.deadline)
		as := s.Agents
		if late {
			// At its timeout a waited send types its message in no more
			// (#170, decision 6).
			as = othersThan(as, t.agent.ID)
		}
		deliverWaiting(d, as)
		var events []state.Event
		if j, err := d.journal.Read(t.agent.ID); err == nil {
			events = above(j.Events, t.sent)
		}
		tk := takingOf(events, t.msg)
		if len(events) == 0 {
			tk.taken = t.taken
		}
		w.agent = traced(d, []agent.Agent{t.agent}, s.Agents)[0]
		end, _, done = w.endAt(d, s.Agents, tk, next, now)
		if !done && late {
			end, done = sendEnd{outcome: endTimeout, next: next}, true
		}
		return true, nil
	})
	if err != nil {
		return true, lookErr(err)
	}
	if !done {
		return false, nil
	}
	return true, printSendWait(out, d, t.agent.Name, t.delivery, true, end)
}

// sendOrHandOff is hq send --wait with args for a call of the send tool
// that may go on as a task (mcptasks.Offered), args holding the call's
// timeout, 0 for none: what it returns within waitHold, or within its
// timeout when that is shorter, as runSend prints it; or, when the hold
// passes with no end and no stall to tell, nothing, the call going on as a
// send's wait task (#364), until its timeout when it has one; or the error
// of a key hq can neither read nor make, the message left as it is.
func sendOrHandOff(ctx context.Context, env Env, d deps, args []string) error {
	return send(env, d, args, func(t sendWaitTask) error {
		if err := handOff(ctx, d, t); err != nil {
			return fmt.Errorf("the message is left, but the wait for its answer cannot go on as a task: %w", err)
		}
		return nil
	})
}
