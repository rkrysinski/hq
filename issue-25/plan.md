# QA plan for #25 (written before the change)

| # | Check | Steps | Expected |
|---|-------|-------|----------|
| 1 | `hq mcp install` with Claude Desktop from the Microsoft Store | in WSL: `hq mcp install`, then again | added to the configuration in the package's folder, everything else in it kept, a copy kept; second run changes nothing; no `%APPDATA%\Claude` created |
| 2 | Claude Desktop loads it and the tools work | quit and reopen Claude Desktop; in a conversation: start an agent, list, send, wait, read | Claude Desktop runs `hq mcp` through `wsl.exe`; each tool answers |
| 3 | `go` from Claude Desktop | dashboard open in Windows Terminal behind Claude Desktop; ask for `go` | the dashboard's window comes to the front |
| 4 | `kill` | ask for `kill`, then `list` | the agent is gone |
| 5 | A `wait` with nothing to report | `list`, then `wait` from its `next_since` | returns empty after about 50 s, not cut off by Claude Desktop |
| 6 | Nothing else changes | unit suite; integration tests of `internal/platform`, `internal/desktop`, `internal/sbx`, `cmd/hq`, `internal/cli` | pass |

Tests planned - unit: the Store package's folder wins when it is there; `%APPDATA%` otherwise and when `%LOCALAPPDATA%` is missing or cannot be mapped. Integration: the stubbed WSL install also installs into a Store package's folder. End-to-end: none.

Claude Desktop, Windows, Windows Terminal and wsl.exe are real; behind hq, sbx is the repository's stub and Claude the fake one.
