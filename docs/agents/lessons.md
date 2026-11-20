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

### 2026-11-17 - A mutant you call equivalent: write the killing test first
In release v0.14.0 three reasons of equivalence hid a change of behaviour: `send --wait` stalled one look later at exactly 5 s (merged, fixed forward in #214), a quiet look made one more `list-windows` call (overturned in #221's review), and a mutant "equivalent in effect" typed the message in after the timeout (overturned in #216). A fourth survivor was handed to the whole release's run, and the supervisor sent it back. "One look later" or "one call more" feels harmless while the reason is written. Next time: write the test that would kill the survivor before writing any reason, and leave no survivor to another run (`docs/hq/testing.md`, *What passes*).

### 2026-11-17 - A 401 from an agent you started: stop
A 401 or "Login expired" from an agent you started means the real Claude Code ran without the sandbox's proxy variables, and its first request logged out every session of the sandbox; each new start logs them out again (three times in release v0.14.0, #227). Next time: stop and report it, and start nothing again.
