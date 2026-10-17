# Test fixtures: where they came from

`screen-*.txt` are a real Claude Code pane as `tmux capture-pane -p` prints it (`-p -e`, styles kept, for `screen-styled-*`), unedited unless its setup below says otherwise. `*.json` are hook payloads as Claude passes them on stdin.
A new capture is made with `tools/capture/` (its README says how) and gets a row here in the same change.

## Setups

- **A - 2.1.283, classic view.** Fable 5.1, Claude Max; no fullscreen hints, no status line; the footer offers `esc to interrupt` while a turn is at work; permission prompts are drawn (manual mode). The design's changelog says these behaviours were verified "with Claude Code 2.1.283 in sbx" (old #71, #72, #99, #135); cwd `/Users/romankrysinski/work/repositories/hq`. `screen-styled-*` come from a QA run of old #135 (cwd `/w/app`). `screen-declined-80` and `screen-interrupted-80` come from the v0.1.0 end-to-end test (`screen_test.go`); `screen-declined-80` has its blank lines left out. Whether hq or a person drove the panes: unknown.
- **B - 2.1.285, no sandbox.** Fable 5.1. WSL 1 on a Windows 11 ARM64 VM, tmux 3.4, the real Claude behind the repository's stub `sbx`, a configuration directory of its own, user settings `bypassPermissions` (PR #36). Edited: the working directory is renamed to `/home/dev/work/app` (`owndialog_test.go`; the originals on `qa-artifacts` `issue-29/` show `$S/cc-work`).
- **C - 2.1.291, no sandbox.** Sonnet 5.5, behind the stub `sbx` in a Linux arm64 sandbox, tmux 3.6, every hook logged (PR #39). `screen-interrupted-then-woken` has its blank lines left out.
- **D - 2.1.300, no sandbox.** Opus 5.5 (high effort). A Linux Docker sandbox, tmux 3.6, behind the stub `sbx`, a configuration directory of its own (PR #67). Replies are drawn in a `● Claude` card, as the `clean-view` mod draws them in E; which other settings applied is unknown. Edited: the cwd reads `$S/cc-work` in `screen-own-effort` and `screen-own-model` (the same on `qa-artifacts` `issue-66/`).
- **E - 2.1.301, sbx template, fullscreen.** Opus 5.5 (high effort). An sbx sandbox with the template's managed settings (`/etc/claude-code/managed-settings.json`, not overridable): `"tui": "fullscreen"` (`tmux detected` hint above the box), a status line below the box, mods `clean-view` and `tools-pane` (tool calls in cards with Polish labels), and a `PermissionRequest` hook that approves every request. Private tmux server, cwd a throwaway git repository, the parent session's `CLAUDE*` variables unset, `--permission-mode bypassPermissions`. Pane 100x30, narrow 40x30. Captured for #70; the capture names are in parentheses in the table.
  - **E' -** the same with `--permission-mode default` and `--settings '{"disableAllHooks":true}'`: mods off, stock tool view; the managed approve hook still answers, so no prompt is drawn.
  - **E'' -** the same Claude and managed settings run as an agent of hq's: hq from the checkout (`scripts/local-dev.sh`), the repository's stub `sbx` running the real `claude` (`SBX_STUB_CLAUDE`), hq's hooks injected, the agent's home window 80x24 (#77).
  - **E''' -** the same template on 2.1.302 with #89's hooks in `--settings` (as in F below): the template's own `claude-approve` `PermissionRequest` hook runs beside them.
- **F - 2.1.302, the sbx sandbox without its template.** Opus 5.5 (high effort). E's sandbox with `/etc/claude-code` hidden (an empty directory mounted over it in a user and mount namespace), so no managed settings: classic view, no status line, no mods, no auto-approve hook; `--permission-mode bypassPermissions`. Private tmux server, cwd a throwaway git repository, pane 100x30. Captured for #89, with its hooks in `--settings`: every event logged, and a `PermissionRequest` and a `PreToolUse` hook for AskUserQuestion and an `Elicitation` hook that hold until an answer file appears and print it as their decision.
- **G - 2.1.302, sbx template, fullscreen, as an agent of hq's.** Fable 5.1 (medium effort), Claude Max. E'' with the sandbox's Claude Code 2.1.302: the same managed settings (fullscreen, status line, mods, the approve hook), hq from the worktree of #88, the stub `sbx` running the real `claude`, the agent's home window 80x24, driven by a script (the QA runs of #88, `qa-artifacts` `issue-88/`), the pane shot every 400 ms with `capture-pane -p`. In 2.1.302 a prompt Claude takes from its queue after an Esc is drawn in one of two ways: with the interrupted turn's reply streaming, the queued line above the box goes and the queued turn follows the `Interrupted` line with no `❯` line; with the interrupted turn still thinking, the prompt is drawn sent as a `❯` line below the `Interrupted` line (as 2.1.301 drew it in the issue's run, `issue-82/repro-E1-screen-after-esc.txt`).

### Open

- Whether the production sandboxes run the fullscreen view is not known. The 2.1.301 template here does, and no working screen there shows `esc to interrupt`, so the check that Claude is at work relies on the spinner line (design §3.4, #70).
- No open permission prompt has been captured in the sbx template: its auto-approve hook answers every request. The only permission-prompt screens are A's, after the answer.

## Screen fixtures

Pane: `WxH` where a source states it; otherwise "at least" the widest line (display columns) and the line count of the file.

| fixture | Claude Code | setup | pane | shows | source |
|---|---|---|---|---|---|
| screen-compacting | 2.1.283 | A | 100x30 | `/compact` running: spinner `Compacting conversation…`, `esc to interrupt` | b058953 (old #99) |
| screen-declined | 2.1.283 | A | 100x30 | AskUserQuestion cancelled with Esc: `User declined to answer questions`, bare box | 39924e5 (old #71) |
| screen-declined-80 | 2.1.283 | A | 80 cols; 10 lines, blank lines left out | the same at 80 columns | 39924e5 (old #71) |
| screen-declined-then-working | 2.1.283 | A | 100x30 | after a declined dialog, the next prompt at work, `esc to interrupt` | 39924e5 (old #71) |
| screen-dialog-open | 2.1.283 | A | 100x30 | AskUserQuestion open (Red / Blue, Type something, Chat about this) | 39924e5 (old #71) |
| screen-interrupted | 2.1.283 | A | 100x30 | Esc on a turn at work: `Interrupted · What should Claude do instead?`, bare box | 32ba38c (old #72) |
| screen-interrupted-80 | 2.1.283 | A | 80 cols; 8 lines | the same at 80 columns | 32ba38c (old #72) |
| screen-interrupted-prompt-restored | 2.1.283 | A | 100x30 | Esc before the tool started: `Interrupted`, the prompt back in the box | 32ba38c (old #72) |
| screen-interrupted-then-working | 2.1.283 | A | 100x30 | after an interrupt, the prompt sent again and at work | 32ba38c (old #72) |
| screen-working | 2.1.283 | A | 100x30 | a turn at work (counting), `esc to interrupt` | 32ba38c (old #72) |
| screen-permission-esc | 2.1.283 | A | 100x30 | permission prompt closed with Esc (manual mode): `Interrupted` | 32ba38c (old #72) |
| screen-permission-no | 2.1.283 | A | 100x30 | permission prompt answered No (manual mode): `Interrupted` | 32ba38c (old #72) |
| screen-menu-while-working | 2.1.283 | A | 100x30 | turn at work with the `/co` slash menu open; no `esc to interrupt` | b058953 (old #99) |
| screen-narrow-streaming | 2.1.283 | A | 50x30 | reply streaming into a bare box, no spinner, no `esc to interrupt` | b058953 (old #99) |
| screen-narrow-working | 2.1.283 | A | 50x30 | spinner and tip; footer without `esc to interrupt` | b058953 (old #99) |
| screen-rewound | 2.1.283 | A | 100x30 | Esc early in a no-tool turn after an earlier one: prompt back in the box, no line | b058953 (old #99) |
| screen-rewound-first | 2.1.283 | A | 100x30 | the same as the session's first turn | b058953 (old #99) |
| screen-rewound-cleared | 2.1.283 | A | 100x30 | after a rewind, the box cleared (`Ctrl+Y to paste deleted text`) | b058953 (old #99) |
| screen-rewound-after-declined | 2.1.283 | A | at least 116 cols, 17 lines | rewind right after a cancelled dialog: `User declined…` above, another prompt in the box | 62a520a (old #99) |
| screen-streaming | 2.1.283 | A | 100x30 | reply streaming, `esc to interrupt` | b058953 (old #99) |
| screen-streaming-typing | 2.1.283 | A | 100x30 | reply streaming while the user types (manual mode); no `esc to interrupt` | b058953 (old #99) |
| screen-tool-running | 2.1.283 | A | 100x30 | Bash loop running, spinner, `esc to interrupt` | b058953 (old #99) |
| screen-typing-while-working | 2.1.283 | A | 100x30 | thinking while the user types (manual mode); no `esc to interrupt` | b058953 (old #99) |
| screen-stop-blocked | 2.1.283 | A | at least 120 cols, 20 lines | Stop hook delivered two inbox messages (`Stop hook error: Message 1 for you…`), turn over, bare box | 2248770 (old #135) |
| screen-styled-done | 2.1.283 | A, `-e` | at least 80 cols, 24 lines | turn over (`finished`), bare box | fb8bad9 (old #135) |
| screen-styled-start-placeholder | 2.1.283 | A, `-e` | at least 80 cols, 24 lines | new session, faint placeholder `Try "create a util logging.py that..."` | fb8bad9 (old #135) |
| screen-styled-working | 2.1.283 | A, `-e` | at least 80 cols, 24 lines | thinking spinner, `esc to interrupt` (footer cut with `…`) | fb8bad9 (old #135) |
| screen-own-auto-mode | 2.1.285 | B | at least 110 cols, 36 lines | start dialog "Make auto mode your default permission mode?" | 254c5ec (#29, PR #36) |
| screen-own-trust | 2.1.285 | B | at least 110 cols, 36 lines | start dialog "trust this folder" | 254c5ec (#29, PR #36) |
| screen-start-ready | 2.1.285 | B | at least 110 cols, 36 lines | session just started, mode pinned so no dialog, empty box | 254c5ec (#29, PR #36) |
| screen-background-three | 2.1.291 | C | at least 120 cols, 40 lines | three background subagents listed below the footer; a suggestion in the box | 7cb35f8 (#38, PR #39) |
| screen-styled-background | 2.1.291 | C, `-e` | at least 120 cols, 40 lines | one background subagent listed below the footer; faint suggestion in the box | 7cb35f8 (#38, PR #39) |
| screen-interrupted-then-woken | 2.1.291 | C | at least 146 cols; 13 lines, blank lines left out | turn interrupted while a subagent ran, then the turn Claude woke itself for | fc772c5 (#38 review, PR #39) |
| screen-own-effort | 2.1.300 | D | 160 cols (PR #67), 45 lines | "Change effort level?" after `/effort max`, under a `▔` rule | ffb9478 (#66, PR #67) |
| screen-own-effort-80 | 2.1.300 | D | 80 cols (PR #67), 24 lines | the same at 80 columns | ffb9478 (#66, PR #67) |
| screen-own-model | 2.1.300 | D | at least 160 cols, 45 lines | `/model` picker with scroll marks | ffb9478 (#66, PR #67) |
| screen-fs-ask-open | 2.1.301 | E | 100x30 | AskUserQuestion open (a1) | 04cc17a (#70, PR #72) |
| screen-fs-ask-chat-working | 2.1.301 | E | 100x30 | 0.4 s after "Chat about this": `User declined…` printed, spinner, Claude writes a reply (a3) | 04cc17a (#70, PR #72) |
| screen-fs-ask-chat-done | 2.1.301 | E | 100x30 | that reply done, bare box (a4) | 04cc17a (#70, PR #72) |
| screen-fs-ask-answered-thinking | 2.1.301 | E | 100x30 | answered "Red", thinking spinner (a5) | 04cc17a (#70, PR #72) |
| screen-fs-ask-answered-tool | 2.1.301 | E | 100x30 | answered, Bash running; spinner labelled with the tool's description (a6) | 04cc17a (#70, PR #72) |
| screen-fs-ask-esc | 2.1.301 | E | 100x30 | cancelled with Esc: `User declined…`, turn over, bare box (a7; a8 is identical) | 04cc17a (#70, PR #72) |
| screen-fs-elicit-open | 2.1.301 | E | 100x30 | MCP elicitation open; drawn like a dialog of Claude's own (b1) | 04cc17a (#70, PR #72) |
| screen-fs-elicit-answered | 2.1.301 | E | 100x30 | 0.5 s after Accept, MCP tool at work: spinner `elicit: ask_color…` (b3) | 04cc17a (#70, PR #72) |
| screen-fs-elicit-done | 2.1.301 | E | 100x30 | turn over, reply, bare box (b5) | 04cc17a (#70, PR #72) |
| screen-fs-elicit-esc-tool | 2.1.301 | E | 100x30 | elicitation cancelled with Esc, MCP tool still running (b6) | 04cc17a (#70, PR #72) |
| screen-fs-manual-tool | 2.1.301 | E' | 100x30 | manual mode, Bash running after auto-approval, spinner, no prompt drawn (c1) | 04cc17a (#70, PR #72) |
| screen-fs-subagent-elicit-open | 2.1.301 | E | 100x30 | background subagent's MCP elicitation in the main pane; main waits on it (d1) | 04cc17a (#70, PR #72) |
| screen-fs-subagent-elicit-answered | 2.1.301 | E | 100x30 | that one answered: bare box, agents list below the footer (d2) | 04cc17a (#70, PR #72) |
| screen-fs-start-ready | 2.1.301 | E | 100x30 | fresh session, placeholder in the box (e0) | 04cc17a (#70, PR #72) |
| screen-fs-thinking | 2.1.301 | E | 100x30 | normal turn, thinking spinner (e1) | 04cc17a (#70, PR #72) |
| screen-fs-streaming | 2.1.301 | E | 100x30 | reply streaming, no spinner: looks like a bare box (e3) | 04cc17a (#70, PR #72) |
| screen-fs-narrow-working | 2.1.301 | E | 40x30 | turn starting, spinner and tip (e4) | 04cc17a (#70, PR #72) |
| screen-fs-narrow-ask-open | 2.1.301 | E | 40x30 | AskUserQuestion open (e5) | 04cc17a (#70, PR #72) |
| screen-fs-narrow-ask-answered | 2.1.301 | E | 40x30 | answered, spinner; footer cut (e6) | 04cc17a (#70, PR #72) |
| screen-fs-narrow-done | 2.1.301 | E | 40x30 | turn over, bare box (e7) | 04cc17a (#70, PR #72) |
| screen-fs-background-at-prompt | 2.1.301 | E | 100x30 | main at its box, a background subagent listed below the footer (x2) | 04cc17a (#70, PR #72) |
| screen-own-model-background | 2.1.301 | E | 100x30 | `/model` picker opened while a background subagent runs (x3) | 04cc17a (#70, PR #72) |
| screen-fs-model-closed-background | 2.1.301 | E | 100x30 | that picker closed with Esc: box with the agents list (x4) | 04cc17a (#70, PR #72) |
| screen-fs-esc-tool | 2.1.301 | E | 100x30 | Esc 11 s into a foreground Bash command, the turn's only tool: no `Interrupted` line, the card collapsed to `▸ 1 komenda · × · 7,7 s`, bare box | #77 |
| screen-fs-later-tool-running | 2.1.301 | E | 100x30 | a second Bash command running after a first ended: card open (`▾ 2 komendy · ◓`), spinner labelled with its description | #77 |
| screen-fs-esc-later-tool | 2.1.301 | E | 100x30 | Esc on that second command: the card collapsed to `▸ 2 komendy · × · 5,1 s`, no line, bare box | #77 |
| screen-fs-esc-after-tool | 2.1.301 | E | 100x30 | Esc while Claude thinks after a Bash command ended: `⎿  Interrupted · What should Claude do instead?` below the card | #77 |
| screen-fs-tool-failed-working | 2.1.301 | E | 100x30 | a Bash command failed, the turn goes on: card open and marked `×`, spinner below | #77 |
| screen-fs-tool-failed-done | 2.1.301 | E | 100x30 | that turn done: card collapsed and marked `×`, the reply in a card below it | #77 |
| screen-fs-esc-later-tool-80 | 2.1.301 | E'' | 80x24 | Esc on a second Bash command after a first ended (the QA run of #77): `▸ 2 komendy · × · 12 s`, no line, bare box | #77 |
| screen-fs-tool-running-80 | 2.1.301 | E'' | 80x24 | a long silent foreground Bash command with no description (the command #77's agent ran), 33 s in: spinner labelled with the command, no timer | #77 |
| screen-fs-queued-at-work | 2.1.302 | G | 80x24 | a prompt queued while the reply streams: drawn above the box as `❯ Run exactly…` with `ctrl+x ctrl+s to send now` under it, the box reads `Press up to edit queued messages`, no spinner | #88 |
| screen-fs-queued-taken-thinking | 2.1.302 | G | 80x24 | 0.3 s after Esc on that turn: its reply card, `⎿  Interrupted`, then Claude thinking on the queued prompt (spinner `✻ Orchestrating…`, a tip), no `❯` for it, bare box | #88 |
| screen-fs-queued-taken-streaming | 2.1.302 | G | 80x24 | 6 s after that Esc: `⎿  Interrupted`, then the queued turn's reply streaming below it (`● I'll write the story first…`), no spinner, bare box | #88 |
| screen-fs-queued-taken-shown-thinking | 2.1.302 | G, Opus 5.5 high | 80x24 | the session's first turn interrupted while Claude still thought, a prompt queued: `⎿  Interrupted`, the queued prompt drawn sent as `❯ Run exactly…`, Claude thinking below it (spinner `✻ Gitifying…`), bare box | #88 |
| screen-fs-queued-taken-shown-tool | 2.1.302 | G, Opus 5.5 high | 80x24 | 19 s later: the same `❯` line, the queued turn's card with its Bash command running, spinner labelled with the command | #88 |
| screen-fs-queued-taken-tool | 2.1.302 | G | 80x24 | 50 s after that Esc, the `Interrupted` line scrolled off: the queued turn's card with its Bash command done, spinner `· Orchestrating… (49s · ↓ 1.1k tokens)` | #88 |
| screen-fs-esc-idle | 2.1.302 | G | 80x24 | Esc on a streaming turn with nothing queued: its reply card, `⎿  Interrupted`, nothing below it, bare box | #88 |
| screen-fs-esc-twice | 2.1.302 | G | 80x24 | Esc on a streaming turn with a prompt queued, then Esc again 0.4 s later on the turn Claude took it for: two `⎿  Interrupted` lines, bare box | #88 |
| screen-fs-ask-several-open | 2.1.302 | E''' | 100x30 | AskUserQuestion with three questions open: tabs `☐ Color  ☐ Sizes  ☐ Name  ✔ Submit` above the first | #89 |
| screen-fs-ask-multi-open | 2.1.302 | E''' | 100x30 | that dialog at its multi-select question: `[ ]` boxes, `Type something` as a box, `Next` | #89 |
| screen-fs-ask-multi-picked | 2.1.302 | E''' | 100x30 | two options ticked and text typed in place of `Type something` (`[✔] XL`) | #89 |
| screen-fs-ask-text-typed | 2.1.302 | E''' | 100x30 | free text typed in place of `Type something.` of a single-choice question | #89 |
| screen-fs-ask-review | 2.1.302 | E''' | 100x30 | that dialog at its review step, the three answered: tabs `☒ Color  ☒ Sizes  ☒ Name  ✔ Submit`, `Review your answers`, `Ready to submit your answers?` with `Submit answers` / `Cancel`, no footer below them | #89 |
| screen-fs-ask-several-answered | 2.1.302 | E''' | 100x30 | the three answered at the terminal: `· Which sizes? → Small, Large, XL`, the reply, bare box | #89 |
| screen-fs-ask-hook-ignored | 2.1.302 | E''' | 100x30 | AskUserQuestion still open 6.5 s after a held PermissionRequest hook printed an allow with the answers: the template's approve hook had answered first | #89 |
| screen-fs-elicit-hook-running | 2.1.302 | E''' | 100x30 | an Elicitation hook holding: no form, the tool's card and `elicit: ask_color… (running Elicitation hooks… 1/2 · 8s …)` | #89 |
| screen-fs-subagent-elicit-hook-running | 2.1.302 | E''' | 100x30 | a background subagent's elicitation while its hook holds: nothing of it shown, `Waiting for 1 background agent to finish`, the agents list | #89 |
| screen-ask-hook-answered | 2.1.302 | F | 100x30 | AskUserQuestion answered by a PermissionRequest hook: `· Which color? → Blue`, `⎿  Allowed by PermissionRequest hook`, turn over | #89 |
| screen-ask-hook-cancelled | 2.1.302 | F | 100x30 | cancelled by the hook (deny, `interrupt: true`): `User declined to answer questions`, turn over | #89 |
| screen-ask-hook-denied | 2.1.302 | F | 100x30 | denied by the hook without interrupt: `⎿  Error: hq: cancelled`, `⎿  Denied by PermissionRequest hook`, Claude's reply, turn over | #89 |
| screen-ask-hook-timeout | 2.1.302 | F | 100x30 | AskUserQuestion 3 s after its PermissionRequest hook reached its `timeout` (10 s): open as before, no message | #89 |
| screen-ask-pretooluse-running | 2.1.302 | F | 100x30 | a PreToolUse hook of AskUserQuestion holding: no dialog, spinner `(running PreToolUse hooks… 1/2 · 8s …)` | #89 |
| screen-timeout-elicit-open | 2.1.302 | F | 100x30 | the MCP elicitation's form, drawn once its Elicitation hook reached its `timeout` (10 s) | #89 |

"old #n": issues and PRs of an earlier tracker (before 2026-09-29), cited so in the design and the commits; today's tracker reuses those numbers for other issues.

## Hook payloads

"Normalized" means the session id (`0b1c2d3e-4f50-…`) or the paths (`/w/app`, `/home/agent`) were replaced, so at least those were edited.

| fixture | event | what it is | real or hand-made | source |
|---|---|---|---|---|
| answer-ask.json | PostToolUse | AskUserQuestion answered | unknown; normalized | 39924e5 (old #71) |
| answer-ask-hook.json | PostToolUse | the same three answered by a PermissionRequest hook (a label, a list of two labels, free text): the list arrives joined with `, ` | real, 2.1.302, F (#89); paths normalized | #89 |
| answer-ask-several.json | PostToolUse | AskUserQuestion with three questions answered at the terminal: a label, two labels and typed text of a multi-select joined with `, `, typed text | real, 2.1.302, E''' (#89); paths normalized | #89 |
| answer-bash.json | PostToolUse | Bash (`sleep 60`, background) after its prompt | unknown; normalized | 39924e5 (old #71) |
| dialog-ask.json | PermissionRequest | AskUserQuestion opening, `bypassPermissions` | unknown; normalized | 39924e5 (old #71) |
| dialog-ask-several.json | PermissionRequest | AskUserQuestion with three questions, one of them `multiSelect` | real, 2.1.302, E''' (#89); paths normalized | #89 |
| dialog-bash.json | PermissionRequest | Bash prompt, `default` mode, with `permission_suggestions` | unknown; normalized | 39924e5 (old #71) |
| dialog-subagent.json | PermissionRequest | a background subagent's MCP tool (`agent_id`, `mcp__elicit__ask_color`) | real, 2.1.301, paths shortened; replaced an in-test edit that added `agent_id` to the agent's own fixtures | f2816b6 (#70) |
| elicitation.json | Elicitation | the agent's own MCP elicitation (`mode` `form`, `requested_schema`; no `elicitation_id`) | real, 2.1.302, E''' (#89); paths normalized | #89 |
| elicitation-subagent.json | Elicitation | a background subagent's: the same shape, no `agent_id`, the `prompt_id` of the main agent's turn that started the subagent | real, as elicitation.json | #89 |
| elicitation-result-accept.json | ElicitationResult | the agent's own form accepted at the terminal (`action`, `content`) | real, 2.1.302, F (#89); paths normalized | #89 |
| elicitation-result-accept-subagent.json | ElicitationResult | a background subagent's form accepted at the terminal (`action`, `content`; no `agent_id`); another session than elicitation-subagent.json | real, 2.1.302, F (#89); paths normalized | #89 |
| elicitation-result-cancel.json | ElicitationResult | the agent's own form cancelled with Esc, drawn after its Elicitation hook timed out | real, as elicitation-result-accept.json | #89 |
| elicitation-result-decline.json | ElicitationResult | the agent's own form declined at the terminal | real, as elicitation.json | #89 |
| notification-elicitation.json | Notification | `elicitation_dialog` of that subagent; carries no `agent_id` | real, 2.1.301, paths shortened | f2816b6 (#70) |
| notification.json | Notification | `permission_prompt`, "Claude needs your permission" | unknown; normalized | 7c4c3ed (old #19) |
| post-tool.json | PostToolUse | Bash ended | real, 2.1.283 in sbx (design changelog, old #135); paths `/w/app` | 2248770 (old #135) |
| post-tool-subagent.json | PostToolUse | a background subagent's Bash ended (`agent_id`) | real, as post-tool.json | 2248770 (old #135) |
| post-tool-own-monitor.json | PostToolUse | the agent's own Monitor tool ended: `"tool_name":"Monitor"`, `"tool_response":{"taskId":...}` (no `agent_id`), the watch the Stop lists as a `shell` | real, 2.1.302, Opus 5.5 (high effort), G's managed settings, hq from the worktree of #99 (`qa-artifacts` `issue-99/`); paths normalized, printed with indentation | #99 |
| post-tool-subagent-monitor.json | PostToolUse | a background subagent's Monitor tool ended (`agent_id`, `taskId`) | real, 2.1.302, Opus 5.5 (high effort), G's managed settings, hq from the worktree of #99 (`qa-artifacts` `issue-99/`); paths normalized, printed with indentation | #99 |
| prompt.json | UserPromptSubmit | a prompt | unknown; normalized | 7c4c3ed (old #19) |
| prompt-pasted.json | UserPromptSubmit | a two-line pasted prompt | real, as post-tool.json | 2248770 (old #135) |
| prompt-wake.json | UserPromptSubmit | Claude's wake-up for a finished subagent (`<task-notification>`) | unknown (PR #39 logged every hook with 2.1.291); normalized | 7cb35f8 (#38) |
| queued-first.json | UserPromptSubmit | a prompt that starts a turn (`prompt_id` `a5c44ac7…`) | real, 2.1.301, E'' (run A3 of #82); paths normalized, one line as Claude sends it | #82 |
| queued-while-working.json | UserPromptSubmit | a prompt typed while that turn works, as Claude queues it: the same `prompt_id` | real, as queued-first.json | #82 |
| queued-stop.json | Stop | that turn's end, the queued prompt still queued | real, as queued-first.json; the message shortened | #82 |
| queued-session-end-clear.json | SessionEnd | `reason` `clear`, a `/clear` queued before the prompt, run right after that Stop | real, as queued-first.json | #82 |
| queued-session-start-clear.json | SessionStart | `source` `clear`, a new `session_id`, no `prompt_id` | real, as queued-first.json | #82 |
| queued-turn-tool-end.json | PostToolUse | the first tool's end in the turn Claude took the queued prompt for (`prompt_id` `f6babc86…`) | real, as queued-first.json; the output shortened | #82 |
| queued-turn-stop.json | Stop | that turn's end | real, as queued-first.json; the message shortened | #82 |
| session-start.json | SessionStart | `source` `startup` | unknown; normalized | 3e80a9d (old #124) |
| session-start-resume.json | SessionStart | `source` `resume`, in the repository | unknown; normalized | 3e80a9d (old #124) |
| session-start-clear.json | SessionStart | `source` `clear` | unknown; normalized | 10a6bff (old #124) |
| session-end.json | SessionEnd | `reason` `prompt_input_exit` | unknown; normalized | 7c4c3ed (old #19) |
| stop-done.json | Stop | turn over, a reply | unknown; normalized | 7c4c3ed (old #19) |
| stop-question.json | Stop | turn over with a question | unknown; normalized | 7c4c3ed (old #19) |
| stop-to-block.json | Stop | a turn end the hook blocks to deliver an inbox message | real, as post-tool.json | 2248770 (old #135) |
| stop-after-block.json | Stop | the turn end after a blocked stop (`stop_hook_active` true) | real, as post-tool.json | 2248770 (old #135) |
| stop-background.json | Stop | turn over with a background subagent running | unknown (as prompt-wake.json); normalized | 7cb35f8 (#38) |
| stop-background-shell.json | Stop | turn over with only a background shell (`npm run dev`) running | unknown (as prompt-wake.json); normalized | 7cb35f8 (#38) |
| stop-own-monitor.json | Stop | turn over with only a watch of the agent's own running, started with the Monitor tool (its PostToolUse said `taskId`): Claude lists it as type `shell`, with the Monitor's `description` and `command`, while its footer says `1 monitor` | real, 2.1.302, G's managed settings, hq from the worktree of #78 (`qa-artifacts` `issue-97/`); paths normalized | #97 |
| stop-subagent-monitor.json | Stop | the turn Claude took for a subagent's interim report: the agent's own watch and the subagent's watch running, both listed as type `shell` | real, 2.1.302, Opus 5.5 (high effort), G's managed settings, hq from the worktree of #99 (`qa-artifacts` `issue-99/`); paths normalized, printed with indentation | #99 |
| stop-background-question.json | Stop | turn over with a question while a shell and a subagent run | unknown (as prompt-wake.json); normalized | 7cb35f8 (#38) |
