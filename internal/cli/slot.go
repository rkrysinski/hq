package cli

import (
	"fmt"
	"io"

	"github.com/rkrysinski/hq/internal/tmux"
)

// slotCommand is the hidden command of the placeholder, the pane in the
// docking slot while nothing is docked (design §3.1).
const slotCommand = "__slot"

// slotStart clears the pane, hides the cursor and asks for mouse presses
// (SGR) and focus changes, so a click in the slot, like a key, reaches the
// placeholder and sends the keys back to the list.
const slotStart = "\x1b[H\x1b[2J\x1b[?25l\x1b[?1000h\x1b[?1006h\x1b[?1004h"

// focusOut is what the terminal sends when the pane loses the focus.
const focusOut = "\x1b[O"

// runSlot runs the placeholder in its pane (S1, S6): it shows the hint and
// takes no commands.
func runSlot(env Env, d deps, args []string) error {
	if len(args) > 1 {
		return usageErr("usage: hq %s [HINT]", slotCommand)
	}
	hint := tmux.PlaceholderHint
	if len(args) == 1 {
		hint = args[0]
	}
	defer d.rawTerminal(env.Stdin)()
	return placeholder(env.Stdin, env.Stdout, hint, func() { _ = d.tmux.FocusList() })
}

// placeholder shows hint, then swallows whatever it is given (keys, a paste,
// clicks): nothing typed runs, and each time the keys go back to the list,
// whose keys are the dashboard's. It returns when its input ends.
func placeholder(in io.Reader, out io.Writer, hint string, focusList func()) error {
	if _, err := fmt.Fprint(out, slotStart+hint+"\r\n"); err != nil {
		return err
	}
	buf := make([]byte, 256)
	for {
		n, err := in.Read(buf)
		if n > 0 && string(buf[:n]) != focusOut {
			focusList()
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
