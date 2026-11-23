### #336 Review fixes

Base commit: `b4d8b74` (`spec-336-one-look-module` with #337, #338 and #339; it had not moved when it was merged in before the report). Commit: `ddf327f`. Every finding of the code review of the spec (`spec-336-one-look-module` against `origin/release-v0.17.0`) is fixed, or kept with its reason under *Deviations*. What hq prints, its exit codes and when it asks sbx and tmux do not change, so there is nothing new to show in a terminal and no screenshot; what does change, `hq update`, the chords and docking on entry no longer reading the journals' counter, is shown by tracing hq's calls in check 10.

| # | Finding | Check | Expected | Result | Evidence |
|---|---------|-------|----------|--------|----------|
| 1 | S1 glossary drift | The list's look loop says looks, not ticks: `lookEvery`, `sbxEveryLooks`, `generationEvery` in looks, the counter `m.looks`, `lookMsg` and `nextLook`; the list's own tests renamed with them (`TestTheListLooksAtTmuxEachLookAndAsksSbxEverySecond`, `looked`); the design's 3.7, 3.8, 3.11, 5.1 and 5.2 say look where they said tick (7 places) | no `tick`, `ticks`, `tickEvery`, `tickMsg` or `sbxEvery` in `internal/dash`, no `tick` in the design | ✅ pass | [searches.txt](https://github.com/rkrysinski/hq/blob/3a4210803dfeae15e17577ab5f52ee31eeeff45e/issue-336/searches.txt) |
| 2 | S2 | `TestTheChordsAndDockingOnEntryAskSbxNothing` shows its counting sbx took hold: `hq ls` on the same fakes asks it | the test passes, and fails when the counting sbx is not put in place | ✅ pass | [module-tests.txt](https://github.com/rkrysinski/hq/blob/3a4210803dfeae15e17577ab5f52ee31eeeff45e/issue-336/module-tests.txt), [p1-hand-breaks.txt](https://github.com/rkrysinski/hq/blob/3a4210803dfeae15e17577ab5f52ee31eeeff45e/issue-336/p1-hand-breaks.txt) |
| 3 | S3 | `usableAgent` takes the refusal from `Seen.Usable`, `findAgent` from `Seen.Named`, both through one `actingAgent` | no `Refusal()` call left in `send.go` | ✅ pass | [searches.txt](https://github.com/rkrysinski/hq/blob/3a4210803dfeae15e17577ab5f52ee31eeeff45e/issue-336/searches.txt) |
| 4 | S4, S5 | The counter and whether it was read are one value, `agent.Counter`, and `since.ahead` takes it: one entry point for `hq read` and `hq wait` | no `aheadOf`, no `counted` | ✅ pass | [searches.txt](https://github.com/rkrysinski/hq/blob/3a4210803dfeae15e17577ab5f52ee31eeeff45e/issue-336/searches.txt) |
| 5 | S6 | The I/O is the Looker's: `Looker.Cursor(s)` (asks tmux again) and `Looker.Cover(s)` (looks again) replace `Seen.Cursor` and `Seen.Covered`; `Seen` no longer carries a Looker; `inBackground` is `sbxInBackground` | `Seen`'s methods only read what the look saw | ✅ pass | [searches.txt](https://github.com/rkrysinski/hq/blob/3a4210803dfeae15e17577ab5f52ee31eeeff45e/issue-336/searches.txt) |
| 6 | S7 | `agent.Appeared` is unexported (`appeared`) | no `Appeared` in the module | ✅ pass | [searches.txt](https://github.com/rkrysinski/hq/blob/3a4210803dfeae15e17577ab5f52ee31eeeff45e/issue-336/searches.txt) |
| 7 | S8 | `Agent.Refusal` and `OutdatedError` move to `agent.go`, beside `Agent.Outdated` and `OutdatedErr`; `looker.go` keeps the look, its variants, its errors, the loop and sbx in the background | as described | ✅ pass | [searches.txt](https://github.com/rkrysinski/hq/blob/3a4210803dfeae15e17577ab5f52ee31eeeff45e/issue-336/searches.txt) |
| 8 | S9 | The tail of the look loop's test: a counter of its own, fresh for each loop, the bound that ends a loop gone on said in a comment, and the look that fails checked to end the loop before any step (0 steps, not 1 carried over) | the test passes | ✅ pass | [module-tests.txt](https://github.com/rkrysinski/hq/blob/3a4210803dfeae15e17577ab5f52ee31eeeff45e/issue-336/module-tests.txt) |
| 9 | P1 | A variant says whether its look reads the counter: `Updating` and `Docking` read none, `Shown`, `Explained` and `Acting` (the look loop's too) read it once a look, each what its command did at the base; `TestALookReadsTheCounterBeforeItListsTheWindows` checks every variant | 7 hand-made breaks of `looker.go` (each variant flipped, every look reading it, the counter read after the windows) each fail the test | ✅ pass | [p1-hand-breaks.txt](https://github.com/rkrysinski/hq/blob/3a4210803dfeae15e17577ab5f52ee31eeeff45e/issue-336/p1-hand-breaks.txt) |
| 10 | P1 | The real hq, run by `scripts/local-dev.sh --stub native` with one agent, traced with `strace -f`: opens of the state directory's `counter` file and `tmux list-panes` runs (how hq lists the windows) per command, for the base `476a6ce`, the integration head `b4d8b74` and `ddf327f` | the chord reads the counter as often as at the base (4, `b4d8b74` 5); every other count equal to the base's | ✅ pass | [p1-calls.txt](https://github.com/rkrysinski/hq/blob/3a4210803dfeae15e17577ab5f52ee31eeeff45e/issue-336/p1-calls.txt) |
| 11 | P2 | The three command-level tests the finding names stay, each with why it is not a copy (*Deviations*) | recorded | ✅ pass | *Deviations* |
| 12 | P3 | The branch of `SeenThrough` no behaviour told apart is gone: it gives the lowest read, 0 when a journal was not read or none was, and the cursor gives the counter for any read not past it; the `ok` that only repeated that is gone with it | no mutant alive in `SeenThrough` or `Looker.Cursor`, over the spec's lines as a whole | ✅ pass | mutation block |
| 13 | P4 | `hq kill`'s sbx question through `Looker.AskSbx` stays (*Deviations*) | recorded | ✅ pass | *Deviations* |
| 14 | - | The design (3.8 and the spec's lines of section 10) says which variants read the counter, that `Looker.Cursor` and `Looker.Cover` give the cursor, and that only the plain `hq ls`'s one listing fewer differs from the base | as described | ✅ pass | diff of `docs/design/hq.md` |
| 15 | - | Full build, and the mutation gate against the integration branch and against `origin/release-v0.17.0` (the spec's lines as a whole) | both pass on `ddf327f`, the one survivor equivalent | ✅ pass | blocks below |

Pyramid before (`b4d8b74`, #339's figure): `unit 998, integration 324, e2e 3 (unit share 75%, minimum 75%)`. After: `unit 998, integration 324, e2e 3 (unit share 75%, minimum 75%)`, unchanged. Coverage after: `coverage 95.49% (minimum 94.7%)`, as on the base (#339's figure); no threshold moves.

Tests added: none at any level; changed: `TestALookReadsTheCounterBeforeItListsTheWindows` (every variant's reading of the counter), `TestTheLooksCursorStandsForWhatItSawAndCoversEveryAgentItShows` (through `Looker.Cursor` and `Looker.Cover`; an unread journal also lists the windows no second time), `TestTheLookLoopLooksEvery250msAndAsksSbxInTheBackgroundOnceASecond` (its tail, S9), `TestTheChordsAndDockingOnEntryAskSbxNothing` (S2); renamed: `TestTicksRefreshFromTmuxAndSbxEverySecond` to `TestTheListLooksAtTmuxEachLookAndAsksSbxEverySecond` (S1).

#### Outputs

**Mutation tests**

`GOTOOLCHAIN=go1.26.0 go run ./tools/testgate mutation spec-336-one-look-module`

```text
not covered internal/dash/dash.go:186:37 arithmetic base
alive      internal/dash/dash.go:631:10 increment decrement
mutation: 14 killed, 1 alive, 0 timed out, 1 not covered, 0 not run against spec-336-one-look-module on ddf327f401dcd144379bff7dfa01e771c1bc335b
```

Over the spec's lines as a whole: `GOTOOLCHAIN=go1.26.0 go run ./tools/testgate mutation origin/release-v0.17.0` (`476a6ce`, before #337, so every changed line of the spec, `internal/agent/looker.go` whole, is judged by this commit's tests)

```text
not covered internal/agent/looker.go:134:18 arithmetic base
not covered internal/dash/dash.go:186:37 arithmetic base
alive      internal/dash/dash.go:631:10 increment decrement
mutation: 49 killed, 1 alive, 0 timed out, 2 not covered, 0 not run against origin/release-v0.17.0 on ddf327f401dcd144379bff7dfa01e771c1bc335b
```

- `dash.go:631:10` (`m.looks++` into `m.looks--`, the line renamed from `m.ticks++`): equivalent: the counter is read only as `m.looks%sbxEveryLooks` and `m.looks%generationEvery`, and Go's `%` keeps `-4n % 4` and `-8n % 8` at 0, so counting down asks sbx and looks at the hq on disk at the very same looks (at the 4th and the 8th, and every 4th and 8th after) until the counter overflows, after 2^63 looks.
- `looker.go:134:18` and `dash.go:186:37` are `not covered` (constants' arithmetic: `250 * time.Millisecond` and `agent.SbxEvery / agent.LoopEvery`), as in #339's run; neither fails the run.

**Full build**

`scripts/check.sh`

```text
check: ok   vet
check: ok   format
check: ok   pyramid
check: ok   platform
check: ok   timebound
check: ok   sees
check: ok   unit
check: ok   integration
check: ok   coverage
check: ok   e2e
check: all 10 steps passed on ddf327f401dcd144379bff7dfa01e771c1bc335b
```

**Hand-made breaks of the counter's rule (P1) and of S2's fake**

`p1-hand-breaks.sh` on a copy of `ddf327f`: each break alone (of `internal/agent/looker.go`, and the last of `runAskingSbx` in `internal/cli/look_variants_test.go`), then `go test -count=1 -run '^<the test of its rule>$'` in its package

```text
failed  Docking reads the counter  (TestALookReadsTheCounterBeforeItListsTheWindows)
      looker_test.go:297: Docking: the counter read 1 times, the look's {N:1 Read:true} (the counter 1), want it read: false
failed  Updating reads the counter  (TestALookReadsTheCounterBeforeItListsTheWindows)
      looker_test.go:297: Updating: the counter read 1 times, the look's {N:1 Read:true} (the counter 1), want it read: false
failed  Explained reads no counter  (TestALookReadsTheCounterBeforeItListsTheWindows)
      looker_test.go:297: Explained: the counter read 0 times, the look's {N:0 Read:false} (the counter 1), want it read: true
failed  Acting reads no counter  (TestALookReadsTheCounterBeforeItListsTheWindows)
      looker_test.go:297: Acting: the counter read 0 times, the look's {N:0 Read:false} (the counter 1), want it read: true
failed  Shown reads no counter  (TestALookReadsTheCounterBeforeItListsTheWindows)
      looker_test.go:272: counter {N:0 Read:false}, want 1, the counter before the windows (2 now): <nil>
failed  every look reads the counter  (TestALookReadsTheCounterBeforeItListsTheWindows)
      looker_test.go:297: Updating: the counter read 1 times, the look's {N:1 Read:true} (the counter 1), want it read: false
      looker_test.go:297: Docking: the counter read 1 times, the look's {N:1 Read:true} (the counter 1), want it read: false
failed  the counter read after the windows  (TestALookReadsTheCounterBeforeItListsTheWindows)
      looker_test.go:272: counter {N:2 Read:true}, want 1, the counter before the windows (2 now): <nil>
failed  S2: the counting sbx not put in place  (TestTheChordsAndDockingOnEntryAskSbxNothing)
      look_variants_test.go:69: [__chord next]: hq ls after it: exit 0 "", asked sbx 0 times: the sbx that counts took no hold
```

**Counter reads and window listings of the real hq (P1)**

`p1-calls.sh`: `scripts/local-dev.sh --stub native` with one agent `a` started in the stub repository and two plain `hq ls` first, so nothing is left to append; each command run under `strace -f -e trace=openat,execve`, counting the opens of the state directory's `counter` file (every read of the counter: the look's own read before the windows, and each read of a journal and each append, which read it under the same lock) and the `tmux list-panes` runs; `before` is the binary of `LOCAL_DEV_BEFORE` built at the same path.

```text
== the base: 476a6ce (before, LOCAL_DEV_BEFORE=476a6ce)
hq ls            counter read 5, windows listed 1
hq ls --json     counter read 5, windows listed 1
hq __chord next  counter read 4, windows listed 1
hq read a --json counter read 5, windows listed 1
hq explain a     counter read 6, windows listed 1
== the integration head: b4d8b74 (before, LOCAL_DEV_BEFORE=b4d8b74)
hq ls            counter read 5, windows listed 1
hq ls --json     counter read 5, windows listed 1
hq __chord next  counter read 5, windows listed 1
hq read a --json counter read 5, windows listed 1
hq explain a     counter read 6, windows listed 1
== the review fixes: ddf327f (after)
hq ls            counter read 5, windows listed 1
hq ls --json     counter read 5, windows listed 1
hq __chord next  counter read 4, windows listed 1
hq read a --json counter read 5, windows listed 1
hq explain a     counter read 6, windows listed 1
== the base again: 476a6ce
hq ls            counter read 5, windows listed 1
hq ls --json     counter read 5, windows listed 1
hq __chord next  counter read 4, windows listed 1
hq read a --json counter read 5, windows listed 1
hq explain a     counter read 6, windows listed 1
```

The chord reads the counter once more at `b4d8b74` than at the base and at `ddf327f`: the look's own read, which `Docking` no longer makes. Every other command reads it and lists the windows as often as at the base (in a quiet look the cursor of the plain `hq ls` needs no second listing, so the one listing fewer of *Deviations* does not show here).

**The tests the fixes changed**

```text
$ go test -count=1 -v -run '^(TestALook.*|TestTheLooksCursorStandsForWhatItSawAndCoversEveryAgentItShows|TestAnAgentsCursorIsTheCounterOfTheLooksReadOfItsJournal|TestTheCounterBeforeAnActIsTheCounterThen|TestTheLookLoopLooksEvery250msAndAsksSbxInTheBackgroundOnceASecond)$' ./internal/agent | grep -E '^(--- |ok|FAIL)'
--- PASS: TestALookBetweenTheStepsOfAKillWritesNoJournalBack (0.00s)
--- PASS: TestALookAtFifteenLongJournalsFoldsOnlyWhatIsNew (0.51s)
--- PASS: TestALookLeavesOutAnAgentKilledWhileItLooked (0.00s)
--- PASS: TestALookReadsTheCounterBeforeItListsTheWindows (0.00s)
--- PASS: TestALookNotesTheMomentItAsksSbxBeforeItAsks (0.00s)
--- PASS: TestALookLooksAgainAtAScreenAtRestInTheVariantsThatTakeIt (0.00s)
--- PASS: TestTheLooksCursorStandsForWhatItSawAndCoversEveryAgentItShows (0.00s)
--- PASS: TestAnAgentsCursorIsTheCounterOfTheLooksReadOfItsJournal (0.00s)
--- PASS: TestTheCounterBeforeAnActIsTheCounterThen (0.00s)
--- PASS: TestALookGivesTheAgentNamedOrItsRefusal (0.00s)
--- PASS: TestTheLookLoopLooksEvery250msAndAsksSbxInTheBackgroundOnceASecond (0.02s)
ok  	github.com/rkrysinski/hq/internal/agent	0.537s

$ go test -count=1 -v -run '^(TestTheChordsAndDockingOnEntryAskSbxNothing|TestCommandsActOnNothingTheLookRefuses|TestLsJSONFailsWhenALookAgainFails|TestReadJSONHasWhatLsHasAndTheAgentInFull|TestWaitSinceTheLastCursorReportsWhatChangedBetweenCalls|TestSendToAStartingAgentWaitsForItsFirstReport)$' ./internal/cli | grep -E '^(--- |ok|FAIL)'
--- PASS: TestLsJSONFailsWhenALookAgainFails (0.00s)
--- PASS: TestCommandsActOnNothingTheLookRefuses (0.00s)
--- PASS: TestTheChordsAndDockingOnEntryAskSbxNothing (0.00s)
--- PASS: TestReadJSONHasWhatLsHasAndTheAgentInFull (0.00s)
--- PASS: TestSendToAStartingAgentWaitsForItsFirstReport (0.00s)
--- PASS: TestWaitSinceTheLastCursorReportsWhatChangedBetweenCalls (0.00s)
ok  	github.com/rkrysinski/hq/internal/cli	0.004s

$ go test -count=1 -v -run '^(TestTheListLooksAtTmuxEachLookAndAsksSbxEverySecond|TestTheListLooksAtTheHqOnDiskEveryTwoSeconds)$' ./internal/dash | grep -E '^(--- |ok|FAIL)'
--- PASS: TestTheListLooksAtTmuxEachLookAndAsksSbxEverySecond (0.15s)
--- PASS: TestTheListLooksAtTheHqOnDiskEveryTwoSeconds (0.40s)
ok  	github.com/rkrysinski/hq/internal/dash	0.548s
```

#### Deviations

- **The plain `hq ls` lists the windows up to once fewer than at the base (P1), and stays so.** At the base it worked out a cursor it never printed, and that asked tmux for the windows again whenever a journal was read past the counter; at `b4d8b74` and now it works one out only for `--json`. Keeping the base's call would mean computing a value only to throw it away; story 2 asks for no more calls than today, and this is one fewer. The design's line for #337 says so.
- **`Explained` and `Acting` read the counter though their commands print no cursor (P1).** `hq explain`, `hq send` and `hq answer` read it at the base (through `lookWith`), so they keep reading it: the fix makes `hq update`, the chords and docking on entry do exactly what they did at the base, and changes no other command's reads.
- **Command-level tests kept (P2), each not a copy of a rule of the module:**
  - `TestCommandsActOnNothingTheLookRefuses` (`internal/cli/look_rules_test.go`, unit): a different job. It checks what the commands' package owns: the exit code each command turns the look's refusal into (2 for none of that name, 1 for an outdated one), that it prints the refusal alone and acts on nothing (no message left, no answer, no edit, nothing pasted); the words come from the module (`agent.NotFoundError`, `agent.OutdatedError`), so a change of the rule changes only the module's test.
  - `TestWaitRightAfterNewNeverSeesTheAgentEndedOnAnOlderAnswerOfSbx` (`internal/cli/wait_integration_test.go`, integration): a different level. `hq new` and `hq wait` run on real tmux with the stub `sbx` and the files' store, the sandbox started by `hq new` while `hq wait` asks sbx; the module's test (`TestALookNotesTheMomentItAsksSbxBeforeItAsks`) fakes sbx's answer and the clock and cannot reach a sandbox that starts under a real `hq new`.
  - `TestCursorsOfLoopsLookingAtOnceMissNoChangeAndRepeatNone` (`internal/cli/cursor_integration_test.go`, integration, #166's acceptance run): a different level. Several hq processes look at once on real tmux and the files' store while hooks leave 1,000 changes; the module's fakes run one process in one goroutine and cannot reach several processes numbering changes on one counter. The spec lists it among the tests that must pass untouched.
- **`hq kill`'s sbx question stays on `Looker.AskSbx` (P4).** `runningSandboxes` is in `ls.go`, which the spec builds on, not in `kill.go`, which no commit of the spec touches (O1 keeps `hq kill`'s files out for #333 and #334, and they are untouched); it asks sbx and notes the moment as at the base, the answer the same. Reverting it would write the rule of the moment of asking sbx (#48, story 12) out by hand a second time, in a file the module is otherwise the only place of.
- **`Seen.Cursor` and `Seen.Covered` become `Looker.Cursor(s)` and `Looker.Cover(s)` (S6)** rather than a new name on `Seen`: a method of the Looker says it asks tmux, and `Seen` keeps only what the look saw. `Looker.Cover` keeps the rule of `Seen.Covered` unchanged.
- **Seams:** none new; the module's store fake counts its reads of the counter.
- **Deploy steps:** none.
