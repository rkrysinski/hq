# Release QA: this project's steps

Read by the release tester in `/release-ship`. Release QA runs once per release, on the candidate (the release branch tip after the version bump), after every ticket is merged. How the results are written into the master PR's `### Release QA` and where evidence goes are the skill's and `docs/agents/qa-evidence.md`'s; this file holds only the project's steps.

## Candidate

hq deploys nothing: the candidate is the release worktree at the candidate's commit, and every step runs there.

1. `cd .claude/worktrees/release-vX.Y.Z`, from the primary checkout (`X.Y.Z` the release's version)
2. `test -z "$(git status --short)" && test "$(git rev-parse HEAD)" = "$CANDIDATE_SHA"`: the worktree stays on the release branch, and when its tip is not the candidate, Release QA stops there

## Steps

In order. ETA and spend are estimates the skill reports against; evidence is what the tester records for the step (a file, a report section, a command's output). The first three run in the sandbox, which has no real `sbx`; the fourth is the user's, on the host, before go.

| Step | Command | ETA | Spend, USD | Evidence |
| --- | --- | --- | --- | --- |
| Full build | `scripts/check.sh` | 5 minutes | 0 | its lines, the last naming `$CANDIDATE_SHA` without ` with uncommitted changes` |
| Release files of all four platforms | `scripts/build-release.sh vX.Y.Z .local-dev/release-files && ls .local-dev/release-files && ".local-dev/release-files/hq-linux-$(go env GOARCH)" --version` | 3 minutes | 0 | the listing (`hq-darwin-amd64`, `hq-darwin-arm64`, `hq-linux-amd64`, `hq-linux-arm64`, `install.sh`, `SHA256SUMS`) and `hq vX.Y.Z` |
| One journey on the stubbed setup | the commands under *Journey* below | 10 minutes | 0 | a terminal screenshot after each of its steps and the commands' output |
| On the host, before go: the built binary against a real sandbox | the commands under *Host step* below, run by the user | 10 minutes | 0 | the user's word on the release issue that each command did what it says, with any output that differed |

### Journey

From the candidate's worktree, with the stub `sbx` (`docs/hq/agents.md`, *Local run*) and a tmux pane as the terminal (`docs/hq/qa-evidence.md`):

```bash
export LOCAL_DEV_ID=release-qa
scripts/local-dev.sh --stub native --version             # builds the candidate, creates /tmp/hq-stub-release-qa
tmux -L release-qa-term new-session -d -s rq-term -x 120 -y 30 -c "$PWD"   # the terminal for the screenshots
```

Then in that terminal's pane (`tmux -L release-qa-term send-keys -t rq-term: "COMMAND" Enter`), each followed by `scripts/qa-screenshot.sh -L release-qa-term rq-term: STEP.png`:

1. `scripts/local-dev.sh --stub native new rq /tmp/hq-stub-release-qa/repo "say hi"` - the agent starts.
2. `scripts/local-dev.sh --stub native ls` - it is listed, and done once it answered.
3. `scripts/local-dev.sh --stub native send rq "say bye"`, then `scripts/local-dev.sh --stub native read rq` - its next reply.
4. `scripts/local-dev.sh --stub native kill rq -y`, then `scripts/local-dev.sh --stub native ls` - it is gone.

Clean up: `tmux -L release-qa-term kill-server` and `scripts/local-dev.sh --kill-server`.

### Host step

On the host, in the repository's primary checkout, with the binary of the host's platform from the release files of step 2 (they are in the release worktree, which the host shares; `hq-darwin-arm64` on an Apple silicon Mac, `hq-linux-amd64` in WSL), against a tmux server of its own so the user's own hq is not touched:

```bash
hq=.claude/worktrees/release-vX.Y.Z/.local-dev/release-files/hq-darwin-arm64
export HQ_TMUX_SOCKET=hq-release-check
"$hq" --version                    # hq vX.Y.Z
"$hq" new rq . "say hi"            # starts Claude in the repository's real sandbox
"$hq" ls                           # rq listed, done once it answered
"$hq" send rq "say bye"            # its next reply follows
"$hq" kill rq -y                   # ended; the sandbox stays
tmux -L hq-release-check kill-server
```
