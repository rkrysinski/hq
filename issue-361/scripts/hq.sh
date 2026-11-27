#!/bin/sh
# usage: hq.sh ARGS - hq from the issue's worktree (scripts/local-dev.sh) against its stub environment
exec env LOCAL_DEV_ID=t361 "$WORKTREE"/scripts/local-dev.sh --stub native "$@" 2>/dev/null
