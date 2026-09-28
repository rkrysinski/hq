# hq

One console for many Claude Code agents. Each agent runs in a Docker Sandboxes (`sbx`) microVM and a tmux session; hq shows all of them in one dashboard, sorted by who needs you, and lets you enter, start, stop and inspect any of them from the dashboard or from any shell.

Platforms: macOS (iTerm2) and Windows via WSL (Windows Terminal).

## Install

Prerequisites: git, tmux 3.4 or newer, Docker Sandboxes (`sbx`), GitHub CLI (`gh`, logged in), VS Code with `code` on PATH; iTerm2 on macOS, Windows Terminal and WSL (Ubuntu 24.04 or newer) on Windows.

```bash
gh release download -R rkrysinski/hq -p install.sh -O - | sh
```

This installs `hq` to `~/.local/bin` and checks the prerequisites. On macOS with iTerm2 it also adds an iTerm2 profile named `hq` with Option as Esc+, which hq gives only to the tab the dashboard runs in, so the Alt chords work with no setup. `hq update` moves to the latest release later; `hq --version` says when one is out.

## Claude Desktop as supervisor

Claude Desktop can start, watch and message your agents through hq's MCP tools (`new`, `list`, `read`, `wait`, `send`, `go`, `kill`). Set it up once, from the shell you run hq in (on Windows, in WSL):

```bash
hq mcp install
```

Then quit and reopen Claude Desktop. This adds an `hq` entry to Claude Desktop's configuration (`~/Library/Application Support/Claude/claude_desktop_config.json` on macOS, `%APPDATA%\Claude\claude_desktop_config.json` on Windows, where Claude Desktop starts hq through `wsl.exe`), keeping your other servers, with a copy of the file as it was next to it. It records hq's path and your `PATH`, so run it again after moving hq or changing where tmux, `sbx` or `gh` live; a second run with nothing changed does nothing. When the file cannot be edited safely it changes nothing and prints the entry to add by hand.

In a conversation, tell Claude what to do ("start 42 in ~/work/app on issue #42, and watch it"). It reads status from hq rather than asking the agents, asks an agent with a message answered when the agent stops, and leaves an agent's permission prompts and questions to you. Claude Desktop asks you before `kill`.

To remove hq, delete `~/.local/bin/hq`, the `hq` entry under `mcpServers` in Claude Desktop's configuration if you added it, `~/.config/hq` and, on macOS, `~/Library/Application Support/iTerm2/DynamicProfiles/hq.json`.

## Stack

Go (ADR 0011); see [Design](docs/design/hq.md), [Testing](docs/agents/testing.md) and `scripts/local-dev.sh` for running a checkout.

## Releases

Work lands on `dev`, the default branch; `main` holds the latest release. `scripts/cut-release.sh X.Y.Z` tags a version from `dev` and fast-forwards `main` to it; the release notes are written from the merged pull requests. See [Releasing](docs/releasing.md).

## Documentation

| Where | What |
|-------|------|
| [AGENTS.md](AGENTS.md) | How agents work in this repository (`CLAUDE.md` is a symlink to it) |
| [CONTEXT.md](CONTEXT.md) | The glossary: the domain's terms and the words to avoid |
| [docs/requirements/spec.md](docs/requirements/spec.md) | What hq does; milestones in section 12 |
| [docs/requirements/](docs/requirements/) | The dashboard design, `look-and-feel.pen` (Pencil), one frame per scenario |
| [docs/adr/](docs/adr/) | Decisions that are hard to reverse, and why they were made |
| [docs/design/](docs/design/) | How hq is built |
| [docs/agents/](docs/agents/) | Issue tracker, triage labels, testing, QA evidence |
| [docs/releasing.md](docs/releasing.md) | Versions, release notes, milestones |

`scripts/setup-github.sh` prepares the GitHub repository (labels, the `qa-artifacts` branch, settings); it is safe to run again.
