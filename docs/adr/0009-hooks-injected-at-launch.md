# 0009 - hooks are injected at launch; state lives in the repository's `.git`

Agent state must leave the sandbox through the mounted repository (ADR 0001), and the owner wants no per-repository setup. hq therefore passes everything an agent needs when it starts Claude: `--settings` carries the environment (`HQ_AGENT`, `HQ_ID`, a fresh id per agent) and the hook set. Each hook is an inline shell command that copies Claude's raw event payload into `<repo>/.git/hq/agents/<id>`, replacing the file in one step; hq on the host interprets it. Files inside `.git` are never tracked, so the repository needs no ignore rule, no hook files and no commit. This replaced the spec's `hq init` (removed from the spec with this decision).

## Considered Options

- **`hq init` committing a hook set into `.claude/` plus an ignore rule** (the spec's original design, and the `notify.sh` hook in support-chatbot): works for the whole team, but every repository must be prepared and changes to the hooks must be committed repository by repository.
- **Passing the identity with `sbx run -e`**: works per session, but the variables are also baked into the sandbox when that run creates it, so a repository's sandbox would carry the first agent's id; `--settings` keeps identity per Claude process.
- **Keying state by Claude's session id**: breaks on `/clear`, which starts a new session id in the same process.
- **One shared state file per repository**: concurrent agents in one sandbox race on it.
- **Interpreting events inside the hook** (as `notify.sh` does with python): needs tools inside the sandbox that are not guaranteed; the logic is better tested on the host.

## Consequences

- The sandbox needs only `sh`, `git`, `cat`, `mv`, `cp`, `mkdir`, `rm`, `grep` and `awk` for hooks to work.
- The script travels once, in the environment `--settings` sets, and each hook evaluates it (#71): with a copy per event, seven events made the settings longer than tmux accepts for the command that starts the agent.
- Some of what Claude does fires no hook: a turn the user ends (a dialog cancelled, a turn interrupted with Esc, a permission refused) ends silently. hq reads those cases from the agent's screen (design §3.4); the hooks remain the state channel.
- hq's hooks must coexist with a repository's own hooks; verified at build time.
