package state

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Event is one thing hq learned about an agent (GLOSSARY.md): what it was
// (Kind), when, its source, and the facts the rules of design §3.4 read,
// with a number from the one count of the state directory (ADR 0018).
// Events of the hook come from the event files it leaves (ParseEvent).
type Event struct {
	Number int64     `json:"n"`
	Source string    `json:"source"`
	Kind   string    `json:"kind"`
	Time   time.Time `json:"time"`
	// File is the name of the event file an event of the hook came from.
	File  string `json:"file,omitempty"`
	Facts Facts  `json:"facts"`
}

// The sources of an event (spec #165): the agent's hook, its screen,
// tmux, sbx and hq itself.
const (
	SourceHook   = "hook"
	SourceScreen = "screen"
	SourceTmux   = "tmux"
	SourceSbx    = "sbx"
	SourceHQ     = "hq"
)

// The kinds of an event of the hook besides the hook events Claude names
// (UserPromptSubmit, Stop, PermissionRequest, ...): a delivery of messages,
// and an event file hq cannot read.
const (
	KindDelivery   = deliveryKind
	KindUnreadable = "unreadable"
	// KindTurnEnd is the hook event that ends a turn, whose reply the
	// journal keeps once, for the latest (Reply).
	KindTurnEnd = "Stop"
)

// Facts are what an event holds beside its kind and time: only what a rule
// of design §3.4 reads (spec #165, the operator's decision 3). Prompt and
// Text are the conversation's text a rule or the row reads, never shown by
// hq explain; a turn end's reply is not here but in the journal's Reply.
type Facts struct {
	Branch   string `json:"branch,omitempty"`   // the branch the hook found checked out
	Session  string `json:"session,omitempty"`  // Claude's session id
	Cwd      string `json:"cwd,omitempty"`      // where Claude works, as the sandbox sees it
	Turn     string `json:"turn,omitempty"`     // the turn's id, Claude's prompt_id
	Subagent bool   `json:"subagent,omitempty"` // reported by one of the agent's subagents (agent_id)
	Tool     string `json:"tool,omitempty"`     // the tool a dialog or a tool's end is about
	Server   string `json:"server,omitempty"`   // the MCP server of an elicitation
	Start    string `json:"start,omitempty"`    // how a session started (SessionStart's source)
	Reason   string `json:"reason,omitempty"`   // why a session ended (SessionEnd's reason)
	Notice   string `json:"notice,omitempty"`   // the kind of a Notification
	// Question says a turn end's reply ends with "?" (IsQuestion).
	Question bool `json:"question,omitempty"`
	// Tasks are the background tasks a turn end lists.
	Tasks []EventTask `json:"tasks,omitempty"`
	// Started is the background work a tool started, as the hook's list
	// names it (hookAwk, K=tool): ID, ~ID for a subagent's, =ID for a
	// watch of the agent's own.
	Started string `json:"started,omitempty"`
	// Wake says the prompt is the one Claude gives itself when background
	// work has finished; Woke is the task it names.
	Wake bool   `json:"wake,omitempty"`
	Woke string `json:"woke,omitempty"`
	// Prompt is a prompt as it was sent (none for a wake-up), which the
	// screen rules look for in the prompt box.
	Prompt string `json:"prompt,omitempty"`
	// Text is what a dialog asks, or what a Notification says, on one line.
	Text string `json:"text,omitempty"`
	// Messages are the messages a delivery delivered, each by the name it
	// was left under in the inbox, and At the hook's kind it came with
	// (stop: a stop it blocked, prompt, answer: after a tool's end).
	Messages []string `json:"messages,omitempty"`
	At       string   `json:"at,omitempty"`
	// Size is the size of an event file hq cannot read.
	Size int64 `json:"size,omitempty"`
}

// EventTask is a background task a turn end lists: its id, its type and
// its status, and whether it is the watch Claude arms when the agent
// publishes an artifact (artifactWatch).
type EventTask struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Status   string `json:"status"`
	Artifact bool   `json:"artifact,omitempty"`
}

// Reply is what the journal keeps of the latest turn end beside its event:
// the reply in full, as Claude gave it, and the texts of the tasks it
// listed (spec #165: the latest turn end keeps its reply, earlier ones
// none).
type Reply struct {
	Number int64       `json:"n"`
	Text   string      `json:"text"`
	Tasks  []TaskTexts `json:"tasks,omitempty"`
}

// TaskTexts are the texts of a task a turn end lists.
type TaskTexts struct {
	ID          string `json:"id"`
	Description string `json:"description,omitempty"`
	Command     string `json:"command,omitempty"`
}

// EventsDir is where the hook of agent id of the repository at root leaves
// its event files (design §3.4, ADR 0018).
func EventsDir(root, id string) string { return filepath.Join(root, ".git", eventsPath, id) }

// IsID reports whether id is shaped as the ids hq issues to agents.
func IsID(id string) bool { return idRE.MatchString(id) }

// IsEventFile reports whether a file in an agent's events directory is an
// event file: the hook's temporary files and its count start with a dot.
func IsEventFile(name string) bool { return isMessage(name) }

// ReadFile reads a file the sandbox writes as a state file is read (design
// §7.3): a regular file, never through a link, of at most 16 MiB. Over
// that, size is its size and err says so.
func ReadFile(path string) (data []byte, info os.FileInfo, err error) {
	return readData(path)
}

// TooLargeSize is the size of the file a ReadFile error refused as too
// large to read.
func TooLargeSize(err error) (int64, bool) {
	var big tooLargeError
	if errors.As(err, &big) {
		return big.size, true
	}
	return 0, false
}

// eventPayload is the part of a Claude hook event an event's facts take.
type eventPayload struct {
	payload
	Reason       string `json:"reason"`
	Notification string `json:"notification_type"`
	ToolResponse *struct {
		Status           string `json:"status"`
		AgentID          string `json:"agentId"`
		TaskID           string `json:"taskId"`
		BackgroundTaskID string `json:"backgroundTaskId"`
	} `json:"tool_response"`
}

// wakeTask finds the task a wake-up names first, before any other tag
// (hookAwk, K=wake).
var wakeTask = regexp.MustCompile(`^<task-notification>[^<]*<task-id>([^<"\\]+)`)

// messageNameRE is what a message's name in a delivery may be
// (messageName).
var messageNameRE = regexp.MustCompile(`^[0-9][0-9a-z.-]{0,99}$`)

// ParseEvent is the event of the hook in the event file named name, with
// data its content, at the host's time of the file, and for a turn end its
// reply (nil otherwise). A file that holds no event hq knows is an event
// of kind KindUnreadable with its size, never ignored (design §7.3).
func ParseEvent(name string, data []byte, at time.Time) (Event, *Reply) {
	e := Event{Source: SourceHook, Time: at, File: name}
	if rest, ok := bytes.CutPrefix(data, []byte(deliveryKind+" ")); ok {
		return parseDelivery(e, rest), nil
	}
	branch, raw := splitHeader(data)
	var p eventPayload
	if json.Unmarshal(raw, &p) != nil || p.Event == "" {
		return unreadable(e, int64(len(data))), nil
	}
	e.Kind = Clean(p.Event)
	f := Facts{
		Branch: Clean(branch), Session: Clean(p.SessionID), Cwd: p.Cwd, Turn: Clean(p.PromptID),
		Subagent: p.AgentID != "", Tool: Clean(p.ToolName), Server: Clean(p.Server),
		Start: Clean(p.Source), Reason: Clean(p.Reason), Notice: Clean(p.Notification),
	}
	var reply *Reply
	switch p.Event {
	case "UserPromptSubmit":
		f.Wake = strings.HasPrefix(p.Prompt, wakePrefix)
		if m := wakeTask.FindStringSubmatch(p.Prompt); m != nil {
			f.Woke = Clean(m[1])
		}
		if !f.Wake {
			f.Prompt = p.Prompt
		}
	case "PostToolUse", "PostToolUseFailure":
		f.Started = p.started()
	case "PermissionRequest":
		f.Text = p.dialogText()
	case "Elicitation", "Notification":
		f.Text = Clean(p.Message)
	case KindTurnEnd:
		f.Question = IsQuestion(p.AssistantMessage)
		reply = &Reply{Text: p.AssistantMessage}
		for _, t := range p.BackgroundTasks {
			f.Tasks = append(f.Tasks, EventTask{
				ID: Clean(t.ID), Type: Clean(t.Type), Status: Clean(t.Status),
				Artifact: strings.HasSuffix(strings.TrimSpace(t.Description), artifactWatch),
			})
			reply.Tasks = append(reply.Tasks, TaskTexts{ID: Clean(t.ID), Description: t.Description, Command: t.Command})
		}
	}
	e.Facts = f
	return e, reply
}

// started is the background work the tool of a tool's end started, as the
// hook's list names it (hookAwk, K=tool).
func (p eventPayload) started() string {
	r := p.ToolResponse
	switch {
	case r == nil:
		return ""
	case p.AgentID != "":
		if id := cmp.Or(r.BackgroundTaskID, r.TaskID); id != "" {
			return subagentMark + Clean(id)
		}
	case r.Status == "async_launched":
		return Clean(cmp.Or(r.AgentID, r.TaskID))
	case p.ToolName == "Monitor" && r.TaskID != "":
		return monitorMark + Clean(r.TaskID)
	}
	return ""
}

// parseDelivery is the delivery event of an event file whose first line
// named it, with rest what follows that line's first word: the hook's kind,
// then a message's name per line.
func parseDelivery(e Event, rest []byte) Event {
	e.Kind = KindDelivery
	lines := strings.Split(string(rest), "\n")
	e.Facts.At = Clean(lines[0])
	for _, l := range lines[1:] {
		if messageNameRE.MatchString(l) {
			e.Facts.Messages = append(e.Facts.Messages, l)
		}
	}
	return e
}

// unreadable is e as an event file hq cannot read, of size bytes.
func unreadable(e Event, size int64) Event {
	e.Kind, e.Facts = KindUnreadable, Facts{Size: size}
	return e
}

// UnreadableEvent is the event of an event file named name hq cannot read
// (not a regular file, too large, unreadable), of size bytes, at at.
func UnreadableEvent(name string, size int64, at time.Time) Event {
	return unreadable(Event{Source: SourceHook, Time: at, File: name}, size)
}
