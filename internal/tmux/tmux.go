// Package tmux talks to the tmux server that holds hq's session. tmux is the
// agent registry (design §3.3): an agent exists while its home window exists,
// and what hq knows at launch is stored on that window as user options.
package tmux

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/rkrysinski/hq/internal/proc"
)

// Session is the name of hq's tmux session.
const Session = "hq"

// MinVersion is the oldest tmux hq supports (design §3.2).
const MinVersion = "3.4"

// Window is one window of hq's session with the options hq keeps on it.
type Window struct {
	ID       string // tmux window id, e.g. "@3"
	Name     string
	PaneDead bool
	Options  map[string]string // @hq_* user options, without the "@hq_" prefix
}

// Client runs tmux commands against one server.
type Client struct {
	Run    proc.Runner
	Socket string // tmux -L socket name; empty is the default server
}

// OptionKeys are the user options hq stores on a home window.
var OptionKeys = []string{"id", "name", "repo", "sandbox", "started"}

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
// empty list.
func (c Client) Windows() ([]Window, error) {
	fields := []string{"#{window_id}", "#{window_name}", "#{pane_dead}"}
	for _, k := range OptionKeys {
		fields = append(fields, "#{@hq_"+k+"}")
	}
	out, err := c.tmux("list-windows", "-t", Session+":", "-F", strings.Join(fields, sep))
	if err != nil {
		if isNoServerOrSession(err) {
			return nil, nil
		}
		return nil, err
	}
	var ws []Window
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, sep)
		if len(f) != 3+len(OptionKeys) {
			return nil, fmt.Errorf("tmux: unexpected list-windows line %q", line)
		}
		w := Window{ID: f[0], Name: f[1], PaneDead: f[2] == "1", Options: map[string]string{}}
		for i, k := range OptionKeys {
			if v := f[3+i]; v != "" {
				w.Options[k] = v
			}
		}
		ws = append(ws, w)
	}
	return ws, nil
}

// EnsureSession creates hq's session, detached, when it does not exist. Its
// first window is a shell named "hq" (the dashboard's window in later
// milestones).
func (c Client) EnsureSession(dir string) error {
	if _, err := c.tmux("has-session", "-t", "="+Session); err == nil {
		return nil
	}
	_, err := c.tmux("new-session", "-d", "-s", Session, "-n", Session, "-c", dir)
	if err != nil && strings.Contains(err.Error(), "duplicate session") {
		return nil
	}
	return err
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
	set := []string{"set-option", "-w", "-t", id, "remain-on-exit", "on"}
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

// Start releases the program of a window made by NewWindow.
func (c Client) Start(id string) error {
	_, err := c.tmux("send-keys", "-t", id, "Enter")
	return err
}

// KillWindow removes a window.
func (c Client) KillWindow(id string) error {
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
