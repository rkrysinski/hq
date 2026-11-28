#!/bin/sh
# Check 2: the experimental tasks of revision 2025-11-25 are not the declaration: a wait on p3 (done, nothing changes
# it) with timeout 60 and params.task, from a 2025-11-25 session that declares capabilities.tasks, and from 2026-07-28
# requests with tasks in their _meta capabilities outside extensions: no handle at 50 s, the call returns no agents at
# 60 s, and tasks/get is refused as undeclared (-32021)
cd "$(dirname "$0")"
echo "== initialize, protocol 2025-11-25, capabilities.tasks"
./experimental.py initialize 60 p3 -- ./hqmcp.sh claude-code &
sleep 5
echo "== protocol 2026-07-28, tasks in _meta's client capabilities outside extensions" > c2-meta.txt
./experimental.py meta 60 p3 -- ./hqmcp.sh claude-code >> c2-meta.txt 2>&1
wait
cat c2-meta.txt
