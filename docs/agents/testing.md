# Testing

hq is written in Go (ADR 0011). The suite follows the [Practical Test Pyramid](https://martinfowler.com/articles/practical-test-pyramid.html): many fast unit tests, fewer integration tests, few end-to-end tests. Write each test at the lowest level that can prove the behaviour, not where it is easiest to write. The seams and levels are designed in `docs/design/hq.md` §7.2.

## Where a test goes

| Level | Selected by | Use it for |
|---|---|---|
| Unit | no build tag; `_test.go` next to the code | Pure logic: parsing, state and attention rules, sorting and filtering, what a dashboard row or a command's output contains, mapping the answer of `sbx`, tmux or a hook with a fake. No processes, sockets, terminals or real `sbx`/tmux |
| Integration | `//go:build integration`; `_integration_test.go` next to the code | The contract of a command or an adapter: what it runs against tmux (on a private socket per test, `tmux -L`), a stub `sbx`, `gh` or `wslpath` (`testutil.WSLStubs` runs the WSL side on any machine; `testutil.FakeClaude` puts `tools/fakeclaude` behind the stub `sbx run`, firing hq's injected hooks with Claude's payloads as it is prompted), or the file system, and what it reports. Flows that need a real process, file or socket |
| End-to-end | `//go:build e2e`; in `e2e/` only | A few critical journeys a person drives in a terminal (start an agent, see it, enter it, kill it; install and update), in real tmux with the stub `sbx` and `gh` |

Rules of thumb:

- **New logic starts with unit tests.** If logic sits inside a command and needs tmux or `sbx` only to fetch its inputs, extract it into a pure function and unit-test that. The command then gets one integration test for its contract.
- **Don't re-check unit-tested logic through the integration level.** An integration test proves the pieces are wired together.
- **Add an end-to-end test only for a new critical journey.** Keep them few.
- **External tools sit behind an interface** (tmux, `sbx`, `gh`, the platform adapter, the clock, the state-file reader) with a fake for tests, so unit tests never call the real tool. Platform-specific adapters (macOS, WSL) get the same contract tests.
- **Never touch the user's tmux server or sandboxes from a test.** Integration and end-to-end tests use a private tmux socket and the stub `sbx`.

## Commands

```bash
go test ./...                                          # unit
go test -tags integration ./...                        # unit + integration
go test -tags e2e ./e2e/...                            # end-to-end journeys
go run ./tools/testgate pyramid                        # counts per level, checks the shape
go test -tags integration -coverpkg=./internal/... -coverprofile=cover.out ./... \
  && go run ./tools/testgate coverage cover.out        # coverage threshold
```

## Thresholds

`tools/testgate` holds the minimum unit share of unit + integration tests and the minimum statement coverage of `internal/`. CI (`.github/workflows/tests.yml`, on macOS and Ubuntu) runs the pyramid check, unit and integration tests with coverage, and the end-to-end journeys. Both thresholds only go up: a PR that raises the actual figure raises the threshold in the same PR. Never lower one to get a PR through. A PR with a red CI job is not merged.

## In the QA plan and PR

The QA plan (`docs/agents/qa-evidence.md` §1) lists the tests the change adds at each level before any code is written; the PR quotes `testgate pyramid` before and after.
