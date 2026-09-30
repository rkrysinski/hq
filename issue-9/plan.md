# QA plan for #9 (written before the change)

| # | Check | Steps | Expected |
|---|-------|-------|----------|
| 1 | `hq go` from a background process | dashboard in a Windows Terminal window, Notepad in front, `hq go NAME` from a process with no foreground window, three times | the window titled `hq - agents` is in front afterwards |
| 2 | A refusal is seen | the same with the part that gets past the foreground lock taken out | exit 1 and a line saying another window stayed in front, not success |
| 3 | No window with the title | dashboard attached somewhere that is no window titled `hq - agents`; `hq go NAME` | agent docked; one line saying it could not bring the window to the front and why |
| 4 | `hq go` typed in a front window still works | second Windows Terminal window in front, type `hq go NAME` | dashboard window in front |
| 5 | Nothing else changes | unit suite; integration tests of `internal/cli`, `internal/platform` | pass |

Tests planned - unit: the WSL raise is an encoded script that decodes to `RaiseScript`; the script looks at the foreground window before every success and ends in failure. Integration and end-to-end: none (PowerShell and a Windows desktop cannot run in the suite).

Windows and Windows Terminal are the real ones; sbx is the repository's stub and Claude the fake one.
