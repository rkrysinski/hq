# 0007 - the dashboard is a tmux composition; docking swaps the agent's real pane

The spec requires the docked session to be the agent's real, fully interactive session (§6.1, S3), and the mocks show a list on top, the session below it with a title frame, a key footer along the bottom of the window, and dialogs centered over both. The dashboard is therefore one tmux window built from tmux's own parts: the list is a program in the top pane; the docking slot is the bottom pane, and docking swaps the agent's own pane into it (`swap-pane`), sending the previously docked pane back to the agent's home window; an empty slot holds a placeholder, a pane that shows a hint and takes no commands (not a shell, #66); the frame and its title are tmux pane borders; the footer is tmux's status line, set by the list program; dialogs are tmux popups over the whole window. Quitting (`q`) ends the list program and takes the terminal off hq's session: a plain terminal is detached back to its own shell, a client that came from another session of the same tmux server goes back there; the docked pane stays in the slot, untouched, and `hq` restores the same layout (S8; amended by #67, which replaced "quitting the list returns its pane to a shell", a pane that left a few live lines over a stale docked pane).

## Considered Options

- **Nested tmux client in the slot** (`tmux attach` to the agent inside the dashboard): nested prefix keys and mouse break, and two clients fight over sizes.
- **Terminal emulation inside the list program** (render the agent's pane content itself): a copy of the session, not the session; contradicts §6.1.
- **List and session in separate tabs or windows**: contradicts the layout of §6.1 and the mocks.

## Consequences

- Everything visible in the mocks depends on tmux drawing the window: status line, pane-border titles and popups. A terminal front end that replaces tmux's drawing (iTerm2's `-CC` integration) cannot show them; see the platform decision that follows this one.
- Requires a tmux version with popups and pane-border titles on both platforms.
- Desktop notifications reach the terminal through tmux (ADR 0010), so after `q` no terminal is attached to hq's session and none are shown until `hq` is run again. Accepted (#67): the user wants the terminal back on `q`; running `hq` again or keeping the dashboard open is how notifications keep coming.
