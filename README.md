# hq

One console for many Claude Code agents. Each agent runs in a Docker Sandboxes (`sbx`) microVM and a tmux session; hq shows all of them in one dashboard, sorted by who needs you, and lets you enter, start, stop and inspect any of them from the dashboard or from any shell.

- Specification: `docs/spec.md` (milestones in section 12)
- Decisions: `docs/decisions/`
- Design: `docs/look-and-feel.pen`, exports `docs/dash*.png`

Platforms: macOS (iTerm2) and Windows via WSL (Windows Terminal).
