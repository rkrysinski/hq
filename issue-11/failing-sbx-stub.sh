#!/bin/sh
# QA stub: fails the way sbx 0.46.0 was seen to fail (texts from qa-artifacts issue-3), CRLF as sbx.exe writes.
case ${SBX_FAIL:-} in
auth) printf 'error: Not authenticated to Docker\r\n  try: sbx login\r\n' >&2; exit 1 ;;
hv) if [ "$1" = ls ]; then printf '{"sandboxes":[]}\n'; exit 0; fi
    printf 'WARN: mcp gateway teardown\r\nerror: the Windows Hypervisor Platform is unavailable: either the optional feature is not enabled, or Windows is itself running in a virtual machine or VDI desktop whose host does not expose virtualization extensions (nested virtualization)\r\n' >&2; exit 1 ;;
esac
echo "stub: set SBX_FAIL" >&2; exit 1
