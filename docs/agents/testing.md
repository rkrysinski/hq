# Testing

The suite follows the [Practical Test Pyramid](https://martinfowler.com/articles/practical-test-pyramid.html): many fast unit tests, fewer integration tests, few end-to-end tests. Write each test at the lowest level that can prove the behaviour, not where it is easiest to write.

hq's test levels, commands, CI, thresholds, platform code and private details: `docs/hq/testing.md`, which holds for hq where it differs from this file.

## Where a test goes

| Level | Use it for | Must not |
|---|---|---|
| Unit | Pure logic: parsing, dates and statuses, calculations, what a page or an export row contains, mapping the answer of an external service with a fake client | Open a network connection, listen on a port or open a database |
| Integration | The contract of a request handler, a command or an adapter: permission checks, the main success path, what gets saved, what the response shows | Re-check edge cases the unit tests already cover |
| End-to-end | A few critical journeys a person clicks through, in a real browser against the running app (`scripts/local-dev.sh`), and JavaScript behaviour | Grow beyond the critical journeys; each one starts a browser |

Rules of thumb:

- **New logic starts with unit tests.** If the logic sits inside a handler and needs the database only to fetch its inputs, extract it into a pure function and unit-test that. The handler then gets one integration test for its contract.
- **Don't re-check unit-tested logic through the integration level.** An integration test proves the pieces are wired together.
- **Add an end-to-end test only for a new critical journey or new JavaScript.**
- **External services sit behind a client interface** with a fake for tests and local QA, so neither level calls the real service.

## Wiring the checks

The template does not know this project's stack, so three files start unwired and fail until they are:

| File | What to put there |
|---|---|
| `scripts/check.sh` | The full build: one `step` per command of the stack, in the order the script lists: lint, format check, the pyramid check, unit and integration tests with coverage, the coverage threshold, the browser journeys. The script already prints a line per step, stops at the first failure and names the commit. CI runs this script and nothing else |
| `.github/workflows/tests.yml` | The toolchain setup (`actions/setup-java`, `setup-go`, `setup-node`...) and the browser the journeys need, before `scripts/check.sh`; the checks themselves stay in the script |
| `scripts/local-dev.project.sh` | How the app installs, prepares and starts, so agents can run it per checkout and verify changes in a browser |

What `scripts/check.sh` enforces, whatever the stack:

- **The pyramid's shape**: tests only in the three levels, fewer end-to-end tests than integration and unit tests, and the unit share of unit + integration tests at or above a minimum (the Python stack of the template starts at 75% and counts test methods per level; do the same with the stack's test runner).
- **Unit tests cannot reach the network or a database**: a base class or a runner setting that fails such a test, with a message saying where the test belongs.
- **Coverage** (branch coverage where the tool has it) at or above a threshold (the Python stack starts at 90%).
- **Thresholds are changed by a human.** An agent never changes a threshold wired into `scripts/check.sh` (the minimum unit share, the coverage threshold), up or down: it reports the measured values in QA (see below). A pull request that falls below a threshold fixes its tests, not the threshold. For the human: raise a threshold when the measured value has grown; never lower one to get a pull request through.

Then replace the table in *Running* below with the real commands.

## Running

```bash
scripts/check.sh      # the full build: every check CI runs, the browser journeys included
```

## Merging

The full build is `scripts/check.sh`: every check, the browser journeys included, one line per step, stopping at the first that fails; its last line names the commit it ran on. A pull request gets no `Tests` run in CI, so the agent runs the full build itself:

1. **After implementing**: `scripts/check.sh` on the branch. Push only when it passes.
2. **Before the merge**: check whether the branch is behind its base (`git fetch origin <base>`, then `git merge-base --is-ancestor origin/<base> HEAD` fails when it is). If it is: `git rebase origin/<base>`, `scripts/check.sh` again, `git push --force-with-lease`, and the full build in the pull request's `## QA` section is replaced with this run's (`docs/agents/qa-evidence.md`, step 5). The master pull request of a release (`release-vX.Y.Z` to `dev`) is never rebased: its tag must stay an ancestor of `dev`, so it merges with a merge commit (`docs/agents/issue-tracker.md`, *Working a release*).
3. **After the merge**: CI runs `Tests` (`.github/workflows/tests.yml`, the same script) on every push to `dev`: a squash merge is one commit, so each merge gets a run of its own, and none is cancelled; a commit that changes only documentation gets none. A red `Tests` run on `dev` blocks every further merge until a fix is merged: the run names the commit that broke the build. Before merging, the latest run must not be red: `gh run list --branch dev --workflow Tests --limit 1 --json conclusion,headSha,url`.

The organisation's GitHub plan has no required checks or branch protection for a private repository, so these rules are a convention.

## In the QA plan and PR

The QA plan (`docs/agents/qa-evidence.md` §1) lists the tests the change adds at each level before any code is written. The PR's `## QA` section reports the test counts per level before and after the change, and the coverage figure after it, and ends with the full build on the head commit (`docs/agents/qa-evidence.md`, step 5).
