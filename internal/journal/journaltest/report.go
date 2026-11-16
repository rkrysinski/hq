package journaltest

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/rkrysinski/hq/internal/state"
)

// HookState is an agent's state as its hook's reports give it: what the
// tests describe an agent by, which the fake journal turns into the event
// files its hook leaves (ReportFiles).
type HookState struct {
	State string
	Since time.Time // when the hook reported it: the event files' host time (design §7.1)
	Last  string    // the agent's last message, one line, stripped
	// LastStop is the latest turn end's message, one line, stripped, when
	// a dialog's text stands in for Last.
	LastStop  string
	SessionID string // Claude's session id
	Cwd       string // where Claude works (a worktree or the repository), as the sandbox sees it
	Branch    string // the branch checked out in Cwd, empty when detached or unknown
	Prompt    string // while working since a prompt: the prompt, as the user sent it
	// Latest is, for a Background agent, the time of the turn end that left
	// it working; Since when zero.
	Latest time.Time
	// Background says the agent's turn ended while a subagent still runs:
	// it waits at its prompt, working (design §3.4).
	Background bool
	// SubagentDialog says the dialog the agent needs input for is one of
	// its subagents'.
	SubagentDialog bool
	// OwnTasks are what a done agent, or one with a question, left running
	// itself at its turn end (#78).
	OwnTasks []state.Task
}

// Report leaves for agent id of the repository at root the event files
// its hook leaves to report r, with d what it holds in full
// (ReportFiles), each named by the fake's count of the hook, as the hook
// names them.
func (m *Memory) Report(root, id string, r HookState, d state.Detail) {
	for _, f := range ReportFiles(r, d) {
		m.mu.Lock()
		m.hooks++
		name := fmt.Sprintf("%09d.%s.1", m.hooks, f.Kind)
		m.mu.Unlock()
		m.Leave(root, id, name, f.Data, f.At)
	}
}

// File is an event file the hook leaves: the hook's kind, what it
// holds and when it was written.
type File struct {
	Kind string
	Data []byte
	At   time.Time
}

// ReportFiles are the event files the hook leaves for an agent to report
// r, with d what it holds in full (its reply, what its dialog asks), each
// with Claude's payload as the hook gets it:
//
//   - working: the prompt (Prompt), after a turn end with the last
//     message; with Background, a tool that started a subagent and the turn
//     end that left it running, at Latest;
//   - done or a question: the turn end, its reply d.Reply or the last
//     message (a question's ends with "?"), with its own tasks running;
//   - needs input: a turn end with LastStop first, then the dialog: d.Ask's
//     question dialog, permission prompt or MCP elicitation, else a
//     Notification saying the last message, a subagent's with
//     SubagentDialog;
//   - ended: a turn end with the last message, then the session's end;
//   - starting: nothing.
func ReportFiles(r HookState, d state.Detail) []File {
	at := r.Since
	base := func(event string) map[string]any {
		p := map[string]any{"hook_event_name": event, "session_id": r.SessionID, "cwd": r.Cwd}
		if r.Prompt != "" {
			p["prompt_id"] = "p-" + r.Prompt
		}
		return p
	}
	file := func(kind string, p map[string]any, at time.Time) File {
		data, _ := json.Marshal(p)
		return File{kind, append([]byte("branch "+r.Branch+"\n"), data...), at}
	}
	stop := func(reply string, at time.Time, tasks ...map[string]any) File {
		p := base("Stop")
		p["last_assistant_message"] = reply
		if len(tasks) > 0 {
			p["background_tasks"] = tasks
		}
		return file("stop", p, at)
	}
	reply := d.Reply
	if reply == "" {
		reply = r.Last
	}
	before := at.Add(-time.Millisecond)
	if !d.ReplyAt.IsZero() && d.ReplyAt.Before(at) {
		before = d.ReplyAt
	}
	var out []File
	switch r.State {
	case state.Working:
		prompt := base("UserPromptSubmit")
		prompt["prompt"] = r.Prompt
		if r.Background {
			tool := base("PostToolUse")
			tool["tool_name"], tool["tool_response"] = "Agent", map[string]any{"status": "async_launched", "agentId": "sub1"}
			latest := r.Latest
			if latest.IsZero() {
				latest = at
			}
			return append(out, file("prompt", prompt, at), file("answer", tool, at),
				stop(reply, latest, map[string]any{"id": "sub1", "type": "local_agent", "status": "running"}))
		}
		if reply != "" {
			out = append(out, stop(reply, before))
		}
		return append(out, file("prompt", prompt, at))
	case state.Done, state.Question:
		if r.State == state.Question && !state.IsQuestion(reply) {
			reply += "?"
		}
		var tasks []map[string]any
		for i, t := range r.OwnTasks {
			task := map[string]any{"id": fmt.Sprintf("b%d", i+1), "type": t.Type, "status": "running"}
			if t.Type == state.ShellTask {
				task["command"] = t.Text
			} else {
				task["description"] = t.Text
			}
			tasks = append(tasks, task)
		}
		at := at
		if !d.ReplyAt.IsZero() {
			at = d.ReplyAt
		}
		return append(out, stop(reply, at, tasks...))
	case state.NeedsInput:
		if last := firstOf(d.Reply, r.LastStop); last != "" {
			out = append(out, stop(last, before))
		}
		return append(out, file("dialog", dialogPayload(base, r, d.Ask), at))
	case state.Ended:
		if reply != "" {
			out = append(out, stop(reply, before))
		}
		return append(out, file("end", base("SessionEnd"), at))
	}
	return nil
}

// dialogPayload is Claude's payload of the dialog that asks ask, of report
// r (base makes a payload of an event): a question dialog, a permission
// prompt, an MCP elicitation's form, or with no ask a Notification that
// says r's last message.
func dialogPayload(base func(string) map[string]any, r HookState, ask *state.Ask) map[string]any {
	var p map[string]any
	switch {
	case ask != nil && len(ask.Questions) > 0:
		p = base("PermissionRequest")
		var qs []map[string]any
		for _, q := range ask.Questions {
			var opts []map[string]any
			for _, o := range q.Options {
				opts = append(opts, map[string]any{"label": o.Label, "description": o.Description})
			}
			qs = append(qs, map[string]any{"question": q.Question, "header": q.Header, "multiSelect": q.MultiSelect, "options": opts})
		}
		p["tool_name"], p["tool_input"] = firstOf(ask.Tool, state.AskTool), map[string]any{"questions": qs}
	case ask != nil && ask.Server != "":
		p = base("Elicitation")
		// The form's fields in their order, which a JSON object of Go's
		// would sort.
		props, required := "", []string{}
		for i, field := range ask.Fields {
			prop, _ := json.Marshal(fieldSchema(field))
			name, _ := json.Marshal(field.Name)
			if i > 0 {
				props += ","
			}
			props += string(name) + ":" + string(prop)
			if field.Required {
				required = append(required, field.Name)
			}
		}
		req, _ := json.Marshal(required)
		p["mcp_server_name"], p["message"] = ask.Server, ask.Message
		if ask.URL != "" {
			p["url"] = ask.URL
		}
		p["requested_schema"] = json.RawMessage(`{"type":"object","properties":{` + props + `},"required":` + string(req) + `}`)
	case ask != nil && ask.Tool != "":
		p = base("PermissionRequest")
		p["tool_name"], p["tool_input"] = ask.Tool, map[string]any{"description": ask.Description, "command": ask.Command}
	default:
		p = base("Notification")
		p["message"] = r.Last
		if ask != nil && ask.Message != "" {
			p["message"] = ask.Message
		}
	}
	if r.SubagentDialog {
		p["agent_id"] = "sub1"
	}
	return p
}

// fieldSchema is the schema of a field of an MCP elicitation's form, as
// Claude gives it: its options as an enum, or with their titles as oneOf.
func fieldSchema(field state.AskField) map[string]any {
	p := map[string]any{"type": field.Type}
	if field.Title != "" {
		p["title"] = field.Title
	}
	if field.Description != "" {
		p["description"] = field.Description
	}
	var values []string
	var oneOf []map[string]any
	titled := false
	for _, o := range field.Options {
		values = append(values, o.Value)
		oneOf = append(oneOf, map[string]any{"const": o.Value, "title": o.Title})
		titled = titled || o.Title != ""
	}
	switch {
	case titled:
		p["oneOf"] = oneOf
	case len(values) > 0:
		p["enum"] = values
	}
	if field.Default != nil {
		p["default"] = field.Default
	}
	return p
}

// firstOf is the first of ss that is not empty.
func firstOf(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
