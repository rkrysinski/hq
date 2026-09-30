# QA plan for #8 (written before the change)

The issue allows two outcomes: the README and spec §11 name the setting, or hq's setup sets it. This change takes the first.

| # | Check | Steps | Expected |
|---|-------|-------|----------|
| 1 | The problem as reported | Windows Terminal's settings as installed, dashboard open, another application in front, BEL in a hidden agent's pane | no flash |
| 2 | The setting the docs name works | `"bellStyle": ["audible", "taskbar"]` in `profiles.defaults`, same BEL, bare and in tmux passthrough | the Terminal taskbar button flashes |
| 3 | The quieter variant the docs name works | `"bellStyle": "taskbar"` | flashes |
| 4 | Settings restored | put the settings file back, same BEL | no flash; file identical to before |
| 5 | The docs say it | README (Install, Notifications), spec §11, design §3.10 and §9 | the setting is named where a Windows user sets hq up; no em dash |

Tests: none at any level; no code changes.

Windows Terminal and tmux are the real ones; sbx is the repository's stub and Claude the fake one, which emits no notification sequence, so the BEL is written into the pane by hand.
