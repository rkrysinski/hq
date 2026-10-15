# QA plan for #66 (written before the change)

Claude Code 2.1.300 of this machine, started by the repository's stub `sbx` in place of the fake Claude, with a configuration directory of its own (a copy of this machine's login); no real sandbox. The agent `a` has finished one turn ("hi") and is `done`.

| # | Check | Steps | Expected |
|---|-------|-------|----------|
| 0 | Reproduce on `dev` | `hq send a '/effort max'`; `hq ls`, `hq read a`, `hq read a --json`, `hq send a 'say hi'`; dashboard | as reported: `done`, LAST `hi`, `asks: null`, "has something in its prompt box", attention view "nothing needs you" |
| 1 | The agent needs input | the same on this branch: `hq ls`, `hq read a`, `--json` | `needs input`, LAST `Change effort level?`; `hq read` has the question, both options and the text; `asks` in JSON |
| 2 | A message sent meanwhile | `hq send a 'say hi'` | queued, saying it waits at a dialog of Claude's and goes with the next prompt once answered |
| 3 | `hq wait` tells | `hq wait a --since <a moment before the dialog>` while the dialog is up | returns `a` `needs input` |
| 4 | Dashboard, and answering | `hq`; the row; answer "No" in the docked session | amber `needs input` in the attention view; after the answer `done` with LAST `hi` |
| 5 | A dialog opened from inside the session | type `/effort max` in the docked session | `needs input` again |
| 6 | Nothing else changes | unit suite; integration tests of state, agent, cli, cmd/hq; e2e; `testgate` | pass |

Tests planned - unit: the dialog read off the real captures (160 and 80 columns, the `/model` picker) and off no other captured screen; a `done` or `question` agent's screen with a dialog needs input from the moment hq first saw it, the record kept on its window; `hq read`, `hq send`, `hq wait`. Integration: an agent of the fake Claude that opens the effort dialog after its turn needs input, a message waits, `hq wait` returns it, the answer makes it done. End-to-end: none.
