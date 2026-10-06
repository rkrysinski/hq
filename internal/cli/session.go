package cli

import (
	"io"
	"strings"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/state"
)

// sessionCommand is the hidden command an agent's pane runs its session
// with, sbx run and its arguments (design §3.3).
const sessionCommand = "__session"

// exitNotStarted is the exit status of a session that could not start, as a
// shell gives a command it cannot run.
const exitNotStarted = 127

// runSession runs an agent's session on its pane's terminal and, when it
// ends, shows its latest output on the screen again where the session left
// the screen blank: sbx run resets the terminal when the sandbox stops, and
// an ended agent's session stays readable (S7). It exits with the session's
// status, so tmux shows it on the dead pane.
func runSession(env Env, d deps, args []string) error {
	if len(args) == 0 {
		return usageErr("usage: hq %s COMMAND [ARGUMENT...]", sessionCommand)
	}
	argv, err := settingsArgs(args, d.notify)
	if err != nil {
		return err
	}
	code, err := d.foreground(argv)
	if err != nil {
		return &Error{Code: exitNotStarted, Msg: err.Error()}
	}
	if pane := d.getenv("TMUX_PANE"); pane != "" {
		if s, err := d.tmux.LastScreen(pane); err == nil {
			_, _ = io.WriteString(env.Stdout, s)
		}
	}
	if code != ExitOK {
		return &Error{Code: code}
	}
	return nil
}

// settingsRef starts the argument that stands for an agent's settings in its
// window's command: settingsRef NAME:ID.
const settingsRef = "hq-settings:"

// settingsArgs is the command line args with the settings it refers to
// written out (claudeArgs): only the argument after the first --settings is
// a reference, so a prompt that reads like one stays as typed. notify is the
// platform's notification sequence, which this hq knows as well as the one
// that made the window.
func settingsArgs(args []string, notify string) ([]string, error) {
	for i, a := range args[:len(args)-1] {
		if a != "--settings" {
			continue
		}
		ref, ok := strings.CutPrefix(args[i+1], settingsRef)
		if !ok {
			return args, nil
		}
		name, id, ok := strings.Cut(ref, ":")
		if !ok || !agent.NameChars(name) || id == "" {
			return nil, usageErr("invalid settings reference '%s'", args[i+1])
		}
		out := append([]string{}, args...)
		out[i+1] = state.Settings(name, id, notify)
		return out, nil
	}
	return args, nil
}
