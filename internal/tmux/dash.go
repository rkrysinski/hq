package tmux

import (
	"fmt"
	"strconv"
	"strings"
)

// The dashboard is the first window of hq's session (design §3.1): the list
// pane on top, the docking slot below, the footer on the session's status
// line. Its panes are told apart by the pane option @hq_role, so docking can
// move them between windows.
const (
	roleList = "list"
	roleSlot = "slot"

	// PlaceholderTitle frames the slot while nothing is docked (S1).
	PlaceholderTitle = "placeholder shell"
	// PlaceholderHint is what the placeholder shell prints first (S1).
	PlaceholderHint = "hq: nothing docked - select an agent above or press n"
)

// Dash is the dashboard window as hq found or made it.
type Dash struct {
	Window  string // window id
	List    string // pane id of the list pane
	Slot    string // pane id of the docking slot
	ListPID int    // the list program's process, 0 when none has said so
	Started bool   // the list pane was just made, its program is starting
}

// placeholder is the slot's shell: the hint, then the user's shell.
func placeholder() []string {
	return []string{"sh", "-c", `printf '%s\n\n' "$1"; exec "${SHELL:-/bin/sh}"`, "sh", PlaceholderHint}
}

// listShell runs the list program in a shell that stays when it exits, so
// the user can bring the list back there (S8).
func listShell(list []string) []string {
	return append([]string{"sh", "-c", `"$@"; exec "${SHELL:-/bin/sh}"`, "sh"}, list...)
}

// env passes the server's socket name to the list program, so that it talks
// to this server.
func (c Client) env() []string {
	if c.Socket == "" {
		return nil
	}
	return []string{"-e", "HQ_TMUX_SOCKET=" + c.Socket}
}

// EnsureSession creates hq's session, detached, when it does not exist. Its
// first window is the dashboard, holding the placeholder slot until the list
// program is started in it.
func (c Client) EnsureSession(dir string) error {
	if _, err := c.tmux("has-session", "-t", "="+Session); err == nil {
		return nil
	}
	args := append([]string{"new-session", "-d", "-P", "-F", "#{window_id} #{pane_id}", "-s", Session, "-n", Session, "-c", dir, "--"}, placeholder()...)
	out, err := c.tmux(args...)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate session") {
			return nil
		}
		return err
	}
	win, pane, _ := strings.Cut(strings.TrimSpace(string(out)), " ")
	return c.decorate(win, pane)
}

// decorate makes a window the dashboard with pane as its slot.
func (c Client) decorate(win, pane string) error {
	return c.batch(
		[]string{"set-option", "-w", "-t", win, "@hq_dash", "1"},
		[]string{"set-option", "-p", "-t", pane, "@hq_role", roleSlot},
		[]string{"set-option", "-p", "-t", pane, "@hq_title", PlaceholderTitle},
	)
}

// style gives the dashboard and hq's session the look of the mocks: framed,
// titled panes; a status line that is only the footer, without tmux's window
// list; the terminal titled hq (design §3.1, §3.11). Only hq's session and
// its dashboard window are touched, never the server's global options.
func (c Client) style(win string) error {
	border := `#{?#{==:#{@hq_role},list},,#[fg=colour243] ▸ #{?#{@hq_title},#{@hq_title},#{pane_title}} }`
	return c.batch(
		[]string{"set-option", "-w", "-t", win, "pane-border-status", "top"},
		[]string{"set-option", "-w", "-t", win, "pane-border-format", border},
		[]string{"set-option", "-w", "-t", win, "pane-border-style", "fg=colour238"},
		[]string{"set-option", "-w", "-t", win, "pane-active-border-style", "fg=colour238"},
		[]string{"set-option", "-t", Session, "status-style", "bg=default,fg=colour245"},
		[]string{"set-option", "-t", Session, "status-format[0]", "#{T:status-left}"},
		[]string{"set-option", "-t", Session, "set-titles", "on"},
		[]string{"set-option", "-t", Session, "set-titles-string", "hq"},
	)
}

// batch runs tmux commands in one call.
func (c Client) batch(cmds ...[]string) error {
	var args []string
	for i, cmd := range cmds {
		if i > 0 {
			args = append(args, ";")
		}
		args = append(args, cmd...)
	}
	_, err := c.tmux(args...)
	return err
}

// pane is one pane of hq's session with what hq keeps on it and its window.
type pane struct {
	window, windowName, dash, agentID string
	id, role                          string
	listPID                           int
}

func (c Client) panes() ([]pane, error) {
	fields := []string{"#{window_id}", "#{window_name}", "#{@hq_dash}", "#{@hq_id}", "#{pane_id}", "#{@hq_role}", "#{@hq_list_pid}"}
	out, err := c.tmux("list-panes", "-s", "-t", Session+":", "-F", strings.Join(fields, sep))
	if err != nil {
		return nil, err
	}
	var ps []pane
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, sep)
		if len(f) != len(fields) {
			return nil, fmt.Errorf("tmux: unexpected list-panes line %q", line)
		}
		pid, _ := strconv.Atoi(f[6])
		ps = append(ps, pane{window: f[0], windowName: f[1], dash: f[2], agentID: f[3], id: f[4], role: f[5], listPID: pid})
	}
	return ps, nil
}

// Dashboard finds or makes the dashboard window, with the list pane running
// list and the slot below it (design §3.1). A list pane it makes gets the
// keys. A session made before the
// dashboard existed gets its first "hq" window turned into it, keeping the
// shell there as the slot.
func (c Client) Dashboard(dir string, list []string) (Dash, error) {
	if err := c.EnsureSession(dir); err != nil {
		return Dash{}, err
	}
	ps, err := c.panes()
	if err != nil {
		return Dash{}, err
	}
	var d Dash
	for _, p := range ps {
		if p.dash == "1" {
			d.Window = p.window
			break
		}
	}
	if d.Window == "" {
		for _, p := range ps {
			if p.windowName == Session && p.agentID == "" {
				d.Window = p.window
				if err := c.decorate(p.window, p.id); err != nil {
					return Dash{}, err
				}
				d.Slot = p.id
				break
			}
		}
	}
	if d.Window == "" {
		args := append([]string{"new-window", "-d", "-P", "-F", "#{window_id} #{pane_id}", "-t", Session + ":", "-n", Session, "-c", dir, "--"}, placeholder()...)
		out, err := c.tmux(args...)
		if err != nil {
			return Dash{}, err
		}
		win, p, _ := strings.Cut(strings.TrimSpace(string(out)), " ")
		if err := c.decorate(win, p); err != nil {
			return Dash{}, err
		}
		d.Window, d.Slot = win, p
	}
	if err := c.style(d.Window); err != nil {
		return Dash{}, err
	}
	for _, p := range ps {
		if p.window != d.Window {
			continue
		}
		switch {
		case p.role == roleList:
			d.List, d.ListPID = p.id, p.listPID
		case d.Slot == "":
			d.Slot = p.id
		}
	}
	if d.Slot == "" {
		args := append([]string{"split-window", "-v", "-d", "-P", "-F", "#{pane_id}", "-t", d.List, "-c", dir, "--"}, placeholder()...)
		out, err := c.tmux(args...)
		if err != nil {
			return Dash{}, err
		}
		d.Slot = strings.TrimSpace(string(out))
		if _, err := c.tmux("set-option", "-p", "-t", d.Slot, "@hq_role", roleSlot, ";", "set-option", "-p", "-t", d.Slot, "@hq_title", PlaceholderTitle); err != nil {
			return Dash{}, err
		}
	}
	if d.List == "" {
		// The list program fits its own height once it runs.
		args := append([]string{"split-window", "-v", "-b", "-d", "-l", "10", "-P", "-F", "#{pane_id}", "-t", d.Slot, "-c", dir}, c.env()...)
		args = append(append(args, "--"), listShell(list)...)
		out, err := c.tmux(args...)
		if err != nil {
			return Dash{}, err
		}
		d.List, d.Started = strings.TrimSpace(string(out)), true
		if _, err := c.tmux("set-option", "-p", "-t", d.List, "@hq_role", roleList, ";", "select-pane", "-t", d.List); err != nil {
			return Dash{}, err
		}
	}
	return d, nil
}

// RespawnList starts the list program again in the list pane, replacing the
// shell left there after q, and puts the keys there.
func (c Client) RespawnList(pane string, list []string) error {
	args := append(append([]string{"respawn-pane", "-k", "-t", pane}, c.env()...), "--")
	args = append(append(args, listShell(list)...), ";", "select-pane", "-t", pane)
	_, err := c.tmux(args...)
	return err
}

// WindowHeight is the height of the window holding pane.
func (c Client) WindowHeight(pane string) (int, error) {
	out, err := c.tmux("display-message", "-p", "-t", pane, "#{window_height}")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

// ResizeHeight sets the height of pane; the other pane of its window takes
// the rest.
func (c Client) ResizeHeight(pane string, lines int) error {
	_, err := c.tmux("resize-pane", "-t", pane, "-y", strconv.Itoa(lines))
	return err
}

// SetFooter puts text, a tmux format, on the status line of hq's session.
func (c Client) SetFooter(text string) error {
	_, err := c.tmux("set-option", "-t", Session, "status-left", text)
	return err
}

// MarkList records on the list pane which process runs the list program;
// 0 clears it when the program exits.
func (c Client) MarkList(pane string, pid int) error {
	if pid == 0 {
		_, err := c.tmux("set-option", "-p", "-u", "-t", pane, "@hq_list_pid")
		return err
	}
	_, err := c.tmux("set-option", "-p", "-t", pane, "@hq_list_pid", strconv.Itoa(pid))
	return err
}

// SessionValue reads a user option hq keeps on its session (design §3.3:
// the list's session state); unset is empty.
func (c Client) SessionValue(key string) (string, error) {
	out, err := c.tmux("show-options", "-v", "-q", "-t", Session, "@hq_"+key)
	return strings.TrimSpace(string(out)), err
}

// SetSessionValue stores a user option on hq's session.
func (c Client) SetSessionValue(key, value string) error {
	_, err := c.tmux("set-option", "-t", Session, "@hq_"+key, value)
	return err
}
