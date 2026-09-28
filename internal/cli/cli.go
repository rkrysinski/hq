// Package cli is hq's command line: it parses the arguments, runs the command
// and turns its outcome into output and an exit code (spec §4).
package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/version"
)

// Env is what a command reads from and writes to.
type Env struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// command is one hq subcommand.
type command struct {
	name    string
	usage   string // arguments, as shown by hq help
	summary string
	run     func(env Env, d deps, args []string) error
}

func commands() []command {
	return []command{
		{"dash", "", "open the dashboard (also: hq alone)", runDash},
		{"new", "NAME [DIR] [PROMPT]", "start an agent for the repository in DIR, with an optional first prompt", runNew},
		{"ls", "[--json]", "list agents", runLs},
		{"wait", "[NAME...] [--json]", "wait until an agent is done, asks, needs input or ends", runWait},
		{"go", "NAME", "enter that agent's session", runGo},
		{"code", "NAME", "open VS Code on that agent's worktree", runCode},
		{"send", "NAME TEXT [--now]", "leave a message for that agent, delivered when it is ready", runSend},
		{"kill", "NAME [-y]", "end that agent's Claude session; the sandbox stays", runKill},
		{"stop", "[-y]", "end all agents; sandboxes stay", runStop},
		{"sandbox", "rm|restart REPO|SANDBOX [-y]", "remove or restart a repository's sandbox", runSandbox},
		{"update", "", "replace hq with the latest release", runUpdate},
		{"help", "", "show this help", runHelp},
	}
}

// Main runs hq with args (without the program name) and returns the exit code.
func Main(args []string, env Env) int {
	return mainWith(args, env, defaultDeps())
}

func mainWith(args []string, env Env, d deps) int {
	err := run(env, d, args)
	if err == nil {
		return ExitOK
	}
	var e *Error
	if !errors.As(err, &e) {
		e = &Error{Code: ExitUsage, Msg: err.Error()}
	}
	if e.Msg != "" {
		fmt.Fprintf(env.Stderr, "hq: %s\n", e.Msg)
	}
	return e.Code
}

func run(env Env, d deps, args []string) error {
	if len(args) == 0 {
		return runDash(env, d, nil)
	}
	name, rest := args[0], args[1:]
	switch name {
	case listCommand:
		return runList(env, d, rest)
	case newDialogCommand:
		return runNewDialog(env, d, rest)
	case killDialogCommand:
		return runKillDialog(env, d, rest)
	case slotCommand:
		return runSlot(env, d, rest)
	case sessionCommand:
		return runSession(env, d, rest)
	case chordCommand:
		return runChord(env, d, rest)
	case itermProfileCommand:
		return runItermProfile(env, d, rest)
	case "--version", "-V":
		fmt.Fprintf(env.Stdout, "hq %s\n%s", version.Version, updateHint(d))
		return nil
	case "--help", "-h":
		return runHelp(env, d, nil)
	}
	for _, c := range commands() {
		if c.name == name {
			return c.run(env, d, rest)
		}
	}
	return usageErr("unknown command '%s' (see hq help)", name)
}

func runHelp(env Env, _ deps, _ []string) error {
	var b strings.Builder
	b.WriteString("hq - one console for many Claude Code agents\n\nUsage:\n")
	for _, c := range commands() {
		line := strings.TrimSpace("hq " + c.name + " " + c.usage)
		fmt.Fprintf(&b, "  %-32s %s\n", line, c.summary)
	}
	fmt.Fprintf(&b, "  %-32s %s\n", "hq --version", "print the version")
	fmt.Fprintf(&b, "\nNAME: letters, digits, - and _, at most %d characters; taken until that agent is killed.\n", agent.MaxName)
	fmt.Fprintf(&b, "hq wait also takes --since TIME (the moment it printed last, or 10m back) and --timeout DURATION (default %s; 0 waits with no limit).\n", DefaultWaitTimeout)
	b.WriteString("Exit codes: 0 success, 1 usage or refused, 2 not found, 3 environment.\n")
	_, err := io.WriteString(env.Stdout, b.String())
	return err
}
