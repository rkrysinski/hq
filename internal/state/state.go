// Package state is the agent's side of hq's state channel (design §3.4): the
// hook injected into Claude at launch, and the reading of what it writes.
package state

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// The states of spec §5.
const (
	Starting   = "starting"
	Working    = "working"
	Question   = "question"
	NeedsInput = "needs input"
	Done       = "done"
	Ended      = "ended"
)

// Report is what an agent's state file says.
type Report struct {
	State     string
	Since     time.Time // host modification time of the file (design §7.1)
	Last      string    // the agent's last message, one line, stripped
	SessionID string    // Claude's session id
	Cwd       string    // where Claude works (a worktree or the repository), as the sandbox sees it
	Branch    string    // the branch checked out in Cwd, empty when detached or unknown
	Prompt    string    // while working since a prompt: the prompt, as the user sent it
	// AtStart says the report is a session's start: Claude has started
	// and, as far as its hooks tell, waits at its prompt. It may show a
	// dialog of its own first, which no hook reports (StartDialog).
	AtStart bool
	// Latest is the time of the agent's latest report: Since, or the later
	// time of an event that left it working without restarting its time
	// (Kept).
	Latest time.Time
	// Background says the agent's turn ended while background work still
	// runs (subagents): it waits at its prompt, working, until Claude wakes
	// it for the closing turn (design §3.4).
	Background bool
	// Owed says that of a Background agent nothing runs any more: its
	// background work finished together, and Claude has not yet taken the
	// turn it owes for the last of it. That turn comes within moments, or
	// the agent is done (design §3.4).
	Owed bool
	// Woken says the agent works on a turn Claude woke itself for after
	// background work finished: no prompt of the user's shows on its screen
	// for it.
	Woken bool
}

// payload is the part of a Claude hook event hq reads.
type payload struct {
	Event            string `json:"hook_event_name"`
	SessionID        string `json:"session_id"`
	Cwd              string `json:"cwd"`
	AssistantMessage string `json:"last_assistant_message"`
	Message          string `json:"message"`
	Prompt           string `json:"prompt"`
	Source           string `json:"source"`
	ToolName         string `json:"tool_name"`
	BackgroundTasks  []struct {
		ID     string `json:"id"`
		Type   string `json:"type"`
		Status string `json:"status"`
	} `json:"background_tasks"`
	ToolInput struct {
		Questions   []struct{ Question string } `json:"questions"`
		Description string                      `json:"description"`
		Command     string                      `json:"command"`
		FilePath    string                      `json:"file_path"`
		URL         string                      `json:"url"`
	} `json:"tool_input"`
}

// The SessionStart sources of a new session and of one started with
// --resume.
const (
	newSource    = "startup"
	resumeSource = "resume"
)

// wakePrefix starts the prompt Claude gives itself when background work
// has finished (design §3.4).
const wakePrefix = "<task-notification>"

// askTool is Claude's tool that asks the user questions in a dialog.
const askTool = "AskUserQuestion"

// busy reports whether the event, a Stop, lists background work that
// still runs: a running task that is no shell command, or a shell command
// one of the agent's subagents started, which the hook's list (owed, the
// .owe file) names on a line ~ID. It is the rule of the hook's awk, which
// decides (design §3.4).
func (p payload) busy(owed []byte) bool {
	theirs := map[string]bool{}
	for _, line := range strings.Split(string(owed), "\n") {
		if id, ok := strings.CutPrefix(line, "~"); ok && id != "" {
			theirs[id] = true
		}
	}
	for _, t := range p.BackgroundTasks {
		if t.Status == "running" && (t.Type != "shell" || theirs[t.ID]) {
			return true
		}
	}
	return false
}

// dialogText is what an open dialog shows as the last message: the first
// question Claude asks, or for a permission prompt the tool and what it is
// about to do.
func (p payload) dialogText() string {
	in := p.ToolInput
	if p.ToolName == askTool && len(in.Questions) > 0 {
		return Clean(in.Questions[0].Question)
	}
	for _, s := range []string{in.Description, in.Command, in.FilePath, in.URL} {
		if s = Clean(s); s != "" {
			return Clean(p.ToolName) + ": " + s
		}
	}
	return Clean(p.ToolName)
}

// Parse derives the report from the latest event and the latest Stop event
// (empty when there is none), which keeps the last message across events,
// and the event before a resumed session started (empty when there is none).
func Parse(latest, lastStop, prev []byte) Report {
	branch, latest := splitHeader(latest)
	var p payload
	if json.Unmarshal(latest, &p) != nil {
		return Report{State: Starting}
	}
	r := Report{SessionID: Clean(p.SessionID), Cwd: p.Cwd, Branch: Clean(branch)}
	var stop payload
	if _, lastStop = splitHeader(lastStop); json.Unmarshal(lastStop, &stop) == nil {
		r.Last = Clean(stop.AssistantMessage)
	}
	switch p.Event {
	case "UserPromptSubmit", "PostToolUse", "PostToolUseFailure":
		// The hook keeps a tool's end only when it closes a dialog: the
		// user answered and Claude works on.
		r.State = Working
		r.Prompt = p.Prompt
		r.Woken = strings.HasPrefix(p.Prompt, wakePrefix)
	case "PermissionRequest":
		r.State = NeedsInput
		if m := p.dialogText(); m != "" {
			r.Last = m
		}
	case "Stop":
		r.Last = Clean(p.AssistantMessage)
		r.State = Done
		if IsQuestion(p.AssistantMessage) {
			r.State = Question
		}
	case "Notification":
		r.State = NeedsInput
		if m := Clean(p.Message); m != "" {
			r.Last = m
		}
	case "SessionStart":
		// Claude has started, or started over after /clear, and waits at
		// its prompt for the user. A resumed session starts in the
		// repository, not in the worktree its conversation left: until it
		// reports again it keeps the branch, worktree and last message the
		// event before it gave (design §3.4).
		r.State = Done
		r.AtStart = true
		if p.Source == newSource {
			// Nothing to resume yet: Claude keeps no conversation for a
			// session that never had a prompt, and --resume with its id
			// fails (design §3.6).
			r.SessionID = ""
		}
		if p.Source == resumeSource {
			if before := Parse(prev, lastStop, nil); before.State != Starting {
				r.Branch, r.Cwd, r.Last = before.Branch, before.Cwd, before.Last
			}
		}
	case "SessionEnd":
		r.State = Ended
	default:
		r.State = Starting
	}
	return r
}

// Kept adds to the report of the state file latest what the hook kept
// beside it (kept, written at; empty when there is nothing; owed is the
// hook's list of the background work it follows, the .owe file): the latest
// event that left the agent working without restarting its time (design
// §3.4). A turn end with background work still running (a Stop) counts
// beside a report that says working, and as the report itself when the hook
// wrote it there too, the agent not working before: the agent works, at its
// prompt, with that turn's message. Claude's wake-up for the closing turn
// (a UserPromptSubmit) counts beside a report that says working: the agent
// works on, since the same moment.
func (r Report) Kept(latest, kept, owed []byte, at time.Time) Report {
	var p payload
	if _, event := splitHeader(kept); json.Unmarshal(event, &p) != nil {
		return r
	}
	switch {
	case p.Event == "Stop" && (r.State == Working || bytes.Equal(kept, latest)):
		r.State, r.Background, r.Last = Working, true, Clean(p.AssistantMessage)
		r.Owed = !p.busy(owed)
	case p.Event == "UserPromptSubmit" && r.State == Working:
		r.Woken = true
	default:
		return r
	}
	if at.After(r.Latest) {
		r.Latest = at
	}
	return r
}

// splitHeader separates the hook's branch line from the payload after it; a
// file without the line (written by an older hook) is all payload.
func splitHeader(data []byte) (branch string, payload []byte) {
	head, rest, ok := bytes.Cut(data, []byte("\n"))
	if !ok || !bytes.HasPrefix(head, []byte(branchHeader+" ")) {
		return "", data
	}
	return string(head[len(branchHeader)+1:]), rest
}

// IsQuestion is the rule that tells question from done: the last assistant
// message ends with a question mark (the hook's awk applies the same rule).
func IsQuestion(message string) bool {
	return strings.HasSuffix(strings.TrimRightFunc(message, unicode.IsSpace), "?")
}

// maxLast caps the message kept from a state file.
const maxLast = 500

// Clean makes text from a state file safe to show on one line: escape
// sequences and control characters go, whitespace runs become one space
// (design §7.3).
func Clean(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == 0x1b:
			i += escapeLen(s[i:])
			continue
		case r == '\n' || r == '\r' || r == '\t':
			b.WriteByte(' ')
		case r == utf8.RuneError && size == 1, unicode.IsControl(r), r == 0x2028, r == 0x2029:
		default:
			b.WriteRune(r)
		}
		i += size
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	if utf8.RuneCountInString(out) > maxLast {
		out = string([]rune(out)[:maxLast])
	}
	return out
}

// escapeLen is the length of the escape sequence at the start of s: CSI up
// to its final byte, OSC, DCS, SOS, PM and APC up to BEL or ST, otherwise
// ESC and the one character after it.
func escapeLen(s string) int {
	if len(s) < 2 {
		return len(s)
	}
	switch s[1] {
	case '[':
		for i := 2; i < len(s); i++ {
			if s[i] >= 0x40 && s[i] <= 0x7e {
				return i + 1
			}
		}
		return len(s)
	case ']', 'P', 'X', '^', '_':
		for i := 2; i < len(s); i++ {
			if s[i] == 0x07 {
				return i + 1
			}
			if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
		}
		return len(s)
	}
	_, size := utf8.DecodeRuneInString(s[1:])
	return 1 + size
}
