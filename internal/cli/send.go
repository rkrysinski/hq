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
	if a.State == state.Done || a.State == state.Question || a.OnBackgroundWork() {
		return typeIn(d, a)
	}
	if d.pending(a.RepoPath, a.ID) == 0 {
		return fmt.Sprintf("delivered: %s took it with its hooks", a.Name), nil
	}
	return queued(a, now), nil
}

// queued says when the hooks of an agent that is not at its prompt deliver
// a message (ADR 0012).
func queued(a agent.Agent, now bool) string {
	var when string
	switch a.State {
	case state.Working:
		when = "is working, delivered when it stops"
		if now {
			when = "is working, delivered after its next tool call, or when it stops"
		}
	case state.NeedsInput:
		if a.StartAsk() != nil {
			// Claude's own dialog at its start: closing it fires no hook,
			// so nothing delivers the message until the next prompt (#29).
			return fmt.Sprintf("queued: %s waits at a dialog Claude shows at its start; once it is answered there (hq go %s), delivered with its next prompt", a.Name, a.Name)
		}
		when = "needs input, delivered once its dialog is closed, when it stops"
		if now {
			when = "needs input, delivered once its dialog is closed, after its next tool call"
		}
	case state.Ended:
		when = "has ended; delivered once it is relaunched"
	default:
		when = "is starting, delivered with its first prompt, or when it stops"
	}
	return fmt.Sprintf("queued: %s %s", a.Name, when)
}

// typeIn types every message waiting for an agent that waits at its prompt
// into the prompt box as its next prompt, oldest first, when the box is
// empty (Claude's faint hints there aside); with anything in the box (the user typing), or a screen that does
// not show the box, the messages wait and ride along with the next prompt
// sent there. They leave the inbox first, so no hook delivers them too. A
// prompt made only of the supervisor's messages is the supervisor's: the
// agent's hooks are told before it is typed (spec §5).
func typeIn(d deps, a agent.Agent) (string, error) {
	screen, err := d.tmux.StyledScreen(a.Pane)
	if empty, _ := state.AtRest(state.WithoutHints(screen), ""); err != nil || !empty {
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
	if supervised {
		awaitPrompt(d, a)
	}
	return fmt.Sprintf("delivered: typed into %s as its next prompt", a.Name), nil
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
