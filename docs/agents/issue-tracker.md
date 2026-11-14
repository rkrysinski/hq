# Issue tracker: GitHub

Issues and PRDs for this repo live as GitHub issues on the repository behind the `origin` remote (`gh repo view --json nameWithOwner -q .nameWithOwner`). Use the `gh` CLI for all operations; it infers the repo from `git remote -v` when run inside a clone.

Running the checks and cleaning up an issue's worktree in hq: `docs/hq/issue-tracker.md`, which holds for hq where it differs from this file.

## Conventions

- **Create an issue**: `gh issue create --title "..." --body "..."`. Use a heredoc for multi-line bodies.
- **Read an issue**: `gh issue view <n> --json title,body,comments` (add `labels,state` to the fields when you need them). Always pass `--json` when viewing an issue or a pull request: the plain forms fail on the older `gh` (2.46) found in sandboxes.
- **Edit an issue's description**: write the new body to a file, then `gh api --method PATCH repos/{owner}/{repo}/issues/<n> -F body=@<file>` (`-f body="..."` for one line).
- **List issues**: `gh issue list --state open --json number,title,body,labels --jq '[.[] | {number, title, body, labels: [.labels[].name]}]'` with appropriate `--label` and `--state` filters.
- **Comment**: `gh issue comment <n> --body "..."`. Comment only what someone needs later: a decision, a finding, a blocker, a question. Progress, commit notices and test counts belong in the PR and CI; lessons that apply across issues go to `docs/agents/lessons.md`.
- **Apply / remove labels**: `gh issue edit <n> --add-label "..."` / `--remove-label "..."`
- **Close**: `gh issue close <n> --reason completed --comment "..."`; for `wontfix` use `--reason "not planned"`.
- **Read a pull request**: `gh pr view <n> --json title,body,state` (name the fields you need).
- **Edit a pull request** (title, body): `gh api --method PATCH repos/{owner}/{repo}/pulls/<n> -F body=@<file>` / `-f title="..."`. Mark a draft ready with `gh pr ready <n>`.
- A closed issue is resolved.
- **Never set a milestone by hand** outside a supervised release. Milestones are versions, and
  `scripts/release.sh` creates `vX.Y.Z`, puts every issue and pull request of that release in it,
  and closes it when the release is published. Assigning one during triage would claim a version
  that has not shipped. To see what went into a version, read its milestone or its release notes;
  see `docs/releasing.md`. A supervised release (*Working a release*, *Milestone*) is the one
  exception: its milestone and tickets exist before `scripts/release.sh` runs.
- **Type labels** (`enhancement`, `bug`, `internal`) decide where an issue appears in the release
  notes, so give every issue one — see `docs/agents/triage-labels.md`.

Most GitHub API calls below need an issue's numeric **database id**, not its `#number`: `gh api repos/{owner}/{repo}/issues/<n> --jq .id` (`gh api` fills in `{owner}/{repo}`).

## Language

- Comments and sub-issues use the language of the issue they belong to (stakeholder requests are often in Polish).
- New PRDs and issues default to English unless the user writes in Polish.
- Domain terms always match `GLOSSARY.md`, whatever the surrounding language.

## PRDs and implementation issues

- **PRD**: an issue labelled `prd`.
- **Implementation issue**: a GitHub **sub-issue** of the PRD. Create the issue, then link it:
  `gh api --method POST repos/{owner}/{repo}/issues/<prd>/sub_issues -F sub_issue_id=<child-db-id>`
- **List a PRD's issues**: `gh api repos/{owner}/{repo}/issues/<prd>/sub_issues --jq '.[] | {number, title, state}'`
- **Blocking**: GitHub's native issue dependencies.
  - Add: `gh api --method POST repos/{owner}/{repo}/issues/<blocked>/dependencies/blocked_by -F issue_id=<blocker-db-id>`
  - List: `gh api repos/{owner}/{repo}/issues/<n>/dependencies/blocked_by --jq '.[] | {number, state}'`
  - An issue is unblocked when `gh api repos/{owner}/{repo}/issues/<n> --jq .issue_dependencies_summary.blocked_by` is `0` (it counts open blockers only).

## Working a spec

A spec issue labelled `prd` whose sub-issues carry `ready-for-agent` is built as a whole when the user runs `/implement-spec #<spec>`. The skill reads the tickets as a task graph (the blocking edges from `/to-tickets`), works the unblocked frontier with implementer subagents in parallel, and lands everything on one integration branch and one pull request. This repository's conventions for that run:

1. Integration branch `spec-<n>-<slug>` (n is the spec issue) from `origin/<base>`, in its own worktree: `git fetch origin <base>`, then `git worktree add -b spec-<n>-<slug> .claude/worktrees/spec-<n>-<slug> origin/<base>`. `<base>` is the branch the brief that started this run names; `dev` when no brief names one. The primary checkout stays on `dev`.
2. Each implementer works its ticket in `.claude/worktrees/issue-<n>-<slug>` on branch `issue-<n>-<slug>`, created from the integration branch (not from `dev`). The orchestrator's brief to an implementer names the ticket, the spec, the integration branch, the worktree path and `docs/agents/implementer.md`, the implementer's process doc. The brief restates nothing from that file and does not send the implementer to this one.
3. The orchestrator merges each finished ticket branch into the integration branch inside the integration worktree (`git -C .claude/worktrees/spec-<n>-<slug> merge --ff-only issue-<m>-<slug>`), pushes it, and starts the tickets this unblocks.
4. After the first merge, a draft pull request from the integration branch to `<base>`: `gh pr create --draft --base <base> --title "..." --body-file <file>`: the title states the spec's outcome; the body follows `.github/PULL_REQUEST_TEMPLATE.md` with `Closes #<spec>` and one `Closes #<ticket>` per ticket, Summary, Deviations from the issue, `## QA` with one `### #<n> <ticket title>` subsection per ticket, Merge Danger; and, when a ticket's QA subsection names deploy steps under `#### Deviations`, `## Before deployment` and/or `## After deployment` with those steps and `## Rollback` with their compensations, copied from the subsections. A branch with no commits ahead of `<base>` cannot open a pull request, hence after the first merge.
   - The orchestrator assembles `## QA` from the files the implementers committed with `scripts/assemble-qa.sh <spec> <ticket>...` (`docs/agents/qa-evidence.md`, step 5: the recipe and the limit on the body's length), appends the full build block defined there once it has run (step 5), and writes the body from a file with `gh api --method PATCH repos/{owner}/{repo}/pulls/<pr> -F body=@<file>`. It never retypes or summarises a section.
5. When every ticket is merged: `mattpocock-skills:code-review` over the whole integration branch against `origin/<base>` (the fixed point the skill asks for), the fixes in one implementer, the full build (`scripts/check.sh`) on the integration branch's head in `## QA`, the pull request marked ready, and a comment on the spec issue with the link. Before the merge, a branch behind `<base>` is rebased and built again (`docs/agents/testing.md`, *Merging*).
   - **Review fixes** go on branch `issue-<spec>-review-fixes` in its own worktree, its commits naming the spec (`Closes #<spec>`), created from the integration branch: `git worktree add -b issue-<spec>-review-fixes .claude/worktrees/issue-<spec>-review-fixes spec-<spec>-<slug>`.
   - Every finding of `mattpocock-skills:code-review` goes to that implementer, a glossary drift included (a domain term in the code that `GLOSSARY.md` lacks or describes differently); none is left to a human. It follows `docs/agents/implementer.md`, *Review fixes*, and the orchestrator merges its branch like a ticket's (`merge --ff-only issue-<spec>-review-fixes`, step 3).

A gap in a ticket that changes what gets built stops that ticket the same way as a single issue: the concrete questions on the ticket, `ready-for-agent` swapped for `needs-info`; the other tickets go on. With `<base>` = `dev`, the tickets close on the squash merge with the default commit message (`AGENTS.md`, *Issue tracker*). With `<base>` a release branch, nothing closes at the squash: the squash commit carries the `Closes` lines to `dev` at the merge-back, and the spec closes through the master pull request's `## Closes` (*Working a release*). Without review-fix commits no commit names the spec, so the spec closes only if GitHub links the body's `Closes #<spec>` line; otherwise the user closes it by hand (see `docs/agents/lessons.md`).

The rule for a defect outside the scope (*Working a single issue*, after step 4) applies to every ticket and to what the orchestrator finds while merging or reviewing.

Cleanup: a ticket worktree once its branch is merged into the integration branch (`git worktree remove --force .claude/worktrees/issue-<n>-<slug>`, then `git -C .claude/worktrees/spec-<m>-<slug> branch -d issue-<n>-<slug>`, the skill's last step); the integration worktree and branch after the pull request is merged, with the rules under *Cleaning up after a PR closes* (`-D`, remote delete). The rule for the ticket branch: it is deleted from inside the integration worktree with `git branch -d`, or with `-D` after its merge into the integration branch is verified. `git branch -d` compares the branch with the `HEAD` of the worktree it runs in; the primary checkout is on `dev`, which does not contain a branch merged only into the integration branch, so from there the same command fails with "not fully merged". The fallback, from the primary checkout: `git merge-base --is-ancestor issue-<n>-<slug> spec-<m>-<slug> && git branch -D issue-<n>-<slug>`. The review-fixes branch (`issue-<spec>-review-fixes`) is cleaned up the same way.

## Working a single issue

An issue labelled `ready-for-agent` that is not a ticket of a spec (a bug, a request that came through triage) is the go-ahead for `/implement`: carry it through the steps below to the pull request without asking for confirmation. Stop only for a gap that changes what gets built: an ambiguous or contradictory acceptance criterion, or a decision the issue leaves open. Then comment the concrete questions on the issue, swap `ready-for-agent` for `needs-info`, and leave the issue. What the code, `GLOSSARY.md`, the ADRs or `docs/agents/lessons.md` answer is not a question. A request to brainstorm, explore or plan stays discussion-first.

Several agents may work different issues at the same time, each in its own git worktree. Work lands through pull requests the user reviews:

1. Branch `issue-<n>-<short-slug>` in a dedicated worktree at `.claude/worktrees/<branch>` inside the primary checkout, from `origin/<base>`: `git fetch origin <base>`, then `git worktree add -b <branch> .claude/worktrees/<branch> origin/<base>`. `<base>` is the branch the brief that started this work names; `dev` when no brief names one. `mattpocock-skills:code-review`, when `/implement` runs it, takes `origin/<base>` as its fixed point.
2. Plan QA checks from the acceptance criteria, implement, run every check against the running app (see `$local-dev`), and publish the evidence - screenshots of every UI change, downloaded files, command output - to the `qa-artifacts` branch. Follow `docs/agents/qa-evidence.md`. Every commit message on the branch carries a `Closes #<n>` line (`AGENTS.md`, *Issue tracker*).
3. Run the full build, `scripts/check.sh` (`docs/agents/testing.md`, *Merging*); push the branch only when it passes, and open a PR against the same base: `gh pr create --base <base> --title "..." --body "..."`. The title states the intended outcome ("Show the delivery date on the order list", not "Update order view" or "Fix #12"); the body follows `.github/PULL_REQUEST_TEMPLATE.md`, and the `Pull request` workflow (`scripts/check-pr.sh`) fails a PR that is ready for review without it. The PR body contains `Closes #<n>`, a summary (the smallest visual that makes the change clear, per the `pr` skill), deviations from the issue, a `## QA` section with every check's result and its evidence inline, and `## Merge Danger` (door and blast radius). When the change needs anything outside the code to go live - IT consents, a mailbox, environment variables, cron entries, a manual command - the body also has `## Before deployment` and/or `## After deployment` with those steps as a checklist, in English; `scripts/release.sh` copies them verbatim into the release notes, all but a ticked (done) step, so they hold only steps the operator or IT performs to deploy - follow-up work, plans and "next steps" go in issues (see `docs/releasing.md`, *Release notes*).
4. Comment on the issue with a link to the PR.
5. Before the merge: a branch behind `<base>` is rebased onto it, built again with `scripts/check.sh` and pushed; the merge waits while the latest `Tests` run on `dev` is red (`docs/agents/testing.md`, *Merging*).

A defect found while working the issue or running its QA that this change did not cause, and that does not stop an acceptance criterion, is outside the scope: it is not fixed in this pull request. Search first: `gh issue list --state open --search "<words>" --json number,title`. When no open issue describes it, create one: `gh issue create --title "..." --label bug --label needs-triage --body "..."`; the body says what happens, how to reproduce it, and where it was found (the issue being worked, the check, the commit). It is a plain issue: not a sub-issue of the spec, without `ready-for-agent`, in the language the *Language* section gives. Link the issue, found or created, from Deviations: `#### Deviations` of the QA subsection in a spec, `## Deviations from the issue` in a single-issue pull request. A defect this change causes, or one that stops an acceptance criterion, is in scope and is fixed here.

With `<base>` = `dev`, the issue closes when the user squash-merges the PR with the default commit message; with `<base>` a release branch, the squash into that branch closes nothing and the issue closes at the merge-back (*Working a release*); after a PR merged another way, the user closes it by hand. Commit straight to `dev` only when the user explicitly asks; that commit carries the `Closes #<n>` line too.

### Cleaning up after a PR closes

Clean up a worktree once its PR is closed (`gh pr view <pr> --json state` is `MERGED` or `CLOSED`); for a spec, the integration worktree and branch. The `qa-artifacts` evidence stays.

- **Merged**: confirm the PR is merged (`gh pr view <pr> --json state` is `MERGED`; a squash merge leaves the branch's own commits outside `dev`, so `git merge-base --is-ancestor` says nothing useful) and the worktree has no uncommitted changes (`git -C .claude/worktrees/<branch> status --short` is empty). Then, from the primary checkout: `scripts/local-dev.sh stop` inside the worktree, `git worktree remove --force .claude/worktrees/<branch>` (`--force` only discards ignored files such as `.venv/` and `.local-dev/`; if the directory is left behind, `rm -rf` it and `git worktree prune`), `git pull --ff-only` on `dev`, `git branch -D <branch>` (`-D`: after a squash merge `-d` refuses), and `git push origin --delete <branch>` when the remote branch still exists (`gh pr merge --delete-branch` fails while a worktree holds the branch, so it usually does).
- **Closed without merging**: ask the user before removing anything; the branch may hold the only copy of the work.

A spec (`prd`) built with `/implement-spec` closes with its pull request (see *Working a spec*). A spec worked ticket by ticket closes when its last open sub-issue's PR merges (add `Closes #<prd>` to that PR's body and commit messages only if every other sub-issue is already closed), or manually. In a release, either way, the spec closes through the master pull request's `## Closes` at the merge-back (*Working a release*).

## Working a release

A release built with the release skills (`/release-prepare`, `/release-implement`, `/release-ship`) puts a milestone's tickets on one release branch, verifies it as a whole, tags it, and merges it back. This section is what the repository fixes for that; the skills hold the procedure. The merger and the scribe roles read only this section of this file.

**Release issue.** One issue per release, label `release` (one of the labels `scripts/setup-github.sh` creates, `docs/agents/triage-labels.md`), title `Release vX.Y.Z`, in the milestone; its comments are the release's log. The milestone description holds only a link to it.

**Milestone.** The operator's plan creates the milestone `vX.Y.Z` and puts the release's tickets and its release issue in it. An issue the release does not ship (gapped, parked, `out`, excluded, reverted) leaves it for no milestone. A gapped ticket and its parked dependents leave at once, unless *Gapped ticket rejoins* in `docs/agents/supervisor.md`, *Agent policy*, is on: then they stay until the next wave goes out, and rejoin it if the ticket is back to `ready-for-agent` by then. A renumber (*Candidate and tag*) renames it, or, when the old one is already the hotfix's, creates the new one on the operator's word and moves the release's issues into it. No other milestone is created or used.

**Branches.** `release-vX.Y.Z` from the `dev` commit the scope scan recorded, so nothing merged after the operator's disposition ships unseen (from the latest published tag for a hotfix release), in the worktree `.claude/worktrees/release-vX.Y.Z`. Tickets and specs of the release are worked exactly as *Working a single issue* and *Working a spec* say, with `<base>` = `release-vX.Y.Z`: their branches fork from it and their pull requests target it. The supervisor checks the base of every new pull request and corrects a wrong one with `gh api --method PATCH repos/{owner}/{repo}/pulls/<pr> -f base=release-vX.Y.Z`, checked with `gh pr view <pr> --json baseRefName`, then closes and reopens the pull request (`-f state=closed`, then `-f state=open`), which reruns its checks against the new base.

**Ticket pull requests** follow `.github/PULL_REQUEST_TEMPLATE.md`, its `## Rollback` section included: one line per step of `## Before deployment` and `## After deployment` naming the step's compensation, or `None: one-way door` for a step that cannot be undone (`scripts/check-pr.sh` requires it whenever a deploy section is present, in a release or not). The merger squash-merges a ticket pull request into the release branch with the default commit message once the integration review has no open *block* finding, the branch is up to date with the release branch, the `Pull request` check is green on that head and the full build in its `## QA` section names that head (`docs/agents/testing.md`, *Merging*; the release branch gets no `Tests` run).

**Bookkeeping commits** on the release branch (the version bump, review fixes, a revert, a hotfix merge resolution) carry `Closes #<release issue>` on a line of their own, the way every commit carries a `Closes` line. The version bump is `scripts/bump-version.sh X.Y.Z`, the last commit before the tag.

**Who merges what.** The merger role merges ticket pull requests into the release branch; only the operator merges the master pull request into `dev` and pushes the tag. A *block* finding of the integration review goes back to the ticket's implementer, never to the operator.

**Scope.** Everything on `dev` since the latest tag ships with the next release, whatever its milestone: a merged pull request or a direct commit outside the milestone is listed in the release issue for the operator's disposition, include or revert before release. Taking an issue out of the milestone never removes code already on `dev` or on the release branch.

**Candidate and tag.** The release branch tip after the version bump is the candidate: Release QA runs on it, and `scripts/cut-release.sh X.Y.Z --no-push` in release mode (on the release branch) tags it locally; the operator publishes by `git push origin vX.Y.Z`, which runs `scripts/release.sh`. The release branch gets no `Tests` run, so `scripts/check.sh` passes on the candidate before it is tagged, its lines in the master pull request's `### Full build` (`docs/agents/testing.md`, *Merging*). A code change after Release QA makes a new candidate; a local tag on an old candidate is deleted and the new one tagged. Before the tag push, every open issue of the milestone the release does not ship leaves it (*Milestone*) and is listed on the release issue; the release issue and the merged tickets and specs stay in it and close at the merge-back. A hotfix released from the latest tag while a release is in progress takes the next patch number; the supervisor renumbers the release in progress (the version, the release issue title, the milestone) on the operator's word, and the hotfix tag is merged into the release branch before the release is tagged. The branch keeps its name: renaming the head of the open master pull request would close it.

**Master pull request.** One, from `release-vX.Y.Z` to `dev`, opened as a draft after the first ticket merge (or after the version bump when no ticket merged) and marked ready before the tag as the operator's go/no-go reading. Its body follows `.github/PULL_REQUEST_TEMPLATE/release.md`. The only closing keywords in it stand in `## Closes`: `Closes #<release issue>` and one `Closes #<prd>` per spec of the release; every other issue reference is a bare `#n`, and `scripts/check-pr.sh` fails a closing keyword elsewhere in a pull request whose head is `release-vX.Y.Z`. After the tag push the operator merges it with a merge commit (`gh pr merge <pr> --merge`, never squash): `dev` keeps one commit per ticket with its `Closes` line, and the tagged commit stays an ancestor of `dev`. That merge closes the tickets (their squash commits reach `dev`), the specs and the release issue; it finishes the release. The production deploy is outside this process: `## Rollout plan` in the master pull request and the ticket pull requests' `## Rollback` are its handover.

**Revert** (`/release-implement --revert #n`, run only on the operator's word, logged on the release issue): the ticket's squash commit is reverted on the release branch (`Closes #<release issue>` in the message); in the ticket pull request `Closes #n` is removed, `## Before deployment` and `## After deployment` are renamed `## Reverted deployment steps` and the title gets the prefix `Reverted:`, so `scripts/release.sh` lists it under Internal with no deploy steps; the issue leaves the milestone for no milestone and is reopened after the merge-back, because its squash commit still reaches `dev` and closes it again.

**Trailer.** Every commit and pull request comment an automated role makes carries a `Supervisor-Role: <role>` trailer (`merger`, `scribe`, `release-tester`, `supervisor`; `implementer` on an implementer's retro comment).

**Retro.** Before an hq agent of the release is killed, it runs `/retro` and posts its candidates as a comment on its own pull request (the master pull request for the release tester); the supervisor merges them on the release issue.

**Cleanup** after the merge-back: ticket worktrees and branches as *Cleaning up after a PR closes* says; the release worktree and branch the same way once the master pull request is merged.

## Pull requests as a triage surface

**PRs as a request surface: no.** External PRs are not triaged. Agent PRs exist only for the user's review.

GitHub shares one number space across issues and PRs, so a bare `#42` may be either - resolve with one call: `gh api repos/{owner}/{repo}/issues/42 --jq '.pull_request != null'` (`true` is a pull request, `false` an issue).

## When a skill says "publish to the issue tracker"

Create a GitHub issue (a `prd`-labelled issue for a PRD; sub-issues for its implementation issues).

## When a skill says "fetch the relevant ticket"

Run `gh issue view <n> --json title,body,comments`.

## Wayfinding operations

Used by `/wayfinder`. The **map** is a single issue with **child** issues as tickets.

- **Map**: an issue labelled `wayfinder:map`, holding the Notes / Decisions-so-far / Fog body, edited in place with `gh api --method PATCH repos/{owner}/{repo}/issues/<map> -F body=@<file>`.
- **Child ticket**: a sub-issue of the map (see above), labelled `wayfinder:<type>` (`research`/`prototype`/`grilling`/`task`).
- **Blocking**: native issue dependencies (see above). A ticket is unblocked when it has no open blockers.
- **Frontier**: the map's open sub-issues without the `claimed` label and with no open blockers; lowest number wins.
- **Claim** (the session's first write): `gh issue edit <n> --add-label claimed`, then `gh issue comment <n> --body "Claimed by <session/worktree>"`. Re-read the comments: if another claim comment precedes yours, back off to the next frontier ticket. Assignees are not used — all agents act under the same GitHub identity.
- **Resolve**: `gh issue comment <n> --body "## Answer ..."`, then `gh issue close <n> --reason completed`, then append a context pointer (gist + link to the answer comment) to the map's Decisions-so-far.
