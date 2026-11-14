# QA evidence

Every PR that works an issue hands over with QA evidence: a plan of checks derived from the issue, each check run against the running app, and proof of every result recorded in the PR body for the user's review. In a spec built with `/implement-spec`, each implementer subagent does steps 1-4 for its own ticket and commits its finished QA subsection as a file (step 4), and the orchestrator assembles the PR body (step 5) from those files with `scripts/assemble-qa.sh`, one subsection per ticket.

hq's checks in a terminal, its screenshots in every pull request, the private-details check of the evidence and the CI row: `docs/hq/qa-evidence.md`, which holds for hq where it differs from this file.

## 1. Plan the checks

Write the QA plan before implementing, from the issue's acceptance criteria (the agent brief comment, when there is one). Number the checks; each has the steps to perform and the expected result. Every acceptance criterion is covered by at least one check. Add checks for existing behaviour the change could break nearby.

List the automated tests the change adds or changes at each level (unit, integration, end-to-end), following `docs/agents/testing.md`: pure logic gets unit tests; a request handler, command or adapter gets an integration test for its contract; only a new critical journey or new JavaScript gets an end-to-end test.

Done when every acceptance criterion maps to a check and the planned tests are listed per level.

## 2. Capture the before state

For each check whose outcome is visible and will change (UI look, page content, downloaded file), capture the current state first: run `$local-dev` in the issue worktree before changing code, or in a `dev` worktree.

## 3. Run every check

Implement, then run each check against the app started with `$local-dev`, driving a real browser (Playwright, a fresh browser context per run). Record the outcome (pass or fail) and its evidence:

- **UI change**: a screenshot of every screen and state the change touches, paired with the before screenshot where the look changed. Name files `<check>-<screen>-before.png` / `-after.png`.
- **Download** (generated PDF, CSV/XLSX export): the file itself, plus a screenshot or rendered page image when its look matters.
- **Non-visual behaviour** (redirects, permissions, e-mails, commands, API responses): the command and its output, saved as a `.txt` file or quoted in the PR.

Done when every check has an outcome and evidence. A check that fails or cannot be run is recorded with that outcome and the reason.

## 4. Publish the evidence to `qa-artifacts`

Evidence lives on the `qa-artifacts` branch, which never merges into `dev`. Each issue owns the folder `issue-<n>/`; re-running QA after review replaces that folder's contents, `qa-section.md` included: in a spec, the implementer rewrites it with links pinned to the new `sha` and commits it again (below). (`scripts/setup-github.sh` creates the branch in a new repository.)

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

In a spec built with `/implement-spec`, the implementer then writes its finished subsection (step 5) with links pinned to that `sha`, in this layout and order:

1. the heading `### #<n> <ticket title>`;
2. the check table (number, check, expected, result, evidence), its evidence links pinned to `sha`;
3. the before and after screenshots of every UI change, and the short lines under the table: the pyramid counts before and after, the coverage total after, the tests added per level;
4. `#### Outputs`: every quoted command and its output, of any length. No other line of this block starts with `####`: a quoted line that does is indented by one space;
5. `#### Deviations`, last ("None." when there are none).

The pull request body takes everything except the `#### Outputs` block (step 5), so the quoted commands and outputs (the fenced blocks) go under `#### Outputs`; the short lines of item 3 stay under the table. The implementer commits and pushes the file to `qa-artifacts` as `issue-<n>/qa-section.md`, as a second commit: a file cannot hold the sha of its own commit.

```bash
qa=$(mktemp -d)
git fetch origin qa-artifacts
git worktree add --detach "$qa" origin/qa-artifacts
cp <subsection file> "$qa/issue-<n>/qa-section.md"
git -C "$qa" add "issue-<n>/qa-section.md" && git -C "$qa" commit -m "QA section for #<n>"
until git -C "$qa" push origin HEAD:qa-artifacts; do git -C "$qa" pull --rebase origin qa-artifacts; done
git worktree remove --force "$qa"
```

The implementer reports only that it is done and the path (`issue-<n>/qa-section.md`); it does not paste the section into its report. The review-fixes implementer of a spec does the same under `issue-<spec>/`, with the heading `### #<spec> Review fixes`.

## 5. Write the `## QA` section of the PR body

Link evidence pinned to `sha`. `<repo>` is this repository's `owner/name` (`gh repo view --json nameWithOwner -q .nameWithOwner`):

- Image: `![<check> after](https://github.com/<repo>/blob/<sha>/issue-<n>/<file>?raw=true)`
- Other file: `[<file>](https://github.com/<repo>/blob/<sha>/issue-<n>/<file>)`

Read the PR body with `gh pr view <pr> --json body --jq .body`; write the edited body back from a file with `gh api --method PATCH repos/{owner}/{repo}/pulls/<pr> -F body=@<file>`.

```markdown
## QA

| # | Check | Expected | Result | Evidence |
|---|-------|----------|--------|----------|
| 1 | Login page uses brand colours | Orange buttons on #fff1e5 | ✅ pass | before / after below |
| 2 | Exported PDF recoloured | No green elements | ❌ fail: header bar still green | export.pdf |

### 1. Login page uses brand colours
Before: ![...](...)  After: ![...](...)
```

Below the check table, quote the test pyramid counts from before and after the change and the coverage total after it (the commands are in `docs/agents/testing.md`), and list the tests added per level (unit, integration, end-to-end). End the section with the full build (defined below); a failed one blocks the merge.

In a spec pull request the `## QA` section has one `### #<n> <ticket title>` subsection per ticket, each with its own table, pyramid counts and evidence pinned to its own `sha` in `issue-<n>/`; the full build is one for the whole pull request, run by the orchestrator on the integration branch's head once every ticket and the review fixes are merged. The orchestrator does not write these subsections. A single issue (`/implement`) is unchanged: its agent writes the PR body itself.

The full build is the last block of `## QA`: the output of `scripts/check.sh` (`docs/agents/testing.md`, *Merging*) run on the head commit of the pull request's branch. A pull request gets no `Tests` run in CI, so there is no run to link; the `Pull request` workflow checks only the description and the commits. Paste the script's lines as they are, under a heading of their own (the steps here are the Python stack's; another stack's script names its own):

~~~markdown
### Full build

```text
check: ok   sync
check: ok   lint
check: ok   format
check: ok   pyramid
check: ok   unit
check: ok   integration
check: ok   coverage
check: ok   e2e
check: all 8 steps passed on 4f0c2a9e1b7d3c5a8e6f9b0d2c4a6e8f1b3d5c7a
```
~~~

The sha in the last line is the head commit of the branch on origin (`git rev-parse origin/<branch>` prints the same). A last line ending in `with uncommitted changes` or a `FAIL` line is not evidence: commit, push and run the script again. After a rebase before the merge, the block is replaced with the new run's. The pull request is marked ready only on a passing full build.

GitHub limits a pull request body to 65,536 characters, and the full QA sections of a spec exceed it (the nine sections of one spec came to 94 KB). So the orchestrator builds the section with `scripts/assemble-qa.sh <spec> <ticket>...`. The script fetches `qa-artifacts` and prints `## QA` with the subsections in ticket-number order and `### #<spec> Review fixes` last, when that one is published. Each subsection is `issue-<n>/qa-section.md` without its `#### Outputs` block: everything from `#### Outputs` up to the next `####` heading is replaced by one line linking the file, pinned to the commit that holds it (a file with no `#### Outputs` heading gets the link as its last line). The check table, the screenshots and the Deviations of every ticket stay in the body. The script fails, printing nothing, when a ticket's file is missing, when a `/blob/` link into the repository is not pinned to the full sha of a commit or its file does not exist there, or when the section is over `--limit`. The recipe:

```bash
rest=$(cat <body before QA> <body after QA> | wc -m)
scripts/assemble-qa.sh --limit $((65536 - rest - 600)) <spec> <ticket>... > <qa file>   # 600: room for the full build block
<the full build block> >> <qa file>
cat <body before QA> <qa file> <body after QA> > <body file>
wc -m <body file>     # the final check: under 65,536
gh api --method PATCH repos/{owner}/{repo}/pulls/<pr> -F body=@<body file>
```

When the script fails on the limit, it names every subsection with its size, largest first. The orchestrator re-runs it with `--link-only <n>` for the largest ones, largest first, until it passes: such a subsection is its heading and its link line, nothing else, and each one is named under "Deviations from the issue". The orchestrator never retypes or summarises a section.

Done when every check from the plan has a row, every linked file exists at `sha` (`gh api "repos/{owner}/{repo}/contents/issue-<n>/<file>?ref=<sha>"` succeeds), and each failure is explained. Failed checks also appear under the PR's deviations.

In a spec pull request the orchestrator is done when the script exited 0 and the body is under the limit.
