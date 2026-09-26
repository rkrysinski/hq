# hq

Tooling for running many Claude Code agents in parallel, on macOS and on Windows via WSL. Each agent runs inside a Docker Sandboxes (`sbx`) microVM and lives in its own tmux window, shown as an iTerm2 tab through iTerm2's tmux integration (`tmux -CC`) on macOS, or as a plain tmux window inside Windows Terminal on WSL.

## Layout

- `bin/hq` - bash CLI: `new`, `ls`, `go`, `kill`, `stop`, attach. Installed on PATH via symlink from `~/work/bin/hq`.
- `bin/hq-dash` - dashboard TUI (see `docs/features/dash.md`).
- `hooks/` - Claude Code hook script + settings snippet that repos copy into `.claude/`.
- `docs/look-and-feel.pen` - Pencil design of the dashboard; `docs/dash.png` is its export.

## Constraints that shape everything

- Agents run inside sbx VMs: no host binaries, no tmux socket, no `~/.claude` from inside. The only channel out is the mounted repo directory. Hooks therefore write state files into the repo; host tools read them.
- Hook output must stay valid JSON on stdout (Claude Code parses it). Notifications go through the `terminalSequence` field (OSC 9), never `/dev/tty`.
- Claude Code creates git worktrees itself; hq never manages worktrees or branches.
- No dependencies beyond bash, python3 (stdlib, curses), tmux, sbx, git, gh. All exist on macOS (brew) and in WSL (apt); `sbx` on Windows is the Windows binary, reachable from WSL as `sbx.exe` through interop.
- Must run on macOS and on Windows/WSL (Ubuntu). Platform-specific code (iTerm2 AppleScript, `sbx` vs `sbx.exe`, Windows vs WSL paths) is isolated behind small helper functions with a detected `HQ_PLATFORM` (`mac` | `wsl`), never scattered through the code.
- Terminal notifications use OSC 9, which both iTerm2 and Windows Terminal understand.

## Conventions

- Shell: bash with `set -euo pipefail`, `die()` for errors, `printf %q` when building commands.
- One sandbox per repo, named `claude-<repo-folder>`; several tabs may share it.
- Tab names are short (issue number or slug); the branch name is the primary identifier in status output.
- Never use the em dash; use `-`.
