# Triage Labels

The skills speak in terms of five canonical triage roles. This file maps those roles to the actual label strings used in this repo's issue tracker.

| Label in mattpocock/skills | Label in our tracker | Meaning                                  |
| -------------------------- | -------------------- | ---------------------------------------- |
| `needs-triage`             | `needs-triage`       | Maintainer needs to evaluate this issue  |
| `needs-info`               | `needs-info`         | Waiting on reporter for more information |
| `ready-for-agent`          | `ready-for-agent`    | Fully specified, ready for an AFK agent  |
| `ready-for-human`          | `ready-for-human`    | Requires human implementation            |
| `wontfix`                  | `wontfix`            | Will not be actioned                     |

When a skill mentions a role (e.g. "apply the AFK-ready triage label"), use the corresponding label string from this table.

Edit the right-hand column to match whatever vocabulary you actually use.

These are GitHub labels; an issue carries at most one of them at a time — remove the old role label when adding a new one. `wontfix` issues are also closed with `gh issue close <n> --reason "not planned"`. Newly reported issues start as `needs-triage`.

## Type labels

Separate from the triage role above, an issue carries a label saying what kind of change it is.
`scripts/release.sh` reads these to group the release notes, so they decide where a change appears
for the reader:

| Label         | Release-notes section | Meaning                                                      |
| ------------- | --------------------- | ------------------------------------------------------------ |
| `enhancement` | Nowe funkcje          | Something a user of the product can see or do                |
| `bug`         | Poprawki              | Something that was broken for a user                         |
| `internal`    | Wewnętrzne            | Tooling, CI, deployment, docs — invisible to the product's users |
| (none)        | Pozostałe             | Anything not yet classified                                  |

Give every issue one of these when it is triaged; an unlabelled issue still appears in the notes,
just under the catch-all heading (except a `prd` issue, which gets no line: see below).

## Other labels

| Label | Meaning |
| ----- | ------- |
| `prd` | A product requirements document; its implementation issues are sub-issues (`docs/agents/issue-tracker.md`). Left out of the release notes, even with a type label; it still gets the release's milestone and comment |
| `claimed` | An agent session has claimed the ticket (`/wayfinder`) |
| `release` | The release issue of a supervised release (`docs/agents/issue-tracker.md`, *Working a release*) |
| `watch` | A metric to keep an eye on, not work |
| `wayfinder:map`, `wayfinder:research`, `wayfinder:prototype`, `wayfinder:grilling`, `wayfinder:task` | The wayfinding map and its tickets |

`scripts/setup-github.sh` creates every label named in this file that the repository lacks; re-run
it after adding a label. It never changes an existing label, so a label's description and colour
stay as the project set them; to take the template's text for an existing label, edit the label on
GitHub.

`copier update` creates no labels, so run `scripts/setup-github.sh --labels-only` after every
update: it creates the missing labels and leaves the `qa-artifacts` branch and the repository
settings alone (`--dry-run` lists them first). It is a no-op when nothing is missing.

A project with a label script of its own runs `scripts/setup-github.sh --labels-only` first and its
own script after it, or drops its script. Its own script alone misses every label the template adds
later. Its script then adds the project's extra labels and, to keep its own description or colour
for a label the template just created, updates that label (`gh label edit`, or
`gh label create --force`): a plain `gh label create` fails on a label that exists.
