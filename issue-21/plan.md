# QA plan for #21 (written before the change)

| # | Check | Steps | Expected |
|---|-------|-------|----------|
| 1 | A repository in a directory named `my$repo`, on tmux 3.4 | `hq new a 'my$repo' 'say hi'`, `hq ls`, `hq ls --json` | REPO `my$repo`, the branch and state shown, paths without a backslash |
| 2 | Its sandbox commands | second agent, `hq sandbox restart`, `hq sandbox rm` with agents running, then after `hq kill` | restart relaunches both; rm refused while they run, done after |
| 3 | Nothing else changes | unit suite; integration tests of `internal/tmux`, `internal/cli`, `cmd/hq`; e2e | pass |

Tests planned - unit: what hq makes of tmux's output with and without the probe (`asStored`). Integration: options, a session value and the socket path holding `$name` read back as stored (real tmux); `KeepFirst`'s test back to exact equality. End-to-end: none.
