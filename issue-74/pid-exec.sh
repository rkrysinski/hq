#!/bin/sh
# go test -exec: records the test binary's pid (exec keeps it) in $QA_PIDS.
echo $$ >>"$QA_PIDS"
exec "$@"
