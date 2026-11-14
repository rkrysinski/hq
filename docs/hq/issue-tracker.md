# Filing and working an issue in hq

How hq files an issue, and hq's own steps for an issue worked in a worktree, beside the template's `docs/agents/issue-tracker.md`; where the two differ, this file holds for hq.

## Filing an issue

hq files issues the way Matt Pocock's skills do (`/triage`, `/to-spec`, `/to-tickets`), with the label vocabulary of `docs/agents/triage-labels.md`; hq adds no rule of its own.

- **An issue that comes from outside** (a bug report, a request, a defect found while working another issue) is filed with its category label (`bug` or `enhancement`; `internal` for tooling) and `needs-triage`, as the skills' own issue forms file it. `/triage` takes it from there: it confirms or changes the category and moves it to one other state. An issue filed without labels goes to `needs-triage` first.
- **An issue you write yourself** from a plan is not triaged: a spec from `/to-spec` carries `prd` only (`AGENTS.md`, *Issue tracker*), and each ticket from `/to-tickets` is filed `ready-for-agent`.
- **What is out of scope** is stated where the skills put it: in the spec's `## Out of Scope` section, and in the `Out of scope` part of the agent brief `/triage` posts when it moves an issue to `ready-for-agent` or `ready-for-human`. A ticket's own template has no such section.

## Working it

- **Running the checks** (*Working a single issue*, step 2): hq runs from the issue's worktree with `scripts/local-dev.sh` (`docs/hq/agents.md`, *Local run*), and the evidence follows `docs/hq/qa-evidence.md`, terminal screenshots in every PR included.
- **Cleaning up** (*Cleaning up after a PR closes*): hq's `scripts/local-dev.sh` has no `stop`; run `scripts/local-dev.sh --kill-server` inside the worktree instead, which ends its private tmux server and, after a `--stub` run, removes the stub environment. Kill every agent the QA started first, or it outlives `--kill-server` as a lost agent (skill `local-dev`). `git worktree remove --force` discards the ignored `.local-dev/`.
