# QA plan for #7 (written before the change)

| # | Check | Steps | Expected |
|---|-------|-------|----------|
| 1 | `hq code NAME` opens VS Code | `hq new a repo "say hi"`, `hq code a`, then list the Windows desktop's windows | a window `repo [WSL: Ubuntu-24.04] - Visual Studio Code`; hq prints `opened <dir> in VS Code`, exit 0 |
| 2 | `c` in the dashboard opens VS Code | `hq`, `a` (all agents), `c` on the row of agent a | the same window opens |
| 3 | Failures are still reported | `hq code nosuch`; `hq code a` with no `code` on PATH; with a `code` that fails | exit 2 `no agent`; exit 3 with the remedy; exit 3 with the editor's message |
| 4 | Nothing else changes | unit suite; integration tests of `internal/cli` and `internal/proc` | pass |

Tests planned - integration: `proc.Run` with stderr through a file gives the program no pipe and still reports a failure's first line (it needs a real process). Unit and end-to-end: none.

VS Code and its WSL launcher are the real ones; sbx is the repository's stub and Claude the fake one. WSL 1 only on this machine.
