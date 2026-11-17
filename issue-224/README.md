# Issue 224: an expired login, real screens and hook payloads

The host session's delivery for #224 (*What the host session delivers* in its agent brief). Taken 2026-11-17 on the host, in a sandbox made for this run alone, never the agents' sandbox. All times are UTC.

## Setup

- `claude --version`: `2.1.326 (Claude Code)` (the image ships 2.1.319; 2.1.326 was installed in the sandbox with `claude install 2.1.326` before the run).
- Start screen, model and plan line: `Sonnet 5.5 · Claude API`. After the login expired the same line reads `Sonnet 5.5 · API Usage Billing` (first seen in `a-error-100x30`, a redraw after a resize).
- Image: the agents' sandbox image, tag `latest`, with its managed settings unchanged: fullscreen view (`tui`), output style `concise`, the status line command, default permission mode `bypassPermissions`, a `PermissionRequest` hook of the image's own, the department's plugin, and the three mods (tools pane, clean view, GitHub pane).
- Claude Code ran in a pane of a private tmux server, started with `tools/capture/capture.sh start a 80x24 <dir> --settings <settings.json>`; the working directory was a throwaway git repository with one empty commit.
- `--settings` named one logging command, with no matcher, for each of: `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `PermissionRequest`, `Notification`, `Stop`, `StopFailure`, `SubagentStop`, `SessionEnd`. The command is the brief's `log-hook.sh`: one file per firing, the payload as it came on stdin.
- Pane sizes: 80x24 for the whole run. The 100x30 shots are of the same session: the pane was resized to 100x30, shot one second later, and resized back. There was no second run at 100x30.

## How the login was made to expire

A second real Claude Code was run in the same sandbox, outside the session, with the sandbox's proxy variables removed (`HTTPS_PROXY`, `HTTP_PROXY`, `https_proxy`, `http_proxy`, `NODE_USE_ENV_PROXY`, `NO_PROXY`, `no_proxy`): `claude -p "Reply with the single word: hi"`. It ran from 14:47:52 to 14:47:54 and printed `Failed to authenticate: OAuth session expired and could not be refreshed`. The session under capture was then in its second `sleep 20`; its next request, after that tool call ended, was refused.

## Steps and shots

| Time | What |
| --- | --- |
| 14:46:47 | session started (`SessionStart` 14:46:48) |
| 14:47:07 | case A prompt: ten foreground `sleep 20` Bash calls, then the word `done` |
| 14:47:31 | first tool call ended |
| 14:47:52 | shot `a-working` (second `sleep 20` at work) |
| 14:47:52 - 14:47:54 | the login made to expire (above) |
| 14:48:01 | second tool call ended; `StopFailure` fired 0.03 s later |
| 14:49:01 | `Notification` (`idle_prompt`) |
| 14:49:05 | shot `a-error` (80x24) |
| 14:49:06 | shot `a-error-100x30` |
| 14:49:09 | shot `a-rest` (80x24) |
| 14:49:10 | shot `a-rest-100x30` |
| 14:49:20 | case B prompt: `Reply with the single word: hi`; `StopFailure` fired 0.04 s after `UserPromptSubmit` |
| 14:49:21 | shot `b-error` (80x24) |
| 14:49:22 | shot `b-error-100x30` |
| 14:50:20 | `Notification` (`idle_prompt`) |
| 14:50:52 | shot `b-rest-90s` (80x24), the session untouched since the case B prompt |
| 14:51:22 | the tmux server stopped; no hook file was written after it |

## Which hooks fired

- **Case A** (the login expires during a turn): `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `PreToolUse`, `PostToolUse`, `StopFailure` (`error`: `authentication_failed`, `last_assistant_message`: `Login expired · Please run /login`, no `error_details`), then `Notification` (`idle_prompt`) 60 s later. No `Stop`.
- **Case B** (a prompt to a session whose login has expired): `UserPromptSubmit`, `StopFailure` (the same `error` and `last_assistant_message`), then `Notification` (`idle_prompt`) 60 s later. No `Stop`.

Never fired in the run: `Stop`, `PostToolUseFailure`, `PermissionRequest`, `SubagentStop`, `SessionEnd` (the session was ended by stopping the tmux server).

## Where the run differs from the brief

- `a-error` and `a-rest` were not shot as soon as the error line showed: the error line came at 14:48:01 and the shots are 64 s and 68 s later, after the `Notification`. They show the screen at rest. What the screen showed in the first seconds after the error in case A was not captured; `b-error` (1 s after the error) is the only shot that close to an error.
- The 100x30 shots are resizes of the 80x24 session, as said above.
- `b-rest-90s` is 92 s after the case B prompt.

## Files

- `screens/`: each shot as `capture.sh shot` wrote it, `NAME.txt` (plain, as hq reads it) and `NAME.ansi.txt` (styled).
- `hooks/`: every file the logger wrote, 11 of them, named by the time of the firing; the name's order is the order of firing.

## Private details taken out

In the screens and the payloads alike: the session id and the two prompt ids are replaced by made-up ones of the same shape (each real id always by the same one); the working directory is `/w/app`, and the directory's name inside `transcript_path` and `scratchpad_dir` is replaced to match. Nothing else was changed.
