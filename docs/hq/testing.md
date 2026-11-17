# Testing hq

hq's own testing rules, beside the template's `docs/agents/testing.md`; where the two differ, this file holds for hq. hq is written in Go (ADR 0011). The seams and levels are designed in `docs/design/hq.md` §7.2.

## Where a test goes

| Level | Selected by | Use it for |
|---|---|---|
| Unit | no build tag; `_test.go` next to the code | Pure logic: parsing, state and attention rules, sorting and filtering, what a dashboard row or a command's output contains, mapping the answer of `sbx`, tmux or a hook with a fake. No processes, sockets, terminals or real `sbx`/tmux |
| Integration | `//go:build integration`; `_integration_test.go` next to the code | The contract of a command or an adapter: what it runs against tmux (on a private socket per test, `tmux -L`), a stub `sbx`, `gh` or `wslpath` (`testutil.WSLStubs` runs the WSL side on any machine, with the stubs listed in `internal/testutil/wsl-stubs`, which `scripts/local-dev.sh --stub` puts in place for QA too; `testutil.FakeClaude` puts `tools/fakeclaude` behind the stub `sbx run`, firing hq's injected hooks with Claude's payloads as it is prompted, and running a question dialog's hooks side by side, so it takes an answer from a hook's decision as from a typed line), or the file system, and what it reports. Flows that need a real process, file or socket |
| End-to-end | `//go:build e2e`; in `e2e/` only | A few critical journeys a person drives in a terminal (start an agent, see it, enter it, kill it; install and update), in real tmux with the stub `sbx` and `gh` |

Rules of thumb, beside the template's:

- **External tools sit behind an interface** (tmux, `sbx`, `gh`, the platform adapter, the clock, the journal's store) with a fake for tests, so unit tests never call the real tool. Platform-specific adapters (macOS, WSL) get the same contract tests.
- **Wait for tmux to reap an ended pane before looking at it.** On WSL 1 tmux now and then misses that a pane's program exited: the pane is dead, but without its time, status and "Pane is dead" line until another child of the server exits (#22). A tmux test that asserts any of those calls `reaped(t, socket)` after the pane is dead.
- **Run tmux with `-u`.** hq passes it to every tmux command (design 3.2); a test that runs tmux itself does too, or what it reads depends on the machine's locale (#41).
- **Never touch the user's tmux server or sandboxes from a test.** Integration and end-to-end tests use a private tmux socket and the stub `sbx`. A test that starts hq says which platform it runs as, so the suite behaves the same on macOS, Linux and WSL: `testutil.SbxStub` sets `HQ_PLATFORM=native` (hq runs `sbx`, the stub, never the `sbx.exe` of a WSL machine) and `testutil.WSLStubs` sets `HQ_PLATFORM=wsl` (hq runs the stub as `sbx.exe`, whatever `sbx` is in `PATH`); a test that uses neither and starts the hq binary sets it itself.

## Commands

The template's *Wiring the checks* is done: `scripts/check.sh` is hq's, `.github/workflows/tests.yml` sets up Go at `go.mod`'s version and tmux, and `scripts/local-dev.project.sh` is not used (hq runs with its own `scripts/local-dev.sh`, `docs/hq/agents.md`, *Local run*).

`scripts/check.sh` is the full build: every step the test workflows run, in their order (vet for every build tag, formatting, the pyramid gate, the platform gate, unit tests, integration tests with coverage, the coverage gate, the end-to-end journeys), and they run nothing else (`.github/workflows/private.yml`, below, checks private details apart). Run it, from any directory, before you hand work over. It prints `check: ok   <step>` per passed step and stops at the first failure with `check: FAIL <step>`, that step's output and `check: stopped at <step> on <commit>` (exit 1). Its last line, `check: all <N> steps passed on <commit>` (exit 0), is the test evidence: `<commit>` is HEAD's full sha, followed by ` with uncommitted changes` when the tree is not clean, so only a clean tree's line vouches for a commit. A passed step's output is not printed: CI puts the pyramid and coverage figures in the job summary, and locally the single commands below print them. The single steps, to run one at a time:

```bash
go test ./...                                          # unit
go test -tags integration ./...                        # unit + integration
go test -tags e2e ./e2e/...                            # end-to-end journeys
go run ./tools/testgate pyramid                        # counts per level, checks the shape
go run ./tools/testgate private [PATH...]              # no private details (needs the pattern list)
go run ./tools/testgate platform                       # platform-specific names only in internal/platform
go test -count=1 -tags integration -coverpkg=./internal/... -coverprofile=cover.out ./... \
  && go run ./tools/testgate coverage cover.out        # coverage threshold
go run ./tools/testgate mutation BASE                  # mutation tests of the lines changed against BASE (not in the full build)
```

## Mutation tests

Coverage says a line ran under a test, not that a test would fail if the line were wrong; a mutation run checks the second (#204). A mutant is one small change to the code, for example `<` into `<=`. It is killed when a unit test then fails, alive when every unit test still passes, timed out when the tests did not finish within the limit, and not covered when no unit test runs its line.

**The rule.** Every pull request that runs the full build also runs `go run ./tools/testgate mutation BASE`, each time the full build is run for it: after implementing, after review fixes, and after the rebase before the merge. It runs after the full build, on the same tree, and is not a step of it: CI does not run it, and its block in the pull request's `## QA` section is the evidence (`docs/hq/qa-evidence.md`).

- **BASE** is the branch the pull request goes into: `origin/dev` for a single issue, the release branch for a ticket of a supervised release. In a spec built on one integration branch, each ticket's implementer runs it against the integration branch before reporting, and the orchestrator runs it once on the integration branch against `origin/dev`, after the review fixes and before the pull request is ready.
- **What passes**: no mutant left alive in the changed lines. For each survivor, write a unit test that kills it, or give a one-line reason in `## QA` next to its line: a mutant that does not change behaviour (an equivalent mutant, such as `<` into `<=` where the two values are never equal) cannot be killed by any test. A timed-out mutant fails the run the same way, until a rerun kills it or it is explained. An accepted survivor stays in the command's output; the reason next to it is what makes it pass. Not covered is a count, never a failure. There is no minimum share and no figure for the whole module.
- **Exempt** are only the master pull request of a release (each of its tickets had a run) and a revert. No kind of change is exempt: where there is nothing to mutate (documents, CI files, shell scripts, tests only, Go lines with nothing to change), the command says so, and that line is the evidence.
- **Shell scripts get no mutation check**: the tool changes Go code only. Their Go integration tests stay their check.

**What it does.** It makes mutants only in the lines of Go code, not tests, that the working tree changes against its merge base with BASE, uncommitted and untracked files included: a branch behind BASE is not charged for what BASE gained meanwhile; every changed line, also one that follows unchanged lines in its hunk, since the staging repository diffs with no context lines (`diff.context` 0), where the tool reads a hunk as one run of added lines. The mutants are the five kinds the tool makes by default: arithmetic operators (`arithmetic base`), comparison boundaries (`conditionals boundary`), negated conditions (`conditionals negation`), increments and decrements (`increment decrement`) and negated numbers (`invert negatives`). Each mutant is judged by the unit tests of the whole module (`go test ./...`, no build tag), so a line counts as covered when a unit test of any package runs it, and a mutant in a `package main` directory gets a real test run. The tool is go-gremlins, pinned in `tools/testgate/mutation.go` (nothing in `go.mod`) and installed fresh for each run. A run copies the files git tracks or would track into a temporary directory (git's objects are borrowed, not copied; other worktrees and ignored directories stay out), uses a build cache of its own there and removes the directory when it ends, also when it fails or is interrupted (Ctrl-C, SIGTERM): the shared Go build cache does not grow from it. It needs the module cache to hold the tool, or the network once.

**Its output.** One line per alive and per timed-out mutant, in file and line order: the verdict, the file, the line and column, the kind. The last line gives the counts, BASE and HEAD's full sha, followed by ` with uncommitted changes` when the tree is not clean, as the full build's last line does; only a clean tree's line vouches for a commit. Progress goes to stderr.

```text
alive      tools/testgate/push.go:94:14 conditionals boundary
timed out  internal/dash/dash.go:120:9 arithmetic base
mutation: 23 killed, 1 alive, 1 timed out, 0 not covered against origin/dev on <commit>
```

When the changed lines hold no mutant, the last line is `mutation: no mutants in the lines changed against <BASE> on <commit>`. It exits 0 when no mutant is alive or timed out, 1 when one is, and 2 on an error of its own: no BASE, a BASE git does not know (`git fetch origin dev` first), unit tests that fail before any mutation, the tool failing, or an interruption.

**Cost.** The tool runs the unit tests once with coverage, then once per mutant, in parallel on every core; a mutant's run rebuilds the packages it touches and runs the unit tests the test cache cannot answer. On 10 cores the changes of pull request #148 (292 added lines of Go, 66 mutants) took 35 s in all, those of #151 (7 mutants) 20 s. A mutant times out when its run takes ten times as long as that first run, which builds the whole module from an empty cache.

**Known limits of the tool.** A mutant that does not compile is counted as killed (the tool's issue 321); for these five kinds it is rare. The tool cannot tell an equivalent mutant from a weak test: that judgement is the reason in `## QA`.

## CI

A pull request into `dev` or into a release branch (`release-vX.Y.Z`, `docs/agents/issue-tracker.md`, *Working a release*) gets a run, unlike the template's *Merging* says: `.github/workflows/pull-request-tests.yml` runs `scripts/check.sh` on macOS and Ubuntu, for every change but one of only the paths its header lists as skipped. For a ticket of a supervised release it is the only CI run before its squash, since the release branch gets no `Tests` run. Every push to `dev` runs it once more on Ubuntu (`.github/workflows/tests.yml`), for every change but one of only the paths its `paths-ignore` lists. A pull request into any other branch gets no run. A PR with a red CI job is not merged.

## Thresholds

`tools/testgate` holds the minimum unit share of unit + integration tests and the minimum statement coverage of `internal/`. Unlike the template's `docs/agents/testing.md` and `docs/agents/implementer.md` say, a pull request moves them, by these rules. Both thresholds only go up. The unit share's minimum follows the figure: a PR that raises the share raises the minimum in the same PR. The coverage minimum stays 0.5 points under CI's figure, rounded down to a tenth, so a PR that changes nothing about tests cannot fall under it: a PR that lifts the figure more than 0.5 points above the minimum raises the minimum to the figure less 0.5 (#42). Never lower one to get a PR through.

## Platform code

Both platforms behave the same, and the code that tells them apart lives in `internal/platform` only (`docs/hq/agents.md`, *Rules*; design §3.10). `testgate platform` fails on a Go file, not a test, that names a program or variable of one platform outside it: `cmd.exe`, `powershell.exe`, `wsl.exe`, `wslpath`, `sbx.exe`, `WSL_DISTRO_NAME` or `osascript` in a string (ignoring case), or `runtime.GOOS` in code (ask `platform.OS` instead). It prints the file, the line and the name. Comments are not checked, and no other word is looked for: a message to the user about the system tray passes. The list and its exemptions (`internal/platform`, the stubs in `internal/testutil`, the gate itself), each with its reason, are in `tools/testgate/platform.go`; a new exemption needs a reason there and a line in the PR.

## Private details

The repository is public. Names of private organisations and repositories, agent and repository names taken from them, a local user's paths, host names and Claude session ids stay out of the code, the docs, the fixtures and the QA evidence: use a placeholder (`<org>`, `~/work/app`, `agent-17`, `/w/app`), and when a capture carries such a detail, edit it and say so in its fixture README (#127). `testgate private` fails on a line that matches one of the patterns in the list (one regular expression per line, matched ignoring case, `#` comments); it checks the tracked files, or the PATHs given. The list itself is not in the repository: the gate reads it from `HQ_PRIVATE_PATTERNS` or, without the variable, from the file `.git/info/hq-private-patterns` of the repository it runs in (in the common git directory, `git rev-parse --git-common-dir`, so every worktree shares it; never tracked, never pushed). CI takes it from the repository secret `HQ_PRIVATE_PATTERNS` (`.github/workflows/private.yml`, on every pull request, every push to `dev` and once a day; each run also checks the tip of `qa-artifacts`), and a local run without a list passes with a notice. Ask the owner for the list to run it locally; the gate prints the file, the line and the pattern's number (counting patterns, not comment or blank lines), never the text, since CI logs are public. It reads text only: look at images yourself.

That workflow sees a commit only once it is public, and a commit that a later one cleans up stays reachable by its sha. So a push is checked before it leaves the machine (#185): the pre-push hook `scripts/git-hooks/pre-push` (enabled once per clone, `docs/hq/agents.md`, *Pushing*) runs `testgate prepush`, which checks what the push sends: the name of every branch or tag it updates, an annotated tag's message, and the message, the added lines and the added paths of every commit the remote does not have yet (when its tip there is unknown locally, of every commit no remote-tracking branch has), a merge as its change to the first parent. The names and addresses of author, committer and tagger are not checked: they are the pusher's own. It prints `private: N patterns, C commits, M matching lines` and, on a match, each commit, file and line with the pattern's number, and refuses the push. Without a list it refuses the push too, saying where the list goes: forgetting the list must not mean pushing unchecked. `git push --no-verify` skips the hook, for a push that has to go out anyway; the workflow stays the last line.
