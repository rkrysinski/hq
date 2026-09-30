# QA plan for #12 (written before the change)

| # | Check | Steps | Expected |
|---|-------|-------|----------|
| 1 | Every command the text names exists and does what the text says | `sbx login --help`, `sbx policy init --help`, `sbx diagnose`, `winget show Docker.sbx`, against the real sbx 0.46.0 | the commands exist; the policies are allow-all, balanced, deny-all; diagnose reports virtualization |
| 2 | The platform statement is true | `file sbx.exe`; `sbx diagnose` on this ARM64 VM | sbx.exe is x86-64; `Virtualization - not available` |
| 3 | The four points of the issue are in README and spec §11 | read the diff | login, policy init, new WSL session, platform requirements |
| 4 | No em dash; nothing else changed | grep, diff stat | - |

Tests: none at any level; no code changes.

The errors the text quotes (`Not authenticated to Docker`, the installer's `sbx: install Docker Sandboxes`) come from the v0.3.0 QA run's evidence (issue-3/01, 03, 05), not re-run: that would mean signing the machine's sbx out and uninstalling it.
