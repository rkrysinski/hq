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

// askTool is Claude's tool that asks the user questions in a dialog.
const askTool = "AskUserQuestion"

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
// cancels it (spec §4.1, design §3.4, ADR 0013).
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
		return usageErr("%s's dialog cannot be answered from hq: its hook no longer holds it; answer it at the agent (hq go %s)", a.Name, a.Name)
	}
	now, got, err := awaitClosed(d, a, open)
	if err != nil {
		return err
	}
	if !r.cancel && got != nil {
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
// answer: only the agent's own question dialog is, AskUserQuestion, which
// asks; a permission prompt, an MCP elicitation and a dialog of Claude's
// own stay the user's at the agent (spec Phase 2).
func answerable(a agent.Agent, ask *state.Ask) error {
	at := fmt.Sprintf("answer it at the agent (hq go %s)", a.Name)
	relaunch := fmt.Sprintf("relaunch it with hq sandbox restart %s, or kill it and start it again", a.Repo())
	dialog := a.Dialog()
	switch {
	case a.State == state.Ended:
		return usageErr("%s has ended: it is at no dialog", a.Name)
	case !a.Answerable():
		return usageErr("%s was started by an older hq and cannot be answered from hq; %s", a.Name, relaunch)
	case a.State == state.Question:
		return usageErr("%s asks in its reply, not in a dialog: answer it with hq send %s TEXT", a.Name, a.Name)
	case !dialog.Open():
		return usageErr("%s is at no dialog: it is %s", a.Name, a.State)
	case dialog.Whose == agent.OwnDialog:
		return usageErr("%s is at a dialog of Claude's own (%s), which hq does not answer; %s", a.Name, a.Last, at)
	case dialog.Whose == agent.SubagentDialog:
		return usageErr("%s's dialog is one of its subagents' (%s), which hq does not answer; %s", a.Name, a.Last, at)
	case ask == nil || ask.Tool == "":
		return usageErr("%s is at a dialog hq does not answer (%s), such as an MCP elicitation; %s", a.Name, a.Last, at)
	case ask.Tool != askTool || len(ask.Questions) == 0:
		return usageErr("%s is at a permission prompt (%s); permission prompts are answered at the agent (hq go %s)", a.Name, a.Last, a.Name)
	}
	return nil
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
		got, ok := d.answersGot(a.RepoPath, a.ID)
		now, err := findAgent(d, a.Name)
		if err != nil {
			return now, nil, err
		}
		if ok || now.Dialog().ID() != open.ID() {
			if !ok {
				got = nil
			}
			return now, got, nil
		}
		if !d.now().Before(deadline) {
			return now, nil, envErr("%s took the answer but did not leave its dialog within %s: it is %s; its sandbox's template may have a claude-approve hook that still answers AskUserQuestion (recreate the sandbox from a newer image), or answer it at the agent (hq go %s)", a.Name, closeWait, now.State, a.Name)
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
