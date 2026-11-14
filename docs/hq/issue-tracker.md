# Working an issue in hq

hq's own steps for an issue worked in a worktree, beside the template's `docs/agents/issue-tracker.md`; where the two differ, this file holds for hq.

- **Running the checks** (*Working a single issue*, step 2): hq runs from the issue's worktree with `scripts/local-dev.sh` (`docs/hq/agents.md`, *Local run*), and the evidence follows `docs/hq/qa-evidence.md`, terminal screenshots in every PR included.
- **Cleaning up** (*Cleaning up after a PR closes*): hq's `scripts/local-dev.sh` has no `stop`; run `scripts/local-dev.sh --kill-server` inside the worktree instead, which ends its private tmux server and, after a `--stub` run, removes the stub environment. Kill every agent the QA started first, or it outlives `--kill-server` as a lost agent (skill `local-dev`). `git worktree remove --force` discards the ignored `.local-dev/`.
