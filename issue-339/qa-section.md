### #339 Each rule of the look and the look loop has one test at the module's interface, and the copies at the commands are gone

Base commit: `6dd1bd9` (`spec-336-one-look-module` with #337 and #338, when the branch was cut and when it was merged in before the report: the integration branch had not moved). Commit: `b4d8b74`. The change is tests, the removal of the commands' package's forwarders no production code calls, and the design: hq prints, exits and asks sbx and tmux as before, so there is nothing to show in a terminal, and no screenshot.

| # | Check | Expected | Result | Evidence |
|---|-------|----------|--------|----------|
| 1 | Exactly one test of each rule, at the look module's interface (`internal/agent/looker_test.go`) | 9 tests, one per rule (table *The rules and their tests* below) | ✅ pass | [6-module-tests.txt](https://github.com/rkrysinski/hq/blob/5e8faa146bf84af5d2dbc6b29739b32b59f87f09/issue-339/6-module-tests.txt) |
| 2 | The tests enter through the module's interface (`Looker.Look`, `Loop`, `AskSbx`, `CounterBefore`, `Seen.*`, `Agent.Refusal`) and check what it gives; a race comes in through the faked journals' store (a hook at a read of a journal, the kill removing it) or tmux (the windows listed); each fails when its rule is broken | 25 hand-made breaks of `looker.go`, one at a time, each failed by its rule's test | ✅ pass | [4-hand-mutations.txt](https://github.com/rkrysinski/hq/blob/5e8faa146bf84af5d2dbc6b29739b32b59f87f09/issue-339/4-hand-mutations.txt) |
| 3 | The copies at the commands are removed, but `hq wait` returning a killed agent with its last message and `hq ls` leaving it out | 14 tests and 4 refusal checks inside other tests removed (table *Removed and their replacements*); `TestWaitReturnsAnAgentKilledBetweenTheStepsOfItsLookWithItsLastMessage` and `TestLsBetweenTheStepsOfAKillLeavesTheAgentOut` unchanged | ✅ pass | [1-files.txt](https://github.com/rkrysinski/hq/blob/5e8faa146bf84af5d2dbc6b29739b32b59f87f09/issue-339/1-files.txt) |
| 4 | The three tests of #263 at `hq ls` and the helpers `onAppend` and `noJournalOf` are unchanged | no line of them in the diff; `ls_test.go` loses only two tests | ✅ pass | [3-kept-263.txt](https://github.com/rkrysinski/hq/blob/5e8faa146bf84af5d2dbc6b29739b32b59f87f09/issue-339/3-kept-263.txt) |
| 5 | This section lists every removed test and the module test that replaces it | the table below | ✅ pass | this section |
| 6 | The tests of what a command does with the look stay: delivery, `hq explain`'s lost agent, the waits of `hq send` and `hq answer`, several names in `hq wait` | none of them removed (table *Kept at the commands*) | ✅ pass | [1-files.txt](https://github.com/rkrysinski/hq/blob/5e8faa146bf84af5d2dbc6b29739b32b59f87f09/issue-339/1-files.txt) |
| 7 | The forwarders step 1 left for the old tests are gone | `agentAppeared`, `looked`, `lookNow`, `coverLooks`, `waitEvery` removed; the tests that stay use `agent.CoverLooks`, `agent.LoopEvery`, `Looker.Look(agent.Acting)`; none named in the package | ✅ pass | [2-searches.txt](https://github.com/rkrysinski/hq/blob/5e8faa146bf84af5d2dbc6b29739b32b59f87f09/issue-339/2-searches.txt) |
| 8 | A guard test walks the commands' package's sources and fails on `no agent '` outside the commands of O1 | `TestOnlyTheCommandsWithNoLookComposeTheRefusalOfAMissingName`: nothing found on this code (the 5 places are `hq go`, `hq code`, `hq kill`, the Kill window, docking the pointed row); given sources, it reports a refusal composed in `runLs` and one outside every function, and passes one in `runGo` | ✅ pass | [6-module-tests.txt](https://github.com/rkrysinski/hq/blob/5e8faa146bf84af5d2dbc6b29739b32b59f87f09/issue-339/6-module-tests.txt), [2-searches.txt](https://github.com/rkrysinski/hq/blob/5e8faa146bf84af5d2dbc6b29739b32b59f87f09/issue-339/2-searches.txt) |
| 9 | Nothing changes but tests, the removed forwarders and the design; no ADR | the production diff only deletes the forwarders' declarations (and an import they used); no file under `docs/adr` | ✅ pass | [1-files.txt](https://github.com/rkrysinski/hq/blob/5e8faa146bf84af5d2dbc6b29739b32b59f87f09/issue-339/1-files.txt) |
| 10 | The new store fake implements `Stands` under today's name; `Stands` keeps its name | `lookerStore` embeds `memStore`, whose `Stands` it takes; `git grep -n -w Stands` shows the base's lines | ✅ pass | [2-searches.txt](https://github.com/rkrysinski/hq/blob/5e8faa146bf84af5d2dbc6b29739b32b59f87f09/issue-339/2-searches.txt) |
| 11 | Removing the copies leaves no line of the module without a test and no mutant alive, shown by a mutation run over the module's lines | the gate against `476a6ce` (before #337), so every line of `looker.go` and of the commands #337 and #338 changed is judged by the tests of this commit: 46 killed, 0 alive, 0 timed out, 0 not run | ✅ pass | mutation block |
| 12 | The design (testability) says the rules are tested at the module's interface and the commands test what they do with the result | 7.2 *The look in tests* and a line in section 10 | ✅ pass | diff of `docs/design/hq.md` |
| 13 | The look loop's test, which waits for a goroutine of the module, holds under load | `testgate load -p 3 -count 100`: 300 passed with every core busy | ✅ pass | [5-load-look-loop.txt](https://github.com/rkrysinski/hq/blob/5e8faa146bf84af5d2dbc6b29739b32b59f87f09/issue-339/5-load-look-loop.txt) |
| 14 | Full build and the mutation gate of the changed lines pass | both pass on `b4d8b74` | ✅ pass | blocks below |

Pyramid before (`6dd1bd9`, #338's figure): `unit 1001, integration 324, e2e 3 (unit share 76%, minimum 75%)`. After: `unit 998, integration 324, e2e 3 (unit share 75%, minimum 75%)`. The minimum stays 0.75: the share is 75.5% (998 of 1,322), so a minimum of 76% would fail it. Coverage after: `coverage 95.49% (minimum 94.7%)` (95.49% on the base, #338's figure, already more than 0.5 points above the minimum, so this change moves no threshold).

Tests added: unit 11: 9 in `internal/agent/looker_test.go` (below), `TestCommandsActOnNothingTheLookRefuses` (`internal/cli/look_rules_test.go`) and `TestOnlyTheCommandsWithNoLookComposeTheRefusalOfAMissingName` (`internal/cli/cli_test.go`); integration none; end-to-end none. Removed: unit 14 (below). Renamed, their refusal checks removed: `TestReadNeedsOneKnownAgent` to `TestReadNeedsOneAgentAndTmux`, `TestExplainRefusesAnUnknownOrOutdatedAgent` to `TestExplainSaysItsUsage`.

**The rules and their tests** (all in `internal/agent/looker_test.go`)

| Rule | Test at the module's interface |
|---|---|
| The race with `hq kill`: an agent killed during the look is left out | `TestALookLeavesOutAnAgentKilledWhileItLooked` |
| The counter read before the windows are listed | `TestALookReadsTheCounterBeforeItListsTheWindows` |
| The moment of asking sbx noted before the question (`Look`, `Loop`, `AskSbx`) | `TestALookNotesTheMomentItAsksSbxBeforeItAsks` |
| The second look after 2 s, in the variants that take it | `TestALookLooksAgainAtAScreenAtRestInTheVariantsThatTakeIt` |
| The look's cursor, with the looks again that cover every row | `TestTheLooksCursorStandsForWhatItSawAndCoversEveryAgentItShows` |
| One agent's cursor (`hq read`) | `TestAnAgentsCursorIsTheCounterOfTheLooksReadOfItsJournal` |
| The counter just before an action (`hq send`, `hq answer`) | `TestTheCounterBeforeAnActIsTheCounterThen` |
| The refusal by name | `TestALookGivesTheAgentNamedOrItsRefusal` |
| The look loop's rhythm | `TestTheLookLoopLooksEvery250msAndAsksSbxInTheBackgroundOnceASecond` |

**Removed and their replacements**

| Removed (file) | What it checked through a command | Replaced by |
|---|---|---|
| `TestALooksCursorLeavesWhatAnotherProcessNumberedMeanwhileToTheNextWait` (`cli/cursor_test.go`) | `hq ls --json`'s cursor is the lowest read when another process numbers a change meanwhile | `TestTheLooksCursorStandsForWhatItSawAndCoversEveryAgentItShows`, `TestALookReadsTheCounterBeforeItListsTheWindows` |
| `TestAQuietLookListsTheWindowsOnce` (`cli/cursor_test.go`) | a look with nothing past its counter lists the windows once | `TestTheLooksCursorStandsForWhatItSawAndCoversEveryAgentItShows` (quiet) |
| `TestAnAgentAppearedWhenItsWindowDid` (`cli/cursor_test.go`, through `agentAppeared`) | an agent whose window appeared | `TestTheLooksCursorStandsForWhatItSawAndCoversEveryAgentItShows` (an agent appeared) |
| `TestTheListsCursorStandsForEveryRowItShows` (`cli/cursor_test.go`) | `hq ls --json` looks again until its cursor covers every row; the plain table looks once | `TestTheLooksCursorStandsForWhatItSawAndCoversEveryAgentItShows` (covering) |
| `TestTheListLooksAgainOnlyAFewTimes` (`cli/cursor_test.go`) | at most `coverLooks` looks | `TestTheLooksCursorStandsForWhatItSawAndCoversEveryAgentItShows` (changing at every look) |
| `TestTheLooksCursorIsItsLowestReadUnlessAnAgentMayHaveAppeared` (`cli/look_rules_test.go`) | lowest read; an agent appeared or tmux failing gives the counter | `TestTheLooksCursorStandsForWhatItSawAndCoversEveryAgentItShows` |
| `TestTheLookSawEveryChangeUpToTheLowestCounterOfItsReads` (`agent/numbers_test.go`, through `SeenThrough`) | the lowest read, an outdated agent aside, an unread journal | `TestTheLooksCursorStandsForWhatItSawAndCoversEveryAgentItShows` (read past the counter, an outdated agent aside, a journal unread) |
| `TestSendsCursorIsTheCounterAsItLeavesTheMessage` (`cli/cursor_test.go`) | `hq send`'s cursor is the counter as it leaves the message | `TestTheCounterBeforeAnActIsTheCounterThen` |
| `TestSendsCursorWithoutTheCounterIsItsReadOfTheAgent` (`cli/look_rules_test.go`) | without the counter, the look's read of the agent | `TestTheCounterBeforeAnActIsTheCounterThen` (no counter) |
| `TestLsDoesNotEndAnAgentThatReportedAfterSbxWasAsked` (`cli/ls_test.go`) | #48 through `hq ls` | `TestALookNotesTheMomentItAsksSbxBeforeItAsks` (Look) |
| `TestWaitNeverReturnsAnAgentEndedOnAnAnswerOfSbxOlderThanItsReport` (`cli/wait_test.go`) | #48 through `hq wait` | `TestALookNotesTheMomentItAsksSbxBeforeItAsks` (Loop) |
| `TestLsShowsARewoundTurnDoneOnceItsScreenStaysAtRest` (`cli/ls_test.go`) | `hq ls` looks again 2 s later at a rewound screen | `TestALookLooksAgainAtAScreenAtRestInTheVariantsThatTakeIt`; the rewind's hold until the next report stays tested in `internal/state` (`seen_test.go`) |
| `TestWaitAsksSbxBeforeItsFirstLookThenInTheBackgroundOnceASecond` (`cli/look_loop_test.go`, removed with its file) | the look loop's rhythm through `hq wait` | `TestTheLookLoopLooksEvery250msAndAsksSbxInTheBackgroundOnceASecond` |
| `TestCommandsRefuseAnOutdatedAgent` (`cli/outdated_test.go`) | each command refuses an outdated agent in its words | `TestALookGivesTheAgentNamedOrItsRefusal`; what the commands do with it: `TestCommandsActOnNothingTheLookRefuses` |
| The rows refusing a missing name in `TestSendUsage`, `TestReadNeedsOneKnownAgent`, `TestExplainRefusesAnUnknownOrOutdatedAgent` (with its outdated rows) | each command's refusal in its words | `TestALookGivesTheAgentNamedOrItsRefusal`; `TestCommandsActOnNothingTheLookRefuses` |

**Kept at the commands** (what the command does with the look): `TestWaitReturnsAnAgentKilledBetweenTheStepsOfItsLookWithItsLastMessage` and `TestLsBetweenTheStepsOfAKillLeavesTheAgentOut` (the two the ticket keeps); the three #263 tests; delivery (`TestAMessageLeftWaiting...`, `TestEachLookDeliversTheMessagesLeftWaitingOneRoundAtATime`, `TestExplainTypesInNoMessageLeftWaiting`); `TestExplainExplainsAnEndedAndALostAgent`; the waits of `hq send` and `hq answer` (`TestSendToAStartingAgentWaitsForItsFirstReport`, `TestAnswerTakenButNotClosedNamesTheLikelyCause`, `TestSendLooksEvery250ms...`, `TestAnswerLooksEvery100ms...`, `TestAnswerToAnAgentKilledMeanwhile`); several names in `hq wait` and its rule without `--since` (`TestWaitOnNamedAgentsIgnoresTheOthers`, `TestWaitWithoutSinceReturnsAChangeNumberedRightAfterItsFirstLook`); `hq wait`'s one look more after a hit and its `--since` (`TestWaitReturnsAgentsThatFinishWithinALookOfEachOtherTogether`, `TestWaitSinceTheLastCursorReportsWhatChangedBetweenCalls`); the outputs that carry a cursor (`TestReadJSONHasWhatLsHasAndTheAgentInFull`, `TestAnswerGivesEveryQuestionItsAnswer`, `TestAnswerPicksAnOptionOfADialogOfClaudesOwnWithKeys`); a failed look as the exit code (`TestLsJSONFailsWhenALookAgainFails`, `TestAChordAndExplainFailWhenTmuxCannotListTheWindows`, `TestWaitFailsWhenTmuxCannotListTheWindows`, moved to `look_rules_test.go` with its file removed); the variants' sbx (`TestTheChordsAndDockingOnEntryAskSbxNothing`, `TestExplainTakesAnSbxTooOldAsAFailedSbxLs`, `TestUpdateAcrossGenerations...`).

#### Outputs

**Mutation tests**

`GOTOOLCHAIN=go1.26.0 go run ./tools/testgate mutation spec-336-one-look-module`

```text
mutation: no mutants in the lines changed, 0 not run against spec-336-one-look-module on b4d8b745f10701ccb8f0351cc29c01732d83dcbd
```

The same with the sandbox's own Go (go1.27.3) prints the same line.

Over the module's lines: `GOTOOLCHAIN=go1.26.0 go run ./tools/testgate mutation 476a6ce` (the base before #337, so every changed line of #337 and #338, `internal/agent/looker.go` whole, is judged by this commit's tests)

```text
not covered internal/agent/looker.go:128:18 arithmetic base
not covered internal/dash/dash.go:186:35 arithmetic base
mutation: 46 killed, 0 alive, 0 timed out, 2 not covered, 0 not run against 476a6ce on b4d8b745f10701ccb8f0351cc29c01732d83dcbd
```

- `looker.go:128:18` and `dash.go:186:35` are `not covered` (a constant's arithmetic, `250 * time.Millisecond`, and the list's ticker built from it), as in #338's run; neither fails the run.

Its first run, before `b4d8b74` bounded the covering looks in `TestTheLooksCursorStandsForWhatItSawAndCoversEveryAgentItShows`, for the record:

```text
not covered internal/agent/looker.go:128:18 arithmetic base
timed out  internal/agent/looker.go:337:58 increment decrement
not covered internal/dash/dash.go:186:35 arithmetic base
mutation: 45 killed, 0 alive, 1 timed out, 2 not covered, 0 not run against 476a6ce on a2209936fddf07b676c6ba020401e5c7734b2a7e
```

(`n++` into `n--` in `Covered` looked for ever; the test now fails past `2*CoverLooks` looks, as the removed `TestTheListLooksAgainOnlyAFewTimes` did: `still looking at look 7`.)

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
check: all 10 steps passed on b4d8b745f10701ccb8f0351cc29c01732d83dcbd
```

**The module's tests and the guard**

```text
$ go test -count=1 -v -run '^(<the 9 tests of the table)$' ./internal/agent | grep -E '^(--- |ok|FAIL)'
--- PASS: TestALookLeavesOutAnAgentKilledWhileItLooked (0.00s)
--- PASS: TestALookReadsTheCounterBeforeItListsTheWindows (0.00s)
--- PASS: TestALookNotesTheMomentItAsksSbxBeforeItAsks (0.00s)
--- PASS: TestALookLooksAgainAtAScreenAtRestInTheVariantsThatTakeIt (0.00s)
--- PASS: TestTheLooksCursorStandsForWhatItSawAndCoversEveryAgentItShows (0.00s)
--- PASS: TestAnAgentsCursorIsTheCounterOfTheLooksReadOfItsJournal (0.00s)
--- PASS: TestTheCounterBeforeAnActIsTheCounterThen (0.00s)
--- PASS: TestALookGivesTheAgentNamedOrItsRefusal (0.00s)
--- PASS: TestTheLookLoopLooksEvery250msAndAsksSbxInTheBackgroundOnceASecond (0.02s)
ok  	github.com/rkrysinski/hq/internal/agent	0.022s

$ go test -count=1 -v -run '^(TestOnlyTheCommandsWithNoLookComposeTheRefusalOfAMissingName|TestCommandsActOnNothingTheLookRefuses|TestLsJSONFailsWhenALookAgainFails|TestAChordAndExplainFailWhenTmuxCannotListTheWindows|TestWaitFailsWhenTmuxCannotListTheWindows)$' ./internal/cli
--- PASS: TestOnlyTheCommandsWithNoLookComposeTheRefusalOfAMissingName (0.01s)
--- PASS: TestLsJSONFailsWhenALookAgainFails (0.00s)
--- PASS: TestAChordAndExplainFailWhenTmuxCannotListTheWindows (0.00s)
--- PASS: TestWaitFailsWhenTmuxCannotListTheWindows (0.00s)
--- PASS: TestCommandsActOnNothingTheLookRefuses (0.00s)
ok  	github.com/rkrysinski/hq/internal/cli	0.009s
```

#### Deviations

- **The refusal by name: one command test of what the commands do with it.** The checks of the refusal at `hq read`, `hq send`, `hq answer`, `hq explain` and `hq wait` each wrote out the module's words; they are removed, and `TestCommandsActOnNothingTheLookRefuses` drives every command that takes the look's refusal through both refusals, checking its exit code, that it prints nothing else and acts on nothing, with the words taken from the module (`agent.NotFoundError`, `agent.OutdatedError`), so a change of the words changes one test. `hq code`'s row of the removed `TestCommandsRefuseAnOutdatedAgent` (O1, its own words) moved into it. The kept command tests that meet a missing name (`TestWaitOnNamedAgentsIgnoresTheOthers`, `TestAnswerToAnAgentKilledMeanwhile`) take the words from the module too (`noAgent`). The tests of the MCP tools, the integration tests and the tests of the O1 commands keep the words written out: the first two are other surfaces and levels of the same commands, and the O1 commands compose the words themselves.
- **Integration tests kept:** `TestCursorsOfLoopsLookingAtOnceMissNoChangeAndRepeatNone` (in the spec's table) and `TestWaitRightAfterNewNeverSeesTheAgentEndedOnAnOlderAnswerOfSbx` run hq processes at once against real tmux, the stub `sbx` and the files' store, which the module's fakes do not: they are the commands' contract at the integration level, not copies at the commands' unit level.
- **`TestTheLookSawEveryChangeUpToTheLowestCounterOfItsReads` removed from `internal/agent`:** a unit test of `SeenThrough`, which only `Seen.Cursor` calls; the cursor's test covers its cases through the interface, and the gate's mutants of `SeenThrough` are killed by it. One case it told apart no longer is: for an unread journal `SeenThrough` returns `(0, false)`, and `(0, true)` would give the same cursor, since 0 is never above the counter (removing that branch by hand passes every test); nothing hq does depends on the difference.
- **Moved:** `TestWaitFailsWhenTmuxCannotListTheWindows` from `look_loop_test.go`, removed with its other test, to `look_rules_test.go`, unchanged; `dialogOnB` there as `changingDialogOnB`, with only the changing screen its one remaining user takes.
- **Seams:** none new; the module's tests use the journals' fake with its screens (`memStore`) and fakes of tmux's windows, sbx's answer and the clock.
- **Deploy steps:** none.
