#!/bin/sh
# The real Claude Code behind the stub sbx: the parent session's CLAUDE*
# variables dropped, bypass mode as sbx's agents run, and the elicitation
# MCP server for #70's QA.
for v in $(env | grep -E '^CLAUDE' | grep -v '^CLAUDE_ENV_FILE' | cut -d= -f1); do unset "$v"; done
export TERM=xterm-256color
exec /home/agent/.local/bin/claude --permission-mode bypassPermissions --mcp-config /tmp/claude-1000/-Users-romankrysinski-work-repositories-hq/a41f9452-666c-4138-8314-b0e3a20745cb/scratchpad/qa70/mcp.json "$@"
