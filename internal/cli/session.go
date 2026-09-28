package cli

import "io"

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
	code, err := d.foreground(args)
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
