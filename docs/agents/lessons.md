# Lessons

Non-obvious things learned while working in this repository that should change how the next piece of work is done. Read this file before starting work on an issue.

## What goes here

Before adding an entry, ask: will it help someone make a future decision or avoid rediscovering something, and is it missing from the code, the docs, the ADRs and this file? If not, write nothing.

- **Yes**: a constraint the code does not show, a corrected assumption, an approach that failed and why, a tool or environment behaviour that cost time.
- **No**: progress, change summaries, test counts, what a pull request did. Those live in the pull request and in CI.

One home per fact:

- a finding about one issue: a comment on that issue;
- a decision between alternatives: an ADR in `docs/adr/`;
- a domain term: `GLOSSARY.md`;
- a lesson that applies across issues: here.

An entry the new lesson refines is updated, not repeated. An entry that stops being true is deleted.

The usual source of entries is `/retro`, run after a build in the session that did it: it proposes changes to the agent's environment, and the judgement calls among them land here (mechanical ones become checks instead).

## Format

Newest first. Each entry has a date, a short title, the lesson and what to do next time, in two to four lines:

```markdown
### 2026-10-02 - Short title
What was learned and why it matters. Next time: what to do differently.
```

## Entries

### 2026-11-20 - An existing screen rule applied to a new state is a new screen rule
For #217 the rule that ends a turn Claude owed (`TurnNotTaken`, the screen at rest for 2 s) was applied to an agent waiting for its first prompt, on the reviewer's suggestion and with no capture of that screen: a first prompt that was only late gave an empty `done`. It cost a reverted commit, a review pass, an operator decision and #220. Next time: a rule read off the screen, new or reused for another state, waits for a real capture of that state (`tools/capture/README.md`), whoever suggests it; without one, ask for the operator's decision (`Approval needed` in a release) and write no code.

### 2026-11-20 - Review, mutate and bring in the base before the runs you make once
In release v0.15.0 the review's and the mutation gate's fixes made full builds and load runs again (#211, #232, #233); a fix that replaced the mechanism went out unreviewed (#234), and a base that moved voided load runs (#242). Next time: once the unit tests pass, run the review and fix what it finds, again on the new diff when a fix replaces the mechanism; bring in a base that moved (`git fetch origin <base>`, `git merge-base --is-ancestor origin/<base> HEAD`); then the mutation gate (`docs/hq/testing.md`, *Mutation tests*), and only then the full build and the once-only runs, on that head.

### 2026-11-20 - Read the rules the release's other pull requests add before your own review
In release v0.15.0 the integration review of #225 blocked on a rule (pass a capture through `ScreenMayTell` before `Sees`) that #232 added to `docs/hq/testing.md` after #225's own review: a review round, a fix and one more full build. The integration review holds a ticket to the rules on the release branch when it reads the diff, not to those on its own base. Next time: before your own review of a release ticket, read what the release's other pull requests, open or merged since your branch was cut, add to `docs/hq/testing.md` and `CODING_STANDARDS.md`, and hold your diff to it.

### 2026-11-20 - Grep a guarantee's wording before you qualify it
"Exactly one notification" stood in the spec (§5 and its acceptance line), in three places of the design and in the README; #224 qualified §5, and the integration review blocked on three more copies. Next time: before adding an exception to a guarantee, `git grep` its wording ("exactly one", "once", "every") across the spec, the design, the README and the glossary, and change every copy in the same pull request.

### 2026-11-17 - A mutant you call equivalent: write the killing test first
In release v0.14.0 three reasons of equivalence hid a change of behaviour: `send --wait` stalled one look later at exactly 5 s (merged, fixed forward in #214), a quiet look made one more `list-windows` call (overturned in #221's review), and a mutant "equivalent in effect" typed the message in after the timeout (overturned in #216). A fourth survivor was handed to the whole release's run, and the supervisor sent it back. "One look later" or "one call more" feels harmless while the reason is written. Next time: write the test that would kill the survivor before writing any reason, and leave no survivor to another run (`docs/hq/testing.md`, *What passes*).

### 2026-11-17 - A 401 from an agent you started: stop
A 401 or "Login expired" from an agent you started means the real Claude Code ran without the sandbox's proxy variables, and its first request logged out every session of the sandbox; each new start logs them out again (three times in release v0.14.0, #227). Next time: stop and report it, and start nothing again.
