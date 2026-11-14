# Implementer

The process doc of an implementer subagent in a spec built with `/implement-spec`. The orchestrator's brief names the ticket, the spec, the integration branch, the worktree path and this file.

## Who reads it

A subagent started by `/implement-spec` for one ticket, or for the review fixes, of a spec. This file is your whole process, with the docs *Read first* names and the one section *A defect outside the ticket's scope* names: do not read `docs/workflow.md`, the rest of `docs/agents/issue-tracker.md`, `docs/agents/triage-labels.md` or `docs/releasing.md`.

## Read first

- The ticket: `gh issue view <n> --json title,body,comments`.
- The spec: `gh issue view <spec> --json title,body`. Read its "Testing Decisions" and the decisions the ticket builds.
- `GLOSSARY.md`, `docs/agents/lessons.md`, `CODING_STANDARDS.md` and `docs/agents/testing.md`.
- `docs/agents/qa-evidence.md`, steps 1 to 4.

## Worktree

Work in `.claude/worktrees/issue-<n>-<slug>` on branch `issue-<n>-<slug>`, created from the integration branch the brief names, not from `dev`.

- The worktree does not exist: `git worktree add -b issue-<n>-<slug> .claude/worktrees/issue-<n>-<slug> <integration branch>`.
- It exists: confirm the base with `git merge-base --is-ancestor <integration branch> HEAD`. If that fails, reset onto the integration branch: `git reset --hard <integration branch>`.

Run the app with `$local-dev` in that worktree.

## Build

Plan the QA checks from the ticket's acceptance criteria first (`docs/agents/qa-evidence.md`, step 1). Then build with `tdd` and run every check against the app.

- **Seams**: the seams come from the spec's "Testing Decisions" section. Treat them as agreed and do not negotiate them; this is the confirmation `tdd` asks for. When the ticket needs a seam the spec does not name, or the spec has no "Testing Decisions", take the highest existing seam per `docs/agents/testing.md`, keep going and record the choice under Deviations in your QA subsection.
- **Glossary**: a ticket that introduces or changes a domain term updates `GLOSSARY.md` in the same commit.
- **Thresholds**: never change one, up or down (`docs/agents/testing.md`).
- **Commits**: every commit message carries a `Closes #<ticket>` line (`AGENTS.md`, *Issue tracker*).
- **Deploy steps**: when the ticket needs anything outside the code to go live, your QA subsection's `#### Deviations` names it the way `## Przed wdrożeniem` and `## Po wdrożeniu` would, and for each step one line of `## Wycofanie`: the command or action that undoes it, or `Brak: drzwi jednokierunkowe` when it cannot be undone. A step without its line is a *block* finding of the integration review in a release.

## A gap

A gap changes what gets built: an ambiguous or contradictory acceptance criterion, or a decision the ticket leaves open. Comment the concrete questions on the ticket (`gh issue comment <n> --body "..."`), then `gh issue edit <n> --remove-label ready-for-agent --add-label needs-info`, then stop.

What the code, `GLOSSARY.md`, the ADRs or `docs/agents/lessons.md` answer is not a question. A missing seam is not a gap (see *Build*).

## A defect outside the ticket's scope

A defect found while working the ticket or running its QA that this change did not cause, and that does not stop an acceptance criterion, is outside the scope: it is not fixed on this branch.

1. Search first: `gh issue list --state open --search "<words>" --json number,title`.
2. When no open issue describes it, create one: `gh issue create --title "..." --label bug --label needs-triage --body "..."`. The body says what happens, how to reproduce it, and where it was found (the ticket being worked, the check, the commit).
3. It is a plain issue: not a sub-issue of the spec, without `ready-for-agent`, in the language `docs/agents/issue-tracker.md`, *Language*, gives a new issue (read that section only).
4. Link the issue, found or created, from `#### Deviations` of your QA subsection.

A defect this change causes, or one that stops an acceptance criterion, is in scope and is fixed here.

## QA

Publish as `docs/agents/qa-evidence.md`, step 4, says: first the evidence under `issue-<n>/` on `qa-artifacts`, then your finished subsection as `issue-<n>/qa-section.md` in a second commit. The file has the layout of that step: the heading `### #<n> <ticket title>`, every quoted command and its output under `#### Outputs`, `#### Deviations` last.

## Before reporting

Merge the integration branch tip into your branch (`git merge <integration branch>`), so the orchestrator's merge is a fast-forward. Then run the full build, `scripts/check.sh` (`docs/agents/testing.md`, *Merging*), and report only when it passes; a failure is yours to fix first.

## Report

- **Done**: the path `issue-<n>/qa-section.md`, the numbers of any issues you created, and the paid spend of this session in USD, your subagents' included, counting what `docs/agents/supervisor.md`, *Agent policy*, *Counted against the cap*, lists (0 when none).
- **Stopped**: the ticket number and that it carries `needs-info`.

The orchestrator of `/implement-spec` adds its implementers' figures to its own spend when it reports.

Nothing else: no pasted section, no summary of the change, no test counts, no questions about thresholds or seams.

## Review fixes

The implementer of the review fixes works the same way, with the spec's number in place of the ticket's:

- branch `issue-<spec>-review-fixes`, in the worktree `.claude/worktrees/issue-<spec>-review-fixes`;
- commits with `Closes #<spec>`;
- folder `issue-<spec>/` on `qa-artifacts`;
- heading `### #<spec> Review fixes` in `issue-<spec>/qa-section.md`.

Fix every finding of `mattpocock-skills:code-review`, a glossary drift included: a domain term in the code that `GLOSSARY.md` lacks or describes differently.
