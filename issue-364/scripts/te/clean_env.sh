#!/bin/sh
# Runs its arguments without the calling agent's hq and Claude Code variables.
exec env -i HOME="$HOME" PATH="$PATH" TERM=xterm-256color SHELL=/bin/bash TZ="$TZ" \
  HTTP_PROXY="$HTTP_PROXY" HTTPS_PROXY="$HTTPS_PROXY" NO_PROXY="$NO_PROXY" \
  http_proxy="$http_proxy" https_proxy="$https_proxy" no_proxy="$no_proxy" \
  NODE_EXTRA_CA_CERTS="$NODE_EXTRA_CA_CERTS" NODE_USE_ENV_PROXY="$NODE_USE_ENV_PROXY" \
  SSL_CERT_FILE="$SSL_CERT_FILE" REQUESTS_CA_BUNDLE="$REQUESTS_CA_BUNDLE" \
  XDG_RUNTIME_DIR="$XDG_RUNTIME_DIR" SBX_CRED_ANTHROPIC_MODE="$SBX_CRED_ANTHROPIC_MODE" \
  MCP_SENTINEL_TOKEN_NAME="$MCP_SENTINEL_TOKEN_NAME" SANDBOX_NAME="$SANDBOX_NAME" "$@"
