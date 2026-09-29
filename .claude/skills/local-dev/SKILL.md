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
- `sbx` is real: `hq new` starts Claude in the repository's real sandbox. Prefer this repository's own sandbox for QA, keep prompts short, and `kill` every agent you started before finishing.
- End the private tmux server with `--kill-server` when done.
- QA evidence needs terminal screenshots in every PR (`docs/agents/qa-evidence.md`): render a pane with `scripts/qa-screenshot.sh -L SOCKET TARGET OUT.png`.
