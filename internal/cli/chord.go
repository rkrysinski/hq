package cli

import (
	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/dash"
)

// chordCommand is the hidden command behind the Alt chords (design §3.7,
// §3.8): tmux runs it in the background, so it tells the user what went
// wrong on the status line rather than on a terminal.
const chordCommand = "__chord"

// bindChords binds the Alt chords to this hq for hq's session.
func bindChords(d deps, exe string) error {
	return d.tmux.BindChords([]string{exe, chordCommand}, footer(dash.ChordHints))
}

// runChord docks the next or previous row, or the first agent needing the
// user, or opens the New agent dialog, whatever the list program is doing
// (spec §6.5). Docking moves the cursor there.
func runChord(_ Env, d deps, args []string) error {
	if len(args) != 1 {
		return usageErr("usage: hq %s next|previous|attention|new", chordCommand)
	}
	err := chord(d, args[0])
	if err != nil {
		_ = d.tmux.Message("hq: " + err.Error())
	}
	return err
}

func chord(d deps, action string) error {
	ws, err := d.tmux.Windows()
	if err != nil {
		return tmuxErr(err)
	}
	as := seeEnds(d, settle(d, agent.Collect(ws, d.readState, nil)))
	var docked agent.Agent
	for _, a := range as {
		if a.Docked {
			docked = a
		}
	}
	cursor, _ := d.tmux.SessionValue("cursor")
	p := d.loadPrefs()
	var name string
	var ok bool
	switch action {
	case "next", "previous":
		step := map[string]int{"next": 1, "previous": -1}[action]
		if name, ok = dash.Neighbour(dash.Arrange(as, p.Sort, p.View), docked.Name, cursor, step); !ok {
			return nil // the end of the list: nothing to do
		}
	case "attention":
		if name, ok = dash.FirstNeedingYou(as, docked.Name); !ok {
			return d.tmux.Message("hq: no other agent needs you")
		}
	case "new":
		return chordNew(d, as, docked, cursor)
	default:
		return usageErr("unknown chord '%s'", action)
	}
	if err := dock(d, name); err != nil {
		return tmuxErr(err)
	}
	return d.tmux.SetSessionValue("cursor", name)
}

// chordNew opens the New agent dialog over the dashboard, its directory the
// docked agent's repository, else the cursor row's.
func chordNew(d deps, as []agent.Agent, docked agent.Agent, cursor string) error {
	dir := docked.RepoPath
	if c, ok := agent.Find(as, cursor); ok && dir == "" {
		dir = c.RepoPath
	}
	exe, err := d.executable()
	if err != nil {
		return err
	}
	cwd, err := d.getwd()
	if err != nil {
		return err
	}
	w, err := d.tmux.Dashboard(cwd, []string{exe, listCommand})
	if err != nil {
		return tmuxErr(err)
	}
	return newDialog(d, w.List, dir)
}
