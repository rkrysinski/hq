<!--
Title: the outcome this change intends, e.g. "Show the delivery date on the order list",
not "Update order view" or "Fix #12". CI checks the title and this description
(scripts/check-pr.sh), and the `Closes #<n>` line of every commit when the description
closes an issue; the check runs again when you edit them.

A spec pull request lists the spec and every ticket it closes, one `Closes #<n>` per line.
-->

Closes #

## Summary

<!--
The smallest visual that makes the change clear (pseudocode, a call tree, a file tree,
Mermaid or a diff) plus one or two sentences: what problem there was and what the change
does instead. Not a file-by-file recap. This is the `pr` skill's Summary.
-->

## Deviations from the issue

<!-- What differs from the issue and why, or "None". -->

## QA

<!--
Evidence that it works, before and after, per docs/agents/qa-evidence.md: the check table
with evidence, the pyramid check before and after, the tests added per level and, last,
the full build (scripts/check.sh) on the head commit. A spec pull request has one
`### #<n> <ticket title>` subsection per ticket. This is where the `pr` skill's Evidence
goes. A change with nothing to check in the running app (docs, CI, tooling): "N/A" and one
line saying why, then the full build.
-->

## Merge Danger

<!--
**Door:** one-way or two-way. Can the merge be rolled back cheaply? Destructive migrations,
removed files and changed contracts are one-way.
**Blast Radius:** one or two words, then what the change can break and for whom.
-->

<!--
Work outside the code needed to go live (IT consents, environment variables, a manual command):
add a section with the exact heading "Before deployment" or "After deployment" at level two, with
the steps as a checklist in English. They hold only steps the operator or IT performs to deploy:
scripts/release.sh copies them verbatim into the release notes (docs/releasing.md), so follow-up
work, plans and "next steps" go in issues, never in these sections. A ticked step ("- [x]") is
already done and stays out of the notes. Leave both out when there is nothing to do.
-->

## Rollback

<!--
Required when the description has "Before deployment" or "After deployment": how to undo the
deployment, one line per step, in English. A change that cannot be undone: "None: one-way door".
Delete this section when there are no deployment steps.
-->
