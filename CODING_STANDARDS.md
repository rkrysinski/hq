# Coding standards

The rules for this project's code that take judgement. `mattpocock-skills:code-review` checks every change against them.

- Identifiers (names of packages, types, functions, variables) are in English. Domain terms are the ones [GLOSSARY.md](GLOSSARY.md) settles.
- Code that tells the platforms apart lives in `internal/platform` only ([docs/hq/agents.md](docs/hq/agents.md), *Rules*).
- A test that loops until something happens bounds its loop and fails past the bound, saying what did not happen: an unbounded loop hangs the run and times out mutants instead of failing.
- The cases of a table test share no state: each case builds what it uses, so a case passes or fails the same alone, in another order or after a failing one.

Everything mechanical (formatting, vet, the platform names) is enforced by `scripts/check.sh`, not written here.
