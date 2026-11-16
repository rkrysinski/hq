package state

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
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
	File string `json:"file,omitempty"`
	// Basis is the number of the last event of the hook an event of hq's
	// own (screen, tmux, sbx, hq) was based on, 0 before the first: its
	// place among the hook's events, which no clock decides.
	Basis int64 `json:"basis,omitempty"`
	// Aside is why the event was set aside, when it was: AsideDuplicate or
	// AsideOvertaken. An event set aside stays in the journal and changes
	// nothing.
	Aside string `json:"aside,omitempty"`
	Facts Facts  `json:"facts"`
}

// The two reasons an event is set aside (spec #165): another hq process
// appended the same evidence first, or a report of the hook later than
// the evidence's basis was appended before it.
const (
	AsideDuplicate = "duplicate"
	AsideOvertaken = "overtaken"
)

// The kinds of hq's own events (#195), by source. From the screen, what a
// look sees there that no hook reports: a turn the user ended, with its
// line (Text); a screen first seen at rest, and one at rest at a tool the
// user cut short, with its fingerprint (Screen); the decision, RestDelay
// later with the screen unchanged, that the turn was rewound, or that the
// turn Claude owed does not come, dated from the screen first seen at rest
// (From); a dialog of Claude's own seen, with its fingerprint, and gone;
// Claude at work after a dialog its hooks reported; Claude at work on a
// prompt it took from its queue; Claude's wake-up after a turn the user
// ended, first seen, dated from its report (From). From tmux, the pane's
// death, dated by tmux (From, zero when tmux does not say). From sbx, the
// sandbox not running, with the moment sbx was asked (Asked). From hq: the
// agent launched (Start "new" or "restore", with its repository and tmux
// server) and relaunched, a restart taking it down, a delivery of the
// messages typed in as its prompt (KindDelivery), an answer given to its
// dialog (Whose), and either taken back when it did not reach the agent
// (KindWithdrawn, Withdrawn the kind taken back). The kinds of hq's own
// events are unique across their sources.
const (
	KindTurnEnded     = "TurnEnded"
	KindAtRest        = "AtRest"
	KindToolCut       = "ToolCut"
	KindRewound       = "Rewound"
	KindTurnNotTaken  = "TurnNotTaken"
	KindOwnDialog     = "OwnDialog"
	KindOwnDialogGone = "OwnDialogGone"
	KindAtWork        = "AtWork"
	KindTookQueued    = "TookQueued"
	KindWokeUp        = "WokeUp"

	KindPaneDied = "PaneDied"

	KindNotRunning = "NotRunning"

	KindLaunched   = "Launched"
	KindRelaunched = "Relaunched"
	KindTakenDown  = "TakenDown"
	KindAnswer     = "Answer"
	KindWithdrawn  = "Withdrawn"
)

// The sources of an event (spec #165): the agent's hook, its screen,
// tmux, sbx and hq itself.
const (
	SourceHook   = "hook"
	SourceScreen = "screen"
	SourceTmux   = "tmux"
	SourceSbx    = "sbx"
	SourceHQ     = "hq"
)

// The kinds of an event of the hook: the hook events Claude names that a
// rule reads (hook_event_name), a delivery of messages, and an event file
// hq cannot read.
const (
	KindPrompt   = "UserPromptSubmit"
	KindToolEnd  = "PostToolUse"
	KindToolFail = "PostToolUseFailure"
	// KindTurnEnd is the hook event that ends a turn, whose reply the
	// journal keeps once, for the latest (Reply).
	KindTurnEnd           = "Stop"
	KindPermission        = "PermissionRequest"
	KindElicitation       = "Elicitation"
	KindElicitationResult = "ElicitationResult"
	KindNotification      = "Notification"
	KindSessionStart      = "SessionStart"
	KindSessionEnd        = "SessionEnd"

	KindDelivery   = deliveryKind
	KindUnreadable = "unreadable"
)

// The hook's cases, as its event files name them (Facts.Hook): which of
// the hook's cases wrote an event of the hook, which the reducer follows.
const (
	CasePrompt = "prompt"
	CaseStop   = "stop"
	CaseAnswer = "answer"
	CaseStart  = "start"
	CaseResume = "resume"
	CaseEnd    = "end"
)

// Facts are what an event holds beside its kind and time: only what a rule
// of design §3.4 reads (spec #165, the operator's decision 3), and Reason
// and Notice, which no rule reads, kept for hq explain (ADR 0018). Prompt and
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
	Start    string `json:"start,omitempty"`    // how a session started (SessionStart's source), or how hq launched an agent ("new", "restore")
	Reason   string `json:"reason,omitempty"`   // why a session ended (SessionEnd's reason)
	Notice   string `json:"notice,omitempty"`   // the kind of a Notification
	// Screen is the fingerprint of a screen at rest, or of a dialog of
	// Claude's own (its title and text), which tells screens apart without
	// keeping them.
	Screen string `json:"screen,omitempty"`
	// From is the earlier moment an event dates the state from: the screen
	// first seen at rest for a decision that took time, the pane's death,
	// the report of a wake-up.
	From time.Time `json:"from,omitzero"`
	// Asked is when hq asked sbx which sandboxes run.
	Asked time.Time `json:"asked,omitzero"`
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
	// Text is what a dialog asks, or what a Notification says, on one
	// line, or the line Claude printed for a turn the user ended.
	Text string `json:"text,omitempty"`
	// Messages are the messages a delivery delivered, each by the name it
	// was left under in the inbox; a delivery of hq's own typed them in.
	Messages []string `json:"messages,omitempty"`
	// Hook is the hook's case an event of the hook came with, as its event
	// file is named (CasePrompt, CaseStop, ...). For the hook's delivery,
	// the case it delivered at: CaseStop, a stop it blocked; CasePrompt,
	// with the next prompt; CaseAnswer, after a tool's end.
	Hook string `json:"hook,omitempty"`
	// Whose is whose dialog an answer of hq's went to: its hooks' (a
	// question dialog, DialogHooks) or Claude's own (DialogOwn).
	Whose string `json:"whose,omitempty"`
	// Withdrawn is the kind of the event of hq's own a KindWithdrawn takes
	// back.
	Withdrawn string `json:"withdrawn,omitempty"`
	// Repo and Tmux are, for a launch, the repository hq launched the agent
	// in and the tmux server it runs on (the -L socket name, "" for the
	// default server): where its record and its home window stand while
	// the agent exists (journal.Home).
	Repo string `json:"repo,omitempty"`
	Tmux string `json:"tmux,omitempty"`
	// Prompted is, for a launch, that hq gave the agent a first prompt: it
	// is starting until it reports it, not done at its session's start
	// (spec §5, S2). A launch from before the fact has none.
	Prompted bool `json:"prompted,omitempty"`
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
// event file: its name starts with the hook's count, a digit, where the
// hook's temporary files and its count start with a dot.
func IsEventFile(name string) bool { return name != "" && name[0] >= '0' && name[0] <= '9' }

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
		e = unreadable(e, int64(len(data)))
		e.Facts.Branch = Clean(branch)
		return e, nil
	}
	e.Kind = Clean(p.Event)
	f := Facts{
		Branch: Clean(branch), Session: Clean(p.SessionID), Cwd: p.Cwd, Turn: Clean(p.PromptID),
		Subagent: p.AgentID != "", Tool: Clean(p.ToolName), Server: Clean(p.Server),
		Start: Clean(p.Source), Reason: Clean(p.Reason), Notice: Clean(p.Notification),
		Hook: hookKind(name),
	}
	var reply *Reply
	switch p.Event {
	case KindPrompt:
		f.Wake = strings.HasPrefix(p.Prompt, wakePrefix)
		if m := wakeTask.FindStringSubmatch(p.Prompt); m != nil {
			f.Woke = Clean(m[1])
		}
		if !f.Wake {
			f.Prompt = p.Prompt
		}
	case KindToolEnd, KindToolFail:
		f.Started = p.started()
	case KindPermission:
		f.Text = p.dialogText()
	case KindElicitation, KindNotification:
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
			return SubagentMark + Clean(id)
		}
	case r.Status == "async_launched":
		return Clean(cmp.Or(r.AgentID, r.TaskID))
	case p.ToolName == "Monitor" && r.TaskID != "":
		return MonitorMark + Clean(r.TaskID)
	}
	return ""
}

// parseDelivery is the delivery event of an event file whose first line
// named it, with rest what follows that line's first word: the hook's kind,
// then a message's name per line.
func parseDelivery(e Event, rest []byte) Event {
	e.Kind = KindDelivery
	lines := strings.Split(string(rest), "\n")
	e.Facts.Hook = Clean(lines[0])
	for _, l := range lines[1:] {
		if messageNameRE.MatchString(l) {
			e.Facts.Messages = append(e.Facts.Messages, l)
		}
	}
	return e
}

// unreadable is e as an event file hq cannot read, of size bytes: what it
// holds is gone, but the hook's kind its name gives.
func unreadable(e Event, size int64) Event {
	e.Kind, e.Facts = KindUnreadable, Facts{Size: size, Hook: hookKind(e.File)}
	return e
}

// eventName is the name the hook gives an event file (ev): its count, the
// hook's kind and its process id.
var eventName = regexp.MustCompile(`^[0-9]{9}\.([a-z]+)\.[0-9]+$`)

// hookKind is the hook's kind the event file named name came with, empty
// for a name that does not give one.
func hookKind(name string) string {
	if m := eventName.FindStringSubmatch(name); m != nil {
		return m[1]
	}
	return ""
}

// UnreadableEvent is the event of an event file named name hq cannot read
// (not a regular file, too large, unreadable), of size bytes, at at.
func UnreadableEvent(name string, size int64, at time.Time) Event {
	return unreadable(Event{Source: SourceHook, Time: at, File: name}, size)
}
