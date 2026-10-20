# QA plan for #11 (written before the change)

| # | Check | Steps | Expected |
|---|-------|-------|----------|
| 1 | A warning before sbx's error | `hq new` with an sbx whose `create` writes `WARN: mcp gateway teardown` and then `error: the Windows Hypervisor Platform is unavailable ...` | hq prints the `error:` line, exit 3 |
| 2 | The remedy on sbx's second line | `hq new` and `hq sandbox rm` with an sbx that writes `error: Not authenticated to Docker` / `  try: sbx login` | hq prints the error and `try: sbx login` on one line, exit 3 |
| 3 | Nothing else changes | unit suite; integration tests of `internal/sbx` and `internal/cli` with the stub sbx | pass |

Tests planned - unit: what hq takes from sbx's stderr (`sbx.Failure`), and every sbx call reporting it; integration: `proc.Run` hands over the whole stderr (one assertion in the existing test). End-to-end: none.

sbx is a stub that writes the texts recorded from sbx 0.46.0 in the QA run (issue-3/05, 08, 09); the real sbx cannot create a sandbox on this machine.
