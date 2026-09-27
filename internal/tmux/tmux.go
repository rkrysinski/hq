// Package tmux talks to the tmux server that holds hq's session. tmux is the
// agent registry (design §3.3): an agent exists while its home window exists,
// and what hq knows at launch is stored on that window as user options.
package tmux

import (
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/rkrysinski/hq/internal/proc"
)

// Session is the name of hq's tmux session.
const Session = "hq"

// MinVersion is the oldest tmux hq supports (design §3.2).
const MinVersion = "3.4"

// Window is one window of hq's session with the options hq keeps on it. For
// an agent's home window, the pane fields are the agent's own pane, wherever
// it is: in its home window, or docked in the dashboard (design §3.1).
type Window struct {
	ID       string // tmux window id, e.g. "@3"
	Name     string
	Pane     string // the agent's pane id
	PaneDead bool
	Docked   bool              // the agent's pane is in the docking slot
	Title    string            // the frame title on the agent's pane
	Options  map[string]string // @hq_* user options, without the "@hq_" prefix
}

// Client runs tmux commands against one server.
type Client struct {
	Run    proc.Runner
	Socket string // tmux -L socket name; empty is the default server
}

// OptionKeys are the user options hq stores on a home window: new marks an
// agent started with hq new (not relaunched), for the list's S2.
var OptionKeys = []string{"id", "name", "repo", "sandbox", "started", "ending", "new"}

func (c Client) tmux(args ...string) ([]byte, error) {
	if c.Socket != "" {
		args = append([]string{"-L", c.Socket}, args...)
	}
	return c.Run.Run("tmux", args...)
}

// Version returns the tmux version, e.g. "3.4" or "3.7c".
func (c Client) Version() (string, error) {
	out, err := c.Run.Run("tmux", "-V")
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(strings.TrimSpace(string(out)), "tmux "), nil
}

var versionRE = regexp.MustCompile(`(\d+)\.(\d+)`)

// AtLeast reports whether version (as printed by tmux -V) is min or newer.
// Development builds ("next-3.6", "master") count as new enough.
func AtLeast(version, min string) bool {
	if strings.HasPrefix(version, "master") {
		return true
	}
	v, m := versionRE.FindStringSubmatch(version), versionRE.FindStringSubmatch(min)
	if v == nil || m == nil {
		return false
	}
	vMaj, _ := strconv.Atoi(v[1])
	vMin, _ := strconv.Atoi(v[2])
	mMaj, _ := strconv.Atoi(m[1])
	mMin, _ := strconv.Atoi(m[2])
	return vMaj > mMaj || (vMaj == mMaj && vMin >= mMin)
}

func isNoServerOrSession(err error) bool {
	var pe *proc.Error
	if !errors.As(err, &pe) {
		return false
	}
	return strings.Contains(pe.Msg, "no server running") ||
		strings.Contains(pe.Msg, "can't find session") ||
		strings.Contains(pe.Msg, "error connecting to") ||
		strings.Contains(pe.Msg, "No such file or directory")
}

// sep separates fields of list-windows output. Printable on purpose: tmux 3.4
// escapes control characters in formats (\037), newer versions do not.
const sep = "::hq::"

// Windows lists the windows of hq's session. No server or no session is an
// empty list. A home window whose pane is the placeholder has its agent
// docked; the agent's pane is then the one carrying its id (see Dock).
func (c Client) Windows() ([]Window, error) {
	ps, err := c.panes()
	if err != nil {
		if isNoServerOrSession(err) {
			return nil, nil
		}
		return nil, err
	}
	byAgent := map[string]pane{}
	for _, p := range ps {
		if p.agent != "" {
			byAgent[p.agent] = p
		}
	}
	var ws []Window
	seen := map[string]bool{}
	for _, p := range ps {
		if seen[p.window] {
			continue
		}
		seen[p.window] = true
		w := Window{ID: p.window, Name: p.windowName, Pane: p.id, PaneDead: p.dead, Title: p.title, Options: p.options}
		if id := p.options["id"]; id != "" && p.role == roleSlot {
			w.Docked = true
			a, ok := byAgent[id]
			// A docked pane that is gone took the agent with it.
			w.Pane, w.PaneDead, w.Title = a.id, a.dead || !ok, a.title
		}
		ws = append(ws, w)
	}
	return ws, nil
}

// NewWindow creates a home window named name in hq's session running argv,
// with options stored on it. The program starts only after the options and
// remain-on-exit are set, so a program that exits at once still leaves its
// window, and its output, behind. Returns the window id.
func (c Client) NewWindow(name, dir string, options map[string]string, argv []string) (string, error) {
	// sh waits for one line on its terminal, then execs argv unchanged ("$@"):
	// no shell parsing of any argument.
	gate := append([]string{"sh", "-c", `read _ ; exec "$@"`, "sh"}, argv...)
	args := append([]string{"new-window", "-d", "-P", "-F", "#{window_id}", "-t", Session + ":", "-n", name, "-c", dir, "--"}, gate...)
	out, err := c.tmux(args...)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(out))
	set := agentPane(id, options["id"])
	for _, k := range OptionKeys {
		if v, ok := options[k]; ok {
			set = append(set, ";", "set-option", "-w", "-t", id, "@hq_"+k, v)
		}
	}
	if _, err := c.tmux(set...); err != nil {
		_ = c.KillWindow(id)
		return "", err
	}
	return id, nil
}

// SetOption stores one of OptionKeys on a window.
func (c Client) SetOption(id, key, value string) error {
	_, err := c.tmux("set-option", "-w", "-t", id, "@hq_"+key, value)
	return err
}

// Start releases the program of a window made by NewWindow.
func (c Client) Start(id string) error {
	_, err := c.tmux("send-keys", "-t", id, "Enter")
	return err
}

// agentPane marks target, an agent's pane, as the agent's own: its id, so it
// is found when docked, and what must travel with it when it moves between
// windows. It stays readable after its process ends (S7), and passthrough
// lets the agent's notifications (design §3.5) out of a window nobody looks
// at.
func agentPane(target, id string) []string {
	cmds := []string{"set-option", "-p", "-t", target, "remain-on-exit", "on",
		";", "set-option", "-p", "-t", target, "allow-passthrough", "all"}
	if id != "" {
		cmds = append(cmds, ";", "set-option", "-p", "-t", target, "@hq_agent", id)
	}
	return cmds
}

// KillWindow removes a window. An agent that is docked goes back to its home
// window first, so its own pane goes with the window and the slot gets the
// placeholder back (S6).
func (c Client) KillWindow(id string) error {
	if ps, err := c.panes(); err == nil {
		for _, p := range ps {
			if p.window == id && p.role == roleSlot && p.options["id"] != "" {
				name := p.options["name"]
				if name == "" {
					name = p.windowName
				}
				if err := c.undock(ps, KilledHint(name)); err != nil {
					return err
				}
				break
			}
		}
	}
	_, err := c.tmux("kill-window", "-t", id)
	return err
}

// SocketPath returns the path of the server's socket.
func (c Client) SocketPath() (string, error) {
	out, err := c.tmux("display-message", "-p", "#{socket_path}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Enter shows a window of hq's session to a client already inside this tmux
// server: select the window, then switch the client to hq's session.
func (c Client) Enter(id string) error {
	_, err := c.tmux("select-window", "-t", id, ";", "switch-client", "-t", Session)
	return err
}

// ShowAttached shows a window to the clients attached to hq's session and
// returns their terminals, none when no client is attached (design §3.11).
func (c Client) ShowAttached(id string) ([]string, error) {
	out, err := c.tmux("select-window", "-t", id, ";", "list-clients", "-t", Session, "-F", "#{client_tty}")
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(out)), nil
}

// Attach selects a window and attaches this terminal to hq's session,
// detaching any other client, until the user detaches (design §3.11).
func (c Client) Attach(id string, t Terminal) error {
	if _, err := c.tmux("select-window", "-t", id); err != nil {
		return err
	}
	args := []string{"attach-session", "-d", "-t", Session}
	if c.Socket != "" {
		args = append([]string{"-L", c.Socket}, args...)
	}
	return t.Interactive("tmux", args...)
}

// Terminal runs a program attached to the user's terminal.
type Terminal interface {
	Interactive(name string, args ...string) error
}
