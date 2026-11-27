#!/bin/sh
# usage: hqmcp.sh CLIENT - hq mcp CLIENT built from the issue's worktree by scripts/local-dev.sh, against its stub environment
exec env LOCAL_DEV_ID=t361 "$WORKTREE"/scripts/local-dev.sh --stub native mcp "$@" 2>/dev/null
