# hq

One console for many Claude Code agents running in Docker Sandboxes (`sbx`) and tmux, on macOS (iTerm2) and Windows via WSL (Windows Terminal).

## What this repository holds

- `docs/spec.md` - the requirements specification, source of truth for WHAT hq does: commands, state model, dashboard, scenarios, acceptance, and the milestones to build it in steps (M1 CLI -> M2 state -> M3 dashboard -> M4 extras).
- `docs/decisions/` - short records of decisions already taken; do not reopen them without saying so.
- `docs/look-and-feel.pen` and `docs/dash*.png` - the visual design of the dashboard, one export per scenario named in the spec.
- Implementation: none yet. Build it from the spec.

## Rules

- WHAT, not HOW: documents in this repo describe behaviour and givens. Implementation choices (language, libraries, file formats, internal mechanics) belong to the implementation and its own design notes, never to the spec.
- The givens in the spec (sbx sandboxes, tmux, iTerm2/Windows Terminal, Claude Code hooks and worktrees) are the platform; design with them, not against them.
- Both platforms, identical behaviour; platform-specific code isolated, never scattered.
- Never use the em dash; use `-`.
