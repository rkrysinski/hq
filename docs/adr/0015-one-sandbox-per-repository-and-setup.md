# 0015 - One sandbox per repository and setup

Changes the given of spec §2, "one sandbox per repository, shared by all agents of that repository" ([ADR 0001](0001-tmux-and-sbx-are-givens.md) keeps sbx a given), to "one sandbox per repository and setup" (PRD #134, #135).

The user wants to choose per agent which model it talks to: this one on a model served on the host (LM Studio, Ollama, any Anthropic-wire endpoint), that one on Claude, in the same repository, at the same time, with no session ended by the choice. A **setup** is a named configuration a sandbox is created with (an image, MCP servers, a model and its provider); the `sandbox` object of the preferences file is the `default` setup, others are named under `sandboxes`. A repository gets one sandbox per setup it is used with, created on the first `hq new --setup` that asks for it, so using another setup means using another sandbox: nothing is recreated and no session ends.

The model goes to sbx's own experimental model feature: `--model M --provider P`, given only when hq creates the sandbox (with `sbx run --detached`, since `sbx create` takes neither), never when attaching, where `--model` recreates the container. sbx registers the provider, runs its host-side proxy (`llmman`), puts Claude Code's variables into the sandbox and handles the network path. hq passes the values on and checks none of them: sbx refuses what it cannot do, and hq names the setting and the file it came from.

The default setup's sandbox keeps sbx's own name, so every sandbox that exists today, hq's or made by hand, is the default without migration. Another setup's sandbox is named `hq-<setup>-<dir>`. `sbx ls` carries no labels and no model, so the name is the only marker sbx keeps; hq finds a repository's sandboxes by workspace path as before and tells them apart by the reserved `hq-` prefix: the default is the first on the workspace whose name does not start with it, a setup's the one named exactly for it.

## Considered Options

- **One sandbox per repository, its model switched** (`sbx run --model` on the existing sandbox): recreates the container, so every running session of the repository ends, and the repository has one model at a time. It is the very interruption the user wants to avoid.
- **Claude Code's variables per session** (`sbx run -e ANTHROPIC_BASE_URL=... -e ANTHROPIC_MODEL=...`): per agent and verified working in the spike, but the sandbox proxy only reaches the host's `localhost` after `sbx policy allow network --sandbox X localhost`, a widening of the user's policy hq would make silently for every sandbox, and hq would hardcode Claude Code's provider contract (its variable names), which belongs to Claude Code and sbx.
- **A suffix (`<sbx name>-<setup>`)** instead of the `hq-` prefix: repository `foo-local`'s default sandbox `claude-foo-local` would read as setup `local` of repository `foo`, clash with it, and the sandbox of a setup removed from the preferences would pass for a default.
- **A registry file** of which sandbox belongs to which setup: it could drift from what sbx has (a sandbox removed or created by hand); the name cannot.

## Consequences

- A repository may have several sandboxes, one per setup; S11's "same sandbox" holds per setup. The default lookup never takes an `hq-` sandbox, including one whose setup left the preferences; its rows still name that setup.
- A conversation stays in the sandbox it started in and cannot be resumed under another setup.
- A setup applies only at creation, as the template always did: a changed setup takes effect once its sandbox is removed and created again.
- sbx's limits reach the user as sbx's errors: a name over 63 characters, and two repositories with the same directory name on one setup (as sbx already refuses for default names).
- `setup` is read off the sandbox's name for the row's JSON: what stands between `hq-` and `-<dir>`, `default` for any other name. A sandbox made by hand under a name starting with `hq-` is never the default.
- The user's one-time work is sbx's: enable the model feature (`sbx settings set feature.model true`), register the provider (`model.providers`), load the model. hq starts and checks none of it, on macOS and on Windows alike.
