#!/bin/sh
# QA for #154: a simulated Windows (stub cmd.exe, wslpath, powershell.exe) under /tmp/win.
# usage: scenario.sh CHECK [hq|hq-before]
export PATH=/tmp/qa154/bin:/usr/bin:/bin HQ_PLATFORM=wsl WSL_DISTRO_NAME=Ubuntu WSL_STUB_ROOT=/tmp/win HQ_TMUX_SOCKET= XDG_CONFIG_HOME= TMUX_TMPDIR=
hq=${2:-hq}
L=/tmp/win/mnt/c/Users/stub/AppData/Local
R=/tmp/win/mnt/c/Users/stub/AppData/Roaming
PKG=$L/Packages/Claude_pzs8sxrjxfjjc/LocalCache/Roaming/Claude
APP='"C:\Program Files\WindowsApps\Claude_1.0.0_x64__pzs8sxrjxfjjc\app\claude.exe"'
rm -rf /tmp/win; unset WSL_STUB_PROCESSES
case $1 in
1) # Store, Claude-Data, Claude Desktop not running (the package's folder left behind)
   mkdir -p $L/Claude-Data/claude_2f7a1c $PKG $R/Claude; echo '{}' > $PKG/claude_desktop_config.json; touch $PKG/.user-data-left.1761040000 ;;
2) # Package's folder with the marker, no Claude-Data: not chosen
   mkdir -p $PKG $R/Claude; touch $PKG/.user-data-left.1761040000 ;;
3) # Running Claude Desktop reading another Claude-Data folder than the order finds
   mkdir -p $L/Claude-Data/claude_2f7a1c $L/Claude-Data/claude_9b
   export WSL_STUB_PROCESSES="$APP
$APP --type=renderer --user-data-dir=\"C:\\Users\\stub\\AppData\\Local\\Claude-Data\\claude_9b\" --lang=pl
$APP --type=gpu-process --user-data-dir=\"C:\\Users\\stub\\AppData\\Local\\Claude-Data\\claude_9b\"
" ;;
4) # Installer from claude.ai, running: helpers without --user-data-dir
   mkdir -p $R/Claude
   export WSL_STUB_PROCESSES='"C:\Users\stub\AppData\Local\AnthropicClaude\app-1.0.0\claude.exe"
"C:\Users\stub\AppData\Local\AnthropicClaude\app-1.0.0\claude.exe" --type=renderer --lang=pl
' ;;
5) # Running Claude Desktop reading the file the order finds
   mkdir -p $L/Claude-Data/claude_2f7a1c
   export WSL_STUB_PROCESSES="$APP --type=renderer --user-data-dir=C:\\Users\\stub\\AppData\\Local\\Claude-Data\\claude_2f7a1c" ;;
esac
echo "\$ find /tmp/win -name 'claude_*' -o -name '.user-data-left*'"
find /tmp/win \( -name 'claude_*' -o -name '.user-data-left*' \) | sort
if [ -n "${WSL_STUB_PROCESSES:-}" ]; then echo; echo '$ # claude.exe command lines (stub powershell.exe)'; printf '%s\n' "$WSL_STUB_PROCESSES" | sed '/^$/d'; fi
echo; echo "\$ $hq mcp install"; $hq mcp install; echo "(exit $?)"
for f in $(find /tmp/win -name claude_desktop_config.json | sort); do echo; echo "\$ jq -c .mcpServers.hq.command ${f#/tmp/win/mnt/c/Users/stub/AppData/}"; jq -c '.mcpServers.hq.command // "no hq"' "$f"; done
