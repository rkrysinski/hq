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
	name  string
	usage string // arguments, as shown by hq help
	// flags are every spelling of every flag it takes, the only ones its
	// parser recognises (flagSet.is); hq help names each of them
	// (TestHelpNamesEveryDeclaredFlag).
	flags   []string
	summary string
	run     func(env Env, d deps, args []string) error
}

func commands() []command {
	return []command{
		{"dash", "", nil, "open the dashboard (also: hq alone)", runDash},
		{"new", "NAME [DIR] [PROMPT]", []string{"--setup"}, "start an agent for the repository in DIR, with an optional first prompt", runNew},
		{"ls", "[--json]", []string{"--json"}, "list agents", runLs},
		{"read", "NAME [--offset N --since SINCE] [--json]", []string{"--offset", "--since", "--json"}, "show that agent in full: its last reply (a long one in pages) and what it asks", runRead},
		{"explain", "NAME [--all] [--json]", []string{"--all", "--json"}, "show the events that agent's state rests on, with no text of its conversation", runExplain},
		{"wait", "[NAME...] [--json]", []string{"--since", "--timeout", "--json"}, "wait until an agent is done, asks, needs input or ends", runWait},
		{"go", "NAME", nil, "enter that agent's session", runGo},
		{"code", "NAME", nil, "open VS Code on that agent's worktree", runCode},
		{"send", "NAME TEXT [--now] [--wait [--timeout DURATION]] [--json]", []string{"--now", "--wait", "--timeout", "--json"}, "leave a message for that agent, delivered when it is ready; --wait returns when the turn that took it ends", runSend},
		{"answer", "NAME --dialog ID ANSWER...|--cancel [--json]", []string{"--dialog", "--cancel", "--json"}, "answer that agent's question dialog, or cancel it; or pick an option of a dialog of Claude's own", runAnswer},
		{"kill", "NAME [-y]", []string{"-y", "--yes"}, "end that agent's Claude session; the sandbox stays", runKill},
		{"stop", "[-y]", []string{"-y", "--yes"}, "end all agents; sandboxes stay", runStop},
		{"killed", "[NAME]", nil, "show the agents hq killed: when, and who asked for it", runKilled},
		{"sandbox", "rm|restart REPO|SANDBOX [-y]", []string{"--setup", "--all-setups", "-y", "--yes"}, "remove or restart a repository's sandbox", runSandbox},
		{"restore", "[REPO]", nil, "relaunch the agents tmux lost (a host restart), continuing their conversations", runRestore},
		{"mcp", "[install] [claude-desktop|claude-code|sbx] | load|unload REPO", []string{"--setup", "--all-setups"}, "serve these commands as tools to Claude Desktop, Claude Code or sbx; install adds hq to it; load gives a sandbox's agents hq's tools", runMCP},
		{"update", "", nil, "replace hq with the latest release", runUpdate},
		{"help", "", []string{"--help", "-h"}, "show this help (also: hq --help or hq -h)", runHelp},
	}
}

// Main runs hq with args (without the program name) and returns the exit code.
func Main(args []string, env Env) int {
	return mainWith(args, env, defaultDeps())
}

func mainWith(args []string, env Env, d deps) int {
	err := run(env, d, args)
	defer afterCommand(env, d, args)
	if err == nil {
		return ExitOK
	}
	e := asError(err)
	if e.Msg != "" {
		fmt.Fprintf(env.Stderr, "hq: %s\n", e.Msg)
	}
	return e.Code
}

// asError is err as hq reports it: an error that is not an *Error is a
// usage error with its text.
func asError(err error) *Error {
	var e *Error
	if !errors.As(err, &e) {
		e = &Error{Code: ExitUsage, Msg: err.Error()}
	}
	return e
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
	case updateCheckCommand:
		return runUpdateCheck(env, d, rest)
	case mayUpdateCommand:
		return runMayUpdate(env, d, rest)
	case generationCommand:
		return runGeneration(env, d, rest)
	}
	if v := versionCommand(); v.flagSet().is(name, "--version", "-V") {
		return v.run(env, d, rest)
	}
	if flagsOf("help").is(name, "--help", "-h") {
		return runHelp(env, d, nil)
	}
	for _, c := range commands() {
		if c.name == name {
			return c.run(env, d, rest)
		}
	}
	return usageErr("unknown command '%s' (see hq help)", name)
}

// versionCommand is hq --version, which takes the place of a command: not
// one of commands(), but declared as one, its flags included, and listed
// after them by hq help.
func versionCommand() command {
	return command{name: "--version", flags: []string{"--version", "-V"}, summary: "print the version (also: hq -V)", run: runVersion}
}

func runVersion(env Env, d deps, _ []string) error {
	fmt.Fprintf(env.Stdout, "hq %s\n%s", version.Version, updateHint(d))
	return nil
}

func runHelp(env Env, _ deps, _ []string) error {
	var b strings.Builder
	b.WriteString("hq - one console for many Claude Code agents\n\nUsage:\n")
	for _, c := range append(commands(), versionCommand()) {
		line := strings.TrimSpace("hq " + c.name + " " + c.usage)
		fmt.Fprintf(&b, "  %-32s %s\n", line, c.summary)
	}
	fmt.Fprintf(&b, "\nNAME: letters, digits, '-', '_' and '.', at least one letter or digit, at most %d characters; taken until that agent is killed.\n", agent.MaxName)
	b.WriteString("hq new also takes --setup NAME (or --setup=NAME): the agent runs in the repository's sandbox of that setup, from \"sandboxes\" in preferences.json (default: \"sandbox\").\n")
	b.WriteString("hq sandbox also takes --setup NAME (the repository's sandbox of that setup, instead of its default) or --all-setups (every sandbox of the repository).\n")
	b.WriteString("hq mcp load and unload take REPO and those flags (--setup NAME, --all-setups) as hq sandbox does; hq sandbox restart loads hq again into a sandbox it was loaded into, and takes it away after an unload (sbx has no unload).\n")
	b.WriteString("hq kill, hq stop and hq sandbox take --yes as well as -y, which skips their confirmation.\n")
	fmt.Fprintf(&b, "hq wait also takes --since SINCE and --timeout DURATION (default %s; 0 waits with no limit).\n", DefaultWaitTimeout)
	b.WriteString("hq send --wait takes --timeout DURATION too, as hq wait does, counted from the start of the call.\n")
	b.WriteString("SINCE: the next_since hq printed last, passed back as given, which misses no change and repeats none; or a moment (2026-09-28T10:25:01Z) or a duration back from now (10m), both approximate.\n")
	b.WriteString("hq answer takes one ANSWER per question, in the dialog's order: an option's number, numbers joined with commas where several may be picked, or text; a dialog of Claude's own (as /effort opens) takes one option's number; ID is the dialog's, as hq read shows it.\n")
	b.WriteString(exitCodesLine())
	_, err := io.WriteString(env.Stdout, b.String())
	return err
}
