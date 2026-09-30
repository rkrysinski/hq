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
- `hq new NAME [DIR] [PROMPT]` - start an agent for the repository in DIR with an optional first prompt. The second argument is DIR when it is an existing directory, otherwise it is the PROMPT (so `hq new 42 "work on issue #42"` works from inside the repo). DIR defaults to the current directory and must be a git repository. The repository's sandbox is reused if it exists, created otherwise; a freshly created sandbox needs a one-time Claude login, which the user does in the docked session. Fails if an agent named NAME already exists (running or `ended`, until killed).
- `hq ls` - list agents, one per line, columns `NAME REPO BRANCH STATE AGE LAST`, in attention order (see 6.2). `--json` gives the same as a machine-readable list, with how many messages wait for each agent (`hq send`), and the moment of the list to pass to `hq wait --since` (see `hq wait`).
- `hq read NAME` - show that agent in full, without docking it: its name, repository, branch, worktree, state and age as `hq ls` shows them; how many messages wait for it (`hq send`); its complete last reply; while it is `needs input` or `question`, what it asks (each question with its options, or the tool and command a permission is for). `--json` gives the `hq ls --json` fields plus these, and the moment of the read as `hq ls --json` does. An unknown NAME fails as `hq go` does.
- `hq wait [NAME...] [--since TIME] [--timeout DURATION] [--json]` - wait until one of the named agents (every agent when none is named) enters `done`, `question`, `needs input` or `ended` after TIME, then return each such agent's row as `hq ls` shows it; while agents only work, it keeps waiting. It returns within a second of the change (an agent whose session was cut off, as when its sandbox stopped, within 2 s as in 10). TIME defaults to the moment the call starts, so an agent already in such a state then is not returned. The output carries the moment to pass as the next call's `--since`: a change that happened between two calls is returned by the next call, and none is returned twice. TIME is such a moment, or a duration back from now (`10m`). `hq ls --json`, `hq read --json` and `hq send --json` carry such a moment too, the one they were taken at, in the same field: a supervisor passes the moment of its most recent hq result as `--since`, so a change after that result is returned even when it happened before `hq wait` was called (after `hq send`, the agent's reply, however quickly it came). A `hq wait` from its own start sees only what changes after it starts. After DURATION (default 50 s, suited to MCP clients; `0` waits with no limit) it returns "nothing yet" (an empty list with `--json`), which is not an error. A NAME that does not exist fails as `hq go` does; an agent killed while waited on is returned as `ended`. It agrees with `hq ls` and the dashboard, however many hq processes look at once, and leaves nothing running when it returns or is interrupted (Ctrl-C).
- `hq go NAME` - dock that agent's session and bring the dashboard to front, opening the dashboard if it is not open. Called with no terminal (as by the supervisor) and no dashboard open anywhere, it docks the agent and says to run `hq` in a terminal to see it.
- `hq code NAME` - open the editor (VS Code) on the agent's worktree, so the editor shows the agent's branch. Before the worktree is known, opens the repository. Works on macOS and from WSL. The agent keeps running; editor and agent see the same files.
- `hq send NAME TEXT [--now] [--json]` - leave a message for that agent (TEXT one argument, at most 8 KiB), delivered when the agent is ready, never pulling it away from its work (5.1); `--now` asks for it within the running turn. Prints how the message goes (`queued: 42 is working, delivered when it stops`, `delivered: typed into 42 as its next prompt`); `--json` gives it with the moment before the message was left, for `hq wait --since`, so the agent's reply always counts as after it. Refused for an `ended` agent, and for an agent started by an older hq that cannot receive messages, naming the remedy (relaunching it).
- `hq kill NAME` - end that agent's Claude session after confirmation (`-y` skips it). The sandbox stays.
- `hq stop` - end all agents after confirmation (`-y` skips it). Sandboxes stay.
- `hq sandbox rm REPO` / `hq sandbox restart REPO` - remove or restart a repository's sandbox after confirmation (`-y` skips it; REPO may also be the sandbox's own name as `sbx ls` shows it; restart is the remedy after laptop sleep, S9). `rm` refuses while agents of that repo are running; `restart` ends every session in the sandbox and relaunches each agent, continuing its conversation.
- `hq mcp` - serve `new`, `list`, `read`, `wait`, `send`, `go` and `kill` as tools to an MCP client over its standard input and output (Phase 2 below); Claude Desktop starts it and ends it, the user never types it. Each tool gives what the command of the same name gives, with the same errors; `list` is `hq ls`, `wait` requires its `since` (the moment of the supervisor's most recent hq result, see `hq wait`), and `kill` needs no `-y` because the client asks the user first. `hq stop` and `hq sandbox` are not offered.
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
- `working` - the agent is processing a prompt.
- `question` - the agent finished a turn with a direct question to the user.
- `needs input` - the agent is waiting for a permission or an input dialog.
- `done` - the agent finished a turn without asking anything, or the user ended its turn at the agent: interrupted it (Esc), cancelled its dialog or refused a permission. The last message then says so (`Interrupted`, `User declined to answer questions`). Also a session that has started and waits at its prompt for the user's first prompt: an agent started without a PROMPT (last message `-`), a session resumed or started over (`/clear`), which keep their last message.
- `ended` - the session is gone (exited, crashed, sandbox stopped).

Requirements:

- A state change is visible in the dashboard within 1 second.
- Each state carries the time since it was entered (`3s`, `2m`, `1h`) and the agent's last message, one line, truncated.
- Entering `question`, `needs input` or `done` triggers exactly one desktop notification per event, whether or not that agent is docked, while the dashboard is open in a terminal (S8). Notifications name the kind and the branch (`Question: feat/42-...`, `Needs input: ...`, `Done: ...`). `working`, `starting` and `ended` never notify, and neither does a turn the user ended at the agent (interrupted, a dialog cancelled or a permission refused) or a session that has just started: the user is already there, or has just started it.
- Two agents on the same branch are allowed; their states may then be indistinguishable. Documented limitation.
- Every agent started by hq reports its state with no setup: nothing is installed, configured or committed in the repository, and nothing per machine beyond installing hq.

### 5.1 Messages

A message left with `hq send` reaches the agent when it is ready, depending on its state:

- `working`: when the agent would finish its turn, it goes on with the message instead, and finishes after that. It stays `working` meanwhile; only the turn's final end notifies (once). With `--now`, the message comes after the agent's next tool call, within the running turn, and the agent may change course; when no tool call comes, it goes when the agent would finish, as without.
- `done` or `question`, nothing typed in its prompt box: it is entered as the agent's next prompt. With something typed there (the user writing), it waits and goes along with the next prompt sent from there.
- `needs input`: it waits until the dialog is closed, then goes as above. A message never answers a dialog.
- `starting`: it waits for the session to start, then goes as above.
- `ended`: refused.

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

**S4. Agent needs the user** - within 1s the row turns amber and moves up (attention sort), and one desktop notification fires whether the agent is docked or not. The user docks it, answers, the state returns to `working`.

**S5. Agent done** - the row turns green `done` with the last message (e.g. "PR #58 opened"). Review happens on GitHub; the row stays until killed.

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
- Every attention event produces exactly one desktop notification.

### Manual test

1. `hq new a ~/work/repositories/support-chatbot "say hi"`, `hq new b ~/work/repositories/support-chatbot "ask me one yes/no question and stop"`.
2. `hq`; expect `b` as `question` in the attention view; `a` to see `a` as `done` below it.
3. `/a` then `Enter`; expect `a`'s session below, type a follow-up and see Claude answer there.
4. From inside the session, dock the first agent needing attention; expect `b`.
5. `c` on `b`; expect the editor open on `b`'s worktree and branch.
6. `k` on `a`; expect the Kill dialog, `Yes` ends it, the row goes away.

## 11. Setup

- Prerequisites on the host: git, curl, tmux 3.4 or newer, Docker Sandboxes (`sbx`), VS Code with `code` on PATH. macOS: iTerm2. Windows: Windows Terminal, WSL (Ubuntu 24.04 or newer) with the same tools; `sbx` is the Windows binary, reachable from WSL.
- `sbx` is set up by the user before hq can start an agent: signed in (`sbx login`) and with its network policy chosen (`sbx policy init`), once per machine. Until then `hq new` fails with `sbx`'s error and its remedy (4.2).
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

- **The supervisor is an MCP client, not an agent.** It has no row, no state and no session in hq; the agents it starts are ordinary agents, listed, docked, notified and killed as any other, and always directly reachable by the user.
- **Status comes from hq, never from the agent.** The supervisor learns an agent's state, branch, last reply, what it asks and the messages waiting for it with `list` and `read`, live and without taking the agent's time; agents are never told to write a status on a timer.
- **Asking an agent is a message.** The supervisor asks with `send` and reads the answer when the agent stops (5.1), waiting from the moment `send` returns, so an answer that comes before the `wait` is not lost; `--now` is a course correction inside the running turn.
- **Watching is waiting.** `wait` returns as soon as an agent is `done`, `question`, `needs input` or `ended`; called in a loop, each call passing the moment of the supervisor's most recent hq result (`list`, `read`, `send` or `wait`), the supervisor sees every change once, including one that happened between that result and the call. A call returns "nothing yet" before Claude Desktop gives up on a tool call.
- **Dialogs stay the user's.** The supervisor never answers an agent's permission prompt or question dialog, and never asks the user to let it; it tells the user which agent waits. A message sent meanwhile waits until the dialog is closed.
- **No push into Claude Desktop.** A tool call is a request and its answer; the supervisor acts only on a turn of its own. Without a `wait` running, the user is the clock: the usual notifications say when an agent is done, and the user asks the supervisor.
- **Ending an agent is the user's call.** `kill` is marked destructive, so Claude Desktop asks the user before it runs; `stop` and `sandbox` are not offered.
- **Out of scope for now:** agents messaging each other (they report up in their replies and the supervisor passes on what matters), grouping the all view under supervisors, dashboard changes for the supervisor, and MCP clients other than Claude Desktop (any client that starts a server over standard input and output works the same, untested).
- **Acceptance:** from Claude Desktop on macOS, start two agents in two repositories with prompts, watch them with `wait`, read one agent mid-turn, send it a question answered at its next stop and a `--now` correction it follows within the running turn; the supervisor never asks the user to answer an agent's dialog on its behalf. The same on Windows 11 with WSL.
