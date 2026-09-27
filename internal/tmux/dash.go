package tmux

import (
	"errors"
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
func placeholder() []string { return placeholderWith(PlaceholderHint) }

func placeholderWith(hint string) []string {
	return []string{"sh", "-c", `printf '%s\n\n' "$1"; exec "${SHELL:-/bin/sh}"`, "sh", hint}
}

// KilledHint is what the placeholder says when the docked agent was killed
// (S6).
func KilledHint(name string) string {
	return "hq: " + name + " killed - select an agent above or press n"
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
// its dashboard window are touched, never the server's global options. The
// mouse is on for hq's session alone (design §3.7): clicks reach the list
// program and the dialogs, and a click in the docked pane focuses it.
func (c Client) style(win string) error {
	border := `#{?#{==:#{@hq_role},list},,#[fg=colour243] ▸ #{?#{@hq_title},#{@hq_title},#{pane_title}} }`
	return c.batch(
		[]string{"set-option", "-w", "-t", win, "pane-border-status", "top"},
		[]string{"set-option", "-w", "-t", win, "pane-border-format", border},
		[]string{"set-option", "-w", "-t", win, "pane-border-style", "fg=colour238"},
		[]string{"set-option", "-w", "-t", win, "pane-active-border-style", "fg=colour238"},
		[]string{"set-option", "-t", Session, "status-style", "bg=default,fg=colour245"},
		[]string{"set-option", "-t", Session, "status-format[0]", footerFormat},
		[]string{"set-option", "-t", Session, "set-titles", "on"},
		[]string{"set-option", "-t", Session, "set-titles-string", "hq"},
		[]string{"set-option", "-t", Session, "mouse", "on"},
	)
}

// footerFormat is the status line: the chords' hints (@hq_chords) while the
// keys are in the dashboard's slot, else the list's footer (design §3.7).
const footerFormat = `#{?#{&&:#{@hq_dash},#{!=:#{@hq_role},list}},#{T:@hq_chords},#{T:status-left}}`

// ChordKeys are the Alt chords and the argument each gives hq's chord
// command; Alt+l is tmux's own pane switching (design §3.7).
var ChordKeys = []struct{ Key, Arg string }{
	{"M-j", "next"}, {"M-k", "previous"}, {"M-a", "attention"}, {"M-n", "new"}, {"M-l", ""},
}

// BindChords binds the Alt chords for hq's session alone (design §3.7):
// each runs argv, hq's chord command, with its argument, and the chords'
// hints are kept for the footer. Keys are bound in tmux's root table, so
// the binding itself hands the key on in any other session: to the key's
// earlier binding when there was one, else to the pane. The bindings name
// the command through the session option @hq_chord, so they stay the same
// when hq moves and are bound once per server.
func (c Client) BindChords(argv []string, hints string) error {
	env := "HQ_TMUX_SOCKET=" + shellQuote(c.Socket)
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = shellQuote(a)
	}
	if err := c.batch(
		[]string{"set-option", "-t", Session, "@hq_chord", env + " " + strings.Join(quoted, " ")},
		[]string{"set-option", "-t", Session, "@hq_chords", hints},
	); err != nil {
		return err
	}
	out, err := c.tmux("list-keys", "-T", "root")
	if err != nil {
		return err
	}
	var cmds [][]string
	for _, k := range ChordKeys {
		earlier, ours := rootBinding(string(out), k.Key)
		if ours {
			continue
		}
		if earlier == "" {
			earlier = "send-keys " + k.Key
		}
		cmd := "run-shell -b '#{@hq_chord} " + k.Arg + "'"
		if k.Arg == "" {
			cmd = "select-pane -t :.+"
		}
		cmds = append(cmds, []string{"bind-key", "-n", k.Key, "if-shell", "-F", "#{==:#{session_name}," + Session + "}", cmd, earlier})
	}
	if len(cmds) == 0 {
		return nil
	}
	return c.batch(cmds...)
}

// rootBinding finds key's command in list-keys output for the root table;
// ours is true when it is hq's own chord.
func rootBinding(listing, key string) (cmd string, ours bool) {
	for _, l := range strings.Split(listing, "\n") {
		f := strings.Fields(l)
		for i := 0; i+2 < len(f); i++ {
			if f[i] == "-T" && f[i+1] == "root" && f[i+2] == key {
				_, cmd, _ = strings.Cut(l, " "+key+" ")
				cmd = strings.TrimSpace(cmd)
				return cmd, strings.Contains(cmd, "#{==:#{session_name},"+Session+"}")
			}
		}
	}
	return "", false
}

// shellQuote quotes s for sh.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// Message shows text on the dashboard's status line for a few seconds.
func (c Client) Message(text string) error {
	_, err := c.tmux("display-message", "-d", "3000", "-t", Session+":", strings.ReplaceAll(text, "#", "##"))
	return err
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

// pane is one pane of hq's session with what hq keeps on it and on its
// window.
type pane struct {
	window, windowName, dash string
	id, role, agent, title   string
	dead                     bool
	listPID                  int
	options                  map[string]string // the window's OptionKeys
}

func (c Client) panes() ([]pane, error) {
	fields := []string{"#{window_id}", "#{window_name}", "#{@hq_dash}", "#{pane_id}", "#{@hq_role}", "#{@hq_agent}", "#{@hq_title}", "#{pane_dead}", "#{@hq_list_pid}"}
	for _, k := range OptionKeys {
		fields = append(fields, "#{@hq_"+k+"}")
	}
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
		pid, _ := strconv.Atoi(f[8])
		p := pane{window: f[0], windowName: f[1], dash: f[2], id: f[3], role: f[4], agent: f[5], title: f[6], dead: f[7] == "1", listPID: pid, options: map[string]string{}}
		for i, k := range OptionKeys {
			if v := f[9+i]; v != "" {
				p.options[k] = v
			}
		}
		ps = append(ps, p)
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
			if p.windowName == Session && p.options["id"] == "" {
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

// ErrNoDashboard is returned by Dock when the dashboard window is missing.
var ErrNoDashboard = errors.New("tmux: no dashboard window")

// Dock shows an agent's own pane in the docking slot, framed with title,
// and puts the keys there (design §3.1). The pane that was in the slot goes
// back to its home: another agent to its home window, the placeholder to
// the home window of the agent now docked, where it waits. Panes are only
// swapped, so no process, scrollback or cursor is interrupted.
func (c Client) Dock(window, title string) error {
	ps, err := c.panes()
	if err != nil {
		return err
	}
	var home pane
	for _, p := range ps {
		if p.window == window {
			home = p
		}
	}
	if home.id == "" || home.options["id"] == "" {
		return fmt.Errorf("tmux: no agent window %s", window)
	}
	id := home.options["id"]
	if home.role == roleSlot { // already docked
		for _, p := range ps {
			if p.agent == id {
				return c.batch([]string{"set-option", "-p", "-t", p.id, "@hq_title", title}, []string{"select-pane", "-t", p.id})
			}
		}
	}
	slot, ok := dashSlot(ps)
	if !ok {
		return ErrNoDashboard
	}
	if slot.agent != "" {
		if err := c.undock(ps, ""); err != nil {
			return err
		}
		if ps, err = c.panes(); err != nil {
			return err
		}
		if slot, ok = dashSlot(ps); !ok {
			return ErrNoDashboard
		}
	}
	cmds := agentPane(home.id, id)
	cmds = append(cmds, ";", "set-option", "-p", "-t", home.id, "@hq_title", title,
		";", "swap-pane", "-d", "-s", home.id, "-t", slot.id,
		";", "select-pane", "-t", home.id)
	_, err = c.tmux(cmds...)
	return err
}

// dashSlot is the pane in the docking slot: the dashboard's pane that is not
// the list.
func dashSlot(ps []pane) (pane, bool) {
	for _, p := range ps {
		if p.dash == "1" && p.role != roleList {
			return p, true
		}
	}
	return pane{}, false
}

// undock sends the agent in the slot back to its home window, bringing the
// placeholder back into the slot, and puts the keys on the list. With a
// hint, the placeholder starts afresh saying it.
func (c Client) undock(ps []pane, hint string) error {
	slot, ok := dashSlot(ps)
	if !ok || slot.agent == "" {
		return nil
	}
	var list string
	for _, p := range ps {
		if p.dash == "1" && p.role == roleList {
			list = p.id
		}
	}
	for _, p := range ps {
		if p.options["id"] == slot.agent && p.role == roleSlot {
			cmds := []string{"swap-pane", "-d", "-s", slot.id, "-t", p.id}
			if hint != "" {
				cmds = append(append(cmds, ";", "respawn-pane", "-k", "-t", p.id, "--"), placeholderWith(hint)...)
			}
			if list != "" {
				cmds = append(cmds, ";", "select-pane", "-t", list)
			}
			_, err := c.tmux(cmds...)
			return err
		}
	}
	return nil
}

// SetTitle sets the frame title of a pane.
func (c Client) SetTitle(pane, title string) error {
	_, err := c.tmux("set-option", "-p", "-t", pane, "@hq_title", title)
	return err
}

// Popup opens a popup centered over the window of pane, w by h cells with
// its frame, running argv in dir, and returns when it closes (design §3.8).
// The popup holds the client's keys while open.
func (c Client) Popup(pane, dir string, w, h int, argv []string) error {
	args := []string{"display-popup", "-E", "-b", "rounded", "-S", "fg=#A78BFA", "-w", strconv.Itoa(w), "-h", strconv.Itoa(h),
		"-d", strings.ReplaceAll(dir, "#", "##"), "-t", pane}
	args = append(append(append(args, c.env()...), "--"), argv...)
	_, err := c.tmux(args...)
	return err
}
