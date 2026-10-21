#!/bin/sh
# The real claude as an agent in a sandbox: the template's managed settings with approve.sh in place of
# claude-approve, hq's and Claude Code's own environment from the host dropped. CLAUDE_V picks the version.
v=$(cat $QA/version 2>/dev/null || echo 2.1.304)
for x in $(env | sed -n 's/^\(\(CLAUDE\|HQ_\)[A-Za-z0-9_]*\)=.*/\1/p'); do [ "$x" = CLAUDE_ENV_FILE ] || unset "$x"; done
export DISABLE_AUTOUPDATER=1
exec unshare -r -m sh -c 'mount --bind $QA/etc-cc /etc/claude-code && exec unshare --map-user=1000 --map-group=1000 /home/agent/.local/share/claude/versions/'"$v"' "$@"' sh "$@"
