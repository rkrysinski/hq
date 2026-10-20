# QA plan for #69 (written before the call sites changed)

A refactor: hq must behave exactly as on `dev`. Every check runs the same scenario (`qa-run.sh`) twice, once with hq built from `origin/dev` (ede26a6, "before") and once from this branch ("after"), each against its own private tmux server, the repository's stub `sbx` and the fake Claude (`tools/fakeclaude`), in a 160x40 tmux pane used as the terminal. No real sandbox, no real Claude.

Scenario: five agents in one repository, each in a state the look decides differently:
`a` "hi" (done by its hooks), `b` "a question for input" (needs input by its hooks), `c` "slow work" interrupted with Esc (done `Interrupted`, read off the screen and kept as `@hq_turnend`), `d` "background research" (working on background work, not unsettled), `e` "hello" then `/effort max` (a dialog of Claude's own, read off the screen and kept as `@hq_dialogseen`).

| # | Check | Steps | Expected |
|---|-------|-------|----------|
| 1 | `hq ls` | `hq ls` | same states, LAST and order before and after (ages differ only by timing) |
| 2 | `hq wait` | `hq wait --since $T0 --timeout 5s` | the same four agents returned (not `d`), same next hint shape |
| 3 | What the look keeps | `@hq_*` records on the agents' windows after 1 and 2 | the same records on the same windows (`c` turnend, `e` dialogseen), timestamps aside |
| 4 | Dashboard on entry (docking on entry) | `hq` | same rows, the attention view, `e` docked |
| 5 | Dashboard, all agents | `a` | same rows and order |
| 6-9 | Chords | Alt+j, Alt+j, Alt+k, Alt+a | the docked row moves the same way: b, c, b, e |
| 10 | Tests | unit, `-tags integration` with coverage, `-tags e2e`, `testgate` | all pass, unchanged |

Tests planned - unit (new, at the look's interface with a fake store): one capture for all unsettled agents and none when none is; a recalled turn end stands when the capture fails, the hooks' states otherwise; records first-wins, the stored value applied, a failed write unapplied; ends and wake-ups seen after the settling. Integration: none. End-to-end: none.
