# Coding standards

The rules for this project's code that take judgement. `mattpocock-skills:code-review` checks every change against them.

- Identifiers (names of packages, types, functions, variables) are in English. Domain terms are the ones [GLOSSARY.md](GLOSSARY.md) settles.
- Code that tells the platforms apart lives in `internal/platform` only ([docs/hq/agents.md](docs/hq/agents.md), *Rules*).

Everything mechanical (formatting, vet, the platform names) is enforced by `scripts/check.sh`, not written here.
