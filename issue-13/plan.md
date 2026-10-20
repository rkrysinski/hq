# QA plan for #13 (written before the change)

| # | Check | Steps | Expected |
|---|-------|-------|----------|
| 1 | The suite is self-contained on WSL | a tripwire `sbx.exe` on PATH where the real one is (logs each call, fails); whole integration and e2e suites | pass; 0 calls (before: the named tests call it) |
| 2 | `internal/proc` integration tests compile and pass on Linux | `go test -tags integration ./internal/proc`; `GOOS=darwin go vet` | pass |
| 3 | `TestKeepFirstLetsTheFirstRecordWinAcrossRacingWriters` | 3 runs on tmux 3.4 | 3 of 3 |
| 4 | `TestPasteTypesTextAsABracketedPasteAndSubmitPressesEnter` | 3 runs on WSL 1 | 3 of 3 |
| 5 | The user's preferences file is not touched by the suite | compare `~/.config/hq/preferences.json` before and after the runs | identical |
| 6 | Gates | unit suite, `testgate pyramid`, `testgate coverage` | pass, thresholds not lowered |

Tests planned - unit: `platform.Detect` takes the platform it is told (`HQ_PLATFORM`). Integration and e2e: no new tests; the existing ones are what is being repaired.
