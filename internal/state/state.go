// Package state is the agent's side of hq's state channel (design §3.4): the
// hook injected into Claude at launch, and the reading of what it writes.
package state

import (
	"bytes"
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
	// Outdated is the state of an outdated agent (GLOSSARY.md), of another
	// generation than the hq that looks: no hook's report counts for it.
	Outdated = "outdated"
)

// Report is an agent's state as a hook's reports give it: what the tests
// describe an agent by, which the fake journal turns into the event files
// its hook leaves (journaltest.ReportFiles).
type Report struct {
	State string
	Since time.Time // host modification time of the file (design §7.1)
	Last  string    // the agent's last message, one line, stripped
	// LastStop is the latest Stop's message, one line, stripped: Last, unless
	// an open dialog's text stands in for it.
	LastStop  string
	SessionID string // Claude's session id
	Cwd       string // where Claude works (a worktree or the repository), as the sandbox sees it
	Branch    string // the branch checked out in Cwd, empty when detached or unknown
	Prompt    string // while working since a prompt: the prompt, as the user sent it
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
	// Acted says that Claude has acted on the turn the agent works on: a
	// tool of its own has ended in it (the hook's mark, .acted), or an MCP
	// elicitation it opened was answered, so no early Esc rewinds it any
	// more (design §3.4, #77, #100).
	Acted bool
	// SubagentDialog says the dialog the agent needs input for is one of
	// its subagents': the event that reported it carries the subagent's
	// agent_id, as a subagent's tool events do (design §3.4).
	SubagentDialog bool
	// Queued says a prompt the user typed while the agent works waits in
	// Claude's queue (the hook's copy, .queued, pending): Claude takes it
	// into the turn at work, or runs it as a turn of its own after the
	// turn's Stop, which the hook reports, or after an Esc on the turn,
	// which only the screen shows (design §3.4, #82, #88).
	Queued bool
	// OwnTasks are the agent's own tasks while it is done or has a question
	// from its latest event, a Stop: what it left running itself at that
	// turn end (Own); nil otherwise.
	OwnTasks []Task
}

// Task is one of the agent's own tasks (#78): a background shell command
// or a watch (the Monitor tool) the agent itself started, running at the
// turn end its state comes from.
type Task struct {
	Type string `json:"type"` // ShellTask or MonitorTask
	Text string `json:"text"` // the command of a shell, the description of a watch, in full, stripped (design §7.3)
}

// The types of task an own task may be, as Claude names them in a Stop's
// background_tasks: a shell command and a watch.
const (
	ShellTask   = "shell"
	MonitorTask = "monitor"
)

// payload is the part of a Claude hook event hq reads.
type payload struct {
	Event            string `json:"hook_event_name"`
	SessionID        string `json:"session_id"`
	PromptID         string `json:"prompt_id"`
	Cwd              string `json:"cwd"`
	AssistantMessage string `json:"last_assistant_message"`
	Message          string `json:"message"`
	Prompt           string `json:"prompt"`
	Source           string `json:"source"`
	AgentID          string `json:"agent_id"`
	ToolName         string `json:"tool_name"`
	Server           string `json:"mcp_server_name"`

	BackgroundTasks []backgroundTask `json:"background_tasks"`

	ToolInput struct {
		Questions   []struct{ Question string } `json:"questions"`
		Description string                      `json:"description"`
		Command     string                      `json:"command"`
		FilePath    string                      `json:"file_path"`
		URL         string                      `json:"url"`
	} `json:"tool_input"`
}

// backgroundTask is a task of a Stop's background_tasks.
type backgroundTask struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Status      string `json:"status"`
	Description string `json:"description"`
	Command     string `json:"command"`
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

// AskTool is Claude's tool that asks the user questions in a dialog, the
// question dialog hq answer answers.
const AskTool = "AskUserQuestion"

// The marks of the hook's list (the .owe file) beside the ids owed a turn:
// a shell command or a watch one of the agent's subagents started (~ID),
// and a watch the agent itself keeps with the Monitor tool (=ID), which
// Claude Code 2.1.302 lists as a shell (#99). Neither is owed a turn.
const (
	subagentMark = "~"
	monitorMark  = "="
)

// artifactWatch ends the description of the watch Claude arms when the
// agent publishes an artifact, the only thing that tells it from a watch of
// the agent's own in a Stop (seen with Claude Code 2.1.296 as a monitor
// task, #62). It is looked for whatever the task's type: Claude Code 2.1.302
// lists a Monitor tool's watch as a shell (#97), and may the artifact watch
// too. Should Claude describe it otherwise, it shows as an own task: no
// state depends on it (design §3.4).
const artifactWatch = "(auto-armed on publish)"

// dialogText is what an open dialog shows as the last message: the first
// question Claude asks, or for a permission prompt the tool and what it is
// about to do.
func (p payload) dialogText() string {
	in := p.ToolInput
	if p.ToolName == AskTool && len(in.Questions) > 0 {
		return Clean(in.Questions[0].Question)
	}
	for _, s := range []string{in.Description, in.Command, in.FilePath, in.URL} {
		if s = Clean(s); s != "" {
			return Clean(p.ToolName) + ": " + s
		}
	}
	return Clean(p.ToolName)
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

// maxLast caps the message kept from a report.
const maxLast = 500

// Clean makes text from a report safe to show on one line: escape
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
