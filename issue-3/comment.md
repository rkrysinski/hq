## QA run: M1 on Windows/WSL - install and CLI pass, real sandboxes blocked by the test machine

Run on 2026-09-30 by Claude (QA) against the released **hq v0.3.0**, installed with the one-line command.

**Verdict: not closable yet.** Install and every M1 command behave as specified on WSL, but no real sandbox can start on this machine, so the commands were exercised against the repository's own stand-ins (`internal/testutil/sbx-stub` as `sbx.exe`, `tools/fakeclaude` as Claude). The WSL file-system question is only half answered. Both need one more run on an x86-64 Windows 11 machine.

### Machine

| | |
|---|---|
| Windows | 11 Pro 10.0.26200, **ARM64**, a QEMU virtual machine with no nested virtualization |
| WSL | Ubuntu 24.04.5, **WSL 1** (kernel `4.4.0-26100-Microsoft`), not WSL 2 |
| Terminal | Windows Terminal 1.24.11911.0 |
| tmux | 3.4 |
| sbx | 0.46.0 (`winget install -h Docker.sbx`), signed in, network policy `balanced` |

`sbx diagnose`: `Virtualization - not available` (`WinHvPlatform.dll: %1 is not a valid Win32 application`). Docker documents Windows support for x86-64 with the Windows Hypervisor Platform only. `sbx create` fails the same way for a repository on `C:\` and on `\\wsl.localhost\...` ([09-sbx-create-direct.txt]({{B}}/09-sbx-create-direct.txt)).

### Checks

| # | Check | Expected | Result | Evidence |
|---|-------|----------|--------|----------|
| 1 | One-line install with a prerequisite missing | exit 3, names `sbx` and the remedy, installs nothing | ✅ pass | [01]({{B}}/01-install-as-found.txt) |
| 2 | One-line install, prerequisites present | `~/.local/bin/hq`, checksum matches `SHA256SUMS` | ✅ pass (linux-arm64 binary) | [03]({{B}}/03-install-with-sbx.txt) |
| 3 | `hq --version`, `hq help` | version, usage | ✅ pass (`hq v0.3.0`) | [03]({{B}}/03-install-with-sbx.txt) |
| 4 | tmux on Ubuntu 24.04 is 3.4 or newer | 3.4+ | ✅ pass (`tmux 3.4`) | [04]({{B}}/04-cli-no-agents.txt) |
| 5 | Errors and exit codes with no agents (unknown agent, bad name, name over 32, not a repository, unknown command, no terminal) | one `hq:` line, codes 1 / 2 / 3 as spec §4.2 | ✅ pass | [04]({{B}}/04-cli-no-agents.txt) |
| 6 | `hq new` with the real `sbx.exe` | agent starts | ⛔ blocked: no virtualization on this machine | [05]({{B}}/05-new-with-real-sbx.txt), [08]({{B}}/08-new-t1-after-policy.txt) |
| 7 | `hq new`, `hq ls`, duplicate name, `hq kill` (n, then y) | as on macOS | ✅ pass, **stand-in sandbox** | picture 1 |
| 8 | `hq sandbox rm` refused with agents, `hq sandbox restart`, `hq stop`, `hq sandbox rm` | as on macOS | ✅ pass, **stand-in sandbox** | picture 2 |
| 9 | `hq go NAME` from a terminal with no dashboard open; with no terminal | attaches and docks; docks and says so | ✅ pass, **stand-in sandbox** | picture 3, [15]({{B}}/15-go-send.txt) |
| 10 | Paths cross to sbx and back (`wslpath`) for a repository in `~` and on `/mnt/c` | sbx sees `\\wsl.localhost\...` and `C:\...`, hq shows its own paths | ✅ pass (real `wslpath`, stand-in sbx) | picture 2 |
| 11 | `hq update` | up to date, or moves to the latest | ✅ pass for "up to date"; moving from an older release not testable (`v0.2.8` has no public assets, 404) | [16]({{B}}/16-update.txt) |
| 12 | `sbx.exe` mounts a repository on the WSL file system at usable speed | answer recorded | ⚠️ half: sbx **accepts** the `\\wsl.localhost\...` workspace and resolves it; speed **not measurable** here | [09]({{B}}/09-sbx-create-direct.txt) |

![1 new, ls, kill]({{R}}/wt-m1-01-new-ls-kill.png?raw=true)
![2 sandbox, stop]({{R}}/wt-m1-02-sandbox-stop.png?raw=true)
![3 hq go attaches]({{R}}/wt-m1-03-hq-go-attaches.png?raw=true)

(In picture 1 the line `hq new x` followed by a stray `hq-qa/repos/wslrepo` is my key-sending script mistyping `~`; it happens to show the duplicate-name refusal.)

### Findings

1. **hq shows the wrong line of an sbx failure.** When sandbox creation fails, hq prints `hq: sbx.exe: WARN: mcp gateway teardown`; the real reason is on the next line (`error: the Windows Hypervisor Platform is unavailable ...`). hq takes the first line of sbx's stderr. ([08]({{B}}/08-new-t1-after-policy.txt) against [09]({{B}}/09-sbx-create-direct.txt))
2. **hq drops sbx's remedy.** Not signed in: hq prints `hq: sbx.exe: error: Not authenticated to Docker` and leaves out sbx's `try: sbx login` (spec §4.2: errors name the remedy). ([05]({{B}}/05-new-with-real-sbx.txt))
3. **A fresh sbx needs two setup steps nobody mentions.** After installing sbx, `hq new` fails until `sbx login` and then `sbx policy init <allow-all|balanced|deny-all>` have been run (`error: global network policy has not been initialized`). README and spec §11 only say "install Docker Sandboxes". ([07]({{B}}/07-new-t1-wslfs.txt))
4. **The installer's sbx check fails in a WSL session opened before sbx was installed.** winget adds sbx to the Windows user PATH, which a running WSL session does not pick up; a new WSL session is needed. Worth one line in the install instructions. ([02]({{B}}/02-sbx-install-winget.txt), [03]({{B}}/03-install-with-sbx.txt))
5. **Prerequisites do not say WSL 2 or x86-64.** hq itself runs fine on WSL 1 and on ARM64; sbx does not run on ARM64 Windows at all. Spec §11 and the README could say what sbx needs.

### Acceptance criteria

- [ ] Every M1 command checked on WSL, results commented here - checked, but with a stand-in sandbox; `hq new` against the real `sbx.exe` still open.
- [ ] The WSL file-system question answered - sbx accepts the path; speed still open.

What I changed on the machine: installed Docker Sandboxes (winget), initialised its network policy to `balanced`, installed hq v0.3.0. No sandbox was created.
