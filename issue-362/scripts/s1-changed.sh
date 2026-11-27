#!/bin/sh
# Check 1: a wait on b1 goes on as a task after 50 s; tasks/get with its id as given, then tasks/get and
# tasks/cancel with the id changed one way each (variants.py)
QAC_TASK_FILE=s1.task ./qac.py 'call:list:{"names":["b1"]}' 'call:wait:{"names":["b1"],"since":"$since"}' -- ./hqmcp.sh claude-code
id=$(cat s1.task)
./variants.py "$id" > s1-variants.txt
echo "== the id and its variants"; echo "as-given $id"; cat s1-variants.txt
set -- "get:$id"
while read -r label v; do set -- "$@" "get:$v" "cancel:$v"; done < s1-variants.txt
./qac.py "$@" "get:$id" -- ./hqmcp.sh claude-code
