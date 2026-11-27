#!/bin/sh
# Check 2, before: the id of check 1 (stub environment t362) asked of an hq mcp of another stub environment, t362b
QA_ENV=t362b ./qac.py "get:$(cat s1.task)" -- ./hqmcp.sh claude-code
