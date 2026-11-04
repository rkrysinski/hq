# 0016 - the list shows every agent

Supersedes [ADR 0006](0006-attention-view-default.md) (#150). The dashboard has one view: every agent, in the sort the user chose. The attention view, the filter `a` toggled that showed only agents needing the user plus the docked one, is gone, and `a` is unbound.

Triage stays with the attention sort, still the default: `needs input` and `question` are always the top rows, in amber, and the header counts `X need you`. With a list of six rows, hiding the rest bought little calm and cost a key to see agents that were working or done, and the exception for the docked agent (ADR 0006's amendment, #65) made the view harder to read than the rule it served.

## Considered Options

- **Keep the attention view as the default** (ADR 0006): the calm it gave is mostly given by the sort already; it hid the agents the user also wants to watch.
- **Keep both views, all as the default**: a toggle few would use, its header, footer and saved state kept for it.
- **Dim the rows that do not need the user**: a softer stand-in for the filter; not wanted, the sort and the header are enough.

## Consequences

- `hq` opens on every agent. `a` does nothing; the header and footer name no view.
- The view an older hq saved in the preferences is ignored and dropped the next time the file is saved, without a message.
- The empty state "nothing needs you - press a for all" is gone; with no agents the list still says how to start one.
- `Alt+a` (dock the first agent needing the user) and `Alt+j`/`Alt+k` are unchanged; the docked agent is always a row.
- A supervisor (spec Phase 2) adding agents makes the default list longer; the sort keeps those needing the user on top.
