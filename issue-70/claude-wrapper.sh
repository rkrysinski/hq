#!/bin/sh
# The real Claude Code behind the stub sbx: the parent session's CLAUDE*
# variables dropped, bypass mode as sbx's agents run, and the elicitation
# MCP server for #70's QA.
for v in $(env | grep -E '^CLAUDE' | grep -v '^CLAUDE_ENV_FILE' | cut -d= -f1); do unset "$v"; done
export TERM=xterm-256color
exec /home/agent/.local/bin/claude --permission-mode bypassPermissions --mcp-config /tmp/claude-1000/-home-dev-work-repositories-hq/00000031-0000-4000-8000-000000000031/scratchpad/qa70/mcp.json "$@"
