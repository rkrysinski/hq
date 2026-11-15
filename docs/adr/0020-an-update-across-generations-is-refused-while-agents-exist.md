# 0020 - An update across generations is refused while agents exist

Part of #165 (#200). An agent carries the hook hq injected when it started (ADR 0009), and hq keeps what the agent reports in a form of its own. Every change to either used to add an exception to the requirements and the design: what an agent started by an older hq "keeps doing until it is relaunched". Nobody can keep that list in their head, and each hook change made it longer.

Now every release of hq carries a **generation** (`GLOSSARY.md`): a number compiled into hq that changes exactly when what agents report, or the form in which hq keeps it, changes. An hq reads the agents of its own generation and no other: one of another generation that it meets all the same is an outdated agent, which it lists and lets the user open or kill, nothing more. So an update must not leave agents in between:

- Each release publishes its generation in the file `GENERATION`, beside the binaries and listed in `SHA256SUMS` like them. A release without it (any from before this change) counts as another generation.
- `hq update` reads the latest release's generation before it downloads the binary. Within its own generation it updates as before, with agents running. Across generations it lists every agent hq knows: the agents of its tmux server in any state, `ended` ones included, and the lost agents of every repository hq knows, with the agents those repositories hold records of on another tmux server. With any of them, or when it cannot tell (sbx fails, does not answer within its time limit or is too old, or tmux fails), it refuses with exit code 1 and one line that names the release, the reason, the remedy (`hq stop`, `hq kill NAME`) and each agent with its state, or the reason it cannot tell. Nothing is replaced. A development build compares generations the same way.
- The install script asks an hq it would replace the same question, through the hidden `hq __may-update TAG GENERATION`, and refuses with its line and exit code 1. An installed hq from before this change does not know `hq __generation`, which tells it apart; it is asked for its list of agents (`hq ls --json`) instead, and any agent in it, or a list it cannot give, refuses the install. A first install asks nothing.
- There is no option that updates anyway, in `hq update` or in the install script.
- A unit test pins a fingerprint of each form the generation stands for (the hook set and script hq injects, hq's record of an agent, and the journal's form: its events and the reply kept beside them, #193) beside the generation it was pinned at, and fails until a change to one of them is pinned again. Raising the generation is pinning it again with the new number; a renewal that keeps the number says in the pull request why what agents report is unchanged.

## Considered Options

- **Relaunch the agents after the update**: it continues each conversation under a new hook, but an agent in the middle of a turn loses it, and a relaunch is a failure of its own to explain; the user decides when an agent ends.
- **An option that updates anyway**: the outdated agents it leaves are exactly what the rule is there to avoid, and "never in between" would again depend on remembering; with no option the rule holds by itself.
- **Count only running agents**: an `ended` agent still listed and a lost agent are relaunched by hq (`hq sandbox restart`, `hq restore`), which the new hq could not do with what the old one kept.
- **Let an update through when sbx cannot be asked**: lost agents are found in the repositories sbx names, so without sbx's answer hq does not know they are not there; refusing costs a retry, letting it through can cost the agents.
- **Compare versions instead of a generation**: most releases change neither the hook nor the form; a version rule would refuse every update while agents run, a generation only the few that need it.
- **A generation per form** (one for the hook, one for the journal): two numbers to compare and to remember, for no case where one changes and an hq could still read the agent.

## Consequences

- The release that brings this change cannot be refused: the hq installed before it replaces itself without asking. Its notes tell the user to end every agent first; agents left running show as outdated, to kill and start again.
- The requirements and the design say "an agent of another generation is outdated" once, instead of a list of what agents of older hooks keep doing.
- A change to the hook or to the journal's form is a decision in its pull request, made visible by the failing fingerprint test.
- `hq update` and the install script cost one small download more (`GENERATION`), and across generations a look at tmux, sbx and the records of the known repositories.
