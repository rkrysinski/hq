# Issue tracker: GitHub

Issues and PRDs for this repo live as GitHub issues on the repository behind the `origin` remote (`gh repo view --json nameWithOwner -q .nameWithOwner`). Use the `gh` CLI for all operations; it infers the repo from `git remote -v` when run inside a clone.

## Conventions

- **Create an issue**: `gh issue create --title "..." --body "..."`. Use a heredoc for multi-line bodies.
- **Read an issue**: `gh issue view <n> --comments` (add `--json labels,state,body` when you need structured fields).
- **List issues**: `gh issue list --state open --json number,title,body,labels --jq '[.[] | {number, title, body, labels: [.labels[].name]}]'` with appropriate `--label` and `--state` filters.
- **Comment**: `gh issue comment <n> --body "..."`
- **Apply / remove labels**: `gh issue edit <n> --add-label "..."` / `--remove-label "..."`
- **Close**: `gh issue close <n> --reason completed --comment "..."`; for `wontfix` use `--reason "not planned"`.
- A closed issue is resolved.
- **Never set a milestone by hand.** Milestones are versions, and `scripts/release.sh` creates
  `vX.Y.Z`, puts every issue and pull request of that release in it, and closes it when the release
  is published. Assigning one during triage would claim a version that has not shipped. To see what
  went into a version, read its milestone or its release notes; see `docs/releasing.md`.
- **Type labels** (`enhancement`, `bug`, `internal`) decide where an issue appears in the release
  notes, so give every issue one - see `docs/agents/triage-labels.md`.

Most GitHub API calls below need an issue's numeric **database id**, not its `#number`: `gh api repos/{owner}/{repo}/issues/<n> --jq .id` (`gh api` fills in `{owner}/{repo}`).

## Language

- Comments and sub-issues use the language of the issue they belong to (stakeholder requests are often in Polish).
- New PRDs and issues default to English unless the user writes in Polish.
- Domain terms always match `CONTEXT.md`, whatever the surrounding language.

## PRDs and implementation issues

- **PRD**: an issue labelled `prd`.
- **Implementation issue**: a GitHub **sub-issue** of the PRD. Create the issue, then link it:
  `gh api --method POST repos/{owner}/{repo}/issues/<prd>/sub_issues -F sub_issue_id=<child-db-id>`
- **List a PRD's issues**: `gh api repos/{owner}/{repo}/issues/<prd>/sub_issues --jq '.[] | {number, title, state}'`
- **Blocking**: GitHub's native issue dependencies.
  - Add: `gh api --method POST repos/{owner}/{repo}/issues/<blocked>/dependencies/blocked_by -F issue_id=<blocker-db-id>`
  - List: `gh api repos/{owner}/{repo}/issues/<n>/dependencies/blocked_by --jq '.[] | {number, state}'`
  - An issue is unblocked when `gh api repos/{owner}/{repo}/issues/<n> --jq .issue_dependencies_summary.blocked_by` is `0` (it counts open blockers only).

## Working an issue

Several agents may work different issues at the same time, each in its own git worktree. Work lands through pull requests the user reviews:

1. Branch `issue-<n>-<short-slug>` in a dedicated worktree at `.claude/worktrees/<branch>` inside the main checkout: `git worktree add -b <branch> .claude/worktrees/<branch> origin/main`.
2. Plan QA checks from the acceptance criteria, implement, run every check against hq running from the worktree (see `AGENTS.md`, *Local run*), and publish the evidence - screenshots of every UI change, downloaded files, command output - to the `qa-artifacts` branch. Follow `docs/agents/qa-evidence.md`.
3. Push the branch and open a PR: `gh pr create --title "..." --body "..."`. The PR body contains `Closes #<n>`, a summary, deviations from the issue, and a `## QA` section with every check's result and its evidence inline. When the change needs anything outside the code to go live - IT consents, a mailbox, environment variables, cron entries, a manual command - the body also has `## Przed wdrożeniem` and/or `## Po wdrożeniu` with those steps as a checklist, in Polish; `scripts/release.sh` copies them into the release notes (see `docs/releasing.md`, *Release notes*).
4. Comment on the issue with a link to the PR.

The issue closes when the user merges the PR. Commit straight to `main` only when the user explicitly asks; then put `Closes #<n>` in the commit message.

### Cleaning up after a PR closes

Clean up a worktree once its PR is closed (`gh pr view <pr> --json state` is `MERGED` or `CLOSED`). The `qa-artifacts` evidence stays.

- **Merged**: confirm the branch is contained in `origin/main` (`git fetch --prune origin && git merge-base --is-ancestor <branch> origin/main`) and the worktree has no uncommitted changes (`git -C .claude/worktrees/<branch> status --short` is empty). Then, from the main checkout: stop anything the worktree still runs, `git worktree remove --force .claude/worktrees/<branch>` (`--force` only discards ignored files), `git pull --ff-only` on `main`, then `git branch -d <branch>` (it refuses until local `main` contains the merge).
- **Closed without merging**: ask the user before removing anything; the branch may hold the only copy of the work.

A PRD closes when its last open sub-issue's PR merges (add `Closes #<prd>` to that PR only if every other sub-issue is already closed), or manually.

## Pull requests as a triage surface

**PRs as a request surface: no.** External PRs are not triaged. Agent PRs exist only for the user's review.

GitHub shares one number space across issues and PRs, so a bare `#42` may be either - resolve with `gh issue view 42` and fall back to `gh pr view 42`.

## When a skill says "publish to the issue tracker"

Create a GitHub issue (a `prd`-labelled issue for a PRD; sub-issues for its implementation issues).

## When a skill says "fetch the relevant ticket"

Run `gh issue view <n> --comments`.

## Wayfinding operations

Used by `/wayfinder`. The **map** is a single issue with **child** issues as tickets.

- **Map**: an issue labelled `wayfinder:map`, holding the Notes / Decisions-so-far / Fog body, edited in place with `gh issue edit <map> --body-file ...`.
- **Child ticket**: a sub-issue of the map (see above), labelled `wayfinder:<type>` (`research`/`prototype`/`grilling`/`task`).
- **Blocking**: native issue dependencies (see above). A ticket is unblocked when it has no open blockers.
- **Frontier**: the map's open sub-issues without the `claimed` label and with no open blockers; lowest number wins.
- **Claim** (the session's first write): `gh issue edit <n> --add-label claimed`, then `gh issue comment <n> --body "Claimed by <session/worktree>"`. Re-read the comments: if another claim comment precedes yours, back off to the next frontier ticket. Assignees are not used - all agents act under the same GitHub identity.
- **Resolve**: `gh issue comment <n> --body "## Answer ..."`, then `gh issue close <n> --reason completed`, then append a context pointer (gist + link to the answer comment) to the map's Decisions-so-far.
