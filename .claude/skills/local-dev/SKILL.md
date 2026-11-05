---
name: local-dev
description: Build and run hq from this checkout for development and QA, isolated from the user's own hq and from other worktrees. Use when asked to run hq, verify a change by driving hq, or clean up after QA. Covers scripts/local-dev.sh.
---

# Local run

Always run hq through `scripts/local-dev.sh` from the checkout you are working in:

```bash
scripts/local-dev.sh --version          # build .local-dev/hq, then run it with these arguments
scripts/local-dev.sh new a . "say hi"   # any hq command
scripts/local-dev.sh --kill-server      # end this checkout's private tmux server when done
```

- The script sets `HQ_TMUX_SOCKET` to a tmux server private to this checkout (`tmux -L <name>`), so agents you start never show up in the user's `hq` session and parallel worktrees never meet. Inspect it with `tmux -L "$HQ_TMUX_SOCKET" ...` after exporting the same value, or read it from the script.
- It also gives the checkout its own preferences file (`XDG_CONFIG_HOME=.local-dev/config`), so `s`/`a` during QA never change the user's own dashboard; gh keeps the user's login (`GH_CONFIG_DIR`).
- It rebuilds on every run, so code changes are always live.
- The build is a dev build, which never looks for updates. To check update behaviour (the notice, `hq update`), `LOCAL_DEV_VERSION=v0.1.0 scripts/local-dev.sh ...` bakes that version in, and `HQ_RELEASES_URL` points hq (and `scripts/install.sh`) at a stub release server that answers as GitHub's release URLs do.
- `sbx` is real: `hq new` starts Claude in the repository's real sandbox. Prefer this repository's own sandbox for QA, keep prompts short, and `kill` every agent you started before finishing. `--stub` (below) swaps it for the stub.

## Windows/WSL without a Windows machine: `--stub`

```bash
cd /tmp/hq-stub-*/repo                                    # after a first run; a neutral repository
scripts/local-dev.sh --stub wsl-exe new a . hello         # any hq command, against the stubs
scripts/local-dev.sh --stub wsl-exe --before mcp install sbx   # the binary built from origin/dev, same path
scripts/local-dev.sh --kill-server                        # ends the server and removes the stub environment
```

- Modes: `native` (stub `sbx`, no Windows), `wsl-linux` (WSL detected through `WSL_DISTRO_NAME=Stub`, stub `sbx` as the Linux one, stub `wslpath`, `cmd.exe`, `powershell.exe`), `wsl-exe` (the same with the stub as `sbx.exe`), `none` (WSL with no sbx). A real `sbx` or `sbx.exe` on PATH is hidden in every mode.
- The stubs are `internal/testutil`'s, the same `testutil.WSLStubs` uses; the Windows side is listed once in `internal/testutil/wsl-stubs`. Add a new stub there.
- Everything lives in `/tmp/hq-stub-<id>`: the binary (`bin/hq`), a neutral `HOME` with the preferences, the stub sbx's state (`sbx/`, `SBX_STUB_DIR`), `C:\` at `win/mnt/c`, the fake claude, and a git repository `repo` to start agents in. Run QA from that repository so screenshots carry no user's path; PATH directories under your HOME are reached through links there too.
- `--before` builds `LOCAL_DEV_BEFORE` (default `origin/dev`; fetch it first) to the same path as the checkout's build, so the before and after binaries are one program to what registered it (`hq mcp install` says "already set up"). Builds are renamed into place: no "Text file busy".
- The tmux server is still this checkout's. It remembers the mode that started it, and a run in another mode (or without `--stub`) is refused until `--kill-server`.
- End the private tmux server with `--kill-server` when done.
- QA evidence needs terminal screenshots in every PR (`docs/agents/qa-evidence.md`): render a pane with `scripts/qa-screenshot.sh -L SOCKET TARGET OUT.png`.
