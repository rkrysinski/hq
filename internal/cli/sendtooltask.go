package cli

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"strings"

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
	// msg is the name the message was left under.
	msg string
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
// (taskHeader.fields), t when the message was taken, the agent's name, =
// and its id, the message's name and the delivery in base64url (it holds
// colons), between colons, which neither a name nor an id holds.
func (t sendToolTask) terms() string {
	taken := ""
	if t.taken {
		taken = "t"
	}
	return strings.Join(append(t.fields(), taken, t.agent.Name+"="+t.agent.ID, t.msg, base64.RawURLEncoding.EncodeToString([]byte(t.delivery))), ":")
}

// parseSendToolTask is the send tool's task whose id carries the terms f,
// or false for terms no such task's id carries.
func parseSendToolTask(f []string) (sendToolTask, bool) {
	h, ok := parseTaskHeader(f, sendToolTaskKind, 8)
	if !ok {
		return sendToolTask{}, false
	}
	name, id, named := strings.Cut(f[5], "=")
	delivery, deliveryErr := base64.RawURLEncoding.DecodeString(f[7])
	t := sendToolTask{taskHeader: h, agent: agent.Agent{ID: id, Name: name}, msg: f[6], taken: f[4] == "t", delivery: string(delivery)}
	return t, named && id != "" && checkName(name) == nil && t.msg != "" && deliveryErr == nil
}

// look takes a look for t as the call takes each look (waitedSend.wait),
// delivering what waits as each look does, its own message too until its
// deadline, and prints the send's result to out, as the send tool prints
// it, once it ends (waitedSend.endAt): its agent stopped after taking the
// message, stopped or was killed before taking it, or the deadline passed.
// done says that it ended, or failed as the call would, tmux gone or a
// look failing: the task has then completed.
func (t sendToolTask) look(out io.Writer, d deps) (done bool, err error) {
	w := waitedSend{agent: t.agent, msg: t.msg, sent: t.cursor}
	var end sendEnd
	err = lookFor(d, func(s agent.Seen, next int64, _ bool) bool {
		now := d.now()
		late := t.passed(now)
		as := s.Agents
		if late {
			// At its timeout a waited send types its message in no more
			// (#170, decision 6).
			as = othersThan(as, t.agent.ID)
		}
		deliverWaiting(d, as)
		var events []state.Event
		if j, err := d.journal.Read(t.agent.ID); err == nil {
			events = above(j.Events, t.cursor)
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
		return false
	})
	if err != nil {
		return true, err
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
	return send(env, d, args, func(t sendToolTask) error {
		if err := handOff(ctx, d, t); err != nil {
			return fmt.Errorf("the message is left, but the wait for its answer cannot go on as a task: %w", err)
		}
		return nil
	})
}
