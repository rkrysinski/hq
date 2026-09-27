# hq

One console for many Claude Code agents running in Docker Sandboxes (`sbx`) and tmux, on macOS (iTerm2) and Windows via WSL (Windows Terminal).

## What this repository holds

- `docs/requirements/spec.md` - the requirements specification, source of truth for WHAT hq does: commands, state model, dashboard, scenarios, acceptance, and the milestones to build it in steps (M1 CLI -> M2 state -> M3 dashboard -> M4 extras).
- `docs/requirements/look-and-feel.pen` and `docs/requirements/dash*.png` - the visual design of the dashboard, one export per scenario named in the spec.
- `docs/adr/` - short records of decisions already taken; do not reopen them without saying so.
- `docs/design/` - HOW hq is built: technology, architecture, key mechanics (`docs/design/hq.md`).
- `CONTEXT.md` - the glossary. Terms are added when they are settled.
- Implementation: Go (ADR 0011); `cmd/hq` is the binary, `internal/` its packages, `tools/testgate` the CI test gates.

## Rules

- WHAT, not HOW: the requirements describe behaviour and givens. Implementation choices (language, libraries, file formats, internal mechanics) belong to `docs/design/` and the implementation, never to the spec.
- The givens in the spec (sbx sandboxes, tmux, iTerm2/Windows Terminal, Claude Code hooks and worktrees) are the platform; design with them, not against them.
- Both platforms, identical behaviour; platform-specific code isolated, never scattered.
- Never use the em dash; use `-`.

## Agent skills

### Issue tracker

Issues and PRDs are tracked as GitHub issues (PRDs labelled `prd`, implementation issues as sub-issues); work on an issue lands as a PR with `Closes #<n>` and a `## QA` section of executed checks with evidence (screenshots of every dashboard change) for the user's review - see `docs/agents/qa-evidence.md`. External PRs are not a triage surface. See `docs/agents/issue-tracker.md`.

### Triage labels

The repo uses the default triage label vocabulary. See `docs/agents/triage-labels.md`.

### Domain docs

The repo uses a single-context domain docs layout. See `docs/agents/domain.md`.

### Testing

Tests follow the test pyramid: many fast unit tests, fewer integration tests, few end-to-end journeys. Write each test at the lowest level that proves the behaviour, not where it is easiest. Levels are Go build tags (none, `integration`, `e2e`); CI gates the shape and coverage. See `docs/agents/testing.md`.

### Local run

Run hq from the issue's worktree with `scripts/local-dev.sh [hq arguments]` (skill `local-dev`): it builds the checkout and runs it against a tmux server private to that worktree, so it never touches the user's own hq or other worktrees. Drive it the way a person would before calling the work done; `scripts/local-dev.sh --kill-server` cleans up.

### Releases

Work lands on `dev` (the default branch); `main` holds the latest release and is never committed to by hand. Versions are git tags `vX.Y.Z` cut from `dev` with `scripts/cut-release.sh`, which also fast-forwards `main`; the release notes, the version's milestone and the "released in" comments are written by `scripts/release.sh` from the merged pull requests and their issues' labels. Never set a milestone by hand. See `docs/releasing.md`.
