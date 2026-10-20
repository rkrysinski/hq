# QA plan for #10 (written before the change)

| # | Check | Steps | Expected |
|---|-------|-------|----------|
| 1 | No Claude Desktop | Windows without `%APPDATA%\Claude`; in WSL `hq mcp install` | nothing created; the entry printed; one error line naming the folder and the remedy; exit 3 |
| 2 | Claude Desktop's folder there, no configuration yet | create `%APPDATA%\Claude`, `hq mcp install`, again, then with a broken file | file created and reported as added; second run changes nothing; a broken file is left alone |
| 3 | Nothing else changes | unit suite; integration tests of `internal/desktop`, `internal/cli`, `cmd/hq` install | pass |

Tests planned - unit: the command prints the entry and the remedy when the folder is missing (`internal/cli`, fake install). Integration: `desktop.Install` creates nothing without the folder (file system); the `cmd/hq` install tests, native and WSL with stubs, first run without the folder. End-to-end: none.

The Windows side is the real one (`cmd.exe`, `%APPDATA%`); what hq creates there during QA is removed afterwards. Claude Desktop is not installed on this machine, so nothing is checked with it.
