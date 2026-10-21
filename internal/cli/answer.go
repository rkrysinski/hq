package cli

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/state"
)

// takeWait is how long hq answer gives the agent's hook to take the answer:
// it looks for one every 0.2 s while it holds, so one not taken by then is
// not held for (an agent whose hook no longer holds) (#89).
const takeWait = 3 * time.Second

// closeWait is how long hq answer gives the agent to leave the dialog once
// its hook has taken the answer: Claude reports the answer within
// milliseconds of the hook's decision (#89).
const closeWait = 5 * time.Second

// answerPoll is how often hq answer looks whether the answer was taken and
// the dialog closed.
const answerPoll = 100 * time.Millisecond

// keyWait is how long hq answer gives a dialog of Claude's own to draw the
// key it pressed: Claude redraws the ❯ within 0.1 s (#120).
const keyWait = 2 * time.Second

const answerUsage = "usage: hq answer NAME --dialog ID ANSWER... | --cancel [--json]"

// answerRequest is an answer to an agent's question dialog, as hq answer
// and the supervisor's answer tool give it.
type answerRequest struct {
	name    string
	dialog  string         // the dialog's id
	answers []state.Answer // one per question, in the dialog's order
	cancel  bool
	asJSON  bool
}

// runAnswer answers an agent's question dialog on the user's word, or
// cancels it, or picks an option of a dialog of Claude's own (spec §4.1,
// design §3.4, ADR 0013, ADR 0014).
func runAnswer(env Env, d deps, args []string) error {
	var r answerRequest
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		flag, value, inline := strings.Cut(a, "=")
		switch {
		case a == "--json":
			r.asJSON = true
		case a == "--cancel":
			r.cancel = true
		case flag == "--dialog":
			if !inline {
				if i+1 == len(args) {
					return usageErr("--dialog needs the dialog's id, as hq read shows it (%s)", answerUsage)
				}
				i++
				value = args[i]
			}
			r.dialog = value
		case len(a) > 1 && strings.HasPrefix(a, "-") && !strings.ContainsAny(a, " \t\n"):
			return usageErr("unknown option '%s' (%s)", a, answerUsage)
		default:
			rest = append(rest, a)
		}
	}
	switch {
	case len(rest) == 0:
		return usageErr("%s", answerUsage)
	case r.cancel && len(rest) > 1:
		return usageErr("--cancel takes no answers (%s)", answerUsage)
	case !r.cancel && len(rest) == 1:
		return usageErr("no answer: one per question, an option's number, numbers joined with commas, or text; or --cancel (%s)", answerUsage)
	}
	r.name = rest[0]
	for _, a := range rest[1:] {
		r.answers = append(r.answers, parseAnswer(a))
	}
	return answer(env, d, r)
}

// parseAnswer reads one answer as hq answer takes it: an option's number,
// numbers joined with commas, or else free text.
func parseAnswer(arg string) state.Answer {
	var ns []int
	for _, p := range strings.Split(arg, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || strings.TrimSpace(p) != strconv.Itoa(n) {
			return state.Answer{Text: arg}
		}
		ns = append(ns, n)
	}
	return state.Answer{Options: ns}
}

// answerOutput is what hq answer --json prints: the agent's state once it
// has left the dialog, and the moment before the answer was left, for hq
// wait --since, so what the agent does with the answer counts as after it.
type answerOutput struct {
	State     string    `json:"state"`
	NextSince time.Time `json:"next_since"`
}

// answer leaves the answer for the agent's question dialog, for the hook
// that holds on it to give Claude, and returns once the agent has left the
// dialog, comparing what Claude reports it got with what was sent.
func answer(env Env, d deps, r answerRequest) error {
	if err := checkTmux(d.tmux); err != nil {
		return err
	}
	a, err := findAgent(d, r.name)
	if err != nil {
		return err
	}
	det := d.readDetail(a.RepoPath, a.ID)
	if err := answerable(a, det.Ask); err != nil {
		return err
	}
	open := a.Dialog()
	if r.dialog != open.ID() {
		if r.dialog == "" {
			return usageErr("the dialog's id is required: %s is at dialog %s (%s)", a.Name, open.ID(), a.Last)
		}
		return usageErr("dialog %s is not open: %s is at dialog %s now (%s); read it (hq read %s) before answering", r.dialog, a.Name, open.ID(), a.Last, a.Name)
	}
	if open.Whose == agent.OwnDialog {
		return answerOwn(env, d, a, r)
	}
	q, err := d.questions(a.RepoPath, a.ID)
	if err != nil || state.DialogID(q.Since) != open.ID() {
		return usageErr("%s's dialog changed meanwhile; read it again (hq read %s)", a.Name, a.Name)
	}
	decision, sent := q.Cancel(), map[string]string(nil)
	if !r.cancel {
		if decision, sent, err = q.Decision(r.answers); err != nil {
			return usageErr("%s not answered: %v", a.Name, err)
		}
	}
	if hook := templateAnswers(d, a, q); hook != "" {
		return usageErr("%s's dialog cannot be answered from hq: its sandbox's template answers AskUserQuestion by itself (its PermissionRequest hook %s gives a decision), so Claude would take no answer from hq; answer it at the agent (hq go %s), and recreate the sandbox from a template whose hooks leave AskUserQuestion alone (hq sandbox rm %s)", a.Name, hook, a.Name, a.Repo())
	}
	// The moment before the answer is left: what the agent does with it is
	// a change after it, for hq wait --since.
	left := lookedAt(d.now())
	if err := d.leaveAnswer(a.RepoPath, a.ID, q, decision); err != nil {
		if errors.Is(err, state.ErrAnswerWaiting) {
			return usageErr("%s not answered: another answer to its dialog is on its way", a.Name)
		}
		return envErr("cannot leave the answer for %s: %v", a.Name, err)
	}
	if !awaitTaken(d, a, q) {
		// Answered at the agent meanwhile, its hook gone with the dialog?
		if now, err := findAgent(d, a.Name); err == nil && now.Dialog().ID() != open.ID() {
			return usageErr("%s left the dialog before hq's answer reached it; nothing was answered from hq: it is %s", a.Name, now.State)
		}
		return usageErr("%s's dialog cannot be answered from hq: its hook no longer holds it; it is %s, answer it at the agent (hq go %s)", a.Name, a.State, a.Name)
	}
	now, got, err := awaitClosed(d, a, open)
	if err != nil {
		return err
	}
	if !r.cancel {
		// Left without the end of its tool, the dialog took no answer:
		// cancelled at the agent meanwhile, which no hook reports.
		if got == nil {
			return usageErr("%s left the dialog without taking hq's answer: it is %s; read it (hq read %s)", a.Name, now.State, a.Name)
		}
		if diff := answersDiffer(q, sent, got); diff != "" {
			return usageErr("%s got other answers than hq sent: %s; it is %s", a.Name, diff, now.State)
		}
	}
	if r.asJSON {
		return writeJSON(env.Stdout, answerOutput{State: now.State, NextSince: left})
	}
	how := "answered"
	if r.cancel {
		how = "cancelled"
	}
	fmt.Fprintf(env.Stdout, "%s: %s is %s\n", how, a.Name, now.State)
	return nil
}

// answerable refuses, with the reason, an agent whose dialog hq does not
// answer: the agent's own question dialog is, AskUserQuestion, which asks,
// and a dialog of Claude's own whose options all show, which takes keys
// and needs no hook; a permission prompt, an MCP elicitation and a list of
// Claude's own that scrolls stay the user's at the agent (spec Phase 2).
func answerable(a agent.Agent, ask *state.Ask) error {
	at := fmt.Sprintf("answer it at the agent (hq go %s)", a.Name)
	relaunch := fmt.Sprintf("relaunch it with hq sandbox restart %s, or kill it and start it again", a.Repo())
	dialog := a.Dialog()
	switch {
	case a.State == state.Ended:
		return usageErr("%s has ended: it is at no dialog", a.Name)
	case dialog.Scrolls():
		return scrollsErr(a)
	case dialog.Whose == agent.OwnDialog:
		return nil
	case !a.Answerable():
		return usageErr("%s was started by an older hq and cannot be answered from hq; %s", a.Name, relaunch)
	case a.State == state.Question:
		return usageErr("%s asks in its reply, not in a dialog: answer it with hq send %s TEXT", a.Name, a.Name)
	case !dialog.Open():
		return usageErr("%s is at no dialog: it is %s", a.Name, a.State)
	case dialog.Whose == agent.SubagentDialog:
		return usageErr("%s's dialog is one of its subagents' (%s), which hq does not answer; %s", a.Name, a.Last, at)
	case ask != nil && ask.Server != "":
		return usageErr("%s is at an MCP server's form (%s: %s), which hq does not answer; %s", a.Name, ask.Server, a.Last, at)
	case ask == nil || ask.Tool == "":
		return usageErr("%s is at a dialog hq does not answer (%s), such as an MCP elicitation; %s", a.Name, a.Last, at)
	case ask.Tool != state.AskTool || len(ask.Questions) == 0:
		return usageErr("%s is at a permission prompt (%s); permission prompts are answered at the agent (hq go %s)", a.Name, a.Last, a.Name)
	}
	return nil
}

// managedSettings prints the managed settings of the sandbox, its
// template's: managed-settings.json, then the files of managed-settings.d,
// as Claude reads them. SBX_STUB_ROOT, unset in a sandbox, is the stub
// sbx's stand-in for the sandbox's root, whose exec runs on the host.
const managedSettings = `cd "${SBX_STUB_ROOT:-}/etc/claude-code" 2>/dev/null || exit 0
for f in managed-settings.json managed-settings.d/*.json; do [ -f "$f" ] && cat "$f" && echo; done
exit 0`

// runHook runs a hook, the arguments after the payload, as Claude runs it:
// the payload on its standard input, for at most 10 s.
const runHook = `p=$1; shift; command -v timeout >/dev/null && set -- timeout 10 "$@"; printf '%s' "$p" | "$@"`

// templateAnswers returns the PermissionRequest hook of the sandbox's
// template that answers the question dialog q by itself, run once with the
// dialog's payload as Claude ran it; "" when none does, or hq cannot look.
// Claude then takes no answer from hq's hook (ADR 0013, #128).
func templateAnswers(d deps, a agent.Agent, q state.Questions) string {
	settings, err := d.sbx.Output(a.Sandbox, "sh", "-c", managedSettings)
	if err != nil {
		return ""
	}
	for _, h := range state.AskHooks(settings) {
		out, err := d.sbx.Output(a.Sandbox, append([]string{"sh", "-c", runHook, "sh", string(q.Payload())}, h.Argv()...)...)
		if err == nil && state.Decides(out) {
			return h.Command
		}
	}
	return ""
}

// awaitTaken waits up to takeWait for the agent's hook to take the answer
// to q, and takes the answer back when it has not; it reports whether the
// hook took it.
func awaitTaken(d deps, a agent.Agent, q state.Questions) bool {
	for deadline := d.now().Add(takeWait); !d.answerTaken(a.RepoPath, a.ID, q); d.sleep(answerPoll) {
		if !d.now().Before(deadline) {
			// The hook may take it as hq does: then it has it after all.
			return !d.withdrawAnswer(a.RepoPath, a.ID, q)
		}
	}
	return true
}

// awaitClosed waits up to closeWait for the agent to leave the dialog
// open, and returns the agent then, with what Claude reports it got when
// it saw the dialog's tool end (nil when Claude closed the dialog
// otherwise, or the agent moved on before hq looked).
func awaitClosed(d deps, a agent.Agent, open agent.Dialog) (agent.Agent, map[string]string, error) {
	for deadline := d.now().Add(closeWait); ; d.sleep(answerPoll) {
		now, err := findAgent(d, a.Name)
		if err != nil {
			return now, nil, err
		}
		if now.Dialog().ID() != open.ID() {
			// Read after the look at the agent: the tool's end that moved it
			// off the dialog is then there to read, not yet to come.
			got, ok := d.answersGot(a.RepoPath, a.ID)
			if !ok {
				got = nil
			}
			return now, got, nil
		}
		if !d.now().Before(deadline) {
			return now, nil, envErr("%s took the answer but did not leave its dialog within %s: it is %s; its sandbox's template may have a PermissionRequest hook that answers AskUserQuestion by itself (recreate the sandbox from a template whose hooks leave AskUserQuestion alone), or answer it at the agent (hq go %s)", a.Name, closeWait, now.State, a.Name)
		}
	}
}

// answersDiffer says, question by question in the dialog's order, where
// what Claude reports it got differs from what was sent; empty when they
// agree. Both are the strings Claude reports, several labels joined with
// ", ", so they are compared as such (#89).
func answersDiffer(q state.Questions, sent, got map[string]string) string {
	var diffs []string
	keys := make([]string, 0, len(sent))
	for k := range sent {
		keys = append(keys, k)
	}
	order := map[string]int{}
	for i, qq := range q.Ask.Questions {
		order[qq.Question] = i
	}
	slices.SortFunc(keys, func(x, y string) int { return order[state.Clean(x)] - order[state.Clean(y)] })
	for _, k := range keys {
		if g, ok := got[k]; !ok || g != sent[k] {
			diffs = append(diffs, fmt.Sprintf("%s -> %q (sent %q)", state.Clean(k), state.Clean(g), state.Clean(sent[k])))
		}
	}
	return strings.Join(diffs, ", ")
}

// scrollsErr refuses the dialog of Claude's own a is at, whose list
// scrolls to options its screen does not show, as the picker of /model
// does: the numbers it shows are not the whole list's, and the cursor
// moves the list (#120).
func scrollsErr(a agent.Agent) error {
	return usageErr("%s is at a dialog of Claude's own whose list scrolls (%s), which hq does not answer; answer it at the agent (hq go %s)", a.Name, a.Last, a.Name)
}

// answerOwn picks an option of the dialog of Claude's own the agent is at,
// which fires no hook, with keys (ADR 0014): once a fresh look at its
// screen shows the same dialog, it moves the ❯ to the option one key at a
// time, each once Claude has drawn the last, presses Enter on it, and
// returns once the agent has left the dialog. Nothing is pressed for an
// answer that does not fit the dialog, nor for a screen that no longer
// shows it.
func answerOwn(env Env, d deps, a agent.Agent, r answerRequest) error {
	if r.cancel {
		return usageErr("%s not answered: a dialog of Claude's own is answered with one of its options, not cancelled", a.Name)
	}
	open := a.Dialog()
	v, shown, err := ownScreen(d, a, open)
	switch {
	case err != nil:
		return err
	case v.Ask == nil:
		// Answered at the agent meanwhile: the box is back.
		now, err := findAgent(d, a.Name)
		if err != nil {
			return err
		}
		return usageErr("%s left the dialog before hq's answer reached it; nothing was answered from hq: it is %s", a.Name, now.State)
	case !shown:
		return changedErr(a, v, "meanwhile", "")
	case v.Scrolls:
		return scrollsErr(a)
	}
	pick, err := v.Pick(r.answers)
	if err != nil {
		return usageErr("%s not answered: %v", a.Name, err)
	}
	// The moment before the first key: what the agent does after the
	// dialog is a change after it, for hq wait --since.
	left := lookedAt(d.now())
	if err := moveTo(d, a, open, v, pick); err != nil {
		return err
	}
	if err := d.tmux.Press(a.Pane, "Enter"); err != nil {
		return tmuxErr(err)
	}
	now, err := awaitLeft(d, a, open, pick)
	if err != nil {
		return err
	}
	if r.asJSON {
		return writeJSON(env.Stdout, answerOutput{State: now.State, NextSince: left})
	}
	fmt.Fprintf(env.Stdout, "answered: %s is %s\n", a.Name, now.State)
	return nil
}

// changedErr refuses the answer when the agent's screen shows another
// dialog than the one named, v, or none: the agent's state then, and the
// dialog in its place.
func changedErr(a agent.Agent, v state.OwnView, when, picked string) error {
	now := "it is at no dialog of Claude's own"
	if v.Ask != nil {
		now = "it needs input at another (" + v.Ask.Questions[0].Question + ")"
	}
	return usageErr("%s's dialog changed %s%s; %s, read it again (hq read %s)", a.Name, when, picked, now, a.Name)
}

// ownScreen looks at the agent's screen now: the dialog of Claude's own it
// shows (no Ask when it shows none), and whether that is the open one.
func ownScreen(d deps, a agent.Agent, open agent.Dialog) (state.OwnView, bool, error) {
	screens, err := d.tmux.Screens([]string{a.Pane})
	if err != nil {
		return state.OwnView{}, false, tmuxErr(err)
	}
	v, _ := state.ViewOwnDialog(screens[a.Pane])
	return v, open.Shows(v), nil
}

// moveTo moves the ❯ of the open dialog of Claude's own, which v shows,
// to option pick: one Down or Up at a time, the next once a fresh look
// shows the ❯ moved. The dialog changed, or the ❯ not moved within
// keyWait, nothing is picked.
func moveTo(d deps, a agent.Agent, open agent.Dialog, v state.OwnView, pick int) error {
	// Down or Up each move the ❯ one option on; more keys than options
	// would mean it never gets there.
	for moves := 0; v.Cursor != pick; moves++ {
		if moves == len(v.Ask.Questions[0].Options) {
			return envErr("%s's dialog did not move to option %d: nothing was picked; answer it at the agent (hq go %s)", a.Name, pick+1, a.Name)
		}
		key := "Down"
		if pick < v.Cursor {
			key = "Up"
		}
		if err := d.tmux.Press(a.Pane, key); err != nil {
			return tmuxErr(err)
		}
		from := v.Cursor
		for deadline := d.now().Add(keyWait); ; {
			d.sleep(answerPoll)
			var shown bool
			var err error
			if v, shown, err = ownScreen(d, a, open); err != nil {
				return err
			}
			if !shown {
				return changedErr(a, v, fmt.Sprintf("while hq moved to option %d", pick+1), ": nothing was picked")
			}
			if v.Cursor != from {
				break
			}
			if !d.now().Before(deadline) {
				return envErr("%s's dialog did not move to option %d within %s: nothing was picked; answer it at the agent (hq go %s)", a.Name, pick+1, keyWait, a.Name)
			}
		}
	}
	return nil
}

// awaitLeft waits up to closeWait, once Enter is pressed on option pick,
// for the agent to leave the open dialog of Claude's own: back as it was
// before it, or at the dialog's next step; and returns the agent then.
func awaitLeft(d deps, a agent.Agent, open agent.Dialog, pick int) (agent.Agent, error) {
	for deadline := d.now().Add(closeWait); ; d.sleep(answerPoll) {
		now, err := findAgent(d, a.Name)
		if err != nil || now.Dialog().ID() != open.ID() {
			return now, err
		}
		if !d.now().Before(deadline) {
			return now, envErr("%s did not leave its dialog within %s of picking option %d: it is %s; answer it at the agent (hq go %s)", a.Name, closeWait, pick+1, now.State, a.Name)
		}
	}
}
