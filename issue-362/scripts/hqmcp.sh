#!/bin/sh
# usage: [QA_ENV=ID] hqmcp.sh CLIENT - hq mcp CLIENT built from the issue's worktree by scripts/local-dev.sh, against stub environment ID (default t362)
exec env LOCAL_DEV_ID="${QA_ENV:-t362}" <worktree>/scripts/local-dev.sh --stub native mcp "$@" 2>/dev/null
