hq's own rules hold beside everything below and win where they differ: @docs/hq/agents.md

## Agent skills

The whole path of a change, from an issue to a released version, is in `docs/workflow.md`; the sections below are the parts agents carry out.

### Issue tracker

Issues and specs are tracked as GitHub issues (a spec is an issue labelled `prd`, its tickets are sub-issues); work lands as a pull request with `Closes #<n>` in its body, and every commit message carries a `Closes #<n>` line: a line of its own in the message, which a trailer such as `Co-Authored-By` may follow (the squash merge carries the `Closes` line to `dev`; the pull request body alone does not close the issue here). `scripts/check-pr.sh` fails a pull request that closes an issue when one of its commits lacks the line; merge commits are not checked. The pull request has a `## QA` section of executed checks with evidence (screenshots of every UI change) for the user's review, see `docs/agents/qa-evidence.md`. External PRs are not a triage surface. The PR title states the intended outcome and the body follows `.github/PULL_REQUEST_TEMPLATE.md` (Summary, Deviations from the issue, QA, Merge Danger); CI checks both with `scripts/check-pr.sh`. The `pr` skill shapes the body: its Summary and Merge Danger map onto the sections of the same name, its Evidence goes under `## QA`, and `## Deviations from the issue` stays. See `docs/agents/issue-tracker.md`.

A spec issue carries `prd` only; `ready-for-agent` goes on its tickets. This overrides the step of `/to-spec` that applies `ready-for-agent` to the spec: do not apply it there and do not ask about it. Before tickets are published, every story of the spec has an acceptance criterion in a concrete ticket: the breakdown `/to-tickets` shows to the user maps every story number to a ticket, and a story without a ticket is a gap to fill, not to skip.

### Working a spec

`/implement-spec #<spec>` on a spec labelled `prd` whose tickets carry `ready-for-agent` is the go-ahead to build the whole spec on one integration branch and one pull request; the orchestrator follows `docs/agents/issue-tracker.md`, *Working a spec*; an implementer subagent follows `docs/agents/implementer.md` and reads no other process doc.

### Working a single issue

An issue labelled `ready-for-agent` without a spec (a bug, a request that came through triage) is the go-ahead for `/implement`: carry it through implementation, QA and its own pull request without asking for confirmation. Stop only for a gap that changes what gets built, and then ask on the issue and relabel it `needs-info`. A request to brainstorm or plan stays discussion-first. See `docs/agents/issue-tracker.md`, *Working a single issue*.

### Supervised releases

A release built with `/release-prepare`, `/release-implement` and `/release-ship` puts a milestone on one release branch, verifies it as a whole, tags it and merges it back; the roles follow `docs/agents/issue-tracker.md`, *Working a release*, and the project's facts are in `docs/agents/supervisor.md`, `docs/agents/release-qa.md` and `docs/agents/integration-review.md`.

### Next step

The main-flow skills (`/grill-with-docs`, `/to-spec`, `/to-tickets`, `/implement`, `/implement-spec`, `/retro`, `/ask-matt`) are user-invoked, so this agent cannot start them. When the next step is one of them, tell the user the exact command with the issue number (for example: next, `/implement-spec #12`).

### Triage labels

The repo uses the default triage label vocabulary. See `docs/agents/triage-labels.md`.

### Domain docs

The repo uses a single-context domain docs layout. See `docs/agents/domain.md`.

### Testing

Tests follow the test pyramid: many fast unit tests (no network or database), fewer integration tests, few browser journeys. Write each test at the lowest level that proves the behaviour, not where it is easiest. The full build, `scripts/check.sh`, runs every check, the browser journeys included, stopping at the first failure.

A pull request gets no `Tests` run in CI: run the full build after implementing and push only when it passes; before the merge, a branch behind its base is rebased onto it, built again and pushed. CI runs the same script after every merge to `dev`, and a red `Tests` run on `dev` blocks further merges until its fix is merged. The pull request's `## QA` section ends with the full build's lines on its head commit. See `docs/agents/testing.md`, *Merging*.

### Local run and browser verification

Use `$local-dev` when starting this app locally or verifying implementation. Agents run the app with `scripts/local-dev.sh`, which isolates each checkout (safe for parallel git worktrees) on a free port unless `APP_PORT` is set; always use the URL and browser session it prints. When work touches UI, sign-in, a user-facing flow, downloads or exports, or acceptance criteria, verify the running app in a real browser before calling the work done: Playwright, launching its own browser context per run so parallel checkouts stay isolated. The project part of the script (how the app installs and starts) is `scripts/local-dev.project.sh`.

### Shell commands

`.claude/settings.json` runs the hook `.claude/hooks/bash-guard.sh` before every Bash command. It refuses two shapes that keep a core of a shared machine busy, and its message names the safe form. A here-document with an unquoted delimiter runs each backtick and `$(` of its body before the command starts: write `<<'EOF'`, and pass a value the body needs through the environment. A loop whose body is only `:` or `true` runs without a pause: pause between tries and bound the wait, as in `timeout 120 sh -c 'until …; do sleep 1; done'`.

### Releases

Versions are git tags `vX.Y.Z` cut with `scripts/cut-release.sh`, which writes the version to `VERSION` first; the release notes, the version's milestone and the "released in" comments are written by `scripts/release.sh` from the merged pull requests and their issues' labels. Never set a milestone by hand, except in a supervised release (`docs/agents/issue-tracker.md`, *Working a release*). See `docs/releasing.md`.

### Lessons and retro

Non-obvious lessons that should change how the next piece of work is done live in `docs/agents/lessons.md`. Read it before starting an issue; add to it only what passes the threshold at its top. After a spec pull request (or any larger one) is merged, the user runs `/retro` in the session that built it; a finding becomes a deterministic check (a linter rule, a hook, a CI job) for a mechanical mistake, an entry in `docs/agents/lessons.md` for a judgement call, or an issue in the team's project template when it applies to every project. Issue comments carry decisions, blockers and questions, not progress. See `docs/agents/lessons.md`.

### Template

This repository was created from the team's project template with Copier (`.copier-answers.yml` records the template version). `copier update` brings in later versions of the template; change the process tooling (`scripts/`, `docs/agents/`, `.github/`, `.claude/`) in the template, not here, or the next update will conflict with it.
