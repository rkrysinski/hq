# QA plan for #22 (written before the change)

| # | Check | Steps | Expected |
|---|-------|-------|----------|
| 1 | Place the cause | probe: 80 panes ending one at a time; for one dead without a time, look at its process, then let another child of the server exit | the state of the process explains the missing time |
| 2 | The five tests that look at an ended pane | 12 runs of them, before and after | before: failures; after: 12 of 12 |
| 3 | The rest of the suite | `internal/tmux` 4 runs; `internal/cli`, `internal/state`, `cmd/hq` 2 runs; e2e 3 runs | pass |
| 4 | Does hq show anything wrong | 12 agents whose session ends at once, `hq ls` | all `ended`, with an age |

Tests planned: none new at any level; five integration tests of `internal/tmux` wait for tmux to reap the ended pane.
