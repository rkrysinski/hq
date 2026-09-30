// Package tmux talks to the tmux server that holds hq's session. tmux is the
// agent registry (design §3.3): an agent exists while its home window exists,
// and what hq knows at launch is stored on that window as user options.
package tmux

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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
	DeadAt   time.Time         // when the pane's program ended, zero when unknown
	Docked   bool              // the agent's pane is in the docking slot
	Title    string            // the frame title on the agent's pane
	Options  map[string]string // @hq_* user options, without the "@hq_" prefix
}

// Client runs tmux commands against one server.
type Client struct {
	Run    proc.Runner
	Socket string // tmux -L socket name; empty is the default server
	// Placeholder is hq's placeholder program, run with the hint to show in
	// the docking slot while nothing is docked (design §3.1).
	Placeholder []string
	// Session is hq's program that runs an agent's session in its pane,
	// given the session's command line: it shows the session's last screen
	// again when the session reset the terminal as it ended (S7, design
	// §3.3). Empty runs the command line alone.
	Session []string
}

// OptionKeys are the user options hq stores on a home window: new marks an
// agent started with hq new (not relaunched), for the list's S2; turnend
// when hq first saw a turn the user ended; restseen when hq first saw a
// working agent's screen at rest; endseen when hq first saw the agent
// ended, where tmux cannot tell (design §3.4); inbox marks an agent whose
// hooks deliver the messages hq send leaves (ADR 0012), which agents
// started by an older hq lack.
var OptionKeys = []string{"id", "name", "repo", "sandbox", "started", "ending", "new", "turnend", "restseen", "endseen", "inbox"}

func (c Client) tmux(args ...string) ([]byte, error) {
	if c.Socket != "" {
		args = append([]string{"-L", c.Socket}, args...)
	}
	return c.Run.Run("tmux", args...)
}

// dollarProbe is the text read has tmux print after a command's output, to
// see what this server does to a $ that starts a name.
const dollarProbe = "$hq"

// read runs a tmux command whose output hq reads values from (options,
// paths, names) and returns it as the values are stored. tmux 3.4 puts a
// backslash before every $ that starts a name in what it prints; newer
// versions do not (#21). Rather than go by the version, read has the server
// print dollarProbe last and takes the backslashes out when that came back
// with one.
func (c Client) read(args ...string) ([]byte, error) {
	out, err := c.tmux(append(args, ";", "display-message", "-p", dollarProbe)...)
	if err != nil {
		return out, err
	}
	return []byte(asStored(string(out))), nil
}

// dollarEscaped finds what tmux 3.4 makes of a $ that starts a name: the
// names are those of its own parser (a letter, _ or {).
var dollarEscaped = regexp.MustCompile(`\\\$([A-Za-z_{])`)

// asStored takes read's probe line off tmux's output and, when the probe
// shows that this server escapes, the backslash it put before each $ that
// starts a name. Such a backslash is always tmux's: a \$ that was stored
// comes out as \\$, and goes back to \$ here. Output that does not end in
// the probe is returned as it is.
func asStored(out string) string {
	body, ok := strings.CutSuffix(out, "\\"+dollarProbe+"\n")
	if ok && (body == "" || strings.HasSuffix(body, "\n")) {
		return dollarEscaped.ReplaceAllString(body, "$$$1")
	}
	body, ok = strings.CutSuffix(out, dollarProbe+"\n")
	if ok && (body == "" || strings.HasSuffix(body, "\n")) {
		return body
	}
	return out
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
		w := Window{ID: p.window, Name: p.windowName, Pane: p.id, PaneDead: p.dead, DeadAt: p.deadAt, Title: p.title, Options: p.options}
		if id := p.options["id"]; id != "" && p.role == roleSlot {
			w.Docked = true
			a, ok := byAgent[id]
			// A docked pane that is gone took the agent with it.
			w.Pane, w.PaneDead, w.DeadAt, w.Title = a.id, a.dead || !ok, a.deadAt, a.title
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
	gate := append([]string{"sh", "-c", `read _ ; exec "$@"`, "sh"}, c.session(argv)...)
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
	// The slot's size before the program starts, so docking it never
	// resizes it (#141).
	_ = c.FitHomes()
	return id, nil
}

// SetOption stores one of OptionKeys on a window.
func (c Client) SetOption(id, key, value string) error {
	_, err := c.tmux("set-option", "-w", "-t", id, "@hq_"+key, value)
	return err
}

// KeepFirst stores value as one of OptionKeys on a window unless the
// option already holds a value that starts with prefix, and returns what
// the option holds then: the first value stored for a prefix wins, however
// many hq processes store one at once (design §3.4). The check, the store
// and the read run in one tmux call, which the server carries out without
// running another client's commands in between. prefix is plain text: no
// glob or format characters.
func (c Client) KeepFirst(id, key, prefix, value string) (string, error) {
	if strings.ContainsAny(prefix, "*?[]\\#{},") {
		return "", errors.New("tmux: prefix " + strconv.Quote(prefix) + " is not plain")
	}
	opt := "@hq_" + key
	// Empty (false) when the option starts with prefix, 1 otherwise.
	unset := "#{?#{m:" + prefix + "*,#{" + opt + "}},,1}"
	out, err := c.read("if-shell", "-F", "-t", id, unset, "set-option -w -t "+id+" "+opt+" "+tmuxQuote(value),
		";", "display-message", "-p", "-t", id, "#{"+opt+"}")
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(string(out), "\n"), nil
}

// screenMark starts the line Screens prints before each pane's screen.
const screenMark = "::hq::screen "

// Screens returns what the panes show, by pane id, in one tmux call.
func (c Client) Screens(panes []string) (map[string]string, error) {
	var args []string
	for _, p := range panes {
		if len(args) > 0 {
			args = append(args, ";")
		}
		args = append(args, "display-message", "-p", "-t", p, screenMark+"#{pane_id}", ";", "capture-pane", "-p", "-t", p)
	}
	if len(args) == 0 {
		return nil, nil
	}
	out, err := c.tmux(args...)
	if err != nil {
		return nil, err
	}
	screens := map[string]string{}
	var pane string
	var b strings.Builder
	flush := func() {
		if pane != "" {
			screens[pane] = b.String()
		}
		b.Reset()
	}
	for _, line := range strings.SplitAfter(string(out), "\n") {
		if id, ok := strings.CutPrefix(line, screenMark); ok {
			flush()
			pane = strings.TrimSpace(id)
			continue
		}
		b.WriteString(line)
	}
	flush()
	return screens, nil
}

// pasteChunk bounds the text one tmux command carries, well within what
// tmux accepts for a command.
const pasteChunk = 4 << 10

// Paste pastes text into a pane as a terminal pastes it, bracketed when
// the program there asks for that, as Claude Code does, so its lines stay
// one prompt and no key in it acts (hq send, design §3.4). The text goes
// through a buffer of the pane's own, in parts small enough for a tmux
// command, which the paste deletes.
func (c Client) Paste(pane, text string) error {
	buf := "hq-send-" + strings.TrimPrefix(pane, "%")
	for i, part := range chunks(text, pasteChunk) {
		args := []string{"set-buffer", "-b", buf, "--", part}
		if i > 0 {
			args = []string{"set-buffer", "-a", "-b", buf, "--", part}
		}
		if _, err := c.tmux(args...); err != nil {
			_, _ = c.tmux("delete-buffer", "-b", buf)
			return err
		}
	}
	if _, err := c.tmux("paste-buffer", "-p", "-d", "-b", buf, "-t", pane); err != nil {
		_, _ = c.tmux("delete-buffer", "-b", buf)
		return err
	}
	return nil
}

// chunks cuts s into parts of at most n bytes, never inside a character.
func chunks(s string, n int) []string {
	var parts []string
	for len(s) > n {
		cut := n
		for cut > 1 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		parts = append(parts, s[:cut])
		s = s[cut:]
	}
	return append(parts, s)
}

// StyledScreen is what a pane shows, with the escape sequences of its
// styles (capture-pane -e): hq send tells Claude's faint hints in its
// prompt box from what the user typed there (state.WithoutHints).
func (c Client) StyledScreen(pane string) (string, error) {
	out, err := c.tmux("capture-pane", "-p", "-e", "-t", pane)
	return string(out), err
}

// Submit presses Enter in a pane.
func (c Client) Submit(pane string) error {
	_, err := c.tmux("send-keys", "-t", pane, "Enter")
	return err
}

// Start releases the program of a window made by NewWindow.
func (c Client) Start(id string) error {
	_, err := c.tmux("send-keys", "-t", id, "Enter")
	return err
}

// Respawn runs argv afresh in an agent's own pane, in dir, ending what runs
// there; the pane stays where it is (its home window or the docking slot)
// and keeps its options, so it stays the agent's own (design §3.6).
func (c Client) Respawn(pane, dir string, argv []string) error {
	_, err := c.tmux(append([]string{"respawn-pane", "-k", "-t", pane, "-c", dir, "--"}, c.session(argv)...)...)
	return err
}

// session is the command line of an agent's pane that runs argv, its
// session.
func (c Client) session(argv []string) []string {
	if len(c.Session) == 0 {
		return argv
	}
	return append(append([]string{}, c.Session...), argv...)
}

// agentPane marks target, an agent's pane, as the agent's own: its id, so it
// is found when docked, and what must travel with it when it moves between
// windows. It stays readable after its process ends (S7): the pane is kept,
// and it has no alternate screen, so what Claude drew there is not thrown
// away when Claude leaves it; passthrough lets the agent's notifications
// (design §3.5) out of a window nobody looks at.
func agentPane(target, id string) []string {
	cmds := []string{"set-option", "-p", "-t", target, "remain-on-exit", "on",
		";", "set-option", "-p", "-t", target, "alternate-screen", "off",
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
				if err := c.undock(id, KilledHint(name)); err != nil {
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
	out, err := c.read("display-message", "-p", "#{socket_path}")
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
	out, err := c.read("select-window", "-t", id, ";", "list-clients", "-t", Session, "-F", "#{client_tty}")
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(out)), nil
}

// Leave takes every client off hq's session (design §3.11): a client that
// switched to it from another session of this server goes back there and
// is shown message on its status line; any other is detached, so its
// terminal gets back the shell it had before hq, full height.
func (c Client) Leave(message string) error {
	out, err := c.read("list-clients", "-t", Session, "-F", "#{client_name}"+sep+"#{client_last_session}")
	if err != nil {
		if isNoServerOrSession(err) {
			return nil
		}
		return err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name, last, ok := strings.Cut(line, sep)
		if !ok || name == "" {
			continue
		}
		if last != "" && last != Session {
			if _, err := c.tmux("has-session", "-t", "="+last); err == nil {
				if _, err := c.tmux("switch-client", "-c", name, "-t", "="+last, ";",
					"display-message", "-c", name, "-d", "4000", strings.ReplaceAll(message, "#", "##")); err != nil {
					return err
				}
				continue
			}
		}
		if _, err := c.tmux("detach-client", "-t", name); err != nil {
			return err
		}
	}
	return nil
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
