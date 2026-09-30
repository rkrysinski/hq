# QA plan for #34

| # | Check | Steps | Expected |
|---|-------|-------|----------|
| 1 | `:401 c docked`: an agent docked before it is released | new test: make the window, dock (or show) it, then release it | its program starts (before: never) |
| 2 | The same through the real list | a tmux shim delaying hq new's releasing Enter by 1.5 s; the journey | passes (before: the captured failing screen) |
| 3 | `:327 hq go a`: what the journey saw | trace of pane sizes and history around the detach and `hq go a` | no resize; the screen differs only by the turn that ended meanwhile |
| 4 | The journey | 70 runs | no failure |
| 5 | Nothing else changes | unit, integration and e2e suites on the branch merged with dev | pass |
