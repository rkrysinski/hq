#!/bin/sh
# usage: note.sh RUN TEXT - writes TEXT with the time into the run's keys.log (what the trial did to the run, outside Claude Code)
TD=$(cd "$(dirname "$0")" && pwd)
echo "$(date -u +%FT%T.%3NZ) $2" | tee -a "$TD/runs/$1/keys.log"
