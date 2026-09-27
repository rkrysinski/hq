# 0008 - plain tmux on both platforms; no iTerm2 tmux integration for the dashboard

The dashboard (ADR 0007) needs tmux to draw the window: the status-line footer, pane-border titles and popup dialogs. iTerm2's tmux integration (`-CC`) draws the window itself and does not show these, and it would turn every agent's home window into a native tab. So on macOS hq attaches with an ordinary `tmux attach` inside an iTerm2 window, exactly as it does inside Windows Terminal on WSL: one code path, identical behaviour, and the mocks reproduced on both platforms. The cost is that scrollback and copy in the dashboard go through tmux's copy mode instead of iTerm2's native scrollback; the user remains free to use `-CC` for their own tmux sessions outside hq.

## Considered Options

- **Keep `-CC` on macOS**: needs a second, iTerm2-specific rendering of the footer, dialogs and titles; breaks identical behaviour (spec §8) and the mocks.

## Consequences

- Spec §2 describes the user's current iTerm2 setup as using the tmux integration; for hq's dashboard, iTerm2 is only the terminal. The spec wording is to be aligned (raised with the owner, not edited from the design).
- Agents' home windows stay invisible without extra machinery: the footer replaces tmux's window list.
