# QA evidence

Every PR that works an issue hands over with QA evidence: a plan of checks derived from the issue, each check run against hq as it runs, and proof of every result recorded in the PR body for the user's review.

## 1. Plan the checks

Write the QA plan before implementing, from the issue's acceptance criteria (the agent brief comment, when there is one). Number the checks; each has the steps to perform and the expected result. Every acceptance criterion is covered by at least one check. Add checks for existing behaviour the change could break nearby.

List the automated tests the change adds or changes at each level (unit, integration, end-to-end), following `docs/agents/testing.md`: pure logic gets unit tests; a command or adapter gets an integration test for its contract; only a new critical journey gets an end-to-end test.

Done when every acceptance criterion maps to a check and the planned tests are listed per level.

## 2. Capture the before state

For each check whose outcome is visible and will change (dashboard look, command output, a file hq writes), capture the current state first: run hq in the issue worktree before changing code, or in a `main` worktree.

## 3. Run every check

Implement, then run each check against hq started from the issue worktree, driving it in a real terminal the way a person would (see `AGENTS.md`, *Local run*). Record the outcome (pass or fail) and its evidence:

- **Dashboard change**: a screenshot of every screen and state the change touches (a terminal screenshot, or the captured screen text when colour does not matter), paired with the before screenshot where the look changed. Name files `<check>-<screen>-before.png` / `-after.png`.
- **File** hq writes: the file itself.
- **Non-visual behaviour** (commands, exit codes, what hq does to tmux or `sbx`): the command and its output, saved as a `.txt` file or quoted in the PR.

Done when every check has an outcome and evidence. A check that fails or cannot be run is recorded with that outcome and the reason.

## 4. Publish the evidence to `qa-artifacts`

Evidence lives on the `qa-artifacts` branch, which never merges into `main`. Each issue owns the folder `issue-<n>/`; re-running QA after review replaces that folder's contents. (`scripts/setup-github.sh` creates the branch in a new repository.)

```bash
qa=$(mktemp -d)
git fetch origin qa-artifacts
git worktree add --detach "$qa" origin/qa-artifacts
mkdir -p "$qa/issue-<n>" && cp <evidence files> "$qa/issue-<n>/"
git -C "$qa" add "issue-<n>" && git -C "$qa" commit -m "QA evidence for #<n>"
until git -C "$qa" push origin HEAD:qa-artifacts; do git -C "$qa" pull --rebase origin qa-artifacts; done
sha=$(git -C "$qa" rev-parse HEAD)
git worktree remove --force "$qa"
```

Parallel agents push to the same branch; the retry loop rebases onto their commits, and separate `issue-<n>/` folders keep those rebases conflict-free. Take `sha` after the push succeeds.

## 5. Write the `## QA` section of the PR body

Link evidence pinned to `sha`. `<repo>` is this repository's `owner/name` (`gh repo view --json nameWithOwner -q .nameWithOwner`):

- Image: `![<check> after](https://github.com/<repo>/blob/<sha>/issue-<n>/<file>?raw=true)`
- Other file: `[<file>](https://github.com/<repo>/blob/<sha>/issue-<n>/<file>)`

```markdown
## QA

| # | Check | Expected | Result | Evidence |
|---|-------|----------|--------|----------|
| 1 | Attention view sorts waiting agents first | Waiting rows above running ones | ✅ pass | before / after below |
| 2 | `hq kill` asks before killing | Kill dialog shown | ❌ fail: killed without asking | kill.txt |

### 1. Attention view sorts waiting agents first
Before: ![...](...)  After: ![...](...)
```

Below the check table, list the tests added per level (unit, integration, end-to-end); once hq has a pyramid check (`docs/agents/testing.md`), quote its output from before and after the change. Add a row for the CI run on the PR (every job green, with a link to the run); a red run blocks the merge.

Done when every check from the plan has a row, every linked file exists at `sha` (`gh api "repos/{owner}/{repo}/contents/issue-<n>/<file>?ref=<sha>"` succeeds), and each failure is explained. Failed checks also appear under the PR's deviations.
