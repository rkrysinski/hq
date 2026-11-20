package cli

import (
	"fmt"
	"strings"

	"github.com/rkrysinski/hq/internal/agent"
)

// Exit codes (spec §4.2).
const (
	ExitOK          = 0
	ExitUsage       = 1 // usage error or refused (duplicate name, sandbox rm with running agents)
	ExitNotFound    = 2 // agent, repository or sandbox not found
	ExitEnvironment = 3 // tmux, sbx or another prerequisite unavailable
	ExitStalled     = 4 // hq send --wait: the agent did not take the message (#170)
)

// exitCode is an exit code with the words hq help gives it.
type exitCode struct {
	code  int
	words string
}

// exitCodes are the exit codes as hq help names them, one for every Exit*
// constant (TestHelpNamesEveryExitCode).
var exitCodes = []exitCode{
	{ExitOK, "success"},
	{ExitUsage, "usage or refused"},
	{ExitNotFound, "not found"},
	{ExitEnvironment, "environment"},
	{ExitStalled, "stalled (hq send --wait)"},
}

// exitCodesLine is hq help's line of exit codes.
func exitCodesLine() string {
	words := make([]string, len(exitCodes))
	for i, e := range exitCodes {
		words[i] = fmt.Sprintf("%d %s", e.code, e.words)
	}
	return "Exit codes: " + strings.Join(words, ", ") + ".\n"
}

// Error is a failure reported to the user as one line on stderr, prefixed
// "hq:", naming the remedy where there is one, with its exit code. Without a
// message it is only the exit code.
type Error struct {
	Code int
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func usageErr(format string, a ...any) error {
	return &Error{Code: ExitUsage, Msg: fmt.Sprintf(format, a...)}
}

func notFoundErr(format string, a ...any) error {
	return &Error{Code: ExitNotFound, Msg: fmt.Sprintf(format, a...)}
}

// outdatedErr refuses an outdated agent (agent.Agent.Outdated): every
// command but those that open or kill it.
func outdatedErr(a agent.Agent) error { return usageErr("%s", a.OutdatedErr()) }

func envErr(format string, a ...any) error {
	return &Error{Code: ExitEnvironment, Msg: fmt.Sprintf(format, a...)}
}
