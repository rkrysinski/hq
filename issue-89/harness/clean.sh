#!/bin/sh
# clean.sh: ends hook holders left from earlier runs (they would take the next answer).
pkill -f "^/bin/sh /tmp/claude-1000/-home-dev-work-repositories-hq/0000001d-0000-4000-8000-00000000001d/scratchpad/spike/act.sh" ; rm -f /tmp/claude-1000/-home-dev-work-repositories-hq/0000001d-0000-4000-8000-00000000001d/scratchpad/spike/ctl/ans-* /tmp/claude-1000/-home-dev-work-repositories-hq/0000001d-0000-4000-8000-00000000001d/scratchpad/spike/ctl/*.tmp; true
