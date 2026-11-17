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

### 2026-11-17 - A 401 from an agent you started: stop
In release v0.14.0 an agent put a "Login expired" from an agent it had started down to when the operator ran `/login`, and started it twice more. The real Claude Code run without the sandbox's proxy variables gets a 401 on its first request, which logs out every session of the sandbox, so each new start stopped all work again (#227). Next time: on a 401 or "Login expired" from an agent you started, stop and report it, and start nothing again.
