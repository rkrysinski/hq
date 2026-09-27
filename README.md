# hq

One console for many Claude Code agents. Each agent runs in a Docker Sandboxes (`sbx`) microVM and a tmux session; hq shows all of them in one dashboard, sorted by who needs you, and lets you enter, start, stop and inspect any of them from the dashboard or from any shell.

Platforms: macOS (iTerm2) and Windows via WSL (Windows Terminal).

## Install

Prerequisites: git, tmux 3.4 or newer, Docker Sandboxes (`sbx`), GitHub CLI (`gh`, logged in), VS Code with `code` on PATH; iTerm2 on macOS, Windows Terminal and WSL (Ubuntu 24.04 or newer) on Windows.

```bash
gh release download -R rkrysinski/hq -p install.sh -O - | sh
```

This installs `hq` to `~/.local/bin` and checks the prerequisites. `hq update` moves to the latest release later; `hq --version` says when one is out.

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
| [docs/requirements/](docs/requirements/) | The dashboard design, `look-and-feel.pen`, and its exports `dash*.png` |
| [docs/adr/](docs/adr/) | Decisions that are hard to reverse, and why they were made |
| [docs/design/](docs/design/) | How hq is built |
| [docs/agents/](docs/agents/) | Issue tracker, triage labels, testing, QA evidence |
| [docs/releasing.md](docs/releasing.md) | Versions, release notes, milestones |

`scripts/setup-github.sh` prepares the GitHub repository (labels, the `qa-artifacts` branch, settings); it is safe to run again.
