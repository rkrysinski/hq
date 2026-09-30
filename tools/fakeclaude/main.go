// Command fakeclaude stands in for Claude Code behind the stub sbx in
// integration and end-to-end tests (design §7.2). It reads the --settings
// hq passes, and fires the injected hooks with payloads shaped like Claude's
// own on each turn:
//
//   - once started it fires SessionStart (source startup, or resume with
//     --resume), before any prompt, as Claude Code 2.1.283 does;
//   - the first prompt (the last argument) and every line typed are turns:
//     UserPromptSubmit, then Stop with the reply;
//   - a prompt containing "question" gets a reply ending in "?";
//   - a prompt containing "lines" gets a reply of two paragraphs;
//   - a prompt containing "input" first opens a question dialog, as
//     AskUserQuestion does: PermissionRequest at once, Claude's late
//     permission Notification after the delay, then it waits for a line.
//     "esc" (or a line with ESC in it) cancels the dialog, which ends the
//     turn with no hook, as Claude Code 2.1.283 does; any other line answers
//     it: PostToolUse, then the turn goes on;
//   - a prompt containing "slow" works until a line comes: "esc" (or a line
//     with ESC in it) interrupts the turn, which ends it with no hook, as
//     Claude Code 2.1.283 does; any other line lets it finish;
//   - a prompt "worktree BRANCH" makes a worktree on a new BRANCH under
//     .claude/worktrees and moves into it, as Claude does;
//   - a prompt containing "tool" runs a tool: PostToolUse after the delay;
//   - the line "draft" is no prompt: it draws the box holding "a draft", as
//     when the user has typed that and not sent it yet;
//   - "/exit", SIGTERM or SIGHUP fire SessionEnd and exit.
//
// It reads the hooks' output as Claude Code 2.1.283 does (ADR 0012):
// additionalContext of UserPromptSubmit or PostToolUse is noted in the
// turn's reply, and a Stop hook that blocks the stop gets its reason
// answered in a further reply, then Stop again with stop_hook_active.
// A finished turn draws the empty prompt box.
//
// Like Claude Code 2.1.283 it draws in the terminal's alternate screen: it
// leaves it on /exit, and clears it first when it is terminated, as when
// its sandbox stops (#38). It turns on bracketed paste, and takes a pasted
// text of several lines, ended with Enter, as one prompt.
//
// FAKE_CLAUDE_DELAY (a Go duration, default 200ms) is how long it takes to
// start and how long a turn works.
//
// With FAKE_CLAUDE_START_DIALOG set it shows, once started and before it
// takes any prompt, the dialog Claude Code 2.1.285 shows on the first start
// in a sandbox ("Make auto mode your default permission mode?"), which no
// hook reports, until Enter answers it (#29).
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// The terminal sequences Claude draws with.
const (
	enterAltScreen = "\x1b[?1049h"
	leaveAltScreen = "\x1b[?1049l"
	clearScreen    = "\x1b[H\x1b[2J"
	bracketedPaste = "\x1b[?2004h"
	pasteStart     = "\x1b[200~"
	pasteEnd       = "\x1b[201~"
)

// pasted is the line just scanned, or, when a bracketed paste starts in it,
// the pasted text up to the line the paste ends in, its lines kept.
func pasted(in *bufio.Scanner) string {
	line := in.Text()
	if !strings.Contains(line, pasteStart) {
		return line
	}
	lines := []string{line}
	for !strings.Contains(line, pasteEnd) && in.Scan() {
		line = in.Text()
		lines = append(lines, line)
	}
	return strings.NewReplacer(pasteStart, "", pasteEnd, "").Replace(strings.Join(lines, "\n"))
}

type settings struct {
	Env   map[string]string
	Hooks map[string][]struct {
		Matcher string
		Hooks   []struct {
			Command string
			Args    []string
		}
	}
}

type claude struct {
	s       settings
	env     []string
	session string
	cwd     string
	delay   time.Duration
}

func main() {
	c := claude{delay: 200 * time.Millisecond, session: fmt.Sprintf("fa4ec1a0-0000-4000-8000-%012x", os.Getpid())}
	c.cwd, _ = os.Getwd()
	if d, err := time.ParseDuration(os.Getenv("FAKE_CLAUDE_DELAY")); err == nil {
		c.delay = d
	}
	var prompt string
	source := "startup"
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--settings":
			i++
			if err := json.Unmarshal([]byte(args[i]), &c.s); err != nil {
				fmt.Println("fake claude: bad --settings:", err)
			}
		case "--resume":
			i++
			c.session = args[i]
			source = "resume"
			fmt.Println("fake claude: resumed", c.session)
		default:
			prompt = args[i]
		}
	}
	c.env = append(os.Environ(), "CLAUDE_PROJECT_DIR="+c.cwd)
	for k, v := range c.s.Env {
		c.env = append(c.env, k+"="+v)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT)
	go func() {
		<-sig
		c.fire("SessionEnd", map[string]any{"reason": "other"})
		fmt.Print(clearScreen + leaveAltScreen)
		os.Exit(0)
	}()

	time.Sleep(c.delay)
	c.fire("SessionStart", map[string]any{"source": source})
	fmt.Print(enterAltScreen + bracketedPaste)
	fmt.Println("fake claude: ready")
	in := bufio.NewScanner(os.Stdin)
	if os.Getenv("FAKE_CLAUDE_START_DIALOG") != "" {
		fmt.Print(startDialog)
		in.Scan()
		promptBox("")
	}
	if prompt != "" {
		c.turn(prompt, in)
	}
	for in.Scan() {
		line := strings.TrimSpace(pasted(in))
		switch line {
		case "":
		case "draft":
			// The user has typed something in the box and not sent it:
			// Claude draws the box holding it; no hook fires.
			promptBox("", "a draft")
		case "/exit":
			c.fire("SessionEnd", map[string]any{"reason": "prompt_input_exit"})
			fmt.Print(leaveAltScreen)
			return
		default:
			c.turn(line, in)
		}
	}
}

func (c *claude) turn(prompt string, in *bufio.Scanner) {
	var noted []string
	if ctx := c.fire("UserPromptSubmit", map[string]any{"prompt": prompt}).context(); ctx != "" {
		noted = append(noted, ctx)
	}
	fmt.Println("❯ " + strings.ReplaceAll(prompt, "\n", "\n  "))
	time.Sleep(c.delay)
	if b, ok := strings.CutPrefix(prompt, "worktree "); ok {
		dir := filepath.Join(c.cwd, ".claude", "worktrees", strings.ReplaceAll(b, "/", "-"))
		if out, err := exec.Command("git", "-C", c.cwd, "worktree", "add", "-q", "-b", b, dir).CombinedOutput(); err != nil {
			fmt.Printf("fake claude: worktree: %v %s\n", err, out)
		} else {
			c.cwd = dir
		}
	}
	if strings.Contains(prompt, "input") {
		ask := map[string]any{"questions": []map[string]any{{"question": question, "header": "Colour", "multiSelect": false,
			"options": []map[string]string{{"label": "Red", "description": "Pick red."}, {"label": "Blue", "description": "Pick blue."}}}}}
		c.fire("PermissionRequest", map[string]any{"tool_name": "AskUserQuestion", "tool_input": ask})
		fmt.Println(" ☐ Colour\n" + question + "\n❯ 1. Red\n  2. Blue\nEnter to select · ↑/↓ to navigate · Esc to cancel")
		time.Sleep(c.delay)
		c.fire("Notification", map[string]any{"message": "Claude needs your permission", "notification_type": "permission_prompt"})
		in.Scan()
		if answer := in.Text(); strings.TrimSpace(answer) == "esc" || strings.Contains(answer, "\x1b") {
			fmt.Println("●\u00a0User declined to answer questions\n  ⎿  · " + question + " (Red / Blue)")
			promptBox("")
			return
		}
		if ctx := c.fire("PostToolUse", map[string]any{"tool_name": "AskUserQuestion", "tool_input": ask, "tool_use_id": "toolu_fake"}).context(); ctx != "" {
			noted = append(noted, ctx)
		}
		time.Sleep(c.delay)
	}
	if strings.Contains(prompt, "slow") {
		fmt.Println("✻ Working…")
		promptBox("esc to interrupt")
		in.Scan()
		line := in.Text()
		if strings.TrimSpace(line) == "esc" || strings.Contains(line, "\x1b") {
			fmt.Println("  ⎿  Interrupted · What should Claude do instead?")
			promptBox("")
			return
		}
		if strings.TrimSpace(line) == "rewind" {
			// An early Esc that rewinds the turn: no line, no hook, the
			// prompt back in the box (Claude Code 2.1.283, #99).
			fmt.Print(clearScreen)
			promptBox("", prompt)
			return
		}
	}
	if strings.Contains(prompt, "tool") {
		fmt.Println("● Bash(echo tool)")
		tool := map[string]any{"command": "echo tool"}
		if ctx := c.fire("PostToolUse", map[string]any{"tool_name": "Bash", "tool_input": tool, "tool_use_id": "toolu_fake"}).context(); ctx != "" {
			fmt.Println("  ⎿  " + ctx)
			noted = append(noted, ctx)
		}
		time.Sleep(c.delay)
	}
	reply := "Done: " + prompt
	if strings.Contains(prompt, "question") {
		reply = "Shall I go on?"
	}
	if strings.Contains(prompt, "lines") {
		reply = "Done on several lines.\n\n" + reply
	}
	for _, n := range noted {
		reply += " | " + n
	}
	for active := false; ; active = true {
		fmt.Println("● " + reply)
		out := c.fire("Stop", map[string]any{"stop_hook_active": active, "last_assistant_message": reply})
		if out.Decision != "block" {
			break
		}
		fmt.Println("● Ran 1 stop hook\n  ⎿  Stop hook error: " + out.Reason)
		time.Sleep(c.delay)
		reply = "Answered: " + out.Reason
	}
	promptBox("")
}

// question is what the fake's question dialog asks.
const question = "Which colour do you pick?"

// promptBox draws Claude's prompt box, where the user types the next
// prompt, holding input when given; hint is what its footer offers besides,
// as "esc to interrupt" while a turn is at work.
// startDialog is the dialog of Claude Code 2.1.285 as it draws it.
const startDialog = `
────────────────────────────────────────────────────────────────
 Make auto mode your default permission mode?

   Auto mode lets Claude handle permission prompts automatically. Claude checks each tool call for risky
   actions and prompt injection before executing, runs the ones it assesses as lower-risk, and blocks the
   rest.

   ❯ Yes, set auto mode as my default permission mode
     No, keep bypass permissions
`

func promptBox(hint string, input ...string) {
	rule := strings.Repeat("─", 40)
	footer := "  ⏵⏵ bypass permissions on (shift+tab to cycle)"
	if hint != "" {
		footer += " · " + hint
	}
	fmt.Println(rule + "\n❯ " + strings.Join(input, " ") + "\n" + rule + "\n" + footer)
}

// output is what the hooks of an event told Claude, as far as the fake
// heeds it.
type output struct {
	Decision           string
	Reason             string
	HookSpecificOutput struct{ AdditionalContext string }
}

func (o output) context() string { return o.HookSpecificOutput.AdditionalContext }

// fire runs the hooks registered for event with a Claude-like payload, and
// returns what they told Claude on their standard output.
func (c *claude) fire(event string, fields map[string]any) (o output) {
	p := map[string]any{"session_id": c.session, "cwd": c.cwd, "permission_mode": "bypassPermissions", "hook_event_name": event}
	for k, v := range fields {
		p[k] = v
	}
	payload, _ := json.Marshal(p)
	kind, _ := fields["notification_type"].(string)
	if source, ok := fields["source"].(string); ok {
		kind = source
	}
	for _, m := range c.s.Hooks[event] {
		if m.Matcher != "" {
			if ok, _ := regexp.MatchString("^("+m.Matcher+")$", kind); !ok {
				continue
			}
		}
		for _, h := range m.Hooks {
			cmd := exec.Command(h.Command, h.Args...)
			cmd.Env, cmd.Dir, cmd.Stdin = c.env, c.cwd, bytes.NewReader(payload)
			out, _ := cmd.Output()
			var got output
			if json.Unmarshal(out, &got) == nil {
				if got.Decision != "" {
					o.Decision, o.Reason = got.Decision, got.Reason
				}
				if got.context() != "" {
					o.HookSpecificOutput = got.HookSpecificOutput
				}
			}
		}
	}
	return o
}
