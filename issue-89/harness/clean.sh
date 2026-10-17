#!/bin/sh
# clean.sh: ends hook holders left from earlier runs (they would take the next answer).
pkill -f "^/bin/sh /tmp/claude-1000/-Users-romankrysinski-work-repositories-hq/5be29c0e-761b-4f5f-a874-a65b830ec0ef/scratchpad/spike/act.sh" ; rm -f /tmp/claude-1000/-Users-romankrysinski-work-repositories-hq/5be29c0e-761b-4f5f-a874-a65b830ec0ef/scratchpad/spike/ctl/ans-* /tmp/claude-1000/-Users-romankrysinski-work-repositories-hq/5be29c0e-761b-4f5f-a874-a65b830ec0ef/scratchpad/spike/ctl/*.tmp; true
