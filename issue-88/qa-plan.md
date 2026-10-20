# QA plan for #88

Checks (each against hq from the worktree, the stub sbx running the real claude, agent at 80x24):

1. Reproduce before the fix: story turn, tock prompt queued at +6 s, Esc at +8 s. Expect (bug): hq ls `done / Interrupted` from the Esc to turn 2's Stop; hq wait returns at the Esc. Evidence: timeline, hq ls polls, screens.
2. After the fix, the same scenario: hq ls `working` within one poll of the dequeue (measured gap stated) until turn 2's Stop, then `done` with turn 2's message.
3. After the fix: hq wait returns at turn 2's Stop, not at the Esc.
4. The same with a queued turn that runs no tool (`Reply with only the word QUEUED.`): working from the dequeue, done at its Stop with `QUEUED`.
5. Both paths: a poll that lands while the `Interrupted` line shows and one that does not: the hand-over rule reads the Interrupted line with Claude at work below it; the screens captured at 400 ms show the line for about 10 s, so every run has polls on it; the hook's hand-over at turn 2's first tool end covers a look that never saw it (integration test).
6. Esc with nothing queued: `done / Interrupted` as before (before and after).
7. Esc twice (the queued turn interrupted at once): `done / Interrupted`.
8. Existing tests: early-Esc rewind (#77), #82 hand-over at Stop plain and /clear-queued: `go test -tags integration ./...` green.
9. testgate pyramid before/after; CI green.

Tests added:
- unit (state): EndedByUser needs Claude idle below the line (new fixtures); AtWorkAfterEnd; taken (queued vs stop prompt_id).
- unit (agent): settle hands over to the queued prompt Claude took after an Esc; not without a queued prompt, not while the prompt is still queued, not with a nil taker; a woken agent is left alone.
- integration (state): the hook hands over at the first tool end of a third turn with a prompt still queued; TakeQueued makes the state file the queued prompt, dated now, and drops .on and .acted; refuses a link.
- integration (agent): Look with state.Read and state.TakeQueued on real files hands over once.
- e2e: none.
