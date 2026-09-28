# hq - one console for many Claude Code agents

This document is the requirements specification for the whole product, to be implemented from scratch in the milestones of section 12. It describes WHAT hq does, as observable behaviour, and the givens it must live with. It deliberately says nothing about HOW (languages, libraries, file formats, internal mechanics); that is the implementer's design. Design reference for the look: `docs/requirements/look-and-feel.pen` and its exports beside it (section 13).

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
- **Docked session**: the one agent session currently shown live and interactive below the list. **Cursor row**: the row currently selected in the list. They may differ.

## 4. Command line

All operations are available from any shell; the dashboard reflects them within a second.

### 4.1 Commands

- `hq` / `hq dash` - open the dashboard (create it if needed, otherwise return to it, with the previously docked session).
- `hq new NAME [DIR] [PROMPT]` - start an agent for the repository in DIR with an optional first prompt. The second argument is DIR when it is an existing directory, otherwise it is the PROMPT (so `hq new 42 "work on issue #42"` works from inside the repo). DIR defaults to the current directory and must be a git repository. The repository's sandbox is reused if it exists, created otherwise; a freshly created sandbox needs a one-time Claude login, which the user does in the docked session. Fails if an agent named NAME already exists (running or `ended`, until killed).
- `hq ls` - list agents, one per line, columns `NAME REPO BRANCH STATE AGE LAST`, in attention order (see 6.2). `--json` gives the same as a machine-readable list.
- `hq go NAME` - dock that agent's session and bring the dashboard to front, opening the dashboard if it is not open.
- `hq code NAME` - open the editor (VS Code) on the agent's worktree, so the editor shows the agent's branch. Before the worktree is known, opens the repository. Works on macOS and from WSL. The agent keeps running; editor and agent see the same files.
- `hq kill NAME` - end that agent's Claude session after confirmation (`-y` skips it). The sandbox stays.
- `hq stop` - end all agents after confirmation (`-y` skips it). Sandboxes stay.
- `hq sandbox rm REPO` / `hq sandbox restart REPO` - remove or restart a repository's sandbox after confirmation (`-y` skips it; REPO may also be the sandbox's own name as `sbx ls` shows it; restart is the remedy after laptop sleep, S9). `rm` refuses while agents of that repo are running; `restart` ends every session in the sandbox and relaunches each agent, continuing its conversation.
- `hq update` - replace hq with the latest released version, printing the version before and after. Running agents are not affected.
- `hq help`, `hq --version` (also says when a newer version is released).

### 4.2 Conventions

- Names: letters, digits, `-`, `_`, at most 32 characters; unique among existing agents, running or `ended`. Reusable once the previous agent with that name is gone (killed). A name over the limit is refused with an error naming the limit.
- Exit codes: `0` success, `1` usage or refused (duplicate name, `sandbox rm` with running agents), `2` not found (agent, repo, sandbox), `3` environment (tmux or sbx unavailable). Declining a confirmation is not an error: nothing changes and the exit code is `0`; with no terminal to confirm on and no `-y`, the command is refused (`1`).
- Errors are one line on stderr, prefixed `hq:`, and name the remedy where there is one (`hq: no agent 'x' (see hq ls)`).
- Every command works the same from inside the dashboard's own terminal and from any other shell.

## 5. Agent state

Observable definitions; the mechanism is the implementer's choice, within the givens.

- `starting` - agent launched, nothing reported yet.
- `working` - the agent is processing a prompt.
- `question` - the agent finished a turn with a direct question to the user.
- `needs input` - the agent is waiting for a permission or an input dialog.
- `done` - the agent finished a turn without asking anything, or the user ended its turn at the agent: interrupted it (Esc), cancelled its dialog or refused a permission. The last message then says so (`Interrupted`, `User declined to answer questions`).
- `ended` - the session is gone (exited, crashed, sandbox stopped).

Requirements:

- A state change is visible in the dashboard within 1 second.
- Each state carries the time since it was entered (`3s`, `2m`, `1h`) and the agent's last message, one line, truncated.
- Entering `question`, `needs input` or `done` triggers exactly one desktop notification per event, whether or not that agent is docked, while the dashboard is open in a terminal (S8). Notifications name the kind and the branch (`Question: feat/42-...`, `Needs input: ...`, `Done: ...`). `working`, `starting` and `ended` never notify, and neither does a turn the user ended at the agent (interrupted, a dialog cancelled or a permission refused): the user is already there.
- Two agents on the same branch are allowed; their states may then be indistinguishable. Documented limitation.
- Every agent started by hq reports its state with no setup: nothing is installed, configured or committed in the repository, and nothing per machine beyond installing hq.

## 6. Dashboard

### 6.1 Layout

One terminal window, split horizontally:

- **List** (top, fixed height: header + 6 rows + footer; scrolls beyond 6 agents with a `6 of 8  ▾ 2 more` hint).
- **Docked session** (bottom, the rest of the window): the live, fully interactive session of one agent. The user talks to Claude there directly. Nothing is copied or previewed; it is the real session. With nothing docked, the area shows a hint only; it is not a terminal and takes no input, and keys typed there go back to the list. The session sits in a slightly darker area, framed on all four sides by the lighter surround of the list, the margins and the footer; its title `▸ name · branch · sandbox` (or `▸ placeholder`) is at the frame's top-left.

The terminal window is titled `hq - agents`.

Resizing the terminal window keeps the 6 list rows and gives the rest to the session. Below 24 rows the list shows 3 rows. Minimum width 80 columns; narrower terminals drop `LAST`, then `REPO`.

Header: `hq` + summary `N agents · X need you · Y done · Z working` + current view mode + clock/refresh indicator. The summary always counts all agents, whatever the view. When a newer version of hq is released, the header also shows a quiet hint, e.g. `v0.4.0 available - hq update`; hq looks for new releases at most once a day and only while the dashboard runs.

Columns: `TAB` (name), `REPO`, `BRANCH`, `STATE` (colored dot + word), `AGE`, `LAST`. No row numbers: under attention sort rows reshuffle on every state change, so positional shortcuts would point at the wrong agent. Colors: attention amber, done green, working blue, ended grey, dark background; degrade gracefully on terminals with few colors.

The docked row is marked with an outline, the cursor row with a background. They can be different rows.

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
- Mouse: a click on a row moves the cursor there. A click on the row that is already the cursor row does nothing (docking is `Enter` or `open`).

### 6.4 Row actions

The cursor row, and only the cursor row, shows a right-aligned action strip drawn over the tail of its content: `⏎ open`, `c code`, `p pr`, `k kill`. `pr` appears only when the branch has a pull request. Below 100 columns the strip shrinks to glyphs `⏎ c p ✕` and covers the tail of `AGE`/`STATE`; the footer keeps the labels. Columns never move to make room for the strip. Nothing appears on hover; the strip follows the cursor.

- `open` / `Enter`: dock this agent's session below the list and move keyboard focus into it.
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

**S1. Start of day** - `hq` with nothing running: the dashboard opens with an empty list ("no agents yet - press n to start one, or run: hq new NAME [DIR] [PROMPT]") and an empty session slot. With agents already running: the dashboard opens showing them, with the previously docked session still docked.

**S2. New agent** - `n` in the dashboard or `hq new NAME [DIR] [PROMPT]` from any shell: the row appears at once as `starting` and becomes `working` when the agent reports. A new agent gets the cursor and a `new` marker until its first report; in attention view it is shown for those seconds regardless of state. If nothing is docked, the new agent is docked and focus moves to it (so a fresh sandbox can be logged in). Duplicate name: error, nothing created.

**S3. Select and work** - `↑↓` or `/name`, then `Enter`: the agent's live session is docked below, keyboard focus is in it, the list marks it as docked. The user types to Claude directly; the list only updates that row's state and last message. Previous/next/first-needing-attention can be docked from inside the session without leaving it.

**S3b. Name search** - `/bo` shows the matches in the footer and moves the cursor to the first match; `Enter` docks it.

**S4. Agent needs the user** - within 1s the row turns amber and moves up (attention sort), and one desktop notification fires whether the agent is docked or not. The user docks it, answers, the state returns to `working`.

**S5. Agent done** - the row turns green `done` with the last message (e.g. "PR #58 opened"). Review happens on GitHub; the row stays until killed.

**S6. Kill** - `k`, strip `kill`, or `hq kill NAME`: Kill dialog, `No` default. On `Yes` the Claude session ends cleanly, the sandbox stays. If the killed agent was docked, the session slot becomes an empty placeholder with a hint, focus stays in the list. The same name may be reused afterwards.

**S7. Agent ends by itself** (Claude exited, sandbox stopped, launch failed): the row turns grey `ended` with the last known message, `[session ended]` when it had none, in the list and in `hq ls` alike; its output stays readable when docked; `k` removes the row.

**S8. Quit** - `q`: the dashboard closes and the terminal is back at the shell it had before `hq`, full height and scrolling normally, with nothing of hq left on screen; a hint there says how to bring the dashboard back. The docked session stays where it is and keeps working. `hq` restores the dashboard with the list and the same docked session. While the dashboard is closed (after `q`, or with the terminal detached or closed), desktop notifications are not shown.

**S9. Detach / close / sleep** - closing the terminal window or detaching leaves everything running; `hq` reattaches with the same layout. After laptop sleep, if agents fail with clock drift, `hq sandbox restart REPO` fixes it; affected rows go `ended` then `starting` and stay listed throughout (their names stay taken), a docked agent stays docked, and each keeps its branch and last message (`[session ended]` while ended when it had none, S7) until it reports again.

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

- Prerequisites on the host: git, tmux 3.4 or newer, Docker Sandboxes (`sbx`), GitHub CLI (`gh`, authenticated), VS Code with `code` on PATH. macOS: iTerm2. Windows: Windows Terminal, WSL (Ubuntu 24.04 or newer) with the same tools; `sbx` is the Windows binary, reachable from WSL.
- hq is installed once per machine with one command and available on PATH as `hq`; there is no service to start. `hq update` brings it to the latest release.
- Repositories need no preparation: any repository with a sandbox can host agents as it is.

## 12. Milestones

Each milestone is usable on its own and is the acceptance boundary for that step.

- **M1 - CLI**: `hq new`, `ls` (no state yet, only `running`/`ended`), `go`, `kill`, `stop`, `sandbox`, `help`, `--version`, `update`; conventions of 4.2; both platforms. Deliverable: agents can be started, listed, entered and ended from any shell.
- **M2 - State and notifications**: the state model of section 5, `hq ls` with `STATE AGE LAST`, desktop notifications. Deliverable: `hq ls` tells which agents need the user, and the user is notified.
- **M3 - Dashboard core**: layout (6.1), sorting and views (6.2), selecting with keys (6.3 without mouse and name search), `open` and `kill` with dialogs (6.6), `n` New agent dialog, scenarios S0-S8, S10, S11. Deliverable: the consolidated view; S0-S8 pass.
- **M4 - Dashboard extras**: row action strip with `code` and `pr` (6.4), mouse (6.3, 6.4, 6.6), `/name` search, in-session dock shortcuts (6.5), narrow layout (S12), WSL verification (S13), `hq code`. Deliverable: full acceptance of section 10.

## 13. Design reference

`docs/requirements/look-and-feel.pen` (Pencil), exported to:

- `dash-s0-attention.png` - default attention view
- `dash.png` - all view, action strip on the cursor row
- `dash-s1-empty.png`, `dash-s2-new.png` (New agent dialog), `dash-s3-cursor.png` (docked vs cursor row), `dash-s3b-search.png`, `dash-s6-kill.png` (Kill dialog), `dash-s6b-docked-killed.png`, `dash-s8-quit.png`, `dash-s12-narrow.png`

## Phase 2 (not now): supervisor layer

Parked until the dashboard has run for a few weeks and the overview need is met. Recorded so v1 does not paint itself into a corner.

- Optional long-lived supervisor agent per project (`sup-<project>`), briefed with that project's repos, board and rules; it starts workers and answers their routine questions.
- It escalates to the human only a real question, surfaced as a normal attention state; worker-to-supervisor traffic never counts in the header summary.
- In the all view supervisors become group headers with their workers underneath; in the attention view they add no rows, so the default view gets shorter, not busier.
- Any supervised worker remains directly reachable; supervision changes sorting and filtering, never the user's access.
- v1 consequence: keep the row model flat and sorting/filtering separable, so grouping can be added later.
