# Coding standards

The rules for this project's code that take judgement. `mattpocock-skills:code-review` checks every change against them.

- Identifiers (names of packages, types, functions, variables) are in English. Domain terms are the ones [GLOSSARY.md](GLOSSARY.md) settles.
- Code that tells the platforms apart lives in `internal/platform` only ([docs/hq/agents.md](docs/hq/agents.md), *Rules*).
- A message or a document says what the code counts and where each figure comes from, and a figure measured on one change is written as that change's figure (which change, under which load), not as a rule: `after %d runs` that counts records, or one change's measurement stated for every change, promises more than the code counted or anyone measured.
- A test that waits for something, in a loop or in a call that blocks until another process acts (opening a FIFO, reading a pipe, waiting for a process to end), bounds the wait and fails past the bound, saying what did not happen: an unbounded wait hangs the run and times out mutants instead of failing.
- A test whose result rests on one file being newer than another (`-nt`, a modification time) writes the newer one in a later second, waiting, bounded, for the older one and then until the clock is past that file's second (`pastItsCopysSecond` in `internal/state/answer_integration_test.go`): the tests run the hook under the host's `sh`, and on macOS that compares `-nt` by whole seconds, so two writes in one second pass on Ubuntu and fail there (design 3.4, *Where the hook runs*).
- A test that runs under a fake, a shim or a slowed tool (`tools/fakeclaude`, a fake `go`, a wrapper put first in `PATH`) asserts that it took hold, for example by counting the runs it served or slowed (`slowGo` in `tools/testgate/mutation_integration_test.go`): a test whose fake never ran passes while testing nothing.
- The cases of a table test share no state: each case builds what it uses, so a case passes or fails the same alone, in another order or after a failing one.

Everything mechanical (formatting, vet, the platform names) is enforced by `scripts/check.sh`, not written here.
