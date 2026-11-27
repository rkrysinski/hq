#!/bin/sh
# usage: [QA_ENV=ID] [QA_BEFORE=1] hq.sh ARGS - hq from the issue's worktree (scripts/local-dev.sh) against stub environment ID (default t363); with QA_BEFORE=1 built from the integration branch (--before)
b=; [ -n "$QA_BEFORE" ] && b=--before
exec env LOCAL_DEV_ID="${QA_ENV:-t363}" LOCAL_DEV_BEFORE=spec-357-wait-task <worktree>/scripts/local-dev.sh --stub native $b "$@" 2>/dev/null
