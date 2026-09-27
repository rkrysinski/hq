// Package cli is hq's command line: it parses the arguments, runs the command
// and turns its outcome into output and an exit code (spec §4).
package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

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
	run     func(env Env, args []string) error
}

func commands() []command {
	return []command{
		{"new", "NAME [DIR] [PROMPT]", "start an agent for the repository in DIR, with an optional first prompt", notYet("new")},
		{"ls", "[--json]", "list agents", notYet("ls")},
		{"go", "NAME", "enter that agent's session", notYet("go")},
		{"kill", "NAME [-y]", "end that agent's Claude session; the sandbox stays", notYet("kill")},
		{"stop", "[-y]", "end all agents; sandboxes stay", notYet("stop")},
		{"sandbox", "rm|restart REPO", "remove or restart a repository's sandbox", notYet("sandbox")},
		{"update", "", "replace hq with the latest release", notYet("update")},
		{"help", "", "show this help", runHelp},
	}
}

func notYet(name string) func(Env, []string) error {
	return func(Env, []string) error {
		return usageErr("'%s' is not available in this version (see hq help)", name)
	}
}

// Main runs hq with args (without the program name) and returns the exit code.
func Main(args []string, env Env) int {
	err := run(env, args)
	if err == nil {
		return ExitOK
	}
	var e *Error
	if !errors.As(err, &e) {
		e = &Error{Code: ExitUsage, Msg: err.Error()}
	}
	fmt.Fprintf(env.Stderr, "hq: %s\n", e.Msg)
	return e.Code
}

func run(env Env, args []string) error {
	if len(args) == 0 {
		// The dashboard arrives with milestone M3; until then, hq alone shows help.
		return runHelp(env, nil)
	}
	name, rest := args[0], args[1:]
	switch name {
	case "--version", "-V":
		fmt.Fprintf(env.Stdout, "hq %s\n", version.Version)
		return nil
	case "--help", "-h":
		return runHelp(env, nil)
	}
	for _, c := range commands() {
		if c.name == name {
			return c.run(env, rest)
		}
	}
	return usageErr("unknown command '%s' (see hq help)", name)
}

func runHelp(env Env, _ []string) error {
	var b strings.Builder
	b.WriteString("hq - one console for many Claude Code agents\n\nUsage:\n")
	for _, c := range commands() {
		line := strings.TrimSpace("hq " + c.name + " " + c.usage)
		fmt.Fprintf(&b, "  %-32s %s\n", line, c.summary)
	}
	fmt.Fprintf(&b, "  %-32s %s\n", "hq --version", "print the version")
	b.WriteString("\nExit codes: 0 success, 1 usage or refused, 2 not found, 3 environment.\n")
	_, err := io.WriteString(env.Stdout, b.String())
	return err
}
