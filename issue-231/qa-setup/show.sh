#!/bin/sh
# Prints what the agent ran in a QA session and what each command gave it,
# then what the stub gh received.
jq -r '
  if .type=="assistant" then (.message.content[]? | select(.type=="tool_use") | "\n$ " + .input.command)
  elif .type=="user" then (.message.content[]? | select(.type=="tool_result") |
     (if .is_error then "=> REFUSED / error:\n" else "=> ran:\n" end) +
     (if (.content|type)=="string" then .content else ([.content[]?.text] | join("\n")) end))
  elif .type=="result" then "\nagent: " + (.result // "")
  else empty end' <<JSON
$(grep "^{" "/tmp/qa231/$1.jsonl")
JSON
echo
echo "--- stub gh received:"
cat "/tmp/qa231/$1-gh.log"
