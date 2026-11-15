### #199 The old state mechanism is removed

| # | Check | Expected | Result | Evidence |
|---|-------|----------|--------|----------|
| 1 | An agent started by this hq on the fake Claude (`hq new a . "say hi"`), then the hook's directory and the home window's options | no state file, no `.stop` or `.prev` beside the repository: only the hook's notes `.last` and `.ended` and hq's record `.hq`; the window carries none of the six markers (`turnend`, `restseen`, `dialogseen`, `answered`, `endseen`, `wokeseen`) | ✅ pass | [1-files-and-window-after.png](https://github.com/rkrysinski/hq/blob/29caee69002f166372e7908b22b2610253be0783/issue-199/1-files-and-window-after.png), [.txt](https://github.com/rkrysinski/hq/blob/29caee69002f166372e7908b22b2610253be0783/issue-199/1-files-and-window-after.txt) |
| 2 | `hq ls` and `hq read a` before and after garbling the hook's `.last` and `.ended`, then after removing them | the same output each time (`diff`: same) | ✅ pass | [2-garbled-removed.png](https://github.com/rkrysinski/hq/blob/29caee69002f166372e7908b22b2610253be0783/issue-199/2-garbled-removed.png), [.txt](https://github.com/rkrysinski/hq/blob/29caee69002f166372e7908b22b2610253be0783/issue-199/2-garbled-removed.txt) |
| 3 | The next report after that: `.last` garbled, `.ended` gone, `hq send a "say again"` | `done` with `Done: say again`; the hook made its notes again | ✅ pass | [2b-next-report.png](https://github.com/rkrysinski/hq/blob/29caee69002f166372e7908b22b2610253be0783/issue-199/2b-next-report.png), [.txt](https://github.com/rkrysinski/hq/blob/29caee69002f166372e7908b22b2610253be0783/issue-199/2b-next-report.txt) |
| 4 | A question dialog (`hq new b . "need your input"`), `hq read b`, then `hq answer b --dialog d2d1b1 2` | `needs input` with the dialog and its options; the answer reaches Claude (`→ Blue`) and b is `done`; the journal keeps the dialog's report and the answers Claude got (`journals/ID.ask`) | ✅ pass | [3-dialog-asks.png](https://github.com/rkrysinski/hq/blob/29caee69002f166372e7908b22b2610253be0783/issue-199/3-dialog-asks.png), [3-answered-journal.png](https://github.com/rkrysinski/hq/blob/29caee69002f166372e7908b22b2610253be0783/issue-199/3-answered-journal.png), [.txt](https://github.com/rkrysinski/hq/blob/29caee69002f166372e7908b22b2610253be0783/issue-199/3-dialog-asks.txt), [.txt](https://github.com/rkrysinski/hq/blob/29caee69002f166372e7908b22b2610253be0783/issue-199/3-answered-journal.txt) |
| 5 | Generation: a's window set to `@hq_inbox 3`, b's left at this hq's 4 | a `outdated` (`started by an older hq: kill it and start it again`), b `done` | ✅ pass | [4-outdated.png](https://github.com/rkrysinski/hq/blob/29caee69002f166372e7908b22b2610253be0783/issue-199/4-outdated.png), [.txt](https://github.com/rkrysinski/hq/blob/29caee69002f166372e7908b22b2610253be0783/issue-199/4-outdated.txt) |
| 6 | No host code reads the old files or markers; KeepFirst gone (`git grep` of non-test Go code) | no match but the list of legacy file names `Remove` deletes when an agent goes; the six markers nowhere | ✅ pass | [5-no-host-reads.txt](https://github.com/rkrysinski/hq/blob/29caee69002f166372e7908b22b2610253be0783/issue-199/5-no-host-reads.txt) |
| 7 | Notification tests | every existing notification test passes, their expectations unchanged | ✅ pass | [6-notification-tests.txt](https://github.com/rkrysinski/hq/blob/29caee69002f166372e7908b22b2610253be0783/issue-199/6-notification-tests.txt) |
| 8 | Hook script size | within the 10 KiB budget: 10,220 of 10,240 bytes | ✅ pass | [5-no-host-reads.txt](https://github.com/rkrysinski/hq/blob/29caee69002f166372e7908b22b2610253be0783/issue-199/5-no-host-reads.txt) |
| 9 | Fixture journals rebuilt under the new hook (`-update`) | the events unchanged: 30 of 30 identical once the old reading is left out | ✅ pass | [7-fixture-journals.txt](https://github.com/rkrysinski/hq/blob/29caee69002f166372e7908b22b2610253be0783/issue-199/7-fixture-journals.txt) |
| 10 | Every journey, and the full build | e2e and all 8 steps pass | ✅ pass | full build below |
| 11 | Design (state channel 3.3-3.6, data model 6, security 7.3) and the version change's entry | the removed files and markers no longer described; entry for #199 | ✅ pass | `docs/design/hq.md` in the branch |
| 12 | The deploy step for the spec's pull request | under `## Before deployment`, with its rollback line | ✅ pass | Deviations below |

No look of hq changed, so there is no before screenshot.

Pyramid before: `unit 770, integration 268, e2e 3 (unit share 74%, minimum 50%)`
Pyramid after (head 74f52f1): `unit 764, integration 264, e2e 3 (unit share 74%, minimum 50%)`
Coverage after: 95.27% (minimum 94.4%; 95.26% on the integration branch)

Tests added or changed per level:
- Unit: `TestTheHostsStateIgnoresTheHooksWorkingFiles` (state); `TestHqAnswerReadsTheDialogAndItsAnswersFromTheJournal` (cli); the journals' contract "a question dialog keeps its report and then the answers Claude got" (`journaltest`, run on the fake and the files); an agent of generation 3 is `outdated` (agent); the generation fingerprint pinned at 4 with the new hook and the journal's `Asked`. The tests of the old reading are gone with it: `Parse`, `Kept`, `Own`, `Read`, `TakeQueued`, KeepFirst and the reducer's comparison with the old reading; what they held the reducer's tests and the fixture journals hold.
- Integration: `TestTheHooksWorkingFilesAndTheHomeWindowsHoldNothingHqReads` (cli, replaces the test of #198 that removed the old files: no marker on the window, the working files garbled and removed change nothing); the hook's tests read what it reported from its event files through the reducer (`reported`, `detailed`, `asked`, new `reported_integration_test.go`) in place of the state files; the look leaves the hook's working files untouched; the hook's note names (`.last`, `.ended`) in the file tests.
- End-to-end: none added; the journeys pass unchanged.

#### Outputs

`go run ./tools/testgate pyramid` after:

```text
unit 764, integration 264, e2e 3 (unit share 74%, minimum 50%)
```

Check 1 (`ls -A .git/hq/agents`, the home window's options):

```text
5b37e1daa299.ended  5b37e1daa299.hq  5b37e1daa299.last
home window @1 of a:
@hq_id 5b37e1daa299
@hq_inbox 4
@hq_name a
@hq_new 1
@hq_repo /tmp/hq-stub-1195016276/repo
@hq_sandbox claude-repo
@hq_started 1794766830.824
$
```

Check 2 (garbled, then removed):

```text
== before: 5b37e1daa299.ended
5b37e1daa299.hq
5b37e1daa299.last
== garbled .last and .ended
hq ls and hq read a: unchanged after garbling
== removed: 5b37e1daa299.hq
hq ls and hq read a: unchanged after removing
NAME  REPO  BRANCH  STATE  AGE  LAST
a     repo  main    done   2m   Done: say hi
name      a
repo      repo  /tmp/hq-stub-1195016276/repo
branch    main
worktree  /tmp/hq-stub-1195016276/repo
sandbox   claude-repo
state     done 2m
pending   0
reply
  Done: say hi
$
```

Check 3 (the next report):

```text
NAME  REPO  BRANCH  STATE  AGE  LAST
a     repo  main    done   43s  Done: say again
reply
  Done: say again
== files now: 5b37e1daa299.ended
5b37e1daa299.hq
5b37e1daa299.last
$
```

Check 4 (the dialog, the answer, the journal):

```text
started b in repo (sandbox claude-repo); enter it with: hq go b
NAME  REPO  BRANCH  STATE        AGE  LAST
b     repo  main    needs input  2s   Which colour do you pick?
a     repo  main    done         1m   Done: say again
name      b
repo      repo  /tmp/hq-stub-1195016276/repo
branch    main
worktree  /tmp/hq-stub-1195016276/repo
sandbox   claude-repo
state     needs input 3s
pending   0
last      Which colour do you pick?
asks      dialog d2d1b1
  Colour: Which colour do you pick?
    1. Red - Pick red.
    2. Blue - Pick blue.
answer    hq answer b --dialog d2d1b1 N
reply     none
$
ls: cannot access '.git/hq/journals': No such file or directory
answered: b is working
NAME  REPO  BRANCH  STATE  AGE  LAST
b     repo  main    done   4s   Done: need your input
a     repo  main    done   1m   Done: say again
pending   0
reply
  Done: need your input
== agents dir: 5b37e1daa299.ended
5b37e1daa299.hq
5b37e1daa299.last
bb49b05c973d.ended
bb49b05c973d.hq
bb49b05c973d.last
$
== agent b's screen:
❯ need your input
 ☐ Colour
Which colour do you pick?
❯ 1. Red
  2. Blue
Enter to select · ↑/↓ to navigate · Esc to cancel
● User answered Claude's questions:
  ⎿  · Which colour do you pick? → Blue
  ⎿  Allowed by PermissionRequest hook
== what the journal keeps of b's dialog (journals/ID.ask), report cut:
 "n": 11,
  "tool": "AskUserQuestion",
  "questions": [
    "header": "Colour",
    "question": "Which colour do you pick?",
    "options": [
    ],
    "multi_select": false
  ]
 "report": "branch main\n{\"cwd\":\"/tmp/hq-stub-1195016276/repo\",\"hook_even...",
 "answered": true,
  "Which colour do you pick?": "Blue"
$
```

Check 5 (generation):

```text
== a's window, as an agent of the previous hq: @hq_inbox 3
@hq_inbox 3
@hq_inbox 4
NAME  REPO  BRANCH  STATE     AGE  LAST
a     repo  -       outdated  -    started by an older hq: kill it and start it again
b     repo  main    done      49s  Done: need your input
$
```

Checks 6 and 8:

```text
$ git grep -nE "KeepFirst|TakeQueued|state\.Read\(|ReadDetail|ReadQuestions|ReadAnswers|state\.Parse\(|\.stop\"|\.prev\"|hq_(state|since|last|ack|seen|stopped)" -- "*.go" ":!*_test.go"
internal/state/read.go:88:var agentFiles = [...]string{"", ".stop", ".prev", noteSuffix, endedSuffix, keptSuffix, owedSuffix, supervisedSuffix, announcedSuffix, actedSuffix, queuedSuffix, recordSuffix}
(exit 0)

$ git grep -nE "hq_(turnend|restseen|dialogseen|answered|endseen|wokeseen)|\"(turnend|restseen|dialogseen|endseen|wokeseen)\"" -- "*.go" ":!*_test.go"
(exit 1: no match)

$ hook script size
ok  	github.com/rkrysinski/hq/internal/state	0.001s [no tests to run]
hook script of 10220 bytes (budget 10240): TestSettingsCarryIdentityAndOneHookPerEvent passes
```

Check 7:

```text
$ go test -tags integration -count=1 -run "Notif" -v ./internal/state/ ./internal/platform/
--- PASS: TestHookNotifiesOnStopAndInputNamingTheBranch (0.07s)
--- PASS: TestATurnEndingWhileASubagentRunsLeavesTheAgentWorkingAndNotifiesNobody (0.05s)
--- PASS: TestADialogNotifiedWhileTheAgentWaitsOnASubagentIsTheSubagents (0.03s)
--- PASS: TestSubagentsThatFinishTogetherNotifyOnceWhenClaudeHasTakenItsTurnForEach (0.25s)
--- PASS: TestBackgroundWorkIsKnownFromItsStartSoATurnTheUserInterruptedStillNotifiesOnce (0.11s)
--- PASS: TestASupervisedTurnEndsWithoutANotificationButItsDialogNotifies (0.11s)
--- PASS: TestAnyPromptOrMessageOfTheUsersMakesTheTurnNotify (0.36s)
ok  	github.com/rkrysinski/hq/internal/state	0.992s
--- PASS: TestNotificationSequencePerPlatform (0.00s)
ok  	github.com/rkrysinski/hq/internal/platform	0.001s
```

Check 9:

```text
30 fixture journals rebuilt with -update against origin/spec-165-agent-state-journal
30 of 30 identical once the old reading (field "old") is left out
```

Mutation tests, `go run ./tools/testgate mutation spec-165-agent-state-journal`:

```text
mutation: staging the tree in /tmp/hq-mutation-2412186441
mutation: installing go-gremlins v0.6.0
mutation: running the unit tests once, then once per mutant
mutation: done in 33s
mutation: 18 killed, 0 alive, 0 timed out, 50 not covered against spec-165-agent-state-journal on 74f52f15300481e54d1a3b075fb90d1ef981925b
```

Full build, `scripts/check.sh`:

```text
check: ok   vet
check: ok   format
check: ok   pyramid
check: ok   platform
check: ok   unit
check: ok   integration
check: ok   coverage
check: ok   e2e
check: all 8 steps passed on 74f52f15300481e54d1a3b075fb90d1ef981925b
```

#### Deviations

- Deploy step, for the spec's pull request:
  - `## Before deployment`: end your agents before updating to this version (`hq stop`), because the hq you have cannot refuse this update, and agents left running show as `outdated` and can only be opened or killed.
  - `## Rollback`: None: one-way door (agents ended with `hq stop` are not brought back; start them again with `hq new`).
- The notification tests' expectations are unchanged, but the hook's tests read its state through the event files and the reducer (`reported` and the other helpers of `internal/state/reported_integration_test.go`) where they read the state files with `state.Read`/`state.ReadDetail`, which are gone.
- The hook still keeps two notes of its own in `.git/hq/agents`, which the host never reads: `ID.last` (the key fields of the latest report and its checksum, for the hook's own rules and the hold hook's check that a dialog is still its own) and `ID.ended` (the same of the latest reported Stop, in place of `.stop`). `ID.on` is now an empty mark. `Remove` still deletes the legacy `ID`, `ID.stop` and `ID.prev`, so `hq kill` of an outdated agent takes the files its older hook wrote.
- `hq answer` read the open dialog and the answers Claude got from the hook's state file; it now reads them from the journal: `journals/ID.ask` keeps the question dialog's report and, after AskUserQuestion's PostToolUse, the answers (`state.Asked` gained `Report`, `Answered`, `Answers`), so `state.Asked` joined the journal form's generation fingerprint.
- The look no longer hands the queued prompt over to the hook's files (`TakeQueued` and `agent.Taker` are gone): the queued prompt is the journal's.
- `state.Report` stays, only as the description type of `journaltest.ReportFiles`.
- The fixture journals lose the old reading they were compared with (field `old`); their events are unchanged.
