# hq: the project's own rules

One console for many Claude Code agents running in Docker Sandboxes (`sbx`) and tmux, on macOS (iTerm2) and Windows via WSL (Windows Terminal).

`AGENTS.md`, `docs/workflow.md`, `docs/releasing.md` and the documents under `docs/agents/` are the project template's and say what every project made from it does, except four files under `docs/agents/` that hq owns and fills: the facts of a supervised release (`supervisor.md`, `release-qa.md`, `integration-review.md`) and `lessons.md`. The files in `docs/hq/` hold the rest of what is hq's own, each linked from the template's document it belongs to. Where the two differ, `docs/hq/` holds for hq.

## What this repository holds

- `docs/requirements/spec.md` - the requirements specification, source of truth for WHAT hq does: commands, state model, dashboard, scenarios, acceptance, and the milestones to build it in steps (M1 CLI -> M2 state -> M3 dashboard -> M4 extras).
- `docs/requirements/look-and-feel.pen` - the visual design of the dashboard, one frame per scenario named in the spec (open it in Pencil; there are no exported images).
- `docs/adr/` - short records of decisions already taken; do not reopen them without saying so.
- `docs/design/` - HOW hq is built: technology, architecture, key mechanics (`docs/design/hq.md`).
- `GLOSSARY.md` - the glossary. Terms are added when they are settled.
- `CODING_STANDARDS.md` - the rules for hq's code that take judgement.
- `docs/hq/` - hq's own rules: this file, testing (`testing.md`), QA evidence (`qa-evidence.md`), working an issue (`issue-tracker.md`) and releasing (`releasing.md`).
- `docs/agents/supervisor.md`, `release-qa.md`, `integration-review.md` - hq's facts for a supervised release, read by the release skills.
- Implementation: Go (ADR 0011); `cmd/hq` is the binary, `internal/` its packages, `tools/testgate` the CI test gates.

## Rules

- WHAT, not HOW: the requirements describe behaviour and givens. Implementation choices (language, libraries, file formats, internal mechanics) belong to `docs/design/` and the implementation, never to the spec.
- The givens in the spec (sbx sandboxes, tmux, iTerm2/Windows Terminal, Claude Code hooks and worktrees) are the platform; design with them, not against them.
- Both platforms, identical behaviour; platform-specific code isolated in `internal/platform`, never scattered (`go run ./tools/testgate platform` checks it, see `docs/hq/testing.md`).
- Screen rules (anything hq reads off Claude's screen) are settled only against real captures from the agents' own setup, in a spec, PRD or issue alike - see `tools/capture/README.md`.
- Never use the em dash; use `-`.

## Testing

Levels are Go build tags (none, `integration`, `e2e`), and the end-to-end journeys run in a terminal, not a browser. `scripts/check.sh` runs the gates of `tools/testgate` on the shape, the platform code and the coverage. See `docs/hq/testing.md`.

## Local run

hq is a terminal program: this section takes the place of `AGENTS.md`, *Local run and browser verification*, and `scripts/local-dev.project.sh` is not used.

Run hq from the issue's worktree with `scripts/local-dev.sh [hq arguments]` (skill `local-dev`): it builds the checkout and runs it against a tmux server private to that worktree, so it never touches the user's own hq or other worktrees. Drive it the way a person would before calling the work done; `scripts/local-dev.sh --kill-server` cleans up.

## Updating from the template

`.copier-answers.yml` records the template's address as the alias `git+template:upstream`, so that the public repository never names it. Every machine that runs an update from the template (the host, and the sandbox the agents run in) maps the alias once, in its global git configuration, where Copier's own clone of the template reads it:

```bash
git config --global url."https://github.com/OWNER/REPOSITORY.git".insteadOf "template:upstream"
```

`OWNER/REPOSITORY` is the template's address, which the owner gives and which is never written into this repository. Without the mapping the update stops at once: `ssh: Could not resolve hostname template`.
