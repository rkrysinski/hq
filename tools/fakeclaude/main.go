// Command fakeclaude stands in for Claude Code behind the stub sbx in
// integration and end-to-end tests (design §7.2). It reads the --settings
// hq passes, and fires the injected hooks with payloads shaped like Claude's
// own on each turn:
//
//   - the first prompt (the last argument) and every line typed are turns:
//     UserPromptSubmit, then Stop with the reply;
//   - a prompt containing "question" gets a reply ending in "?";
//   - a prompt containing "input" first fires a permission Notification and
//     waits for a line (the answer);
//   - a prompt "worktree BRANCH" makes a worktree on a new BRANCH under
//     .claude/worktrees and moves into it, as Claude does;
//   - "/exit", SIGTERM or SIGHUP fire SessionEnd and exit.
//
// FAKE_CLAUDE_DELAY (a Go duration, default 200ms) is how long it takes to
// start and how long a turn works.
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
	c := claude{delay: 200 * time.Millisecond, session: fmt.Sprintf("fake-%d", os.Getpid())}
	c.cwd, _ = os.Getwd()
	if d, err := time.ParseDuration(os.Getenv("FAKE_CLAUDE_DELAY")); err == nil {
		c.delay = d
	}
	var prompt string
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
		os.Exit(0)
	}()

	time.Sleep(c.delay)
	fmt.Println("fake claude: ready")
	in := bufio.NewScanner(os.Stdin)
	if prompt != "" {
		c.turn(prompt, in)
	}
	for in.Scan() {
		line := strings.TrimSpace(in.Text())
		switch line {
		case "":
		case "/exit":
			c.fire("SessionEnd", map[string]any{"reason": "prompt_input_exit"})
			return
		default:
			c.turn(line, in)
		}
	}
}

func (c *claude) turn(prompt string, in *bufio.Scanner) {
	c.fire("UserPromptSubmit", map[string]any{"prompt": prompt})
	fmt.Println("> " + prompt)
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
		c.fire("Notification", map[string]any{"message": "Claude needs your permission", "notification_type": "permission_prompt"})
		fmt.Println("fake claude: waiting for your answer")
		in.Scan()
		time.Sleep(c.delay)
	}
	reply := "Done: " + prompt
	if strings.Contains(prompt, "question") {
		reply = "Shall I go on?"
	}
	fmt.Println("● " + reply)
	c.fire("Stop", map[string]any{"stop_hook_active": false, "last_assistant_message": reply})
}

// fire runs the hooks registered for event with a Claude-like payload.
func (c *claude) fire(event string, fields map[string]any) {
	p := map[string]any{"session_id": c.session, "cwd": c.cwd, "permission_mode": "bypassPermissions", "hook_event_name": event}
	for k, v := range fields {
		p[k] = v
	}
	payload, _ := json.Marshal(p)
	kind, _ := fields["notification_type"].(string)
	for _, m := range c.s.Hooks[event] {
		if m.Matcher != "" {
			if ok, _ := regexp.MatchString("^("+m.Matcher+")$", kind); !ok {
				continue
			}
		}
		for _, h := range m.Hooks {
			cmd := exec.Command(h.Command, h.Args...)
			cmd.Env, cmd.Dir, cmd.Stdin = c.env, c.cwd, bytes.NewReader(payload)
			_ = cmd.Run()
		}
	}
}
