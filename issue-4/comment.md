## QA run: acceptance on Windows/WSL (spec §10, S13) - dashboard passes in Windows Terminal with a stand-in sandbox; four findings; real-Claude checks blocked

Run on 2026-09-30 by Claude (QA) against the released **hq v0.3.0**, in a real Windows Terminal window driven with real key presses (Windows `SendKeys`), pictures taken of the window itself.

**Verdict: not closable yet.** This machine cannot start a sandbox (Windows 11 ARM64 in a QEMU VM, no virtualization, WSL 1 - details on #3), so the agents below are the repository's stand-ins: `internal/testutil/sbx-stub` as `sbx.exe` and `tools/fakeclaude` as Claude ([sim-env.sh]({{B}}/sim-env.sh)). Everything else is real: the released hq binary, tmux 3.4, Windows Terminal 1.24, `wslpath`, `code`, `powershell.exe`. The stand-in Claude draws plain text with no colour and echoes typed input below its prompt box; that look is the stand-in's, not hq's. Checks that need real Claude or a real microVM are marked blocked.

### §10 manual test

| Step | Expected | Result | Evidence |
|---|---|---|---|
| 1 `hq new a ... "say hi"`, `hq new b ... "ask me one yes/no question and stop"` | two agents | ✅ pass (stand-in) | picture 1 |
| 2 `hq`: `b` as `question` in the attention view; `a` shows `a` as `done` below | as stated | ✅ pass | pictures 1, 2 |
| 3 `/a` then `Enter`; type a follow-up, the agent answers there | session docked, keys reach it | ✅ pass | pictures 3, 4 |
| 4 From inside the session, dock the first agent needing attention | `b` | ✅ pass (`Alt+a`) | picture 5 |
| 5 `c` on `b`: editor on its worktree and branch | VS Code opens | ❌ **fail**: nothing opens (finding 1) | [20]({{B}}/20-code-from-wsl.txt) |
| 6 `k` on `a`: Kill dialog, `Yes` ends it, row goes | as stated; `Enter` means No | ✅ pass; the name is reusable afterwards | pictures 10, 11 |

### The four §10 criteria

| Criterion | Result | Evidence |
|---|---|---|
| Three agents in two repos (`needs input`, `done`, `working`) listed in that order within 1 s of each change | ✅ pass (stand-in): 0.2 to 0.4 s | [33]({{B}}/33-order-within-1s.txt), picture 43 |
| Resize keeps 6 list rows, the rest to the session; under 24 rows the list shows 3; at 80 columns `REPO` and `LAST` drop and the strip is glyphs | ✅ pass | pictures 12, 13, 14 |
| Externally stopped sandbox: rows `ended` within 2 s | ✅ pass (stand-in `sbx stop`): 0.2 s | [30]({{B}}/30-external-stop-ended.txt), picture 15 |
| Exactly one notification per attention event | ⛔ blocked: the stand-in Claude does not emit the notification sequence; needs real Claude | - |

### S13 and design §9

| Check | Result | Evidence |
|---|---|---|
| `Alt+j`, `Alt+k`, `Alt+a`, `Alt+l`, `Alt+n` in Windows Terminal (default key bindings) | ✅ pass: all five reach hq, none is taken by Windows Terminal | pictures 5 to 8 |
| Window title `hq - agents` | ✅ pass | every picture |
| `c` opens VS Code (Remote-WSL) | ❌ fail (finding 1) | [20]({{B}}/20-code-from-wsl.txt), [21]({{B}}/21-code-in-a-terminal.txt) |
| `p` opens the browser | ⛔ not run: no `gh` on the machine, so no pull request to open | - |
| `hq go` raises the window | ⚠️ typed in a terminal window that is in front: ✅ raises. Run by a background process (as the supervisor does): ❌ not raised, the taskbar button flashes instead, and hq still reports success (finding 3) | [31]({{B}}/31-hq-go-raise.txt), picture 38 |
| Taskbar flash on attention; tmux passes BEL from a hidden window | ⚠️ tmux does pass BEL from a hidden agent pane (bare and in passthrough), but Windows Terminal flashes **only** with `bellStyle` set; with its default settings nothing flashes (finding 2) | [32]({{B}}/32-bel-taskbar-flash.txt), pictures 22, 31, 36 |
| An ended agent stays readable after `/exit` and after `sbx stop` | ✅ pass (stand-in) | pictures 16, 17 |
| `q` gives the shell back with the hint; `hq` returns with the same docked agent | ✅ pass | pictures 41, 42 |
| `hq mcp install` from WSL writes the `wsl.exe` entry; started from the Windows side it answers `initialize` and `list`; a run with nothing changed does nothing | ✅ pass (finding 4 on where it writes) | [34]({{B}}/34-mcp-install.txt), [claude_desktop_config.json]({{B}}/claude_desktop_config.json) |
| Supervisor acceptance from Claude Desktop on Windows | ⛔ not run: Claude Desktop is not installed | - |
| `sbx.exe` hosting a repository on the WSL file system at usable speed | ⛔ open: sbx accepts the `\\wsl.localhost\...` path, speed not measurable without a microVM (see #3) | - |

### Findings

1. **`c` and `hq code` open nothing on this WSL, and hq says they did.** `code DIR` typed in a terminal opens `wslrepo [WSL: Ubuntu-24.04] - Visual Studio Code`. `hq code a` prints `opened ... in VS Code`, exits 0, and no window appears; `c` in the dashboard does nothing visible. Cause as far as I could trace it: hq collects the editor's stderr through a pipe (`internal/proc/proc.go:44`), and VS Code's WSL launcher then dies with `Error: open EISDIR` while still exiting 0. `code DIR 2>/dev/null` works; `code DIR 2>&1 | cat` fails the same way. Reproduced three times, last time after the machine was left alone. Seen on WSL 1; not checked on WSL 2.
2. **Windows Terminal does not flash the taskbar by default.** The README says "Windows Terminal flashes its taskbar button". With Windows Terminal's settings as installed, a BEL from an agent pane changes nothing on the taskbar (two tries). After adding `"bellStyle": "all"` to the profile defaults the button flashes, from a hidden pane and from the docked one. Either the README names the setting, or hq's setup sets it. (I restored the settings file afterwards.)
3. **`hq go` from a background process does not raise the window and reports success.** Windows refuses the foreground change and flashes the button; `AppActivate` evidently still returns true, so hq prints `docked a in the open dashboard` with exit 0. This is the design §9 foreground-lock question: it bites the supervisor's `go`, not a person typing `hq go`.
4. **`hq mcp install` creates Claude Desktop's folder when Claude Desktop is not installed.** `%APPDATA%\Claude` did not exist; hq created it with a new `claude_desktop_config.json` and said `added hq to Claude Desktop`. Spec §4.1: when the configuration cannot be found it changes nothing and prints the entry. (I removed the folder afterwards.) The Microsoft Store location from design §9 could not be checked.
5. **Cosmetic.** At 80 columns with the keys in the session, the footer is cut off (`alt+n ne`), picture 14. After `q`, tmux's own `[detached (from session hq)]` line stays above hq's hint, picture 41.

### The project's test suite on this WSL (v0.3.0, Go 1.27.1, linux/arm64)

| Level | Result | Evidence |
|---|---|---|
| Unit | ✅ all pass | [10]({{B}}/10-tests-unit.txt) |
| Integration | ❌ 5 failing | [13]({{B}}/13-tests-integration-run2.txt), [15]({{B}}/15-tests-retest.txt) |
| End-to-end | ❌ 2 of 3 failing | [14]({{B}}/14-tests-e2e-run2.txt) |

- **The suite is not self-contained on WSL.** hq detects WSL and runs `sbx.exe`, while the tests put their stub on PATH as `sbx`. With the real sbx installed the tests called the real `sbx.exe` ([11]({{B}}/11-tests-integration.txt), [12]({{B}}/12-tests-e2e.txt)); with it off PATH they fail with `sbx not found`. Affects `TestMCPServerSupervisesAgentsOverStdio`, `TestMCPInstallAddsHqToClaudeDesktopOnceAndKeepsTheRest`, `TestStartSeeEnterKill`, `TestDashboardFollowsAgentsQuitsAndComesBack`.
- **`internal/proc` integration test does not compile on Linux**: `undefined: syscall.Getsid` (arm64 and amd64).
- **`TestKeepFirstLetsTheFirstRecordWinAcrossRacingWriters` fails 3 of 3** on tmux 3.4: `$x` comes back as `\$x`.
- **`TestPasteTypesTextAsABracketedPasteAndSubmitPressesEnter` fails 3 of 3**: about 8 KB of a 12 KB paste arrives. Through `hq send` itself, messages up to the 8 KiB limit arrived whole (85 lines, 7649 characters), so I found no user-visible loss ([15-go-send.txt]({{R3}}/15-go-send.txt)).
- `TestSandboxRestartRelaunchesAgentsAndRmRemovesTheSandbox` failed once under load and passed 3 of 3 on retest: not counted.

### Acceptance criteria

- [ ] §10 and S13 pass on Windows/WSL; findings filed as issues - step 5 fails; notifications, `p`, and everything needing real Claude still open. I have not filed the findings as separate issues; say the word and I will.
- [ ] The WSL file-system question answered and recorded in design §8 - still open.

### Pictures

1 ![attention view]({{R}}/wt-01-dashboard-attention-view.png?raw=true)
2 ![all view]({{R}}/wt-02-step2-all-view.png?raw=true)
3 ![name search]({{R}}/wt-03-step3-search-a.png?raw=true)
4 ![follow-up in the docked session]({{R}}/wt-04-step3-a-docked-followup.png?raw=true)
5 ![Alt+a docks b]({{R}}/wt-05-step4-alt-a-docks-b.png?raw=true)
6 ![Alt+j]({{R}}/wt-06-alt-j-next.png?raw=true)
7 ![Alt+l]({{R}}/wt-07-alt-l-keys-on-list.png?raw=true)
8 ![Alt+n]({{R}}/wt-08-alt-n-new-agent-dialog.png?raw=true)
10 ![Kill dialog]({{R}}/wt-10-step6-kill-dialog.png?raw=true)
11 ![killed, placeholder]({{R}}/wt-11-step6-a-killed-placeholder.png?raw=true)
12 ![larger window]({{R}}/wt-12-resize-large.png?raw=true)
13 ![smaller window]({{R}}/wt-13-resize-small.png?raw=true)
14 ![80 columns, short]({{R}}/wt-14-narrow-short.png?raw=true)
15 ![ended after external stop]({{R}}/wt-15-external-stop-c-ended.png?raw=true)
16 ![readable after sbx stop]({{R}}/wt-16-ended-after-stop-readable.png?raw=true)
17 ![readable after /exit]({{R}}/wt-17-ended-after-exit-readable.png?raw=true)
22 ![taskbar flashing with bellStyle all]({{R}}/wt-22-taskbar-raw-bel-bellstyle-all.png?raw=true)
31 ![BEL from a hidden pane]({{R}}/wt-31-bel-hidden-bare.png?raw=true)
36 ![default settings, no flash]({{R}}/wt-36-bel-default-settings-1.png?raw=true)
38 ![hq go typed in a second window]({{R}}/wt-38-hq-go-from-second-window.png?raw=true)
41 ![after q]({{R}}/wt-41-after-q-shell-back.png?raw=true)
42 ![hq again]({{R}}/wt-42-hq-again-same-docked.png?raw=true)
43 ![three agents in order]({{R}}/wt-43-three-agents-order.png?raw=true)
