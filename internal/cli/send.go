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

const sendUsage = "hq send NAME TEXT [--now] [--wait [--timeout DURATION]] [--json]"

// sendArgs are hq send's arguments.
type sendArgs struct {
	name, text   string
	now, asJSON  bool
	wait         bool          // --wait: return when the turn that took it ends (#170)
	timeout      time.Duration // with --wait, as hq wait's
	timeoutGiven bool
}

// parseSend reads hq send's arguments: options anywhere, NAME and TEXT in
// that order, a TEXT that starts with a dash but holds a space taken for
// the text, as "--timeout=0 fails" is.
func parseSend(args []string) (sendArgs, error) {
	s := sendArgs{timeout: DefaultWaitTimeout}
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		flag, value, inline := strings.Cut(a, "=")
		switch {
		case a == "--now":
			s.now = true
		case a == "--json":
			s.asJSON = true
		case a == "--wait":
			s.wait = true
		case flag == "--timeout" && !strings.ContainsAny(a, " \t\n"):
			value, err := optionValue(args, &i, flag, value, inline, sendUsage)
			if err != nil {
				return s, err
			}
			if s.timeout, err = parseTimeout(value); err != nil {
				return s, err
			}
			s.timeoutGiven = true
		case len(a) > 1 && strings.HasPrefix(a, "-") && !strings.ContainsAny(a, " \t\n"):
			return s, unknownOption(a, sendUsage)
		default:
			rest = append(rest, a)
		}
	}
	if len(rest) != 2 {
		return s, usageErr("usage: %s; quote the text", sendUsage)
	}
	if s.timeoutGiven && !s.wait {
		return s, usageErr("--timeout goes with --wait (usage: %s)", sendUsage)
	}
	s.name, s.text = rest[0], state.CleanMessage(rest[1])
	switch {
	case s.text == "":
		return s, usageErr("empty message (usage: %s)", sendUsage)
	case len(s.text) > state.MaxMessage:
		return s, usageErr("message too long: at most %d bytes", state.MaxMessage)
	}
	return s, nil
}

// runSend leaves a message for an agent, delivered when the agent is ready
// (spec §4.1, §5, ADR 0012): its hooks deliver it while it works, and an
// agent that waits at its empty prompt gets it typed in as its next prompt.
// With --wait it then waits for the turn that takes it to end (waitedSend).
func runSend(env Env, d deps, args []string) error {
	start := d.now()
	s, err := parseSend(args)
	if err != nil {
		return err
	}
	if err := checkTmux(d.tmux); err != nil {
		return err
	}
	a, err := findAgent(d, s.name)
	if err != nil {
		return err
	}
	switch {
	case a.Outdated():
		return outdatedErr(a)
	case a.State == state.Ended:
		return usageErr("%s has ended and cannot receive messages; relaunch it with %s, or kill it and start it again", a.Name, restartCommand(a))
	}
	// The cursor before the message is left: the reply it brings is a
	// change numbered above it, for hq wait --since.
	sent := counterBefore(d, a)
	msg, err := d.postMessage(a.RepoPath, a.ID, state.Message{Text: s.text, Supervisor: d.supervisor}, s.now)
	if err != nil {
		return envErr("cannot leave the message for %s: %v", a.Name, err)
	}
	// With --wait, the timeout counts the delivery in (decision 6).
	limit := func(wait time.Duration) time.Duration {
		if s.wait && s.timeout > 0 {
			return max(0, min(wait, start.Add(s.timeout).Sub(d.now())))
		}
		return wait
	}
	// A starting agent has not started its session yet, or has not taken
	// the first prompt it was launched with (#191): wait for it to leave
	// starting, then deliver as its state says; a message still waiting
	// then goes along with that first prompt, through its hooks, and waits
	// while that prompt is never reported.
	for deadline := d.now().Add(limit(startWait)); a.State == state.Starting && d.now().Before(deadline); {
		d.sleep(pollEvery)
		if a, err = findAgent(d, s.name); err != nil {
			return err
		}
	}
	ready := d.now()
	how, err := deliver(d, a, msg, s.now, isCommand(s.text), limit(boxWait))
	if err != nil {
		return err
	}
	if !s.wait {
		if s.asJSON {
			return writeJSON(env.Stdout, sendOutput{Delivery: how, NextSince: cursorText(sent)})
		}
		fmt.Fprintln(env.Stdout, how)
		return nil
	}
	if !s.asJSON {
		fmt.Fprintln(env.Stdout, how)
	}
	w := waitedSend{agent: a, msg: msg, slashCommand: isCommand(s.text), sent: sent, ready: ready, start: start, timeout: s.timeout}
	end, err := w.wait(d)
	if err != nil {
		return err
	}
	return printSendWait(env.Stdout, d, a.Name, how, s.asJSON, end)
}

// sendOutput is what hq send --json prints: how the message goes, and the
// agent's cursor from before it was left (ADR 0019), for hq wait --since,
// so the agent's reply to it is returned however late hq wait is called.
type sendOutput struct {
	Delivery  string `json:"delivery"`
	NextSince string `json:"next_since"`
}

// findAgent looks at the agents as hq ls does, turns the user ended
// included, and returns the one named name.
func findAgent(d deps, name string) (agent.Agent, error) {
	l, err := lookNow(d)
	if err != nil {
		return agent.Agent{}, err
	}
	a, ok := agent.Find(l.agents, name)
	if !ok {
		return agent.Agent{}, notFoundErr("no agent '%s' (see hq ls)", name)
	}
	return a, nil
}

// deliver delivers what waits for the agent now, when it waits at its
// prompt with nothing in the box, by typing it in; otherwise it leaves it
// to the agent's hooks. An agent working only on background work waits at
// its prompt too (agent.OnBackgroundWork). It returns how the message goes,
// msg naming the message and command saying it starts with a slash
// command, which the hooks deliver as text (#173); within is how long it
// waits for an empty box.
func deliver(d deps, a agent.Agent, msg string, now, command bool, within time.Duration) (string, error) {
	if atPrompt(a) {
		return typeIn(d, a, within, msg, command)
	}
	if d.pending(a.RepoPath, a.ID) == d.held(a.RepoPath, a.ID) {
		return taken(d, a, msg, command), nil
	}
	return asText(queued(a, now), command), nil
}

// isCommand reports whether a message starts with a slash command, as
// Claude Code takes a prompt typed in that starts with "/" (#173): hq types
// such a message in so that the command runs, and says when the hooks may
// deliver it instead, as text.
func isCommand(text string) bool { return strings.HasPrefix(text, "/") }

// asText adds to the line of a message left to the hooks that a slash
// command it starts with arrives as text when they deliver it. Not that it
// will: an agent back at its prompt before they do gets it typed in
// (deliverWaiting).
func asText(line string, command bool) string {
	if !command {
		return line
	}
	return line + "; if its hooks deliver it, the slash command arrives as text and does not run"
}

// taken is the line of a message msg that was no longer there to take
// when hq send looked: worded by the delivery that names it in the agent's
// journal, typed in by hq (any hq process: deliverWaiting) or taken by the
// agent's hooks, which deliver a slash command as text; with neither in
// the journal, only that it was taken, which is all hq knows (#173).
func taken(d deps, a agent.Agent, msg string, command bool) string {
	var t taking
	if msg != "" {
		_ = d.journal.Ingest(a.RepoPath, a.ID)
		if j, err := d.journal.Read(a.ID); err == nil {
			t = takingOf(j.Events, msg)
		}
	}
	switch {
	case !t.taken:
		return fmt.Sprintf("delivered: %s took it", a.Name)
	case t.byHQ:
		return fmt.Sprintf("delivered: typed into %s as its next prompt", a.Name)
	case command:
		return fmt.Sprintf("delivered: %s took it with its hooks, as text, so the slash command did not run", a.Name)
	default:
		return fmt.Sprintf("delivered: %s took it with its hooks", a.Name)
	}
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
// supervisor drives (#125). hq calls it where it looks at the agents to
// show or watch them: hq ls, hq read, hq wait, hq send --wait (for the
// agents but its own) and the list; hq explain looks and delivers nothing.
func deliverWaiting(d deps, as []agent.Agent) {
	for _, a := range as {
		if !atPrompt(a) {
			continue
		}
		if at, ok := d.oldestMessage(a.RepoPath, a.ID); ok && d.now().Sub(at) >= waitingAfter {
			_, _ = typeIn(d, a, 0, "", false)
		}
	}
}

// queued says when the hooks of an agent that is not at its prompt deliver
// a message (ADR 0012).
func queued(a agent.Agent, now bool) string {
	var when string
	switch dialog := a.Dialog(); {
	case dialog.Whose == state.DialogOwn:
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

// typeIn types the messages waiting for an agent that waits at its prompt
// into the prompt box as its next prompt, oldest first, once the box is
// empty (Claude's faint hints there aside) and at rest, which it waits for
// up to within (boxAtRest); with anything in the box all that time (the
// user typing), or a screen that does not show the box, the messages wait
// and ride along with the next prompt sent there. They leave the inbox
// first, so no hook delivers them too. A message that starts with a slash
// command is a prompt of its own (prompts): typeIn types the first prompt
// in and holds the others for the agent's next prompts, which hq types in
// one at a time as it finds the agent back at its prompt (deliverWaiting,
// #173). A prompt made only of the supervisor's messages is the
// supervisor's: the agent's hooks are told before it is typed (spec §5).
// It returns once Claude has taken the prompt out of the box, or leaveWait
// on (awaitBoxLeft). msg names the message hq send left and command says
// it starts with a slash command, for the line it returns.
func typeIn(d deps, a agent.Agent, within time.Duration, msg string, command bool) (string, error) {
	if !boxAtRest(d, a, within) {
		return asText(fmt.Sprintf("queued: %s has something in its prompt box, delivered with its next prompt", a.Name), command), nil
	}
	all, err := d.takeMessages(a.RepoPath, a.ID)
	if err != nil {
		return "", envErr("cannot take the messages for %s: %v", a.Name, err)
	}
	if len(all) == 0 {
		return taken(d, a, msg, command), nil
	}
	ps := prompts(all)
	msgs := ps[0]
	// The delivery is in the agent's journal before the prompt is typed,
	// so the prompt Claude reports comes after it; it names only the
	// messages of this prompt, which the held ones are not (#195).
	names := messageNames(msgs)
	appendEvent(d, a.ID, state.KindDelivery, state.Facts{Messages: names})
	texts := make([]string, len(msgs))
	supervised := true
	for i, m := range msgs {
		texts[i] = m.Text
		supervised = supervised && m.Supervisor
	}
	// Without the word the turn notifies, as the user's do: no failure.
	supervised = supervised && d.announce(a.RepoPath, a.ID) == nil
	err = typePrompt(d, a.Pane, strings.Join(texts, "\n\n"))
	if err == nil {
		d.sleep(pasteSettle)
		err = d.tmux.Submit(a.Pane)
	}
	if err != nil {
		if supervised {
			_ = d.withdraw(a.RepoPath, a.ID)
		}
		// Back into the inbox, in the order sent, each under its name: the
		// hooks deliver them with the next prompt.
		for _, m := range all {
			_, _ = d.postMessage(a.RepoPath, a.ID, m, false)
		}
		appendWithdrawn(d, a.ID, state.KindDelivery, names)
		return "", tmuxErr(err)
	}
	// The others wait for the agent's next prompts, where the hooks do not
	// deliver them; held only now, so that a failure above leaves none
	// held behind the ones it puts back.
	if held := all[len(msgs):]; len(held) > 0 {
		if err := d.holdMessages(a.RepoPath, a.ID, held); err != nil {
			for _, m := range held {
				_, _ = d.postMessage(a.RepoPath, a.ID, m, false)
			}
			return "", envErr("typed an earlier message into %s, but cannot hold the others for its next prompts (%v): they are left to its hooks", a.Name, err)
		}
	}
	awaitBoxLeft(d, a)
	if supervised {
		awaitPrompt(d, a)
	}
	if len(ps) > 1 {
		return fmt.Sprintf("queued: typed an earlier message into %s as its next prompt; this one follows as a prompt of its own once %s is back at its prompt", a.Name, a.Name), nil
	}
	return fmt.Sprintf("delivered: typed into %s as its next prompt", a.Name), nil
}

// prompts groups messages, oldest first, into the prompts hq types in, in
// order: a run of messages that do not start with a slash command is one
// prompt, joined as typeIn joins them, and a message that starts with one
// is a prompt of its own, where Claude Code runs the command. Joined behind
// another message's text a command would be text; joined before one, the
// text would be the command's arguments (#173).
func prompts(msgs []state.Message) [][]state.Message {
	var ps [][]state.Message
	for i, m := range msgs {
		if i == 0 || isCommand(m.Text) || isCommand(msgs[i-1].Text) {
			ps = append(ps, nil)
		}
		ps[len(ps)-1] = append(ps[len(ps)-1], m)
	}
	return ps
}

// typePrompt puts a prompt into the agent's prompt box, Enter not pressed:
// pasted, as a terminal pastes it, so its lines stay one prompt; but a
// slash command that Claude Code would collapse into "[Pasted text #N]",
// and so not run (state.Collapses), has its command word typed and the
// rest pasted after it, which Claude Code runs as the command with the
// rest as its arguments, whatever their length (Claude Code 2.1.325, #173).
func typePrompt(d deps, pane, text string) error {
	if !isCommand(text) || !state.Collapses(text) {
		return d.tmux.Paste(pane, text)
	}
	word, rest := text, ""
	if i := strings.IndexAny(text, " \t\n"); i >= 0 {
		word, rest = text[:i], text[i:]
	}
	if err := d.tmux.Type(pane, word); err != nil || rest == "" {
		return err
	}
	if err := d.tmux.Paste(pane, rest); err != nil {
		// Not the command word alone left in the box, which would keep
		// the box from being empty and start the user's next prompt.
		_ = d.tmux.Press(pane, "C-u")
		return err
	}
	return nil
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
