package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/state"
)

// startWait is how long hq send waits for a starting agent's first report
// before it leaves the message to its hooks: a sandbox that is not running
// takes seconds to start.
const startWait = 30 * time.Second

// pollEvery is how often hq send looks again at a starting agent.
const pollEvery = 250 * time.Millisecond

// pasteSettle is how long hq send gives Claude to take in the pasted
// message before it presses Enter, so the Enter is not part of the paste.
const pasteSettle = 300 * time.Millisecond

// promptWait is how long hq send gives Claude to take a prompt it typed in
// for the supervisor before it takes back its word that the next prompt is
// the supervisor's: Claude reports a prompt as it is sent, well within it.
// A prompt that comes later, or one of the user's, then notifies at its
// turn's end, as the user's do (design §3.5).
const promptWait = 3 * time.Second

// promptPoll is how often hq send looks whether that prompt was taken.
const promptPoll = 50 * time.Millisecond

// boxWait is how long hq send waits for the prompt box of an agent at its
// prompt to be empty and at rest before it leaves the message for the next
// prompt: right after a prompt or a slash command (the previous hq send's)
// Claude has not taken it yet, or shows a panel in place of the box; after
// /model the screen is not at rest for about 2.5 s (Claude Code 2.1.304,
// #125).
const boxWait = 4 * time.Second

// leaveWait is how long hq send waits, once it has typed a prompt in, for
// Claude to take it out of the box, so that the next hq send meets what
// Claude does with it, not the prompt still in the box (#125).
const leaveWait = 3 * time.Second

// boxPoll is how often hq send looks at the box meanwhile.
const boxPoll = 150 * time.Millisecond

// waitingAfter is how long a message waits for an agent at its prompt before
// hq, looking at the agents, types it in itself (deliverWaiting): longer than
// hq send waits for the box, so that a message is typed in by the hq send
// that left it. Not always: one left for a starting agent waits up to
// startWait before hq send looks at the box, and a look that types in an
// older message takes a newer one with it (state.Take takes them all).
// Whoever takes a message delivers it, once; the hq send that left it
// then says it was taken.
const waitingAfter = boxWait + 2*time.Second

const sendUsage = "usage: hq send NAME TEXT [--now] [--json]"

// runSend leaves a message for an agent, delivered when the agent is ready
// (spec §4.1, §5, ADR 0012): its hooks deliver it while it works, and an
// agent that waits at its empty prompt gets it typed in as its next prompt.
func runSend(env Env, d deps, args []string) error {
	var rest []string
	now, asJSON := false, false
	for _, a := range args {
		switch {
		case a == "--now":
			now = true
		case a == "--json":
			asJSON = true
		case len(a) > 1 && strings.HasPrefix(a, "-") && !strings.ContainsAny(a, " \t\n"):
			return usageErr("unknown option '%s' (%s)", a, sendUsage)
		default:
			rest = append(rest, a)
		}
	}
	if len(rest) != 2 {
		return usageErr("%s; quote the text", sendUsage)
	}
	name, text := rest[0], state.CleanMessage(rest[1])
	switch {
	case text == "":
		return usageErr("empty message (%s)", sendUsage)
	case len(text) > state.MaxMessage:
		return usageErr("message too long: at most %d bytes", state.MaxMessage)
	}
	if err := checkTmux(d.tmux); err != nil {
		return err
	}
	a, err := findAgent(d, name)
	if err != nil {
		return err
	}
	relaunch := fmt.Sprintf("relaunch it with hq sandbox restart %s, or kill it and start it again", a.Repo())
	switch {
	case a.State == state.Ended:
		return usageErr("%s has ended and cannot receive messages; %s", a.Name, relaunch)
	case !a.Inbox:
		return usageErr("%s was started by an older hq and cannot receive messages; %s", a.Name, relaunch)
	}
	// The moment before the message is left: the reply it brings is a
	// change after it, for hq wait --since.
	sent := lookedAt(d.now())
	if err := d.postMessage(a.RepoPath, a.ID, state.Message{Text: text, Supervisor: d.supervisor}, now); err != nil {
		return envErr("cannot leave the message for %s: %v", a.Name, err)
	}
	// A starting agent's session has not started yet: wait for its first
	// report, then deliver as its state says.
	for deadline := d.now().Add(startWait); a.State == state.Starting && d.now().Before(deadline); {
		d.sleep(pollEvery)
		if a, err = findAgent(d, name); err != nil {
			return err
		}
	}
	how, err := deliver(d, a, now)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(env.Stdout, sendOutput{Delivery: how, NextSince: sent})
	}
	fmt.Fprintln(env.Stdout, how)
	return nil
}

// sendOutput is what hq send --json prints: how the message goes, and the
// moment before it was left, for hq wait --since, so the agent's reply to
// it is returned however late hq wait is called.
type sendOutput struct {
	Delivery  string    `json:"delivery"`
	NextSince time.Time `json:"next_since"`
}

// findAgent collects the agents as hq ls does, turns the user ended
// included, and returns the one named name.
func findAgent(d deps, name string) (agent.Agent, error) {
	as, err := collect(d)
	if err != nil {
		return agent.Agent{}, err
	}
	a, ok := agent.Find(as, name)
	if !ok {
		return agent.Agent{}, notFoundErr("no agent '%s' (see hq ls)", name)
	}
	return a, nil
}

// deliver delivers what waits for the agent now, when it waits at its
// prompt with nothing in the box, by typing it in; otherwise it leaves it
// to the agent's hooks. An agent working only on background work waits at
// its prompt too (agent.OnBackgroundWork). It returns how the message goes.
func deliver(d deps, a agent.Agent, now bool) (string, error) {
	if atPrompt(a) {
		return typeIn(d, a, boxWait)
	}
	if d.pending(a.RepoPath, a.ID) == 0 {
		return fmt.Sprintf("delivered: %s took it with its hooks", a.Name), nil
	}
	return queued(a, now), nil
}

// atPrompt reports whether the agent waits at its prompt, where hq types
// a message in: done, with a question, or working only on background work.
func atPrompt(a agent.Agent) bool {
	return a.State == state.Done || a.State == state.Question || a.OnBackgroundWork()
}

// deliverWaiting types in the messages that have waited waitingAfter or longer
// for agents at their prompt, as hq send would have, when their box is
// empty and at rest; it does not wait for the box. hq send leaves the
// message for the next prompt when the box does not settle in time, and a
// slash command fires no hook, so an agent that stays at its prompt would
// keep it for a prompt the user types, which never comes for an agent the
// supervisor drives (#125). hq calls it wherever it looks at the agents:
// hq ls, hq read, hq wait and the list.
func deliverWaiting(d deps, as []agent.Agent) {
	for _, a := range as {
		if !a.Inbox || !atPrompt(a) {
			continue
		}
		if at, ok := d.oldestMessage(a.RepoPath, a.ID); ok && d.now().Sub(at) >= waitingAfter {
			_, _ = typeIn(d, a, 0)
		}
	}
}

// queued says when the hooks of an agent that is not at its prompt deliver
// a message (ADR 0012).
func queued(a agent.Agent, now bool) string {
	var when string
	switch dialog := a.Dialog(); {
	case dialog.Whose == agent.OwnDialog:
		// A dialog of Claude's own, at its start or after a turn: closing
		// it fires no hook, so nothing delivers the message until the next
		// prompt (#29, #66).
		return fmt.Sprintf("queued: %s waits at a dialog of Claude's own; once it is answered there (hq go %s), delivered with its next prompt", a.Name, a.Name)
	case dialog.Open():
		when = "needs input, delivered once its dialog is closed, when it stops"
		if now {
			when = "needs input, delivered once its dialog is closed, after its next tool call"
		}
	case a.State == state.Working:
		when = "is working, delivered when it stops"
		if now {
			when = "is working, delivered after its next tool call, or when it stops"
		}
	case a.State == state.Ended:
		when = "has ended; delivered once it is relaunched"
	default:
		when = "is starting, delivered with its first prompt, or when it stops"
	}
	return fmt.Sprintf("queued: %s %s", a.Name, when)
}

// typeIn types every message waiting for an agent that waits at its prompt
// into the prompt box as its next prompt, oldest first, once the box is
// empty (Claude's faint hints there aside) and at rest, which it waits for
// up to within (boxAtRest); with anything in the box all that time (the
// user typing), or a screen that does not show the box, the messages wait
// and ride along with the next prompt sent there. They leave the inbox
// first, so no hook delivers them too. A prompt made only of the
// supervisor's messages is the supervisor's: the agent's hooks are told
// before it is typed (spec §5). It returns once Claude has taken the
// prompt out of the box, or leaveWait on (awaitBoxLeft).
func typeIn(d deps, a agent.Agent, within time.Duration) (string, error) {
	if !boxAtRest(d, a, within) {
		return fmt.Sprintf("queued: %s has something in its prompt box, delivered with its next prompt", a.Name), nil
	}
	msgs, err := d.takeMessages(a.RepoPath, a.ID)
	if err != nil {
		return "", envErr("cannot take the messages for %s: %v", a.Name, err)
	}
	if len(msgs) == 0 {
		return fmt.Sprintf("delivered: %s took it with its hooks", a.Name), nil
	}
	texts := make([]string, len(msgs))
	supervised := a.Supervisable()
	for i, m := range msgs {
		texts[i] = m.Text
		supervised = supervised && m.Supervisor
	}
	// Without the word the turn notifies, as the user's do: no failure.
	supervised = supervised && d.announce(a.RepoPath, a.ID) == nil
	err = d.tmux.Paste(a.Pane, strings.Join(texts, "\n\n"))
	if err == nil {
		d.sleep(pasteSettle)
		err = d.tmux.Submit(a.Pane)
	}
	if err != nil {
		if supervised {
			_ = d.withdraw(a.RepoPath, a.ID)
		}
		// Back into the inbox: the hooks deliver them with the next prompt.
		for _, m := range msgs {
			_ = d.postMessage(a.RepoPath, a.ID, m, false)
		}
		return "", tmuxErr(err)
	}
	awaitBoxLeft(d, a)
	if supervised {
		awaitPrompt(d, a)
	}
	return fmt.Sprintf("delivered: typed into %s as its next prompt", a.Name), nil
}

// boxAtRest reports whether the agent's prompt box is empty and at rest
// (state.AtRest, faint hints aside), on two looks boxPoll apart that see
// the same screen, so a screen Claude is about to redraw, as when it has
// just taken a prompt and shows no spinner yet, is not typed into. It
// looks again every boxPoll for up to within; with within zero, it gives
// up at the first look not at rest.
func boxAtRest(d deps, a agent.Agent, within time.Duration) bool {
	last := ""
	for i := 0; i < int(within/boxPoll)+2; i++ {
		if i > 0 {
			d.sleep(boxPoll)
		}
		styled, err := d.tmux.StyledScreen(a.Pane)
		screen := state.WithoutHints(styled)
		if empty, _ := state.AtRest(screen, ""); err != nil || !empty {
			last = ""
			if within == 0 {
				return false
			}
			continue
		}
		if screen == last {
			return true
		}
		last = screen
	}
	return false
}

// awaitBoxLeft waits up to leaveWait until the agent's prompt box is empty
// or gone, as once Claude has taken what hq typed in, as a prompt or a
// slash command, whose panel may stand in place of the box (#125). The
// user typing there at once keeps it waiting the whole time.
func awaitBoxLeft(d deps, a agent.Agent) {
	for deadline := d.now().Add(leaveWait); d.now().Before(deadline); d.sleep(boxPoll) {
		styled, err := d.tmux.StyledScreen(a.Pane)
		if err != nil || state.BoxText(state.WithoutHints(styled)) == "" {
			return
		}
	}
}

// awaitPrompt waits until the agent's hooks have taken hq's word that the
// prompt just typed in is the supervisor's, and takes the word back when
// they have not within promptWait, so that it never falls to a prompt the
// user sends later.
func awaitPrompt(d deps, a agent.Agent) {
	for deadline := d.now().Add(promptWait); d.announced(a.RepoPath, a.ID); d.sleep(promptPoll) {
		if !d.now().Before(deadline) {
			_ = d.withdraw(a.RepoPath, a.ID)
			return
		}
	}
}
