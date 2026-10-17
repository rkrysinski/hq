#!/bin/sh
# mk-settings.sh TIMEOUT > settings.json: log every event, act on AskUserQuestion and elicitations.
S=$(cd "$(dirname "$0")" && pwd); to=$1
jq -n --arg S "$S" --argjson to "$to" '
 def lg(e): {hooks:[{type:"command",command:"\($S)/log.sh \(e)"}]};
 def act(e;m): {matcher:m,hooks:[{type:"command",command:"\($S)/act.sh \(e)",timeout:$to}]};
 {hooks:(
  ["SessionStart","SessionEnd","UserPromptSubmit","PostToolUse","PostToolUseFailure","PermissionDenied","Notification","ElicitationResult","Stop","SubagentStart","SubagentStop"]
   | map({key:.,value:[lg(.)]}) | from_entries)
  + {PreToolUse:[lg("PreToolUse"),act("PreToolUse";"AskUserQuestion")],
     PermissionRequest:[lg("PermissionRequest"),act("PermissionRequest";"AskUserQuestion")],
     Elicitation:[lg("Elicitation"),act("Elicitation";"")]}}'
