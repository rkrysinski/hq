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
The selected row of the list; only it shows the action strip. It is the docked row: the docking slot follows it, and only the user's own moves change it.
_Avoid_: selection, highlighted row

**Attention state**:
`question` or `needs input`: the agent is waiting for the user.
_Avoid_: alert, blocked

**Background work**:
Subagents or workflows an agent started in the background that still run when its turn ends; the agent is `working` until the closing turn ends. Shell commands and watches (monitors) its subagents started in the background are background work while they run. Background shell commands and watches the agent itself started are not, and neither is the artifact watch Claude arms when the agent publishes an artifact.
_Avoid_: pending tasks, children, jobs

**Closing turn**:
The turn Claude runs by itself when background work has finished; the one that ends with none running makes the agent `done`.
_Avoid_: wake-up turn, follow-up, callback

**Own task**:
A background shell command or watch the agent itself started that still runs at the turn end its `done` or `question` comes from; Claude takes a turn of its own when it ends. Not background work, and the artifact watch Claude arms when the agent publishes an artifact is not one.
_Avoid_: background work, job, pending task, running

**Message**:
Text the user leaves for an agent with `hq send`, delivered to it when it is ready; it waits in the agent's inbox until then.
_Avoid_: feedback, note, mail

**Supervisor**:
A Claude Desktop conversation that starts, watches and messages agents through hq's MCP tools (`hq mcp`); an MCP client, not an agent, with no row of its own.
_Avoid_: sup agent, orchestrator, manager

**Supervised turn**:
A turn whose every prompt came from the supervisor, through `hq mcp` (`new` with a prompt, or `send`); its `done` and `question` are for the supervisor and raise no desktop notification, while its `needs input` does. It is a property of the turn, never of the agent: one prompt of the user's makes the turn theirs.
_Avoid_: silent turn, supervised agent, muted turn

**Look**:
One reading of every agent's state at a moment: what its hooks report, what sbx says of its sandbox, and what its screen shows that no hook reports, read the same by every hq process. `hq ls`, `hq read`, `hq wait`, `hq send`, the list and the chords each take a look; a screen at rest may need a second one.
_Avoid_: collection, refresh, poll, settling

**Question dialog**:
A dialog a tool opens to ask the user to pick or type an answer, as AskUserQuestion and an MCP elicitation do; its answer continues the agent's turn. A permission prompt and a dialog of Claude's own are not question dialogs.
_Avoid_: ask, prompt, question (the state)

**Answer**:
The user's choice for an open question dialog, given at the agent or through hq (`hq answer`).
_Avoid_: message, reply, pick

**Dialog verdict**:
Whether an agent is at a dialog, whose (the one its hooks reported, Claude's own, or a subagent's), since when, and what it asks, decided in one place from what the look saw; the agent needs input exactly while it is at one. The hooks open it; the agent's screen may close it only on clear evidence (Claude at work), and opens only Claude's own.
_Avoid_: dialog state, open dialog flag, needs-input check
