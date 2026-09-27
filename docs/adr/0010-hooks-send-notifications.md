# 0010 - the agent's hook sends the desktop notification

Entering `question`, `needs input` or `done` must produce exactly one desktop notification, docked or not (spec §5, S4). The injected hook (ADR 0009) sends it: on Stop, on a dialog opening and on the Notification event it returns a `terminalSequence` with the notification escape, Claude writes it to its pane, and tmux passes it to the terminal (`allow-passthrough all`, which also covers panes in hidden home windows). Each hook runs once per event, so exactly-once needs no bookkeeping, and notifications need no list program, only a terminal attached to the `hq` session. Since #67, `q` detaches the terminal (ADR 0007), so after `q` notifications stop until `hq` is run again. The hook applies the same `?` rule as the host (in `awk`) to the same payload, so a notification and its row always agree. The approach is proven by `notify.sh` in support-chatbot.

## Considered Options

- **The list program notifies on state changes**: needs de-duplication across restarts and polling gaps, and stops when the list is closed.
- **A Linux helper binary dropped into `.git/hq/` for the hook**: robust parsing, but a second binary to build, ship and update.

## Consequences

- ADR 0002 (no daemon) stands; its stated cost, "notifications for undocked agents exist only while the dashboard runs", no longer applies. Without any terminal attached to the `hq` session (after `q`, a detach, or a closed window), nothing is shown; the events are not replayed later.
- hq knows the platform when it starts Claude, so it bakes the terminal's notification sequence into the injected hook.
- One event can reach several hooks: a dialog fires PermissionRequest as it opens and Claude's Notification seconds later (#71). The hook notifies on the first and drops the second while the dialog it reported is open, which it tells from the state file, so exactly-once still needs no bookkeeping of its own.
- A turn the user ends with Esc fires no hook and so never notifies; spec §5 says it should not.
