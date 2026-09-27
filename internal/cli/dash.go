package cli

import (
	"fmt"
	"strings"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/dash"
	"github.com/rkrysinski/hq/internal/dialog"
	"github.com/rkrysinski/hq/internal/tmux"
)

// listCommand is the hidden command the list pane runs (design §3.8).
const listCommand = "__list"

// quitHint is what the list pane shows after q (S8).
const quitHint = "hq dash stopped - agents keep running. Run `hq dash` here to bring the list back."

// clearScreen moves the cursor home and clears the terminal.
const clearScreen = "\x1b[H\x1b[2J"

// quitHeight is the list pane's height after q: the hint and a prompt.
const quitHeight = 3

// runDash opens the dashboard in this terminal (spec §4.1, design §3.11):
// it makes the window when missing, starts the list program when it is not
// running, then attaches, switches or refuses as hq go does. Run in the list
// pane itself, it runs the list program there.
func runDash(env Env, d deps, args []string) error {
	if len(args) != 0 {
		return usageErr("usage: hq dash")
	}
	if err := checkTmux(d.tmux); err != nil {
		return err
	}
	w, mode, inList, err := dashboard(d)
	if err != nil {
		return err
	}
	if inList {
		return runList(env, d, nil)
	}
	return show(d, w, mode)
}

// dashboard finds or makes the dashboard window and starts its list program
// when it is not running; inList is true when hq runs in the list pane
// itself, which is left to the caller. mode is how to show it from here.
func dashboard(d deps) (w tmux.Dash, mode string, inList bool, err error) {
	exe, err := d.executable()
	if err != nil {
		return w, "", false, envErr("cannot find the running hq: %v", err)
	}
	dir, err := d.getwd()
	if err != nil {
		return w, "", false, envErr("%v", err)
	}
	list := []string{exe, listCommand}
	if w, err = d.tmux.Dashboard(dir, list); err != nil {
		return w, "", false, tmuxErr(err)
	}
	if err := bindChords(d, exe); err != nil {
		return w, "", false, tmuxErr(err)
	}
	socket, err := d.tmux.SocketPath()
	if err != nil {
		return w, "", false, tmuxErr(err)
	}
	mode = goMode(d.getenv("TMUX"), socket)
	if mode == goSwitch && d.getenv("TMUX_PANE") == w.List {
		return w, mode, true, nil
	}
	if !w.Started && (w.ListPID == 0 || !d.alive(w.ListPID)) {
		if err := d.tmux.RespawnList(w.List, list); err != nil {
			return w, "", false, tmuxErr(err)
		}
	}
	return w, mode, false, nil
}

// show puts the dashboard in front of the user: attached in a plain
// terminal, switched to inside hq's server, refused inside another.
func show(d deps, w tmux.Dash, mode string) error {
	var err error
	switch mode {
	case goSwitch:
		err = d.tmux.Enter(w.Window)
	case goRefused:
		return usageErr("this shell is inside another tmux server; run hq from a plain terminal or detach first")
	default:
		err = d.tmux.Attach(w.Window, d.terminal)
	}
	if err != nil {
		return tmuxErr(err)
	}
	return nil
}

// frameTitle is the docked session's frame title: name · branch · sandbox
// (design §3.1), without the branch while it is not known.
func frameTitle(a agent.Agent) string {
	parts := []string{a.Name}
	if a.Branch != "" {
		parts = append(parts, a.Branch)
	}
	return strings.Join(append(parts, a.Sandbox), " · ")
}

// dock docks the agent named name, framed with its title.
func dock(d deps, name string) error {
	ws, err := d.tmux.Windows()
	if err != nil {
		return err
	}
	a, ok := agent.Find(agent.Collect(ws, d.readState, nil), name)
	if !ok {
		return fmt.Errorf("no agent '%s'", name)
	}
	return d.tmux.Dock(a.Window, frameTitle(a))
}

// newDialog opens the New agent dialog over the dashboard, dir prefilled
// (the directory hq was started from when empty), until it closes.
func newDialog(d deps, pane, dir string) error {
	exe, err := d.executable()
	if err != nil {
		return err
	}
	cwd, err := d.getwd()
	if err != nil {
		return err
	}
	if dir == "" {
		dir = cwd
	}
	return d.tmux.Popup(pane, cwd, dialog.Width, dialog.Height, []string{exe, newDialogCommand, dir})
}

// killDialog opens the Kill dialog on the agent named name over the
// dashboard, until it closes.
func killDialog(d deps, pane, name string) error {
	exe, err := d.executable()
	if err != nil {
		return err
	}
	cwd, err := d.getwd()
	if err != nil {
		return err
	}
	return d.tmux.Popup(pane, cwd, dialog.ConfirmWidth, dialog.ConfirmHeight, []string{exe, killDialogCommand, name})
}

// keepTitles keeps the docked session's frame title current as its branch
// changes.
func keepTitles(d deps, ws []tmux.Window, as []agent.Agent) {
	for _, w := range ws {
		if !w.Docked {
			continue
		}
		for _, a := range as {
			if t := frameTitle(a); a.Window == w.ID && t != w.Title {
				_ = d.tmux.SetTitle(w.Pane, t)
			}
		}
	}
}

// runList runs the list program in the list pane until q, then leaves the
// S8 hint in the pane's shell.
func runList(env Env, d deps, args []string) error {
	if len(args) != 0 {
		return usageErr("usage: hq %s", listCommand)
	}
	pane := d.getenv("TMUX_PANE")
	_ = d.tmux.MarkList(pane, d.pid)
	defer func() {
		_ = d.tmux.SetFooter("")
		_ = d.tmux.MarkList(pane, 0)
	}()
	err := d.runList(listSource(d, pane))
	_ = d.tmux.ResizeHeight(pane, quitHeight)
	// Clear what the shell showed before the list, so the hint stands alone.
	fmt.Fprint(env.Stdout, clearScreen)
	fmt.Fprintln(env.Stdout, quitHint)
	return err
}

// listSource connects the list program to tmux, the state files and sbx.
func listSource(d deps, pane string) dash.Source {
	return dash.Source{
		Agents: func(running map[string]bool) ([]agent.Agent, error) {
			ws, err := d.tmux.Windows()
			if err != nil {
				return nil, err
			}
			as := agent.Collect(ws, d.readState, running)
			keepTitles(d, ws, as)
			return as, nil
		},
		Dock:     func(name string) error { return dock(d, name) },
		NewAgent: func(dir string) error { return newDialog(d, pane, dir) },
		Kill:     func(name string) error { return killDialog(d, pane, name) },
		Code: func(name string) error {
			_, err := openCode(d, name)
			return err
		},
		Running: func() (map[string]bool, error) {
			sbs, err := d.pollSandboxes()
			if err != nil {
				return nil, err
			}
			running := map[string]bool{}
			for _, s := range sbs {
				running[s.Name] = s.Running()
			}
			return running, nil
		},
		Now:        d.now,
		UpdateHint: func() string { return strings.TrimSpace(updateHint(d)) },
		Layout: func() {
			if h, err := d.tmux.WindowHeight(pane); err == nil {
				_ = d.tmux.ResizeHeight(pane, dash.Height(h))
			}
		},
		Footer: func(hs []dash.Hint) { _ = d.tmux.SetFooter(footer(hs)) },
		Modes: func() (string, string) {
			p := d.loadPrefs()
			return p.Sort, p.View
		},
		Cursor: func() string {
			v, _ := d.tmux.SessionValue("cursor")
			return v
		},
		SetCursor: func(name string) { _ = d.tmux.SetSessionValue("cursor", name) },
		SaveModes: func(sort, view string) {
			p := d.loadPrefs()
			p.Sort, p.View = sort, view
			_ = d.savePrefs(p)
		},
	}
}

// footer renders key hints for tmux's status line: the key bright, what it
// does dim, as in the mocks.
func footer(hs []dash.Hint) string {
	var b strings.Builder
	b.WriteString(" ")
	for i, h := range hs {
		if i > 0 {
			b.WriteString("  ")
		}
		fmt.Fprintf(&b, "#[fg=colour255,bold]%s#[default] %s", escapeFormat(h.Key), escapeFormat(h.Label))
	}
	return b.String()
}

// escapeFormat keeps text literal inside a tmux format.
func escapeFormat(s string) string { return strings.ReplaceAll(s, "#", "##") }
