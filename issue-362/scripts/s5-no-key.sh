#!/bin/sh
# Check 5: the id holds no key: each id of checks 1 to 3 against the key of its state directory, written in hex,
# base64 and base64url (the key itself is not printed)
for pair in "t362 s1.task" "t362b s2b.task" "t362d s3-1.task" "t362d s3-2.task"; do
  set -- $pair
  key=/tmp/hq-stub-$1/home/.local/state/hq/task-key
  id=$(cat "$2")
  hex=$(od -An -tx1 "$key" | tr -d ' \n')
  b64=$(base64 -w0 < "$key")
  b64url=$(printf %s "$b64" | tr '+/' '-_' | tr -d '=')
  found=no
  for k in "$hex" "$b64" "$b64url"; do case $id in *"$k"*) found=yes ;; esac; done
  echo "$1: key $(stat -c '%s bytes, mode %a' "$key"); id $id (${#id} characters); the key in it, in hex, base64 or base64url: $found"
done
