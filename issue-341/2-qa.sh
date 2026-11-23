#!/bin/sh
# Runs hq commands of the review fixes; $1 empty for the checkout's build, --before for the base's.
W=${W:?the checkout of the issue}
cd /tmp/hq-stub-555529466/repo || exit 1
run() {
	echo "\$ hq $*"
	LOCAL_DEV_BEFORE=origin/release-v0.17.0 "$W/scripts/local-dev.sh" --stub native $BEFORE "$@" >"$OUT" 2>&1
	code=$?
	grep -v '^local-dev:' "$OUT"
	echo "exit $code"
}
OUT=$(mktemp)
BEFORE=$1
run ls
run ls a a b
run ls -x
run ls a --all
run ls a nope
run ls b --json
run wait a --timeout 1s
run explain a
run send nope hi
run answer nope --dialog abcdef 1
run send a "say pong" --wait --timeout 20s
