package state

import (
	"bytes"
	"encoding/json"
	"strings"
)

// hookScript runs inside the sandbox on each lifecycle event, in Claude's
// current directory, with the event payload on stdin, the event's kind in $1
// and, on stop and input, the notification sequence in $2. It writes the
// branch checked out there as a header line, then the payload, to the main
// repository's .git/hq/agents/$HQ_ID, replacing it in one step, and keeps
// the latest Stop event beside it (.stop) for the last message. On stop and
// input it then prints the desktop notification for Claude to write to its
// terminal (design §3.5). It needs only sh, git, cat, mv, cp, mkdir, rm and
// awk, never blocks Claude and always exits 0 (design §3.4, §7.1).
const hookScript = `case $HQ_ID in '' | *[!0123456789abcdef]*) exit 0 ;; esac
b=$(git branch --show-current 2>/dev/null)
l=${b:-${PWD##*/}}
cd "${CLAUDE_PROJECT_DIR:-.}" 2>/dev/null
d=$(git rev-parse --git-common-dir 2>/dev/null) || exit 0
d=$d/hq/agents
mkdir -p "$d" 2>/dev/null || exit 0
f=$d/$HQ_ID
if { printf '%s %s\n' ` + branchHeader + ` "$b" && cat; } >"$f.$$" 2>/dev/null && mv -f "$f.$$" "$f" 2>/dev/null && [ "$1" = stop ]; then
    cp -f "$f" "$f.$$" 2>/dev/null && mv -f "$f.$$" "$f.stop" 2>/dev/null
fi
rm -f "$f.$$" 2>/dev/null
case $1 in stop | input) [ -n "$2" ] && K=$1 T=$2 L=$l awk '` + notifyAwk + `' "$f" 2>/dev/null ;; esac
exit 0`

// notifyAwk prints the notification for the event in the state file: on
// input "Needs input: <branch>", on stop "Question: <branch>" when the last
// assistant message ends with "?" (the rule of IsQuestion), else "Done:
// <branch>", placed at the %s of the sequence T, as Claude's hook output
// {"terminalSequence": ...}. It reads the file as one record and walks the
// message's JSON string to its end; the branch is only ever data.
const notifyAwk = `function question(s,    i, n, c, m) {
    i = index(s, "\"last_assistant_message\":")
    if (!i) return 0
    s = substr(s, i + 25)
    sub(/^[ \t\r\n]*"/, "", s)
    n = length(s)
    m = ""
    for (i = 1; i <= n; i++) {
        c = substr(s, i, 1)
        if (c == "\\") { m = m substr(s, i, 2); i++; continue }
        if (c == "\"") break
        m = m c
    }
    for (;;) {
        if (m ~ /[ \t\r\n]$/) m = substr(m, 1, length(m) - 1)
        else if (m ~ /\\[nrt]$/) m = substr(m, 1, length(m) - 2)
        else break
    }
    return m ~ /\?$/
}
BEGIN { RS = "\001" }
NR == 1 {
    k = "Done"
    if (ENVIRON["K"] == "input") k = "Needs input"
    else if (question($0)) k = "Question"
    l = ENVIRON["L"]
    gsub(/[[:cntrl:]\\"]/, "", l)
    t = ENVIRON["T"]
    i = index(t, "%s")
    if (i) t = substr(t, 1, i - 1) k ": " l substr(t, i + 2)
    printf "{\"terminalSequence\":\"%s\"}\n", t
}`

// branchHeader starts the line the hook writes before the payload.
const branchHeader = "branch"

// attentionNotifications are the Notification kinds that mean the agent
// waits for the user (design §3.4).
const attentionNotifications = "permission_prompt|agent_needs_input|elicitation_dialog"

type hookCommand struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

type hookMatcher struct {
	Matcher string        `json:"matcher,omitempty"`
	Hooks   []hookCommand `json:"hooks"`
}

// hook runs the script for an event of kind; notify, when set, is the
// notification sequence the event sends.
func hook(kind, notify string) hookCommand {
	args := []string{"-c", hookScript, "hq-hook", kind}
	if notify != "" {
		args = append(args, notify)
	}
	return hookCommand{Type: "command", Command: "sh", Args: args}
}

// inTmux tells Claude its terminal is tmux, which it is: sbx does not pass
// TMUX into the sandbox. Claude then wraps a hook's notification sequence for
// tmux passthrough; tmux drops the sequence unwrapped (design §3.5).
const inTmux = "hq"

// Settings is the --settings hq gives Claude: the agent's identity as
// environment, and the hook set that reports its state (ADR 0009) and sends
// notifications with the platform's sequence (ADR 0010).
func Settings(name, id, notify string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false) // the script's redirections stay readable
	_ = enc.Encode(map[string]any{
		"env": map[string]string{"HQ_AGENT": name, "HQ_ID": id, "TMUX": inTmux},
		"hooks": map[string][]hookMatcher{
			"UserPromptSubmit": {{Hooks: []hookCommand{hook("prompt", "")}}},
			"Stop":             {{Hooks: []hookCommand{hook("stop", notify)}}},
			"Notification":     {{Matcher: attentionNotifications, Hooks: []hookCommand{hook("input", notify)}}},
			"SessionEnd":       {{Hooks: []hookCommand{hook("end", "")}}},
		},
	})
	return strings.TrimSpace(b.String())
}
