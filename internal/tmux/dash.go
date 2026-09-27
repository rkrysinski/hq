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
	roleList   = "list"
	roleSlot   = "slot"
	roleSpacer = "spacer"

	// PlaceholderTitle frames the slot while nothing is docked (S1).
	PlaceholderTitle = "placeholder"
	// PlaceholderHint is what the placeholder shows (S1).
	PlaceholderHint = "hq: nothing docked - select an agent above or press n"

	// placeholderMark marks a pane running the placeholder program, as
	// opposed to the shell older versions of hq left in the slot.
	placeholderMark = "program"
)

// The dashboard's colours (design §3.1, from the design's tokens): the
// docked session and the empty slot sit in a darker area, framed on every
// side by a slightly lighter surround: the list, the margins beside and
// below the slot, the footer.
const (
	surroundColour = "#16181D" // the list, the margins, the footer
	slotColour     = "#111317" // the slot and its title row
	titleColour    = "#5C626C" // the slot's title
)

// TerminalTitle is the terminal's title while the dashboard is attached
// (design §3.11); the WSL adapter raises the window by it.
const TerminalTitle = "hq - agents"

// Dash is the dashboard window as hq found or made it.
type Dash struct {
	Window  string // window id
	List    string // pane id of the list pane
	Slot    string // pane id of the docking slot
	ListPID int    // the list program's process, 0 when none has said so
	Started bool   // the list pane was just made, its program is starting
}

// placeholderArgv is the command of the pane that fills the slot while nothing
// is docked (design §3.1): hq's placeholder program saying hint, which takes
// no commands. Without one (a client made in a test), a stand-in that shows
// the hint and ignores what is typed.
func (c Client) placeholderArgv(hint string) []string {
	if len(c.Placeholder) > 0 {
		return append(append([]string{}, c.Placeholder...), hint)
	}
	return []string{"sh", "-c", `printf '%s\n' "$1"; stty -echo 2>/dev/null; exec cat >/dev/null`, "sh", hint}
}

// placeholderPane marks pane as the placeholder running hq's placeholder
// program: the slot's role and frame title, and kept when its program ends,
// so a home window holding it never closes and takes its agent along
// (design §3.3).
func placeholderPane(pane string) [][]string {
	return [][]string{
		{"set-option", "-p", "-t", pane, "@hq_role", roleSlot},
		{"set-option", "-p", "-t", pane, "@hq_title", PlaceholderTitle},
		{"set-option", "-p", "-t", pane, "@hq_placeholder", placeholderMark},
		{"set-option", "-p", "-t", pane, "remain-on-exit", "on"},
	}
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
	args := append(append([]string{"new-session", "-d", "-P", "-F", "#{window_id} #{pane_id}", "-s", Session, "-n", Session, "-c", dir}, c.env()...), "--")
	args = append(args, c.placeholderArgv(PlaceholderHint)...)
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
	return c.batch(append([][]string{{"set-option", "-w", "-t", win, "@hq_dash", "1"}}, placeholderPane(pane)...)...)
}

// style gives the dashboard and hq's session the look of the mocks (design
// §3.1): the slot's window area darker than the surround, which the list
// pane, the margin panes (spacers), the invisible pane borders and the
// status line share; the slot's title on the row above it, drawn as the
// list pane's bottom status in the slot's colour so the title row and the
// slot make one area; a status line that is only the footer, without
// tmux's window list; the terminal titled hq - agents (design §3.11). Only
// hq's session and its dashboard window are touched, never the server's
// global options. The mouse is on for hq's session alone (design §3.7):
// clicks reach the list program and the dialogs, and a click in the docked
// pane focuses it.
func (c Client) style(win string) error {
	version, err := c.Version()
	if err != nil {
		return err
	}
	return c.batch(
		[]string{"set-option", "-w", "-t", win, "pane-border-status", "bottom"},
		[]string{"set-option", "-w", "-t", win, "pane-border-format", borderFormat(version)},
		[]string{"set-option", "-w", "-t", win, "pane-border-lines", "single"},
		[]string{"set-option", "-w", "-t", win, "pane-border-style", "fg=" + surroundColour + ",bg=" + surroundColour},
		[]string{"set-option", "-w", "-t", win, "pane-active-border-style", "fg=" + surroundColour + ",bg=" + surroundColour},
		[]string{"set-option", "-w", "-t", win, "window-style", "bg=" + slotColour},
		[]string{"set-option", "-w", "-t", win, "window-active-style", "bg=" + slotColour},
		[]string{"set-option", "-t", Session, "status-style", "bg=" + surroundColour + ",fg=colour245"},
		[]string{"set-option", "-t", Session, "status-format[0]", footerFormat},
		[]string{"set-option", "-t", Session, "set-titles", "on"},
		[]string{"set-option", "-t", Session, "set-titles-string", TerminalTitle},
		[]string{"set-option", "-t", Session, "mouse", "on"},
	)
}

// surroundPane gives pane the surround's colour instead of the slot's.
func surroundPane(pane string) [][]string {
	return [][]string{
		{"set-option", "-p", "-t", pane, "window-style", "bg=" + surroundColour},
		{"set-option", "-p", "-t", pane, "window-active-style", "bg=" + surroundColour},
	}
}

// borderFormat titles the slot: the list pane's bottom status, the row
// right above the slot, shows the title of the pane in the slot (the pane
// neither list nor spacer) on the slot's colour, as wide as the slot. The
// other panes' status rows (the margin below the slot) stay empty. tmux
// draws a pane's status from its third column to four columns short of its
// width until 3.5, two short from 3.6 on: there the last two columns are
// given the surround's colour, to end where the slot ends.
func borderFormat(version string) string {
	end := ""
	if AtLeast(version, "3.6") {
		end = `#[align=right bg=` + surroundColour + `]  `
	}
	return `#{?#{==:#{@hq_role},list},#[fill=` + slotColour + ` bg=` + slotColour + ` fg=` + titleColour + `] ▸ ` + slotTitle + end + `,}`
}

// slotTitle is the frame title of the pane in the slot, as a format of the
// dashboard window.
const slotTitle = `#{P:#{?#{||:#{==:#{@hq_role},list},#{==:#{@hq_role},spacer}},,#{?#{@hq_title},#{@hq_title},#{pane_title}}}}`

// footerFormat is the status line: the chords' hints (@hq_chords) while the
// keys are in the dashboard's slot, else the list's footer (design §3.7).
const footerFormat = `#{?#{&&:#{@hq_dash},#{!=:#{@hq_role},list}},#{T:@hq_chords},#{T:status-left}}`

// ChordKeys are the Alt chords and the argument each gives hq's chord
// command; Alt+l is tmux's own pane switching (design §3.7).
var ChordKeys = []struct{ Key, Arg string }{
	{"M-j", "next"}, {"M-k", "previous"}, {"M-a", "attention"}, {"M-n", "new"}, {"M-l", ""},
}

// switchPanes is Alt+l: the keys go from the list to the slot and from
// anywhere else back to the list, by position, never onto a margin pane
// ({bottom} and {top} are the panes at the window's bottom and top centre).
const switchPanes = `if-shell -F '#{==:#{@hq_role},list}' "select-pane -t '{bottom}'" "select-pane -t '{top}'"`

// oldSwitchPanes is Alt+l as hq bound it before the margin panes: the next
// pane, which is now a margin.
const oldSwitchPanes = `"select-pane -t :.+"`

// BindChords binds the Alt chords for hq's session alone (design §3.7):
// each runs argv, hq's chord command, with its argument, and the chords'
// hints are kept for the footer. Keys are bound in tmux's root table, so
// the binding itself hands the key on in any other session: to the key's
// earlier binding when there was one, else to the pane. The bindings name
// the command through the session option @hq_chord, so they stay the same
// when hq moves and are bound once per server; an Alt+l bound by an older
// hq is brought up to date, keeping what it hands on.
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
			if k.Arg == "" && strings.Contains(earlier, oldSwitchPanes) {
				// The binding is tmux's own text: changed in place and
				// parsed again, what it hands on stays as it was.
				line := "bind-key -n " + k.Key + " " + strings.Replace(earlier, oldSwitchPanes, tmuxQuote(switchPanes), 1)
				cmds = append(cmds, []string{"if-shell", "-F", "1", line})
			}
			continue
		}
		if earlier == "" {
			earlier = "send-keys " + k.Key
		}
		cmd := "run-shell -b '#{@hq_chord} " + k.Arg + "'"
		if k.Arg == "" {
			cmd = switchPanes
		}
		cmds = append(cmds, []string{"bind-key", "-n", k.Key, "if-shell", "-F", "#{==:#{session_name}," + Session + "}", cmd, earlier})
	}
	if len(cmds) == 0 {
		return nil
	}
	return c.batch(cmds...)
}

// tmuxQuote quotes s for tmux's command parser.
func tmuxQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`).Replace(s) + `"`
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
	placeholder              string // @hq_placeholder: placeholderMark on a placeholder hq started
	dead                     bool
	listPID                  int
	options                  map[string]string // the window's OptionKeys
}

func (c Client) panes() ([]pane, error) {
	fields := []string{"#{window_id}", "#{window_name}", "#{@hq_dash}", "#{pane_id}", "#{@hq_role}", "#{@hq_agent}", "#{@hq_title}", "#{pane_dead}", "#{@hq_list_pid}", "#{@hq_placeholder}"}
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
		p := pane{window: f[0], windowName: f[1], dash: f[2], id: f[3], role: f[4], agent: f[5], title: f[6], dead: f[7] == "1", listPID: pid, placeholder: f[9], options: map[string]string{}}
		for i, k := range OptionKeys {
			if v := f[10+i]; v != "" {
				p.options[k] = v
			}
		}
		ps = append(ps, p)
	}
	return ps, nil
}

// Dashboard finds or makes the dashboard window, with the list pane running
// list and the slot below it (design §3.1). A list pane it makes gets the
// keys. A session made before the dashboard existed gets its first "hq"
// window turned into it, that window's pane becoming the slot. A
// placeholder that is not running hq's placeholder program (a shell left by
// an older hq, or a program that ended) is started afresh, wherever it
// waits.
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
				if err := c.restartPlaceholder(p.id, PlaceholderHint); err != nil {
					return Dash{}, err
				}
				d.Slot = p.id
				break
			}
		}
	}
	if d.Window == "" {
		args := append(append([]string{"new-window", "-d", "-P", "-F", "#{window_id} #{pane_id}", "-t", Session + ":", "-n", Session, "-c", dir}, c.env()...), "--")
		out, err := c.tmux(append(args, c.placeholderArgv(PlaceholderHint)...)...)
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
	var spacers []string
	for _, p := range ps {
		if p.role == roleSlot && (p.placeholder != placeholderMark || p.dead) {
			if err := c.restartPlaceholder(p.id, PlaceholderHint); err != nil {
				return Dash{}, err
			}
		}
		if p.window != d.Window {
			continue
		}
		switch {
		case p.role == roleList:
			d.List, d.ListPID = p.id, p.listPID
		case p.role == roleSpacer:
			spacers = append(spacers, p.id)
		case d.Slot == "":
			d.Slot = p.id
		}
	}
	// The margins beside the slot are made again whenever they are not
	// both there, and before a slot is made, so they always flank it.
	if d.Slot == "" || len(spacers) != 2 {
		for _, p := range spacers {
			if _, err := c.tmux("kill-pane", "-t", p); err != nil {
				return Dash{}, err
			}
		}
		spacers = nil
	}
	if d.Slot == "" {
		args := append(append([]string{"split-window", "-v", "-d", "-P", "-F", "#{pane_id}", "-t", d.List, "-c", dir}, c.env()...), "--")
		out, err := c.tmux(append(args, c.placeholderArgv(PlaceholderHint)...)...)
		if err != nil {
			return Dash{}, err
		}
		d.Slot = strings.TrimSpace(string(out))
		if err := c.batch(placeholderPane(d.Slot)...); err != nil {
			return Dash{}, err
		}
	}
	if d.List == "" {
		// The list program fits its own height once it runs.
		args := append([]string{"split-window", "-v", "-b", "-f", "-d", "-l", "10", "-P", "-F", "#{pane_id}", "-t", d.Slot, "-c", dir}, c.env()...)
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
	if err := c.batch(surroundPane(d.List)...); err != nil {
		return Dash{}, err
	}
	if len(spacers) == 0 {
		// One column each side of the slot, left then right.
		for _, before := range []bool{true, false} {
			args := []string{"split-window", "-h", "-d", "-l", "1", "-P", "-F", "#{pane_id}", "-t", d.Slot, "-c", dir}
			if before {
				args = append(args, "-b")
			}
			args = append(append(append(args, c.env()...), "--"), c.placeholderArgv("")...)
			out, err := c.tmux(args...)
			if err != nil {
				return Dash{}, err
			}
			p := strings.TrimSpace(string(out))
			if err := c.batch(append(surroundPane(p), []string{"set-option", "-p", "-t", p, "@hq_role", roleSpacer})...); err != nil {
				return Dash{}, err
			}
		}
	}
	return d, nil
}

// restartPlaceholder starts the placeholder program afresh in pane, saying
// hint.
func (c Client) restartPlaceholder(pane, hint string) error {
	return c.batch(append(placeholderPane(pane), c.respawnPlaceholder(pane, hint))...)
}

// respawnPlaceholder is the command that starts the placeholder program in
// pane, ending what ran there.
func (c Client) respawnPlaceholder(pane, hint string) []string {
	args := append(append([]string{"respawn-pane", "-k", "-t", pane}, c.env()...), "--")
	return append(args, c.placeholderArgv(hint)...)
}

// FocusList puts the keys on the dashboard's list pane; the placeholder
// sends them there when it is given any (design §3.1).
func (c Client) FocusList() error {
	ps, err := c.panes()
	if err != nil {
		return err
	}
	for _, p := range ps {
		if p.dash == "1" && p.role == roleList {
			_, err := c.tmux("select-pane", "-t", p.id)
			return err
		}
	}
	return ErrNoDashboard
}

// RespawnList starts the list program again in the list pane, replacing the
// shell left there after q, and puts the keys there.
func (c Client) RespawnList(pane string, list []string) error {
	args := append(append([]string{"respawn-pane", "-k", "-t", pane}, c.env()...), "--")
	args = append(append(args, listShell(list)...), ";", "select-pane", "-t", pane)
	_, err := c.tmux(args...)
	return err
}

// TerminalHeight is the height of the terminal showing the window holding
// pane: the window and its session's status lines, which tmux takes from
// the terminal's rows.
func (c Client) TerminalHeight(pane string) (int, error) {
	out, err := c.tmux("display-message", "-p", "-t", pane, "#{window_height} #{status}")
	if err != nil {
		return 0, err
	}
	return terminalHeight(string(out))
}

// terminalHeight adds up tmux's answer to "#{window_height} #{status}": the
// status option is off, on (one line) or a number of lines.
func terminalHeight(out string) (int, error) {
	f := strings.Fields(out)
	if len(f) != 2 {
		return 0, fmt.Errorf("unexpected window height %q", strings.TrimSpace(out))
	}
	h, err := strconv.Atoi(f[0])
	if err != nil {
		return 0, err
	}
	switch f[1] {
	case "off":
		return h, nil
	case "on":
		return h + 1, nil
	}
	n, err := strconv.Atoi(f[1])
	if err != nil {
		return 0, fmt.Errorf("unexpected status %q", f[1])
	}
	return h + n, nil
}

// ResizeHeight sets the height of pane; the other pane of its window takes
// the rest.
func (c Client) ResizeHeight(pane string, lines int) error {
	_, err := c.tmux("resize-pane", "-t", pane, "-y", strconv.Itoa(lines))
	return err
}

// KeepMargins sets the margins beside the slot back to one column each;
// tmux shares out a change of the terminal's width among all the panes of
// a row, the margins included. pane is any pane of the dashboard.
func (c Client) KeepMargins(pane string) error {
	out, err := c.tmux("list-panes", "-t", pane, "-F", "#{pane_id} #{pane_width} #{@hq_role}")
	if err != nil {
		return err
	}
	var cmds [][]string
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) == 3 && f[2] == roleSpacer && f[1] != "1" {
			cmds = append(cmds, []string{"resize-pane", "-t", f[0], "-x", "1"})
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	return c.batch(cmds...)
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

// dashSlot is the pane in the docking slot: the dashboard's pane that is
// neither the list nor a margin.
func dashSlot(ps []pane) (pane, bool) {
	for _, p := range ps {
		if p.dash == "1" && p.role != roleList && p.role != roleSpacer {
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
				cmds = append(append(cmds, ";"), c.respawnPlaceholder(p.id, hint)...)
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
