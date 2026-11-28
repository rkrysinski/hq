#!/bin/sh
# usage: [QA_ENV=ID] [QA_BEFORE=1] hqmcp.sh CLIENT - hq mcp CLIENT built by scripts/local-dev.sh from the review fixes' worktree (or the integration branch with QA_BEFORE=1), against stub environment ID (default t357r)
b=; [ -n "$QA_BEFORE" ] && b=--before
exec env LOCAL_DEV_ID="${QA_ENV:-t357r}" LOCAL_DEV_BEFORE=spec-357-wait-task <worktree>/scripts/local-dev.sh --stub native $b mcp "$@" 2>/dev/null
