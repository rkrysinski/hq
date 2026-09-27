package cli

import (
	"path/filepath"
	"strings"

	"github.com/rkrysinski/hq/internal/agent"
)

// How hq go shows an agent, from where it is run (design §3.11).
const (
	goAttach  = "attach"  // a plain terminal: attach to hq's session
	goSwitch  = "switch"  // a client of hq's tmux server: switch to the window
	goRefused = "refused" // a client of another tmux server: never nest
)

// goMode decides how to show a window, from the TMUX variable of the calling
// shell ("socket,pid,session") and the socket path of hq's server.
func goMode(tmuxEnv, hqSocket string) string {
	if tmuxEnv == "" {
		return goAttach
	}
	socket, _, _ := strings.Cut(tmuxEnv, ",")
	if hqSocket != "" && filepath.Clean(socket) == filepath.Clean(hqSocket) {
		return goSwitch
	}
	return goRefused
}

func runGo(_ Env, d deps, args []string) error {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		return usageErr("usage: hq go NAME")
	}
	name := args[0]
	if err := checkTmux(d.tmux); err != nil {
		return err
	}
	ws, err := d.tmux.Windows()
	if err != nil {
		return tmuxErr(err)
	}
	a, ok := agent.Find(agent.FromWindows(ws), name)
	if !ok {
		return notFoundErr("no agent '%s' (see hq ls)", name)
	}
	socket, err := d.tmux.SocketPath()
	if err != nil {
		return tmuxErr(err)
	}
	switch goMode(d.getenv("TMUX"), socket) {
	case goSwitch:
		err = d.tmux.Enter(a.Window)
	case goRefused:
		return usageErr("this shell is inside another tmux server; run hq go %s from a plain terminal or detach first", name)
	default:
		err = d.tmux.Attach(a.Window, d.terminal)
	}
	if err != nil {
		return tmuxErr(err)
	}
	return nil
}
