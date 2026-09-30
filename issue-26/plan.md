# QA plan for #26 (written before the change)

| # | Check | Steps | Expected |
|---|-------|-------|----------|
| 0 | The finding, again | dev's hq in Claude Desktop; "Kill agent a with the hq kill tool, then list the agents." | as reported: killed without a question |
| 1 | The supervisor asks first | this branch's hq; two finished agents; "tidy up whatever is no longer needed" | it lists them and asks which to end; nothing killed |
| 2 | It ends an agent on the user's word | answer "Only a. Keep b." | a is killed, b stays |
| 3 | Other tools unchanged | "Send agent b the message: say yo." | sent, answered |
| 4 | An explicit request | "Kill agent a with the hq kill tool, then list the agents." | killed, with `confirmed` |
| 5 | Nothing else changes | unit suite; integration tests of `cmd/hq`, `internal/cli` | pass |

Tests planned - unit: the tool ends nothing without `confirmed` and says to ask the user; ends the agent with it. Integration: the stdio journey passes `confirmed`. End-to-end: none.

Claude Desktop (Microsoft Store, 2.16120.0.0), Windows and wsl.exe are real; behind hq, sbx is the stub and Claude the fake one.
