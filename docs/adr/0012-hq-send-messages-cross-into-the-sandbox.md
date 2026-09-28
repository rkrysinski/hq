# 0012 - hq send: messages cross into the sandbox, and a Stop hook may hold the agent back

`hq send NAME TEXT` gives an agent feedback that it takes into account when it is ready, never pulling it away from its work just to report (#132, #135). Two rules of design §7 held until now: the hook never gets in the way of Claude (7.1), and nothing crosses the sandbox boundary from the host beyond what `sbx run` receives at launch (7.3). This decision makes one exception to each. The host leaves the message in the agent's inbox, `.git/hq/inbox/ID` beside its state file, already encoded as the inside of a JSON string; the injected hook (ADR 0009), which keeps its tool set, only moves it out of the inbox and copies it into its output: on Stop as `decision: block` with the messages as the reason, so Claude goes on with them and stops again; with the next prompt as UserPromptSubmit `additionalContext`; with `--now` after Claude's next tool call as PostToolUse `additionalContext`. An agent that waits at its empty prompt box gets the message typed in by hq as its next prompt, a bracketed paste and Enter through tmux. Verified with Claude Code 2.1.283 in sbx (#135): a blocked Stop continues the turn with the reason and fires Stop again with `stop_hook_active`; both `additionalContext`s reach Claude; the blocked stop's hook prints no notification.

## Considered Options

- **Type every message in through tmux**: needs the prompt box, so it cannot reach a working agent without interrupting it, and keys sent while a dialog opens would answer it.
- **Deliver only when the agent stops (Stop hook alone)**: a long turn gets the correction only at its end; `--now` exists for that.
- **The hook reads and encodes the message itself**: needs a JSON encoder in the sandbox that ADR 0009's tool set does not guarantee; the host encodes it once instead, and the hook copies bytes.
- **A lock file around the inbox**: renaming a message out of the inbox is atomic, so whoever renames it first, a hook or `hq send`, delivers it, and no lock is needed.

## Consequences

- Text from the host reaches Claude: only what the user typed into `hq send`, as a prompt the user could have typed into the session, stripped of escape sequences and control characters but newlines and tabs, and at most 8 KiB. The hook never evaluates it or uses it as a format.
- A Stop hook may hold an agent back, only while a message is unread: each block takes the messages it delivers, so a stop is blocked once per batch and never loops. It blocks again, although `stop_hook_active` is set, when another message came in while Claude answered the first.
- A blocked stop is not a turn's end: it is not reported (the agent stays `working`) and sends no notification; the stop after it is reported and notifies once (spec §5).
- A message never answers a dialog: it reaches Claude only as hook output, or as a prompt typed into the empty prompt box, and hq types nothing while a dialog is open; the message goes once the dialog is closed.
- Agents launched by an older hq carry hooks that ignore the inbox; `hq send` refuses them with the remedy (a relaunch), and hq marks the agents whose hooks deliver (`@hq_inbox`).
- A message waiting for an agent that is killed goes with it (`hq kill`, `hq stop`, the Kill dialog); one waiting across `hq sandbox restart` is delivered by the relaunched agent.
