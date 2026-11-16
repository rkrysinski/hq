# Integration review: this project's surfaces

Read by the integration reviewer, who reads each ticket's diff against the release branch as it stands and once more over the whole release, and by `/release-ship` to classify a hotfix merged into the release branch. The review's questions, the *block* and *note* tags and where findings go are the skill's; this file holds the lists only the project knows. They grow from use: a hotfix path that cost a full Release QA is added afterwards.

## Shared surfaces

Files, keys, prompts, migrations or manifests more than one ticket may touch, and what *coherent* means for each: the condition two changes to the surface must satisfy together, beyond merging without conflict.

| Surface | Coherent means |
| --- | --- |
| The requirements spec, `docs/requirements/spec.md` | Every scenario, command and acceptance line agrees with every other: no behaviour is stated twice in two ways, the section and scenario numbers other documents cite still point at what they meant, and it says WHAT, never HOW (`docs/hq/agents.md`, *Rules*) |
| The design document, `docs/design/hq.md` | It describes the code as merged: each mechanic in one place, no two changes claiming the same section for different designs, and the § numbers the code, the tests and the docs cite still point at the right section |
| The glossary, `GLOSSARY.md` | One entry per term and one term per concept; no entry's term is another's *Avoid* word; the code, the docs and the issues of both changes use its terms |
| The packages for agent state, `internal/state`, `internal/agent` and `internal/journal` | One set of agent states and one attention order: a state or a transition added by one change is handled by the other change's code (the list, `hq wait`, the dashboard, the notifications), and what the hook reports and how hq keeps it (the event files, the journal, the agent's record) keep one form: a change to either raises the generation (ADR 0020), which the fingerprint test of `internal/version` holds |
| The sandbox client, `internal/sbx` | One way to run and parse `sbx`: both changes go through the same calls, and what one changes in how a sandbox is named, created or found holds for the other's callers |
| The command line, `internal/cli` and `cmd/hq` | One usage text and one set of exit codes: every command in `hq help` exists and the other way round, a flag means the same in every command that takes it, and the codes are those `hq help` lists |
| The platform code and the dashboard, `internal/platform` and `internal/dash` | Both platforms behave the same after both changes: what one adds for one platform the other platform has, platform names stay in `internal/platform` (`go run ./tools/testgate platform`), and the dashboard's keys, views and columns do not collide |
| The MCP tool descriptions, `internal/cli/mcp.go` | Every tool's description matches what the tool does after both changes, and the descriptions do not contradict one another (in particular on `since`, `wait` and when to use `send` or `now`) |
| The test gates, `tools/testgate` | A threshold only goes up, in the pull request that moves the figure; two changes that each fit the margin must fit it together |
