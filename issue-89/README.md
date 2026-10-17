# Evidence for #89

- `runs/<run>/` - one directory per case: `events.log` (one clock: `hook` = a hook's process start, `act-*` = the holding hook's start / answer printed / TERM, `KEY` = a key sent, `DROP` = an answer file dropped, `SHOT` = a screen shot), `log/` (every hook payload as Claude sent it, named by its time), `shots/` (`capture-pane -p` and `-e`). Runs `P*`, `C*` and `T640` are setup F (the sandbox without its template), `E*` setup E (the sbx template). In `events.log` a fraction with fewer than 9 digits lost its leading zeros (`.69000052` is `.069000052`).
- `harness/` - the hooks (`log.sh`, `act.sh`, `approve.sh` the stand-in for the template's `claude-approve`), the `--settings` files, the wrapper hiding `/etc/claude-code` (`claude`), the helpers.
- `png/` - screenshots of the key screens, rendered from the styled shots with `scripts/qa-screenshot.sh`.
- `screen-fs-ask-review.txt` - AskUserQuestion's review step (E), not committed as a fixture: `state.OwnDialog` reads it as a dialog of Claude's own.
