#!/bin/sh
# Check 9: an unknown task is refused with -32602; a task method whose request does not declare tasks with -32021
./qac.py get:nope get:w1 notasks get:nope -- ./hqmcp.sh claude-code
