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

**Lost agent**:
An agent whose place in tmux went with the tmux server (a host restart) while hq keeps what it needs to bring it back with `hq restore`; it holds its name until restored or killed.
_Avoid_: orphan, missing agent, dead agent

**Generation**:
The number a release of hq carries for the form of what agents report and of how hq keeps it; it changes exactly when one of the two changes. An agent has the generation of the hq that last launched it.
_Avoid_: hooks version, format version, protocol version

**Outdated agent**:
An agent whose generation is not the running hq's, so hq cannot read its state; it is listed as `outdated` and can only be opened or killed.
_Avoid_: old agent, stale agent, legacy agent, agent started by an older hq

**Trace of kills**:
The user's own record of every agent hq killed: when, and who asked for it (the command line, the dashboard, or a supervisor's MCP client), shown by `hq killed`.
_Avoid_: kill log, audit log, history

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
Subagents or workflows an agent started in the background that still run when its turn ends; the agent is `working` until the closing turn ends, unless the user ended the turn at the agent or an API error ended it, which leaves it `done`. Shell commands and watches (monitors) its subagents started in the background are background work while they run. Background shell commands and watches the agent itself started are not, and neither is the artifact watch Claude arms when the agent publishes an artifact.
_Avoid_: pending tasks, children, jobs

**Claude's agents list**:
The list Claude Code draws below its footer while an agent's subagents run: `main`, then a line for each subagent, over which Claude Code's own cursor moves and from which a subagent's view opens. Not hq's list: Claude Code's cursor on it is neither the cursor nor the cursor row.
_Avoid_: agents list, agent list, background list

**Closing turn**:
The turn Claude runs by itself when background work has finished; the one that ends with none running makes the agent `done`.
_Avoid_: wake-up turn, follow-up, callback

**Own task**:
A background shell command or watch the agent itself started that still runs at the turn end its `done` or `question` comes from; Claude takes a turn of its own when it ends. Not background work, and the artifact watch Claude arms when the agent publishes an artifact is not one.
_Avoid_: background work, job, pending task, running

**Message**:
Text the user leaves for an agent with `hq send`, delivered to it when it is ready; it waits in the agent's inbox until then.
_Avoid_: feedback, note, mail

**Slash command**:
A message whose first line starts with `/`; it runs only when hq types it in as the agent's prompt, and arrives as text when the agent's hooks deliver it.
_Avoid_: command (that is one of hq's own), skill

**Held message**:
A message hq took out of the inbox and keeps there apart, out of its hooks' reach, to enter as a prompt of its own once the agent is back at its prompt; it still waits, and is not taken.
_Avoid_: queued message

**Taken**:
Said of a message once it has left the agent's inbox for Claude: handed over by its hooks, or entered by hq as its prompt; a message hq entered and then put back after a failed typing is not taken.
_Avoid_: delivered (for a message only left), read

**Stalled message**:
A message that an agent waiting at its prompt did not take within 5 s, its box not coming free or Claude not sending what hq entered, so that `hq send --wait` fails; hq leaves it where it is.
_Avoid_: lost message, failed message

**Supervisor**:
A conversation in Claude Desktop or Claude Code that starts, watches and messages agents through hq's MCP tools (`hq mcp`); an MCP client, not an agent, with no row of its own.
_Avoid_: sup agent, orchestrator, manager

**Supervised turn**:
A turn whose every prompt came from the supervisor, through `hq mcp` (`new` with a prompt, or `send`); its `done` and `question` are for the supervisor and raise no desktop notification, while its `needs input` does. It is a property of the turn, never of the agent: one prompt of the user's makes the turn theirs.
_Avoid_: silent turn, supervised agent, muted turn

**Look**:
One reading of every agent's state at a moment, from its journal: it ingests the event files its hooks left, appends what tmux says of its pane, what sbx says of its sandbox and what its screen shows that the journal does not hold yet, and folds the journal, so every hq process reads the same. `hq ls`, `hq read`, `hq wait`, `hq send`, `hq explain`, the list and the chords each take a look; a screen at rest may need a second one.
_Avoid_: collection, refresh, poll, settling

**Event**:
One thing hq learned about an agent: what it was, when, and its source - the agent's hook, its screen, tmux, sbx, or hq itself. Every event has a number from one count for the whole host, never used twice. An event of hq's own carries its basis, the number of the last event of the hook it was based on; an event hq recorded but did not act on, because another hq process recorded the same thing first (a duplicate) or a later report overtook its basis (overtaken), is set aside.
_Avoid_: record, marker, log entry

**Report**:
An event whose source is the agent's hook: what Claude's session told hq through it, as the hook wrote it in an event file and hq ingested it. Every other event is hq's own or evidence.
_Avoid_: hook event, payload, state file

**State number**:
The number of the event at which an agent's state, or the dialog it is at, last changed (`state_seq`); a report that leaves both as they were does not move it. An agent with no event behind its state has none.
_Avoid_: version, revision, timestamp

**Completion number**:
The number of the event that ended the agent's last completed turn (`completion_seq`): a turn end its hook reported as `done` or `question`, or the screen's word that the turn Claude owed after such an end, kept `working` for background work, does not come. An agent with no event behind its state has none.
_Avoid_: completed turn count, version, timestamp

**Cursor**:
What hq gives as `next_since` and takes back as `--since`: an opaque text standing for a number of the host's one count such that the look that gave it saw every change numbered up to it, so a watch that passes each result's cursor to its next call misses no change and repeats none. Not the cursor row of the list.
_Avoid_: moment, timestamp, offset, token

**Evidence**:
An event of what a look saw of an agent that no hook reports: what its screen shows, tmux's word that its pane died, sbx's that its sandbox does not run. The same evidence stands once in a journal, since the agent was last launched: of hq processes that see it at once, the first to append it wins, and evidence a later report of the hook overtook is set aside.
_Avoid_: observation, sighting, marker

**Journal**:
An agent's events in the order hq learned them, kept on the host, out of the sandbox's reach, for as long as the agent exists; the agent's state is worked out from its journal and from nothing else.
_Avoid_: history, log, event log, state file

**Question dialog**:
A dialog a tool opens to ask the user to pick or type an answer, as AskUserQuestion and an MCP elicitation do; its answer continues the agent's turn. A permission prompt and a dialog of Claude's own are not question dialogs.
_Avoid_: ask, prompt, question (the state)

**Answer**:
The user's choice for an open question dialog, given at the agent or through hq (`hq answer`).
_Avoid_: message, reply, pick

**Dialog id**:
The six hex characters that name an agent's open dialog, taken from the moment it opened; an answer names it, so it never lands on a dialog that opened later.
_Avoid_: question id, prompt id

**Dialog verdict**:
Whether an agent is at a dialog, whose (the one its hooks reported, Claude's own, or a subagent's), since when, and what it asks, decided in one place from what the look saw; the agent needs input exactly while it is at one. The hooks open it; the agent's screen may close it only on clear evidence (Claude at work), and opens only Claude's own.
_Avoid_: dialog state, open dialog flag, needs-input check

**Setup**:
A named configuration a sandbox is created with: an image, MCP servers, and a model with where it is served from. `default` is the `sandbox` object of the preferences file and what an agent gets without one; the others are under `sandboxes`. A repository has one sandbox per setup it is used with, and an agent runs in the sandbox of the setup it was started on.
_Avoid_: profile, model setting, flavour, sandbox type
