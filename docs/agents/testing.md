# Testing

hq has no implementation yet, so there are no tests, no pyramid check and no tests workflow. This file states the rules that hold whatever the stack; the levels, the commands and the CI gates are filled in when the stack is chosen (see *When the stack is chosen* below).

The suite follows the [Practical Test Pyramid](https://martinfowler.com/articles/practical-test-pyramid.html): many fast unit tests, fewer integration tests, few end-to-end tests. Write each test at the lowest level that can prove the behaviour, not where it is easiest to write.

## Where a test goes

| Level | Use it for |
|---|---|
| Unit | Pure logic: parsing, state and attention rules, sorting and filtering, what a dashboard row or a command's output contains, mapping the answer of `sbx`, tmux or a hook with a fake. No processes, sockets, terminals or real `sbx`/tmux |
| Integration | The contract of a command or an adapter: what it runs against tmux, `sbx` or the file system, and what it reports. Flows that need a real process, file or socket |
| End-to-end | A few critical journeys a person drives in a terminal (start an agent, see it in the dashboard, enter it, kill it) |

Rules of thumb:

- **New logic starts with unit tests.** If logic sits inside a command and needs tmux or `sbx` only to fetch its inputs, extract it into a pure function and unit-test that. The command then gets one integration test for its contract.
- **Don't re-check unit-tested logic through the integration level.** An integration test proves the pieces are wired together.
- **Add an end-to-end test only for a new critical journey.** Keep them few.
- **External tools sit behind an interface** (`sbx`, tmux, the terminal emulator) with a fake for tests, so unit tests never call the real tool. Platform-specific adapters (macOS/iTerm2, WSL/Windows Terminal) get the same contract tests.

## Thresholds

When the stack is chosen, CI enforces the pyramid's shape (a minimum unit share) and a coverage threshold. Both only go up: a PR that raises the actual figure raises the threshold in the same PR. Never lower one to get a PR through. A PR with a red CI job is not merged.

## In the QA plan and PR

The QA plan (`docs/agents/qa-evidence.md` §1) lists the tests the change adds at each level before any code is written.

## When the stack is chosen

Port these from the project template (`project-template`, its README *On a stack other than Python*), in the same PR that adds the first code:

- a pyramid check (the template's `scripts/check_test_pyramid.py`) and the base classes or tags that mark each level,
- `.github/workflows/tests.yml` running the pyramid check, unit and integration tests with coverage, and the end-to-end journeys,
- the commands and the level table in this file,
- a local-run script and skill for agents (the template's `scripts/local-dev.sh` and `.claude/skills/local-dev/`), and the *Local run* section of `AGENTS.md`.
