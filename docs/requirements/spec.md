# hq - one console for many Claude Code agents

This document is the requirements specification for the whole product, to be implemented from scratch in the milestones of section 12. It describes WHAT hq does, as observable behaviour, and the givens it must live with. It deliberately says nothing about HOW (languages, libraries, file formats, internal mechanics); that is the implementer's design. Design reference for the look: `docs/requirements/look-and-feel.pen`, one frame per scenario (section 13).

## 1. Goal

The user runs several Claude Code agents in parallel, in different repositories, and today has no consolidated view of them. hq gives one terminal window that shows every agent regardless of project, tells at a glance which ones need the user, lets the user enter any agent's session with one action, and start, stop and inspect agents from the same place or from any shell.

## 2. Givens (the platform hq is built for)

These are facts about the environment, not design choices. hq is designed with them, not against them.

- Each agent is a Claude Code session running inside a Docker Sandboxes (`sbx`) microVM. One sandbox per repository, shared by all agents of that repository.
- From inside the sandbox nothing on the host is reachable: no host binaries, no host sockets, no host home directory. The only channel out is the repository directory mounted into the sandbox.
- Agent sessions live in tmux. On macOS the user works in iTerm2; on Windows the team works in Windows Terminal with WSL (Ubuntu). In both, hq's dashboard is plain tmux inside the terminal. Both must be supported with identical behaviour.
- Claude Code creates git worktrees and branches itself. hq never creates or manages worktrees, branches or pull requests.
- Claude Code can run user-provided hooks on its lifecycle events (prompt submitted, turn finished, waiting for input, session ended); this is how an agent's state can be observed from outside.
- Both terminals can show a desktop notification triggered by the terminal output stream.

## 3. Concepts

- **Agent**: one Claude Code session started through hq, identified by a short **name** (typically the issue number or a slug, e.g. `42`, `bok-17`). A name is taken while its agent exists, running or `ended`, until the agent is killed.
- **Repo**: the repository the agent works in. **Worktree/branch**: where the agent actually works; the branch is the primary identifier in status output.
- **State**: `starting`, `working`, `question`, `needs input`, `done`, `ended` (definitions in 5).
- **Attention state**: `question` or `needs input` - the agent is waiting for the user.
- **Dashboard**: the hq window with the agent list and the docked session.
- **Docked session**: the one agent session currently shown live and interactive below the list. **Cursor row**: the row currently selected in the list. They are the same row: the session slot follows the cursor (6.3).

## 4. Command line

All operations are available from any shell; the dashboard reflects them within a second.

### 4.1 Commands

- `hq` / `hq dash` - open the dashboard (create it if needed, otherwise return to it, with the previously docked session).
- `hq new NAME [DIR] [PROMPT]` - start an agent for the repository in DIR with an optional first prompt (at most about 16 KiB; a longer one is refused, saying by how much). The second argument is DIR when it is an existing directory, otherwise it is the PROMPT (so `hq new 42 "work on issue #42"` works from inside the repo). DIR defaults to the current directory and must be a git repository. The repository's sandbox is reused if it exists, created otherwise; a freshly created sandbox needs a one-time Claude login, which the user does in the docked session. Fails if an agent named NAME already exists (running or `ended`, until killed).
- `hq ls` - list agents, one per line, columns `NAME REPO BRANCH STATE AGE LAST`, in attention order (see 6.2). `--json` gives the same as a machine-readable list, with how many messages wait for each agent (`hq send`), its own tasks (5), what it asks (as `hq read --json` gives it) and, while it is `needs input`, the open dialog's id (`hq answer`), and the moment of the list to pass to `hq wait --since` (see `hq wait`).
- `hq read NAME` - show that agent in full, without docking it: its name, repository, branch, worktree, state and age as `hq ls` shows them; its own tasks (5); how many messages wait for it (`hq send`); its complete last reply; while it is `needs input` or `question`, what it asks (each question with its options, the tool and command a permission is for, or an MCP server's form: the server and its message, `elicit asks: Which color should I use?`, then each field in the form's order with its title, whether it is required, its options (or its type when it has none), its default and its description, `color (Color, required): red | blue - Pick one`, each line cut as `LAST` is; for a form that sends the user to a page, the server, its message and the page's URL, the URL in full however long, since cut it would hide where it sends the user), and at a dialog the dialog's id, with the `hq answer` command for it while hq can answer it. `--json` gives the `hq ls --json` fields plus these, and the moment of the read as `hq ls --json` does. An unknown NAME fails as `hq go` does. A reply longer than 32 KiB comes in pages of 32 KiB, never cut short: the read says the whole reply's size and where the next page starts, and `--offset N --since TIME` (N where a page starts, TIME the moment of the read that gave it) gives that page; if the agent has replied anew since TIME, the page is refused, saying the reply changed, rather than mixing two replies. However long the reply, the agent's turn ends as any other (`done` or `question`, notification, `hq wait`, `hq send`) and `LAST` is one line of it. A report too large for hq to read at all (over 16 MiB) is never silent: the agent counts as `done`, and `LAST` and the reply say the report was too large, with its size.
- `hq wait [NAME...] [--since TIME] [--timeout DURATION] [--json]` - wait until one of the named agents (every agent when none is named) enters `done`, `question`, `needs input` or `ended` after TIME, then return its row as `hq ls` shows it, with the row of every other that does so within a moment of it, so agents that finish together come back together; while agents only work, it keeps waiting. It returns within a second of the change (an agent whose session was cut off, as when its sandbox stopped, within 2 s as in 10). TIME defaults to the moment the call starts, so an agent already in such a state then is not returned. The output carries the moment to pass as the next call's `--since`: a change that happened between two calls is returned by the next call, and none is returned twice. TIME is such a moment, or a duration back from now (`10m`). `hq ls --json` carries such a moment too, the one it was taken at, in the same field: a supervisor watching passes the moment of its most recent `hq wait` or `hq ls --json` as `--since`, so a change after that result is returned even when it happened before `hq wait` was called (after `hq send`, the agent's reply, however quickly it came). The moment of a `hq wait` stands for the agents it waited on. `hq read --json`, `hq send --json` and `hq answer --json` carry a moment in the same field that is that one agent's: `hq wait NAME --since` from it returns that agent's next change (after `hq send`, its reply), but a change of another agent before it is lost, so a watch never takes its `--since` from them. A `hq wait` from its own start sees only what changes after it starts. After DURATION (default 50 s, suited to MCP clients; `0` waits with no limit) it returns "nothing yet" (an empty list with `--json`), which is not an error. A NAME that does not exist fails as `hq go` does; an agent killed while waited on is returned as `ended`. It agrees with `hq ls` and the dashboard, however many hq processes look at once, and leaves nothing running when it returns or is interrupted (Ctrl-C).
- `hq go NAME` - dock that agent's session and bring the dashboard to front, opening the dashboard if it is not open. Called with no terminal (as by the supervisor) and no dashboard open anywhere, it docks the agent and says to run `hq` in a terminal to see it.
- `hq code NAME` - open the editor (VS Code) on the agent's worktree, so the editor shows the agent's branch. Before the worktree is known, opens the repository. Works on macOS and from WSL. The agent keeps running; editor and agent see the same files.
- `hq send NAME TEXT [--now] [--json]` - leave a message for that agent (TEXT one argument, at most 8 KiB), delivered when the agent is ready, never pulling it away from its work (5.1); `--now` asks for it within the running turn. Prints how the message goes (`queued: 42 is working, delivered when it stops`, `delivered: typed into 42 as its next prompt`); `--json` gives it with the moment before the message was left, for `hq wait NAME --since`, so the agent's reply always counts as after it. Refused for an `ended` agent, and for an agent started by an older hq that cannot receive messages, naming the remedy (relaunching it).
- `hq answer NAME --dialog ID ANSWER... [--json]`, `hq answer NAME --dialog ID --cancel [--json]` - answer that agent's question dialog (AskUserQuestion) on the user's word, with the same effect as picking the answer at the agent, while the dialog stays open and answerable there the whole time; or cancel it as Esc does, which ends the turn (`done`, `User declined to answer questions`). ID is the open dialog's id, six hex characters, as `hq read` shows it: an answer can never land on a dialog that opened after the user read. One ANSWER per question, in the dialog's order: an option's number as `hq read` lists it, numbers joined with commas where the question allows several, or any other text as an answer in the user's own words. The whole answer is checked against the dialog before anything reaches the agent and refused whole on any fault (a wrong count, an unknown option, several options where one is allowed, empty text). It returns once the agent has left the dialog and prints its state then (`answered: 42 is working`, `cancelled: 42 is done`); `--json` gives the state with the moment before the answer was left, for `hq wait NAME --since`. Refused, with the reason and nothing reaching the agent: a dialog no longer open (answered at the agent first), with the agent's state; a missing or wrong ID, naming the open dialog's id; a permission prompt, an MCP elicitation and a dialog of Claude Code's own, which are answered at the agent; an agent at no dialog, or in `question` (answered with `hq send`); an `ended` agent; and an agent started by an older hq, naming the remedy as `hq send` does. Fails, naming the agent's state: when the agent does not take the answer within 3 s; when it has taken it but not left the dialog within 5 s, naming the likely cause, a sandbox template whose own permission hook answers AskUserQuestion by itself (recreate the sandbox from a template whose hooks leave it alone); and when what the agent reports it got differs from what was sent, saying what it got. hq types nothing into the dialog. The `needs input` notification, whose turn it is (5) and a message waiting for the dialog to close (5.1) are as when the dialog is answered at the agent.
- `hq kill NAME` - end that agent's Claude session after confirmation (`-y` skips it). The sandbox stays.
- `hq stop` - end all agents after confirmation (`-y` skips it). Sandboxes stay.
- `hq sandbox rm REPO` / `hq sandbox restart REPO` - remove or restart a repository's sandbox after confirmation (`-y` skips it; REPO may also be the sandbox's own name as `sbx ls` shows it; restart is the remedy after laptop sleep, S9). `rm` refuses while agents of that repo are running; `restart` ends every session in the sandbox and relaunches each agent, continuing its conversation.
- `hq mcp` - serve `new`, `list`, `read`, `wait`, `send`, `answer`, `go` and `kill` as tools to an MCP client over its standard input and output (Phase 2 below); Claude Desktop starts it and ends it, the user never types it. Each tool gives what the command of the same name gives, with the same errors; `list` is `hq ls`, `wait` requires its `since` (the moment of the supervisor's most recent `wait` or `list`, see `hq wait`), and `kill` takes the user's confirmation with the call (`confirmed`) in place of `-y`: without it nothing ends and the supervisor is told to ask the user. `answer` takes the dialog's id, one answer per question (option numbers, or text) or a cancel, and the user's word the same way (`confirmed`): without it nothing is answered and the supervisor is told to ask the user, naming the agent and its question. `hq stop` and `hq sandbox` are not offered.
- `hq mcp install` - add hq to Claude Desktop's configuration as an MCP server, on macOS and on Windows (where Claude Desktop starts hq in WSL). Running it again changes nothing; every other setting and server in the configuration stays as it was, and a copy of the file as it was is kept next to it. When the configuration cannot be found or edited safely, it changes nothing and prints the entry to add by hand. Claude Desktop is restarted by the user to load it.
- `hq update` - replace hq with the latest released version, printing the version before and after. Running agents are not affected. Needs no GitHub login.
- `hq help`, `hq --version` (also says when a newer version is released).

### 4.2 Conventions

- Names: letters, digits, `-`, `_`, at most 32 characters; unique among existing agents, running or `ended`. Reusable once the previous agent with that name is gone (killed). A name over the limit is refused with an error naming the limit.
- Exit codes: `0` success, `1` usage or refused (duplicate name, `sandbox rm` with running agents), `2` not found (agent, repo, sandbox), `3` environment (tmux or sbx unavailable). Declining a confirmation is not an error: nothing changes and the exit code is `0`; with no terminal to confirm on and no `-y`, the command is refused (`1`).
- Errors are one line on stderr, prefixed `hq:`, and name the remedy where there is one (`hq: no agent 'x' (see hq ls)`).
- Every command works the same from inside the dashboard's own terminal and from any other shell.
- When a newer version of hq is released, a command the user runs in a terminal ends, after its own output, with one line on stderr: `hq v0.4.0 is available - run hq update`. Not with `--json`, not when stderr is not a terminal, not in `hq mcp` (its output is for the MCP client), not in `hq update` or `hq --version` (which says so in its own output), and not in the dashboard, whose header has its own hint (6.1). A development build never says it.
- hq looks for new releases at most once a day and in the background: no command waits for the answer, and none is slowed down, fails or prints anything because GitHub is slow or unreachable; what a check finds shows from the next command on.

## 5. Agent state

Observable definitions; the mechanism is the implementer's choice, within the givens.

- `starting` - agent launched, its session not started yet (Claude still starting in the sandbox).
- `working` - the agent is processing a prompt, or its turn has ended while subagents it started in the background still run: Claude then goes on by itself when they finish, so the agent is not finished yet (see *Background work* below).
- `question` - the agent finished a turn with a direct question to the user.
- `needs input` - the agent is waiting for a permission or an input dialog, or for a dialog Claude Code itself shows in place of its prompt box: when a session starts, before it takes any prompt (such as its question about the default permission mode), or after a turn (such as its confirmation of an effort change, or the model picker). Once such a dialog of Claude's own is answered, the agent is again as it was before it.
- `done` - the agent finished a turn without asking anything and with no background subagent still running, or the user ended its turn at the agent: interrupted it (Esc), cancelled its dialog or refused a permission. The last message then says so (`Interrupted`, `User declined to answer questions`). Also a session that has started and waits at its prompt for the user's first prompt: an agent started without a PROMPT (last message `-`), a session resumed or started over (`/clear`), which keep their last message.
- `ended` - the session is gone (exited, crashed, sandbox stopped).

Requirements:

- A state change is visible in the dashboard within 1 second.
- Each state carries the time since it was entered (`3s`, `2m`, `1h`) and the agent's last message, one line, truncated.
- Entering `question`, `needs input` or `done` triggers exactly one desktop notification per event, whether or not that agent is docked, while the dashboard is open in a terminal (S8); the `done` and `question` of a supervised turn trigger none (*Supervised turns* below). Notifications name the kind and the branch (`Question: feat/42-...`, `Needs input: ...`, `Done: ...`). `working`, `starting` and `ended` never notify, and neither does a turn the user ended at the agent (interrupted, a dialog cancelled or a permission refused) or a session that has just started: the user is already there, or has just started it.
- **Background work.** `done` means "come and look". A turn that ends by itself while subagents the agent started in the background still run leaves the agent `working`: no notification, `hq wait` does not return, the time keeps counting from the user's prompt, and the last message is that turn's. Each subagent that finishes may wake the agent for a turn of its own; every such turn that ends with subagents still running leaves it `working` (only the last message changes), and the turn that ends with none running makes it `done`, with exactly one notification. Every running background task Claude reports at the turn's end counts (subagents, workflows, and shell commands and watches its subagents started), except shell commands and watches (monitors) the agent itself started, including the artifact watch Claude arms when the agent publishes an artifact: an agent that left a dev server running would never be `done`, and neither would one that published an artifact, whose watch runs until its session ends; so an agent waiting only on a background shell command of its own is `done`, and `working` then `done` again when Claude takes a turn at the command's end, and an agent whose own watch alone runs is `done`, as is every later turn of its session that leaves only the watch running. It is plain `working` everywhere: no state of its own, no marker in the row, nothing extra in `hq ls`, `hq read` or the supervisor's tools. The agent's own tasks show, without changing its state (below). hq sets no time limit while background work runs: the agent stays `working` until its next turn ends or its session is gone. In detail:
  - A turn that ends with a direct question to the user is `question`, and notifies, even with subagents running; afterwards the state follows the latest turn end, so the closing turn may replace it with `done`.
  - A turn the user ended at the agent (interrupted, a dialog cancelled, a permission refused) is `done` without a notification, whatever still runs; when the subagents finish, the closing turn shows `working`, its time counting from when Claude took it up (from the interrupted prompt when no hq was looking during that turn), then `done` with a notification.
  - A session that starts, is resumed or started over (`/clear`), or relaunched by `hq sandbox restart`, is `done` as always: the subagents of the session before are forgotten. One that outlives `/clear` and wakes the new session shows as it happens: `working`, then `done` with a notification.
  - Subagents that finish at almost the same moment are no exception: Claude takes a turn for each, and the agent is `done`, with its one notification, at the end of the last of those turns, although nothing runs any more at the end of the earlier ones.
  - Should Claude not take the turn it owes for finished background work, the agent is `done` a few seconds after its turn's end, with that turn's message and without a notification, so it never stays `working` with nothing running.
  - A shell command a subagent started in the background is background work while it runs. A subagent that started one and stopped is reported finished, with an interim report, and Claude wakes it again when the command ends: the agent stays `working` through that report, with its message, and is `done`, with one notification, at the end of the turn after the subagent's final report, once nothing of it runs. Nothing tells whether a subagent waits on its command or left it running (a dev server): an agent whose subagent left a server running stays `working`, with the subagent's last message, until the server ends. Chosen over a `done` while the work goes on, which would recur at every wait for CI. A watch a subagent keeps on a command or an event counts the same.
  - An agent started by an hq older than this rule keeps the old behaviour (`done`, with a notification, at every turn end) until it is relaunched (`hq sandbox restart`).
  - **Own tasks.** The background shell commands and the watches (Monitor tool) the agent itself started that still run at the turn end its `done` or `question` comes from are its own tasks: not background work, so its state, its notification (text included) and `hq wait` are as above, but shown beside the state, since Claude takes another turn when each ends. A shell shows by its command, a watch by its description. A command or watch a subagent started is never one (it is background work), nor is the artifact watch Claude arms when the agent publishes an artifact. `hq read` lists them one per line after the state (`own task: shell  sleep 120`, on one line, cut as `LAST` is); `hq ls --json`, `hq read --json`, `hq wait --json` and the supervisor's `list`, `read` and `wait` carry them in `own_tasks`, each with its type (`shell` or `monitor`) and its command or description in full, `[]` when none; the dashboard row and `hq ls` start `LAST` with their count in Claude Code's words, dim in the row (`1 shell · Waiting for the background task...`, `2 shells · ...`, `1 shell, 1 monitor · ...`), while `last` in the JSON stays the message alone, and where the row drops `LAST` (S12) the count goes with it. They show only while the agent is `done` or `question` from that turn end: nothing is listed while it works on a new prompt, at a dialog, `ended`, after a turn the user ended at the agent (Esc), or at a session's start (new, resumed, `/clear`, relaunched), whatever still runs. Documented limitations: the list is that turn end's, refreshed by the turn Claude takes when a task ends, so a task that ended without a turn (stopped by the user) stays listed until the next turn end; a dev server may never end; and an agent started by an older hq keeps its old behaviour until it is relaunched (`hq sandbox restart`): one started by an hq older than 0.6.0 may list a command or watch one of its subagents started, and one started by hq 0.6.3 or older shows a watch it keeps with the Monitor tool as a shell, by its command (`1 shell · `).
- **MCP elicitations.** An MCP server's form (an elicitation, a question dialog of the agent's own or of a background subagent's) makes the agent `needs input` as soon as Claude Code draws it, with one notification, and its message as the last message; `hq read`, `hq ls --json` and the supervisor's tools say what it asks (4.1). The form is drawn at the agent exactly as without hq, and is answered there: hq answers no elicitation. Accepting, declining or cancelling it at the agent (Esc included) leaves the agent `working` at once, unlike cancelling another question dialog, which ends the turn (`done`, above): the MCP tool gets the answer and Claude goes on, so the turn then ends as usual (`done` or `question`, one notification). A form that sends the user to a page (a URL) is the same, and shows its message and the URL. A subagent's form reported after the agent's turn ended with that subagent running is the subagent's, as any subagent's dialog. Documented limitations: a background subagent's elicitation while the agent is still in its own turn shows as the agent's own; another hook in the user's own settings that answers an elicitation before Claude Code draws it leaves hq having notified for a form never drawn, which the end of the MCP tool closes; answering an elicitation from hq waits for Claude Code to draw the form while its hooks run ([anthropics/claude-code#103115](https://github.com/anthropics/claude-code/issues/103115)); and an agent started by hq 0.6.3 or older reports an elicitation as before until it is relaunched (`hq sandbox restart`): `needs input` from Claude's own notification about 6 s after the form is drawn, with `Claude Code needs your input` and nothing of the form, and a form that sends the user to a page not at all.
- **Supervised turns.** A turn whose every prompt came from the supervisor, that is through `hq mcp` (`new` with a prompt, or `send`; Phase 2), is a supervised turn. Its `done` and `question` trigger no desktop notification: they are for the supervisor, which reads the reply and answers, and tells the user. Its `needs input` (a permission prompt, a question dialog) notifies as ever: dialogs are the user's, answered at the agent or, a question dialog, through hq on their word. Only the desktop notification differs: state, colour, attention sort, `hq ls`, `hq read`, `hq wait` and the supervisor's tools are the same, and nothing marks a supervised turn. In detail:
  - It is the turn's, by where its prompts came from, not the agent's: a turn the user prompts on an agent the supervisor started notifies, and a turn the supervisor sends to an agent the user started is a supervised turn.
  - Only `hq mcp` counts: `hq new` and `hq send` from a shell are the user's and notify. Every MCP client is the supervisor; several are not told apart.
  - Any prompt of the user's in the turn makes its end notify, once: a message of the supervisor's that reaches a turn the user started (5.1, with or without `--now`), a prompt the user types or a message they send (`hq send`) during a supervised turn, and a message of the supervisor's that goes along with a prompt the user was typing (5.1).
  - Answering, refusing or cancelling a dialog on a supervised turn, at the agent or through hq (`hq answer`, the supervisor's `answer`), does not change whose turn it is: `needs input` notified, the later `done` does not.
  - The closing turns of background work are of the turn that started the work: after a supervised turn they notify nobody, after the user's they notify as above. When work of both is owed, the end notifies. A background shell command or watch the agent itself started is no background work (above): the turn Claude takes at its end, after the turn that started it has ended, notifies, whoever sent that turn.
  - After a turn of the user's that they interrupted at the agent (Esc), the next turn the supervisor sends notifies at its end like a turn of theirs. Documented limitation.
  - It is forgotten with the session: background work that outlives `/clear`, a resume or a relaunch and wakes the new session ends `done` with a notification, as above.
  - Whether the supervisor waits for the agent (`wait`) makes no difference: a supervised turn ends without a notification even when nobody is listening. The row shows the state, and the user asks the supervisor. No setting turns this off.
  - An agent started by an hq older than this rule notifies at every turn end until it is relaunched (`hq sandbox restart`).
- Two agents on the same branch are allowed; their states may then be indistinguishable. Documented limitation.
- Every agent started by hq reports its state with no setup: nothing is installed, configured or committed in the repository, and nothing per machine beyond installing hq.

### 5.1 Messages

A message left with `hq send` reaches the agent when it is ready, depending on its state:

- `working`: when the agent would finish its turn, it goes on with the message instead, and finishes after that. It stays `working` meanwhile; only the turn's final end notifies (once; not at all when the turn is a supervised turn and the message the supervisor's, 5). With `--now`, the message comes after the agent's next tool call, within the running turn, and the agent may change course; when no tool call comes, it goes when the agent would finish, as without.
- `done` or `question`, nothing typed in its prompt box: it is entered as the agent's next prompt. With something typed there (the user writing), it waits and goes along with the next prompt sent from there.
- `working` only on background subagents (5, *Background work*): the agent sits at its prompt, so the message is entered as its next prompt at once, as for `done`, or waits for the next prompt sent from a box the user is typing in. The agent stays `working` if subagents still run when that turn ends.
- `needs input`: it waits until the dialog is closed, then goes as above. A message never answers a dialog: a question dialog is answered with `hq answer` (4.1), and the message then goes as promised. At a dialog Claude Code itself shows in place of its prompt box (when a session starts, or after a turn), the message waits for the agent's next prompt after the user has answered the dialog, and `hq send` says so.
- `starting`: it waits for the session to start, then goes as above.
- `ended`: refused.

Whose turn it is afterwards (5, *Supervised turns*): a message of the supervisor's entered as the agent's next prompt starts a supervised turn, unless a message of the user's is entered with it; one that reaches a running turn, or goes along with a prompt the user sends, leaves the turn whose it was; a message of the user's makes the turn it reaches theirs.

Several messages waiting are delivered together, oldest first, and each exactly once. Messages still waiting when the agent is killed go with it; a relaunched agent (`hq sandbox restart`) still receives them. The user docked on the agent sees a message delivered when the agent would finish, or entered as its prompt, in the session.

## 6. Dashboard

### 6.1 Layout

One terminal window, split horizontally:

- **List** (top, fixed height: header + 6 rows + footer; scrolls beyond 6 agents with a `6 of 8  ▾ 2 more` hint).
- **Docked session** (bottom, the rest of the window): the live, fully interactive session of one agent. The user talks to Claude there directly. Nothing is copied or previewed; it is the real session. With nothing docked, the area shows a hint only; it is not a terminal and takes no input, and keys typed there go back to the list. The session is framed on all four sides by the surround (`#16181D`): the margins beside and below it and the footer; its title `▸ name · branch · sandbox` (or `▸ placeholder`) is at the frame's top-left.

The dashboard shows where the keys are. The side that has them, the list or the docked session, sits on the terminal's own background; the other side takes the surround's colour, and an unfocused session's text is also dimmed a little, so it reads as greyed out (cells Claude draws with its own background, such as its input box or a diff, may keep it). The session's title is in the accent (lavender) while the session has the keys and dim otherwise. The look flips whenever the keys move: `Alt+l`, a click, `⏎` docking into the session, a new agent taking the keys, returning from a dialog. The placeholder never holds the keys (they go back to the list), so it always shows as the unfocused side. Esc does not move the keys: Claude uses it to interrupt, cancel dialogs and rewind.

The terminal window is titled `hq - agents`.

Resizing the terminal window keeps the 6 list rows and gives the rest to the session. Below 24 rows the list shows 3 rows. Minimum width 80 columns; narrower terminals drop `LAST`, then `REPO`.

Header: `hq` + summary `N agents · X need you · Y done · Z working` + current view mode + clock; while `sbx` has not answered for a few seconds, a quiet `sbx ?` beside the clock says that `ended` states may be stale, and it goes as soon as `sbx` answers again. The summary always counts all agents, whatever the view. When a newer version of hq is released, the header also shows a quiet hint, e.g. `v0.4.0 available - hq update`; hq looks for new releases at most once a day (4.2).

Columns: `TAB` (name), `REPO`, `BRANCH`, `STATE` (colored dot + word), `AGE`, `LAST`. No row numbers: under attention sort rows reshuffle on every state change, so positional shortcuts would point at the wrong agent. Colors: attention amber, done green, working blue, ended grey, dark background; degrade gracefully on terminals with few colors.

The docked row is marked with an outline, the cursor row with a background. Both marks are on the same row, except while the slot shows the placeholder (S6), when only the cursor row is marked.

### 6.2 Sorting and views

Sort, cycled with `s`, current mode marked in the column header, remembered between runs:

- `attention` (default): `needs input`, `question`, `done`, `working`, `starting`, `ended`; ties by age, newest first.
- `repo`: repo name, then attention order inside a repo.
- `state`: attention order, stable by name inside a state (no reshuffling by age).

View, toggled with `a`, remembered between runs:

- **attention** (default): only agents in an attention state, plus the docked agent in any state, marked with its outline as in the all view, so the list always shows the agent whose session is on screen. Agents neither docked nor in an attention state stay hidden. Empty state, when no row qualifies, reads "nothing needs you - press a for all". The calmest possible list: at most one extra row, and it is the one on screen anyway.
- **all**: every agent, all states.

All row actions work on any visible row in either view; an agent needs no pending question to be docked.

### 6.3 Selecting

- `↑/↓` move the cursor.
- `/` then a name selects by prefix match, case-insensitive, shown in the footer as `/bo · 1 match: bok-17`; `Enter` docks the match, `Esc` clears. The `/` prefix keeps names from colliding with single-letter keys. Same names as `hq go NAME`, so the shortcut is stable across re-sorts.
- Mouse: a click on a row moves the cursor there. A click on the row that is already the cursor row does nothing.

The session slot follows the cursor: the row the cursor stops on is docked, and the keys stay in the list, so the user can look through the sessions row by row; `Enter` or `open` then moves the keys into the docked session. Holding `↓` docks only the row where the cursor stops, not every row it passes.

The cursor row is always the docked row, and only the user's own moves change them: `↑/↓`, a click on a row, `/name`, `Enter`/`open`, the dock shortcuts from inside the session (6.5), `hq go NAME`, and `n` in the dashboard (S2). Nothing else moves either: a re-sort, a refresh or a state change moves the row, and the cursor stays on it; an agent started from outside the dashboard (`hq new` from a shell, or another tool) takes neither the slot nor the cursor while an agent is docked (S2). When the docked agent goes (killed, S6), the slot shows the placeholder and the cursor moves to a neighbouring row without docking it; the user's next move docks again.

### 6.4 Row actions

The cursor row, and only the cursor row, shows a right-aligned action strip drawn over the tail of its content: `⏎ open`, `c code`, `p pr`, `k kill`. `pr` appears only when the branch has a pull request. Below 100 columns the strip shrinks to glyphs `⏎ c p ✕` and covers the tail of `AGE`/`STATE`; the footer keeps the labels. Columns never move to make room for the strip. Nothing appears on hover; the strip follows the cursor.

- `open` / `Enter`: dock this agent's session below the list, if the cursor has not docked it already, and move keyboard focus into it.
- `code` / `c`: open the editor on this agent's worktree (as `hq code`).
- `pr` / `p`: open this branch's pull request in the browser.
- `kill` / `k`: end this agent, always after the Kill dialog.

A click on a strip item fires that action at once (kill still asks).

### 6.5 Other keys

- `n`: New agent dialog.
- `s`: cycle sort. `a`: toggle view. `r`: force refresh.
- `q`: quit the dashboard and give the terminal back to the shell it had before `hq`, full height; agents keep running, `hq` brings the dashboard back with the same docked session.
- From inside the docked session, without leaving it: dock previous/next row, and dock the first row needing attention (bound to modifier-key shortcuts that do not clash with Claude Code's own keys). Switching keyboard focus between list and session uses the terminal's / tmux's own pane switching.

### 6.6 Dialogs

Every question and input in the dashboard is a centered modal dialog drawn over the list and session, never an inline prompt (the look of `sbx`'s own TUI dialogs is the reference). Common behaviour:

- Centered title, `×` top-right, buttons centered below the content; the default button is highlighted and carries `⏎`.
- `Enter` = default button, `Esc` = cancel, `Tab`/`←`/`→` move between fields and buttons, `y`/`n` also answer yes/no dialogs.
- Mouse: click a button to fire it, click `×` to close; clicks outside the dialog do nothing.
- While a dialog is open the list keeps refreshing underneath but receives no keys or clicks.

Dialogs in v1:

- **New agent** (`n`): fields `name`, `dir` (prefilled with the cursor row's repo, else the directory hq was started from), `prompt`; `Start ⏎` / `Cancel`. A duplicate name keeps the dialog open with the error under the field.
- **Kill agent** (`k`, strip `kill`): `Kill NAME (branch)?`, note "Ends the Claude session; the sandbox stays.", `No ⏎` (default) / `Yes`.
- **Stop everything** (if bound inside the dashboard): same shape, states the number of agents.

The `/name` search is the one exception and stays in the footer, because it filters the list while typing and must not cover it.

## 7. Scenarios

Trigger, what the user sees, what must be true afterwards. These are the acceptance narrative for the implementation and the manual test.

**S0. Default view** - `hq` with agents running: the list shows only agents that need the user, and the docked agent whatever its state (6.2); the header still counts all; `a` shows everything. With nothing docked and nothing needing the user, the list reads "nothing needs you - press a for all".

**S1. Start of day** - `hq` with nothing running: the dashboard opens with an empty list ("no agents yet - press n to start one, or run: hq new NAME [DIR] [PROMPT]") and an empty session slot. With agents already running: the dashboard opens showing them, with the previously docked session still docked. When nothing is docked (e.g. the docked agent was killed before the user left, S6), the dashboard opens with the cursor row (6.3) docked and focus in the list; an empty list, or a view showing no rows, keeps the empty slot. This happens only on entering hq (`hq`, `hq dash`, including coming back to a running dashboard); after that, the placeholder stays until the user moves the cursor (6.3).

**S2. New agent** - `n` in the dashboard or `hq new NAME [DIR] [PROMPT]` from any shell: the row appears at once as `starting` and becomes `working` when the agent reports; without a PROMPT it becomes `done` (last message `-`, no notification) once Claude waits at its prompt, within seconds. A new agent gets a `new` marker until its first report; in attention view it is shown for those seconds regardless of state. The cursor is always the docked agent, and only the user's own moves change them (6.3): an agent started with `n` in the dashboard is docked, gets the cursor, and focus moves to it (so a fresh sandbox can be logged in). An agent started from outside the dashboard (`hq new` from a shell, or another tool) takes neither the slot nor the cursor while an agent is docked; if nothing is docked, it is docked, gets the cursor and focus moves to it, as with `n`. Duplicate name: error, nothing created.

**S3. Select and work** - `↑↓`, a click or `/name`: the live session of the row the cursor stops on is docked below, the keys staying in the list, so the user can look from session to session. `Enter`: keyboard focus moves into the docked session. The user types to Claude directly; the list only updates that row's state and last message. Previous/next/first-needing-attention can be docked from inside the session without leaving it.

**S3b. Name search** - `/bo` shows the matches in the footer and moves the cursor to the first match, which the slot follows; `Enter` moves the keys into it.

**S4. Agent needs the user** - within 1s the row turns amber and moves up (attention sort), and one desktop notification fires whether the agent is docked or not (none for the `question` of a supervised turn, S5c). The user docks it, answers, the state returns to `working`.

**S5. Agent done** - the row turns green `done` with the last message (e.g. "PR #58 opened"). Review happens on GitHub; the row stays until killed.

**S5b. Agent waits for its subagents** - an agent told to start subagents in the background and end its turn stays blue `working`, its time counting from the user's prompt and its last message that turn's, with no notification; the user docked on it sees Claude at its prompt, and Esc there changes nothing. As the subagents finish, Claude takes a turn for each (only the last message changes while others still run). When the turn after the last one ends, the row turns green `done` and one notification fires.

**S5c. Agent done for the supervisor** - an agent the supervisor gave a prompt (through `hq mcp`: `new` with a prompt, or `send`) ends its turn: the row turns green `done`, or amber `question`, exactly as in S4 and S5, and no desktop notification fires; the supervisor reads the reply and tells the user. If the agent opens a permission prompt on that turn, one notification fires (S4); after the user approves, its `done` fires none. When the user prompts the agent themselves, or their prompt or message reaches a turn of the supervisor's, or the supervisor's message reaches a turn of theirs, one notification fires at the turn's final end.

**S5d. Agent done with its own task left running** - an agent told to start a command in the background itself (`sleep 120`, a dev server) and end its turn turns green `done` at once, with one notification, as in S5; its row reads `1 shell · ` dim before its last message, and `hq read` shows `own task: shell  sleep 120`, so the user and the supervisor know Claude will take another turn. When the command ends, the row turns blue `working`, then green `done` with one notification; once that turn ends with nothing of its own running, the count is gone. A watch the agent keeps itself shows the same way, by its description (`1 monitor · `); an artifact's watch shows nothing.

**S6. Kill** - `k`, strip `kill`, or `hq kill NAME`: Kill dialog, `No` default. On `Yes` the Claude session ends cleanly, the sandbox stays. If the killed agent was docked, the session slot becomes an empty placeholder with a hint, focus stays in the list, and the cursor moves to a neighbouring row without docking it (6.3). The same name may be reused afterwards.

**S7. Agent ends by itself** (Claude exited, sandbox stopped, launch failed): the row turns grey `ended` with the last known message, `[session ended]` when it had none, in the list and in `hq ls` alike; its output stays readable when docked; `k` removes the row.

**S8. Quit** - `q`: the dashboard closes and the terminal is back at the shell it had before `hq`, full height and scrolling normally, with nothing of hq left on screen; a hint there says how to bring the dashboard back. The docked session stays where it is and keeps working. `hq` restores the dashboard with the list and the same docked session. While the dashboard is closed (after `q`, or with the terminal detached or closed), desktop notifications are not shown.

**S9. Detach / close / sleep** - closing the terminal window or detaching leaves everything running; `hq` reattaches with the same layout. After laptop sleep, if agents fail with clock drift, `hq sandbox restart REPO` fixes it; affected rows go `ended` then `starting` and stay listed throughout (their names stay taken), a docked agent stays docked, and each keeps its branch and last message (`[session ended]` while ended when it had none, S7) until its next prompt; once its session is resumed and waits at its prompt it is `done`, without a notification.

**S10. Stop everything** - `hq stop`: one confirmation, then every agent ends; sandboxes stay.

**S11. Several agents in one repo** - same sandbox, different worktrees/branches; rows differ by branch. Same-branch agents are a documented limitation (see 5).

**S12. Narrow terminal** - at 80 columns `LAST` is dropped, the action strip is glyphs only; below 24 rows the list shows 3 rows. Everything remains operable.

**S13. Windows/WSL** - identical flows in Windows Terminal; notifications, editor opening and sandbox handling work from WSL.

## 8. Constraints

- No background daemon: the dashboard alone observes and refreshes; nothing runs when it is closed except the agents themselves.
- Dependencies limited to what the team can install with one command on macOS and in WSL.
- Repositories need no hq files or configuration; any hq state kept inside a repository is never tracked by git and needs no ignore rule.
- Everything platform-specific (macOS vs WSL) is isolated, not scattered; behaviour is identical.

## 9. Out of scope (v1)

Creating worktrees or branches, managing pull requests, PR status per branch (leave room for a column), several sessions side by side, multiple tmux sessions, anything workmux-like beyond control and visual.

## 10. Acceptance

- Manual test below passes on macOS and on a Windows 11 machine with WSL (Ubuntu 24.04), Windows Terminal and `sbx`.
- With three agents in two repos (one `needs input`, one `done`, one `working`), the list shows them in that order within 1s of each state change; `Enter` on the first docks its live session and keystrokes reach Claude.
- Resizing the window keeps 6 list rows and gives the rest to the session.
- Stopping a sandbox from outside flips its rows to `ended` within 2s of the sandbox having stopped (its sessions ended; `sbx stop` itself takes several seconds before that).
- Every attention event produces exactly one desktop notification, except the `question` of a supervised turn, which like its `done` produces none.
- An agent given a prompt through the supervisor's `new` or `send` reaches `done`, or ends its turn with a question: its row changes as ever and no desktop notification fires. Its permission prompt on that turn fires one; after the user approves, its `done` fires none. A supervised turn that leaves background subagents running ends `done` after its closing turns with no notification. A turn with a prompt of the user's and a message of the supervisor's, either way round, fires one notification at its final end; `hq new NAME DIR PROMPT` and `hq send` from a shell notify as ever.
- An agent told to start background subagents and end its turn stays `working` in the list, `hq ls` and `hq wait`, with no notification, until the turn Claude runs after the last of them has finished ends; then it is `done`, with one notification. An agent whose background subagent waits on a shell command of its own (one it started in the background and reported on) stays `working`, with no notification, through the subagent's interim report until the turn after its final report ends; then it is `done`, with one notification, and `hq wait` returns once, then. An agent told to publish an artifact with the Artifact tool and end its turn, which arms a watch on the artifact, is `done` at once, with one notification; `hq ls` shows `done` and `hq wait` returns once, and the session's later turns end `done` or `question` as usual while the watch runs. An agent told to keep a watch itself with the Monitor tool and end its turn is `done` at once the same way. An agent whose subagent waits with the Monitor tool stays `working` through the subagent's interim report, as above.
- An agent told to start `sleep 120` in the background itself and end its turn is `done` at once, with one notification, as before; `hq read` shows `own task: shell  sleep 120`, `hq ls --json`, `hq read --json`, `hq wait --json` and the supervisor's `list`, `read` and `wait` carry it in `own_tasks`, and its row and `hq ls` start `LAST` with `1 shell ·`, while `last` in the JSON is the message alone. When the command ends the agent is `working`, then `done` with one notification, `hq wait` returns once more, and once that turn's end lists nothing, `own_tasks` is empty and the prefix is gone.
- An agent told to keep a watch itself with the Monitor tool and end its turn is `done` as before, shows `1 monitor ·` and lists the watch's description; with a command of its own running beside it, `1 shell, 1 monitor ·`. An agent whose turn ends with a question while its own command runs is `question`, with one notification, and shows the command the same way.
- An agent that runs nothing in the background shows `own_tasks` empty and no prefix; so does one that published an artifact, on every turn of that session while only its watch runs. An agent working on a new prompt while its earlier command still runs shows no own task; nor does one whose turn the user interrupted (Esc), nor one just started over (`/clear`). A command or watch a subagent started is never an own task: the agent stays `working`, with no prefix.
- An agent at a question dialog is answered from the CLI with `hq answer NAME --dialog ID N` (and through the supervisor's `answer` with the user's word): it goes on as if option N had been picked at the agent, `hq ls` shows it `working`, the dialog showed and stayed answerable at the agent the whole time, and its `needs input` notified once as ever. The same for several questions, a multi-select question and an answer in the user's words; `--cancel` leaves it `done` with `User declined to answer questions`. An answer for a dialog answered at the agent first is refused with the agent's state, and a permission prompt, an MCP elicitation and a dialog of Claude Code's own are refused with the reason. Works in a sandbox whose template's own permission hooks stay silent for AskUserQuestion, and in a plain sandbox.
- An agent whose turn ends with a reply of 82,000 characters is `done` as after any turn: one notification, `hq wait` returns it, `hq send` types a message in as its next prompt; `LAST` is one line of the reply, and `hq read --offset` and the supervisor's `read` give the reply in full in 3 pages.

### Manual test

1. `hq new a ~/work/repositories/support-chatbot "say hi"`, `hq new b ~/work/repositories/support-chatbot "ask me one yes/no question and stop"`.
2. `hq`; expect `b` as `question` in the attention view; `a` to see `a` as `done` below it.
3. `/a` then `Enter`; expect `a`'s session below, type a follow-up and see Claude answer there.
4. From inside the session, dock the first agent needing attention; expect `b`.
5. `c` on `b`; expect the editor open on `b`'s worktree and branch.
6. `k` on `a`; expect the Kill dialog, `Yes` ends it, the row goes away.

## 11. Setup

- Prerequisites on the host: git, curl, tmux 3.4 or newer, Docker Sandboxes (`sbx`), VS Code with `code` on PATH. macOS: iTerm2. Windows: Windows Terminal, WSL (Ubuntu 24.04 or newer) with the same tools; `sbx` is the Windows binary, reachable from WSL. Windows Terminal's bell is set by the user to flash the taskbar (`bellStyle` including `taskbar`): as installed it does not, and the flash is the desktop notification on Windows (5).
- `sbx` is set up by the user before hq can start an agent: signed in (`sbx login`) and with its network policy chosen (`sbx policy init`), once per machine. Until then `hq new` fails with `sbx`'s error and its remedy (4.2).
- The user may choose what hq creates a repository's sandbox with: an image (an `sbx` template) and MCP servers registered with `sbx`, set once per user and overridable for one command. Unset, a sandbox is created with `sbx`'s defaults. They apply only at creation; an existing sandbox is used as it is. hq does not fetch or check the image: when `sbx` refuses the image or a server, `hq new` fails with `sbx`'s error and names the settings it used and where they were set.
- Windows: `sbx` is installed on Windows, not inside WSL, and is seen from WSL sessions opened after it was installed. `sbx` needs an x86-64 machine with the Windows Hypervisor Platform; hq itself runs on WSL 1 and WSL 2, and also where `sbx` cannot start a sandbox (ARM64 Windows, a virtual machine without nested virtualization); there `hq new` fails with `sbx`'s error and `sbx diagnose` says why.
- Optional: GitHub CLI (`gh`, logged in), only for the dashboard's pull requests (`pr`, 6.4). Without it, or logged out, hq works the same and simply shows no pull request.
- hq is installed once per machine with one command, from the public releases and without a GitHub login, and is available on PATH as `hq`; there is no service to start. `hq update` brings it to the latest release.
- Repositories need no preparation: any repository with a sandbox can host agents as it is.
- The supervisor (Phase 2) needs Claude Desktop on the same machine (on Windows, the Windows app with hq in WSL) and one command, `hq mcp install`, then a restart of Claude Desktop. Running it again after moving hq keeps the entry right.

## 12. Milestones

Each milestone is usable on its own and is the acceptance boundary for that step.

- **M1 - CLI**: `hq new`, `ls` (no state yet, only `running`/`ended`), `go`, `kill`, `stop`, `sandbox`, `help`, `--version`, `update`; conventions of 4.2; both platforms. Deliverable: agents can be started, listed, entered and ended from any shell.
- **M2 - State and notifications**: the state model of section 5, `hq ls` with `STATE AGE LAST`, desktop notifications. Deliverable: `hq ls` tells which agents need the user, and the user is notified.
- **M3 - Dashboard core**: layout (6.1), sorting and views (6.2), selecting with keys (6.3 without mouse and name search), `open` and `kill` with dialogs (6.6), `n` New agent dialog, scenarios S0-S8, S10, S11. Deliverable: the consolidated view; S0-S8 pass.
- **M4 - Dashboard extras**: row action strip with `code` and `pr` (6.4), mouse (6.3, 6.4, 6.6), `/name` search, in-session dock shortcuts (6.5), narrow layout (S12), WSL verification (S13), `hq code`. Deliverable: full acceptance of section 10.

## 13. Design reference

`docs/requirements/look-and-feel.pen` (Pencil). It is the only design reference: there are no exported images, so it cannot fall behind a copy. Its frames:

- `S0 - attention view (default)` - default attention view
- `hq dash - terminal window` - all view, action strip on the cursor row, keys on the list
- `S3 - keys in the session: typing to 42` - keys in the docked session: the session on the terminal's background, its title in the accent, the list on the surround
- `S1 - empty state (first hq)`, `S2 - new agent input (n)` (New agent dialog), `S3 - 42 docked by the cursor` (the docked row is the cursor row, keys on the list), `S3b - name search (/bo)` (the match docked), `S6 - kill confirmation (k)` (Kill dialog), `S6b - docked agent killed: placeholder below`, `S8 - after quit (q): list gone, session stays`, `S12 - narrow (80 cols): LAST dropped`

## Phase 2: supervisor over MCP

A supervisor is a Claude Desktop conversation the user tells "do this and that": it starts agents for it in the right repositories, follows their progress and gives them feedback that each agent takes into account when it is ready. Claude Desktop has no shell, so it drives hq through `hq mcp` (4.1), set up once with `hq mcp install` (11). This replaces the earlier idea of a long-lived `sup-<project>` agent per project.

- **The supervisor is an MCP client, not an agent.** It has no row, no state and no session in hq; the agents it starts are ordinary agents, listed, docked and killed as any other, and always directly reachable by the user. Only the notifications of the work it sends them differ (below).
- **Status comes from hq, never from the agent.** The supervisor learns an agent's state, branch, last reply, what it asks (a dialog's questions with their options, or an MCP server's form with its fields, 4.1), the messages waiting for it and its own tasks (5) with `list` and `read`, live and without taking the agent's time; agents are never told to write a status on a timer. A long reply comes in pages as with `hq read` (4.1): `read` takes `offset` and `since`, and gives `reply_size` and `next_offset` (null on the last page).
- **Asking an agent is a message.** The supervisor asks with `send` and reads the answer when the agent stops (5.1): its next `wait` watches from a moment before `send`, so an answer that comes before the `wait` is not lost; `--now` is a course correction inside the running turn.
- **Watching is waiting.** `wait` returns as soon as an agent is `done`, `question`, `needs input` or `ended`, with every other that is within a moment of it; called in a loop, each call passing the moment of the supervisor's most recent `wait` or `list` and naming every agent it follows, the supervisor sees every change once, including one that happened between that result and the call; what a `list` already showed, it is told to act on there, since a `wait` from the list's moment returns only what changes after it. The moment `read`, `send` and `answer` give is that one agent's, and the supervisor is told never to watch from it: a change of another agent before it would be lost. A call returns "nothing yet" before Claude Desktop gives up on a tool call. An agent returned `done` or `question` with own tasks (5) takes another turn when they end, so the supervisor waits again for that result: it is told so in its instructions and in the `wait` and `read` descriptions, with that a dev server may never end and that the list is the turn end's own.
- **Question dialogs are answered on the user's word; other dialogs stay the user's.** The supervisor answers an agent's question dialog with `answer` only for the user: when they have said in the conversation what to pick, or told it to choose for them (whether it may then pick its own recommendation is part of what they said), passing their word with the call (`confirmed`); never on its own initiative. Permission prompts, MCP elicitations and Claude Code's own dialogs stay the user's, at the agent: the supervisor never answers them and never asks the user to let it; it tells the user which agent waits. `read` and `list` give the dialog's id, and `send`'s description points to `answer` for question dialogs. A message sent meanwhile waits until the dialog is closed.
- **What the supervisor sent reports to the supervisor.** A turn whose prompts all came from it (`new` with a prompt, `send`) is a supervised turn (5): its `done` and `question` raise no desktop notification, on any agent, whoever started it. The supervisor is told: its instructions and its `new` and `send` tools say that the user is not notified when work it sent finishes or asks a question, so it waits for that work and tells the user. What its tools return, and `wait`, do not change. Whatever the user prompts, or sends a message into, notifies them as ever.
- **No push into Claude Desktop.** A tool call is a request and its answer; the supervisor acts only on a turn of its own. Without a `wait` running, the user is the clock, and nothing prompts them: a supervised turn ends without a notification even then. They see in the agent's row that it is `done` or asks a question, and ask the supervisor.
- **Ending an agent is the user's call.** `kill` ends nothing unless the supervisor passes the user's confirmation: it asks the user first, naming the agent, unless the user has just said to end it. `kill` is also marked destructive, so a client that asks before destructive tools does; hq does not rely on that. `stop` and `sandbox` are not offered.
- **Out of scope for now:** agents messaging each other (they report up in their replies and the supervisor passes on what matters), grouping the all view under supervisors, dashboard changes for the supervisor, and MCP clients other than Claude Desktop (any client that starts a server over standard input and output works the same, untested).
- **Acceptance:** from Claude Desktop on macOS, start two agents in two repositories with prompts, watch them with `wait`, read one agent mid-turn, send it a question answered at its next stop and a `--now` correction it follows within the running turn; the supervisor answers an agent's question dialog only after the user said what to pick (or told it to choose), and the agent goes on as if it had been picked at the agent; it never asks the user to let it answer a permission prompt; the agents it gave work to reach `done` or ask a question without a desktop notification, a permission prompt of theirs fires one, and the supervisor tells the user what they said. The same on Windows 11 with WSL.
