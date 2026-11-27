#!/bin/sh
# usage: [QA_ENV=ID] [QA_BEFORE=1] hqmcp.sh CLIENT - hq mcp CLIENT built by scripts/local-dev.sh from the issue's worktree (or the integration branch with QA_BEFORE=1), against stub environment ID (default t363)
b=; [ -n "$QA_BEFORE" ] && b=--before
exec env LOCAL_DEV_ID="${QA_ENV:-t363}" LOCAL_DEV_BEFORE=spec-357-wait-task <worktree>/scripts/local-dev.sh --stub native $b mcp "$@" 2>/dev/null
