# Feature: `hq dash` - consolidated agent dashboard

## Goal

One terminal tab that shows every running Claude Code agent across all repositories, sorted by what needs the user's attention, with a live preview of the selected agent and one-key jump into its tab. Must work on macOS (iTerm2 + `tmux -CC`) and on Windows via WSL (Windows Terminal + plain tmux); the dev team is on Windows. Design reference: `docs/look-and-feel.pen` (export: `docs/dash.png`).

Explicitly out of scope: creating worktrees or branches, managing PRs, anything workmux-like beyond the control and visual part.

## Architecture

```
sbx VM (agent)                         host (Mac)
Claude Code hooks ─ write ─▶ <repo>/.claude/status/<branch>.json ─ read ─▶ hq-dash
                   └ emit ──▶ OSC 9 notification (existing behaviour)      │
                                                          tmux ◀─ select-window / capture-pane
```

Hooks cannot reach the host, so state travels through the mounted repo. `hq-dash` correlates state files with tmux windows.

## 1. Status hook (in every repo, committed)

Extend `hooks/notify.sh` so that besides the OSC 9 notification it writes a status file, and add a `UserPromptSubmit` hook.

- Path: `${CLAUDE_PROJECT_DIR}/.claude/status/<branch>.json`, where `<branch>` is `git branch --show-current` run in the hook's `cwd` (the worktree). Slashes in branch names become `__`. Directory is gitignored (`.claude/status/`).
- Content: `{"state": "...", "branch": "...", "cwd": "...", "session_id": "...", "ts": <unix seconds>, "last": "<first 120 chars of last_assistant_message, single line>"}`.
- State transitions:
  - `UserPromptSubmit` -> `working`
  - `Stop` -> `question` if `last_assistant_message` ends with `?`, else `done`
  - `Notification` (`permission_prompt|agent_needs_input|elicitation_dialog`) -> `needs_input`
  - `SessionEnd` -> `ended`
- Must stay POSIX `sh` + `python3` (stdlib) only; the sbx templates have both. Output on stdout stays exactly one JSON object.
- Deliverables: `hooks/notify.sh`, `hooks/settings.hooks.json` (snippet to merge), `hooks/README.md` (how to install into a repo: copy, gitignore line, `CLAUDE.md` line "end with a direct question when a decision is needed").

## 2. `hq-dash` (python3 + curses)

Invoked as `hq dash`; `bin/hq` gets a `dash` subcommand that runs `hq-dash` in the foreground (typically from its own tab, e.g. `hq new dash` is not needed - just run `hq dash` in a tmux tab).

### Data

For each tmux window in session `$AGENTS_SESSION` (default `agents`):
- `tab` = window name, `idx` = window index, `pane_pid`, `pane_current_path`.
- `repo` = git toplevel of `pane_current_path` (the main checkout, not a worktree), basename shown.
- `alive` = a child process of `pane_pid` whose command matches `sbx` (same logic as `hq ls`).
- Status file: newest `<repo>/.claude/status/*.json` whose `cwd` is under the tab's repo. If several tabs share a repo, match on the branch recorded when the tab was created if available (see 3), else newest.
- `state` = from file, or `ended` when not alive, or `unknown` when alive but no file yet.
- `age` = now - `ts`, human short (`3s`, `2m`, `1h`).

Refresh every 1s (cheap: a few `tmux` calls + small files). No daemon.

### Layout (see design)

- Header: `hq` + summary `N agents · X need you · Y done · Z working` + clock and refresh indicator.
- Table columns: `#`, `TAB`, `REPO`, `BRANCH`, `STATE` (colored dot + word), `AGE`, `LAST`. Sort: `needs_input`, `question`, `done`, `working`, `unknown`, `ended`; ties by age desc. Selected row highlighted.
- Preview pane: last ~12 lines of the selected tab (`tmux capture-pane -p -t agents:<idx> -S -40`), trimmed, in a bordered box titled `<tab> · <branch> · live`.
- Footer key hints.
- Colors: attention amber, done green, working blue, ended grey; dark background. Use terminal 256-color approximations of the design tokens; degrade to 8 colors gracefully.
- Minimum size 80x24; narrower terminals drop `LAST`, then `REPO`.

### Keys

- `↑/↓`, `j/k`: select. `1-9`: select by list position.
- `Enter`: jump to the tab (`hq go <tab>` semantics: tmux select-window + iTerm2 tab selection via AppleScript, already implemented in `bin/hq`; reuse it by shelling out to `hq go`).
- `p`: peek - toggle a larger preview (preview takes 2/3 of the screen).
- `n`: prompt for `NAME [DIR] [PROMPT]` in a one-line input and run `hq new ...`.
- `k`: kill selected tab after `y/N` confirmation (`hq kill`).
- `r`: force refresh. `q`: quit.

### Non-goals for v1

PR status per branch (`gh pr view`), multi-session support, mouse support. Leave hooks in the code so PR status can be added as a column later.

## 3. Platform support (macOS + Windows/WSL)

- Detect once: `HQ_PLATFORM=mac` (Darwin) or `wsl` (`/proc/version` contains `microsoft`). Everything platform-specific goes through helpers in `bin/hq` (and a mirrored small module in `hq-dash`):
  - `sbx_cmd`: `sbx` on mac, `sbx.exe` on WSL (Windows binary via interop; verify `sbx.exe ls` works from WSL and document the PATH requirement).
  - `host_path DIR`: the path `sbx` expects. On WSL, repos under `/mnt/c/...` are converted with `wslpath -w`; repos on the WSL filesystem are rejected with a clear error (sbx mounts Windows paths). Confirm which one actually works with the current sbx release and record the result in `hooks/README.md`.
  - `focus_tab NAME`: mac = tmux `select-window` + iTerm2 AppleScript (existing); wsl = tmux `select-window` only (tabs are tmux windows in one Windows Terminal tab, so that is enough).
  - `attach`: mac = `tmux -CC attach`; wsl = `tmux attach`.
- Status hook is unaffected: it runs inside the Linux sbx VM on both platforms and writes into the mounted repo. Verify the file lands where WSL sees it (same repo path from the WSL side).
- Notifications: OSC 9 works in Windows Terminal; verify once through `sbx.exe run` from WSL.
- Keep `hq ls`/`hq-dash` process detection working on WSL: the child of the pane shell is `sbx.exe` there, so match `sbx` as a substring.

## 4. `bin/hq` changes

- `hq dash` subcommand.
- `hq new`: after creating the window, set tmux window option `@hq_dir` to the repo dir (so dash does not depend on `pane_current_path`, which changes when the shell `cd`s). Read it in `hq ls` too.
- Keep behaviour of `ls`, `go`, `kill`, `stop`, attach unchanged.

## Acceptance

- The manual test script below passes on macOS and on a Windows 11 machine with WSL (Ubuntu), Windows Terminal and `sbx` installed via winget.

- With three tabs in two repos (one `needs_input`, one `done`, one `working`), `hq dash` shows them in that order within 1s of state change; `Enter` on the first opens its iTerm2 tab.
- Killing a sandbox from outside (`sbx stop`) flips the row to `ended` within 2s.
- Hook still emits the OSC 9 notification exactly as before (regression: run the Done / Question / Needs input tests from `hooks/README.md`).
- `bash -n bin/hq` and `python3 -m py_compile bin/hq-dash` pass; no external packages imported.

## Manual test script

1. `hq new a ~/work/repositories/support-chatbot "say hi"`, `hq new b ~/work/repositories/support-chatbot "ask me one yes/no question and stop"`.
2. `hq dash` in a third tab; expect `b` as `question` above `a` as `done`.
3. Press `2` then `Enter`; expect iTerm2 to switch to tab `a`.
