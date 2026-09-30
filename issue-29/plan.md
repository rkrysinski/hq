# QA plan for #29 (written before the change)

| # | Check | Steps | Expected |
|---|-------|-------|----------|
| 0 | Reproduce with the real Claude Code | Claude Code 2.1.285 with a fresh configuration whose user settings say `bypassPermissions`; `hq new a DIR` | the dialog shows; hq (dev) says `done`, nothing asked, a message queues "has something in its prompt box" |
| 1 | The agent needs input | the same on this branch: `hq ls`, `hq read a` | `needs input`, the dialog's title as last message; `hq read` has the question and both options |
| 2 | A message sent meanwhile | `hq send a 'say hi'` | queued, saying the dialog must be answered and that it goes with the next prompt |
| 3 | The dashboard, and answering it | `hq`; answer "No" in the docked session | the row amber `needs input`; after the answer `done` |
| 4 | Nothing else changes | unit suite; integration tests of state, agent, cli, cmd/hq; e2e | pass |

Tests planned - unit: the dialog read off real captures, and off every other captured screen not; the agent's state from a start report and a screen; `hq read` and `hq ls`. Integration: an agent of the fake Claude with the dialog needs input, a message waits, the answer makes it done. End-to-end: none.
