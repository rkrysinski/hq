# QA plan for #33 (written before the change)

| # | Check | Steps | Expected |
|---|-------|-------|----------|
| 1 | Docks made at the same time | the same agent docked from four places at once with nothing docked; two agents docked at once; 12 rounds | the agent is docked; exactly one of two is, each pane where its state says |
| 2 | The journey of the issue | `TestDashboardFollowsAgentsQuitsAndComesBack`, 20 runs, before and after | no `never: a docked` |
| 3 | Nothing else changes | unit suite; whole integration suite; e2e | pass |

Tests planned - integration: `TestDocksAtTheSameTimeLeaveOneAgentDocked` (real tmux; the race needs real processes). Unit: the quoting of a plan's commands for tmux's parser. End-to-end: none new.
