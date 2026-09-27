package cli

import (
	"fmt"
	"strings"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/dash"
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
	exe, err := d.executable()
	if err != nil {
		return envErr("cannot find the running hq: %v", err)
	}
	dir, err := d.getwd()
	if err != nil {
		return envErr("%v", err)
	}
	list := []string{exe, listCommand}
	w, err := d.tmux.Dashboard(dir, list)
	if err != nil {
		return tmuxErr(err)
	}
	socket, err := d.tmux.SocketPath()
	if err != nil {
		return tmuxErr(err)
	}
	mode := goMode(d.getenv("TMUX"), socket)
	if mode == goSwitch && d.getenv("TMUX_PANE") == w.List {
		return runList(env, d, nil)
	}
	if !w.Started && (w.ListPID == 0 || !d.alive(w.ListPID)) {
		if err := d.tmux.RespawnList(w.List, list); err != nil {
			return tmuxErr(err)
		}
	}
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
			return agent.Collect(ws, d.readState, running), nil
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
			b.WriteString("   ")
		}
		fmt.Fprintf(&b, "#[fg=colour255,bold]%s#[default] %s", escapeFormat(h.Key), escapeFormat(h.Label))
	}
	return b.String()
}

// escapeFormat keeps text literal inside a tmux format.
func escapeFormat(s string) string { return strings.ReplaceAll(s, "#", "##") }
