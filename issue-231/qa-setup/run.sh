#!/bin/sh
# QA of #231: one Claude Code session in /tmp/qa231/$1/hq with the stub gh first on PATH.
# $2: the pattern list (empty: none).
side=$1; list=$2
cd /tmp/qa231/$side/hq || exit 1
: > /tmp/qa231/gh.log
env -u HQ_PRIVATE_PATTERNS ${list:+HQ_PRIVATE_PATTERNS="$list"} PATH=/tmp/qa231/bin:$PATH \
  claude -p "$(cat /tmp/qa231/${3:-prompt}.txt)" --model claude-haiku-5-5 --dangerously-skip-permissions \
  --max-turns 15 --output-format stream-json --verbose < /dev/null > /tmp/qa231/$side${3:+-$3}.jsonl 2>&1
echo "claude exit $?"
cp /tmp/qa231/gh.log /tmp/qa231/$side${3:+-$3}-gh.log
