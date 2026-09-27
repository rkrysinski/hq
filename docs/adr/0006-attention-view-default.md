# 0006 - attention view is the default

The dashboard opens showing only agents that need the user; `a` shows all. The point of the console is triage; oversight is one key away. The header always counts all agents so nothing is silently hidden. This also keeps a future supervisor layer (spec, Phase 2) from making the default view busier.

Amended (#65): the attention view also shows the docked agent's row, whatever its state, with the docked outline. Without it the list could read "nothing needs you" above a live session, giving no sign of the agent the user is looking at, and the docked outline (spec §6.1) could never show. The default stays calm: at most one extra row, and it is the one on screen anyway. Agents neither docked nor needing the user stay hidden.
