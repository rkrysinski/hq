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
- `docs/hq/` - hq's own rules: this file, testing (`testing.md`), QA evidence (`qa-evidence.md`), filing and working an issue (`issue-tracker.md`) and releasing (`releasing.md`).
- `docs/agents/supervisor.md`, `release-qa.md`, `integration-review.md` - hq's facts for a supervised release, read by the release skills.
- Implementation: Go (ADR 0011); `cmd/hq` is the binary, `internal/` its packages, `tools/testgate` the CI test gates.

## Rules

- WHAT, not HOW: the requirements describe behaviour and givens. Implementation choices (language, libraries, file formats, internal mechanics) belong to `docs/design/` and the implementation, never to the spec.
- The givens in the spec (sbx sandboxes, tmux, iTerm2/Windows Terminal, Claude Code hooks and worktrees) are the platform; design with them, not against them.
- Both platforms, identical behaviour; platform-specific code isolated in `internal/platform`, never scattered (`go run ./tools/testgate platform` checks it, see `docs/hq/testing.md`).
- Screen rules (anything hq reads off Claude's screen) are settled only against real captures from the agents' own setup, in a spec, PRD or issue alike - see `tools/capture/README.md`.
- Never use the em dash; use `-`.

## Testing

Levels are Go build tags (none, `integration`, `e2e`), and the end-to-end journeys run in a terminal, not a browser. `scripts/check.sh` runs the gates of `tools/testgate` on the shape, the platform code and the coverage. Every pull request but a release's master pull request and a revert also runs the mutation tests of its changed lines, `go run ./tools/testgate mutation BASE` with BASE the branch it goes into, each time the full build is run for it (an implementer of a spec's ticket runs it against the integration branch before it reports), and leaves no mutant alive without a test that kills it or a one-line reason in `## QA`. See `docs/hq/testing.md`.

## Local run

hq is a terminal program: this section takes the place of `AGENTS.md`, *Local run and browser verification*, and `scripts/local-dev.project.sh` is not used.

Run hq from the issue's worktree with `scripts/local-dev.sh [hq arguments]` (skill `local-dev`): it builds the checkout and runs it against a tmux server private to that worktree, so it never touches the user's own hq or other worktrees. Drive it the way a person would before calling the work done; `scripts/local-dev.sh --kill-server` cleans up.

## Pushing

A push from a clone of hq runs the pre-push hook `scripts/git-hooks/pre-push`, which refuses a push that would publish a private detail and, without the pattern list, refuses the push as well (`docs/hq/testing.md`, *Private details*). Enable it once per clone; it then holds for every worktree of that clone that has the script:

```bash
git config core.hooksPath scripts/git-hooks
```

The list, which the owner gives, goes into the clone's `.git/info/hq-private-patterns` (`"$(git rev-parse --git-common-dir)/info/hq-private-patterns"`), or into `HQ_PRIVATE_PATTERNS`. A sandbox that mounts the host's checkout shares its `.git`, its configuration and that file, so the hook and the list outlive the sandbox. A clone without the setting runs no hook at all, and neither does a worktree whose checkout has no `scripts/git-hooks` (a branch from before the hook, `qa-artifacts`): nothing there says the push went out unchecked, so a sandbox with a clone of its own needs both put in again before its first push. The hook runs the gate with `go run`, so a machine that pushes needs Go.

## Updating from the template

`.copier-answers.yml` records the template's address as the alias `git+template:upstream`, so that the public repository never names it. Every machine that runs an update from the template (the host, and the sandbox the agents run in) maps the alias once, in its global git configuration, where Copier's own clone of the template reads it:

```bash
git config --global url."https://github.com/OWNER/REPOSITORY.git".insteadOf "template:upstream"
```

`OWNER/REPOSITORY` is the template's address, which the owner gives and which is never written into this repository. Without the mapping the update stops at once: `ssh: Could not resolve hostname template`.
