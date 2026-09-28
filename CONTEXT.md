# hq

One console for many Claude Code agents running in sandboxes and tmux. The glossary of this project's domain: the terms the code, the issues and the documents use, each with the words to avoid for it.

<!-- One entry per domain term, added when the term is settled - not upfront.
     Group entries under `###` subheadings once clusters emerge.

**Term**:
One or two sentences saying what it is, not what it does.
_Avoid_: synonym, another synonym
-->

## Language

**Agent**:
One Claude Code session started through hq, known by a short name unique among existing agents (running or `ended`) until it is killed.
_Avoid_: tab, session, worker

**Dashboard**:
The hq tmux window: the list on top, the docking slot below, the footer along the bottom.
_Avoid_: TUI, console window

**Docking slot**:
The bottom pane of the dashboard; it holds the docked agent's own pane or the placeholder.
_Avoid_: preview, viewer

**Docked session**:
The agent pane currently in the docking slot, live and interactive.
_Avoid_: preview, attached agent

**Home window**:
The hidden tmux window that holds an agent's pane while it is not docked; its existence is the agent's existence.
_Avoid_: tab, agent window

**Placeholder**:
What the docking slot shows when no agent is docked: a hint, not a shell; it takes no input and sends the keys back to the list.
_Avoid_: placeholder shell, empty pane, dummy

**Cursor row**:
The selected row of the list; only it shows the action strip. May differ from the docked row.
_Avoid_: selection, highlighted row

**Attention state**:
`question` or `needs input`: the agent is waiting for the user.
_Avoid_: alert, blocked

**Message**:
Text the user leaves for an agent with `hq send`, delivered to it when it is ready; it waits in the agent's inbox until then.
_Avoid_: feedback, note, mail

**Supervisor**:
A Claude Desktop conversation that starts, watches and messages agents through hq's MCP tools (`hq mcp`); an MCP client, not an agent, with no row of its own.
_Avoid_: sup agent, orchestrator, manager
