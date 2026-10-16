# Capturing Claude Code's screens

hq reads Claude's screen to settle an agent's state (design §3.4). Those readers are checked against real captures in `internal/state/testdata/` (where each came from: its `README.md`). This folder makes new ones.

## When

Before a spec, PRD or issue settles a rule that reads Claude's screen, capture the screens the rule depends on, in the setup the agents run in: the same Claude Code version, the same sbx template and its managed settings (renderer, status line, mods, hooks). Check the rule against the captures, then settle it. #68's rule rested on the 2.1.283 fixtures and did not hold on 2.1.301 in the sbx template (#70).

## How

```bash
tools/capture/capture.sh mcp "$work"                    # optional: the elicitation server, $work/mcp.json
tools/capture/capture.sh start main 100x30 "$work" --permission-mode bypassPermissions --mcp-config "$work/mcp.json"
tools/capture/capture.sh show main                      # look
tools/capture/capture.sh keys main -l "Call the ask_color tool of the elicit MCP server once."
tools/capture/capture.sh keys main Enter
tools/capture/capture.sh shot main "$out/elicit-open"   # elicit-open.txt, elicit-open.ansi.txt
tools/capture/capture.sh stop
```

- `$work` is a scratch directory outside the repository (a fresh `git init` is enough). Claude asks once whether to trust it.
- 100x30 is the size of most fixtures. Capture a narrow pane (40 columns) too when the rule must hold there.
- `elicit` (built by `mcp`) has one tool, `ask_color`. It opens an MCP elicitation, then works on for 20 s after the answer, so the screen shows Claude at work on a tool. In the elicitation, `→` opens the field, `Space` picks, `←` and `↓` reach Accept.
- Shoot each state the rule tells apart: the dialog open, just after the answer, a while later, the turn done.

## Then

- Copy each capture you use to `internal/state/testdata/screen-<name>.txt`, unedited, and add its row to that folder's `README.md`: the `claude --version`, the setup, the pane, what it shows.
- When the setup cannot draw a screen (the sbx template's auto-approve hook answers every permission request, so no permission prompt shows), say so in the README rather than making the screen by hand.
