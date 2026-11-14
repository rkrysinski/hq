<!--
The master pull request of a supervised release: the release branch into `dev`, opened as a
draft by /release-implement and finished by /release-ship (docs/agents/supervisor.md). The
title states the release's outcome. CI checks it with scripts/check-pr.sh, which requires
`## Summary`, `## QA` and `## Merge Danger`. Every issue reference outside `## Closes` is a
bare `#n`: check-pr.sh fails a closing keyword anywhere else in a release pull request.
-->

## Summary

Ship.

<!--
The verdict: ship, ship with named exceptions, or do not ship, in one or two sentences, then
what the release gives its users. The smallest visual that makes the release clear.
-->

## Go/no-go checklist

<!--
One line per condition, ticked or not, each with the evidence that decides it: every ticket
merged, the integration review clear, Release QA green, the full build passed on the candidate,
the gates passed, the prerequisites listed. An unticked line says why the release may still ship.
-->

## Release map

<!--
The tickets of the release by wave: ticket, pull request, state, the paths it changed.
-->

## QA

N/A: wording only.

<!--
Links and tables only, never a ticket's QA copied here: one row per ticket pull request linking
its own `## QA`, then the Release QA step table below with a link per evidence file, then the
full build on the candidate. The full Release QA report is a comment on the release issue,
linked here.
-->

### Release QA

<!--
Headed with the candidate's sha and the Release QA status. The steps of docs/agents/release-qa.md
run on that candidate, one row each with its result and one link per evidence file
(docs/agents/qa-evidence.md), the gates with their values against the baseline, the spend, and
the link to the full report on the release issue.
-->

### Full build

<!--
The lines of scripts/check.sh run on the candidate, its last line naming the candidate's sha
(docs/agents/qa-evidence.md). The release branch gets no `Tests` run, so this passes before
the tag (docs/agents/issue-tracker.md, *Candidate and tag*); a new candidate gets a new run.
-->

## Deviations

<!--
What differs from the release plan and why: a ticket dropped or added, a hotfix merged into
the release branch, a step skipped, a cap exceeded. "None" when nothing does.
-->

## Merge Danger

**Door:** two-way.

<!--
**Door:** one-way or two-way. Can the merge be rolled back cheaply? Destructive migrations,
removed files and changed contracts are one-way.
**Blast Radius:** one or two words, then what the release can break and for whom.
-->

## Rollout plan

<!--
The rollout in order, in English: who does what, when, and how to roll back. Steps the operator
or IT performs to deploy also go under `## Before deployment` or `## After deployment`, which
scripts/release.sh copies into the release notes (docs/releasing.md).
-->

## Links

<!--
The release issue, the inventory, the tickets' pull requests, the Release QA evidence.
-->

## Closes

Closes #1

<!--
One per line: `Closes #<release issue>`, then one `Closes #<prd>` per spec of the release,
nothing else. Tickets are not listed: they close through the `Closes` lines of their own
squash commits when this pull request is merged into `dev`.
-->
