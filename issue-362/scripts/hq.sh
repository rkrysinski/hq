#!/bin/sh
# usage: [QA_ENV=ID] hq.sh ARGS - hq from the issue's worktree (scripts/local-dev.sh) against stub environment ID (default t362)
exec env LOCAL_DEV_ID="${QA_ENV:-t362}" <worktree>/scripts/local-dev.sh --stub native "$@" 2>/dev/null
