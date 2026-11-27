#!/bin/sh
# usage: say.sh RUN TEXT - types a prompt into the run's Claude Code and submits it, noting when
TC=$(cd "$(dirname "$0")" && pwd)
"$TC/tm.sh" send-keys -t "=$1:" -l "$2" && sleep 0.6 && "$TC/tm.sh" send-keys -t "=$1:" Enter && echo "$(date -u +%FT%T.%3NZ) said: $2" | tee -a "$TC/runs/$1/keys.log"
