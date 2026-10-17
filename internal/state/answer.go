package state

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// An answer to a question dialog (AskUserQuestion) reaches Claude through
// the dialog's own hook (design §3.4, ADR 0013): the hook of kind ask holds
// on the dialog's PermissionRequest, beside the hook that reports it, and
// prints the answer hq leaves in the agent's inbox as its decision. hq
// writes the decision whole, JSON-encoded, as it writes a message (ADR
// 0012), so the hook only copies it. The answer's file is named for the
// dialog's report (answerName), so the hook of another dialog never takes
// it; whoever renames it out of the inbox first, the hook or hq taking it
// back, has it.

// answerPrefix starts the name of an answer's file in the inbox: never a
// digit, so no delivery of messages takes it, nor a dot.
const answerPrefix = "answer."

// Questions is the question dialog an agent's state file reports open, the
// agent's own AskUserQuestion: what it asks, as hq read shows it, and what
// an answer to it needs.
type Questions struct {
	Ask *Ask
	// Since is when the dialog was reported, the moment its id comes from
	// (DialogID); zero when the questions were not read from a file.
	Since time.Time
	raw   []rawQuestion              // the questions as Claude gave them
	input map[string]json.RawMessage // the tool's input, the answers to be added
	name  string                     // the name of the answer's file (answerName)
}

// rawQuestion is a question of AskUserQuestion's input, unstripped: the
// answers are keyed by its text and name its labels as Claude gave them.
type rawQuestion struct {
	Question    string `json:"question"`
	MultiSelect bool   `json:"multiSelect"`
	Options     []struct {
		Label string `json:"label"`
	} `json:"options"`
}

// Answer is the answer to one question: the numbers of its options (from
// 1), or free text.
type Answer struct {
	Options []int
	Text    string
}

// DialogID is the id of the dialog reported at since: six hex characters
// of that moment, which keys the open dialog (design §3.4); empty for no
// moment.
func DialogID(since time.Time) string {
	if since.IsZero() {
		return ""
	}
	h := fnv.New32a()
	h.Write([]byte(strconv.FormatInt(since.UnixNano(), 10)))
	return fmt.Sprintf("%06x", h.Sum32()&0xffffff)
}

// ReadQuestions returns the question dialog the state file of agent id of
// the repository at root reports open, dated by the file.
func ReadQuestions(root, id string) (Questions, error) {
	if !idRE.MatchString(id) {
		return Questions{}, errors.New("not an agent id")
	}
	data, info, err := readData(filepath.Join(Dir(root), id))
	if err != nil {
		return Questions{}, err
	}
	q, err := ParseQuestions(data)
	q.Since = info.ModTime()
	return q, err
}

// ParseQuestions reads the state file latest as the question dialog it
// reports: a PermissionRequest of AskUserQuestion, not a subagent's.
func ParseQuestions(latest []byte) (Questions, error) {
	_, event := splitHeader(latest)
	var p struct {
		fullPayload
		Input map[string]json.RawMessage `json:"tool_input"`
	}
	if json.Unmarshal(event, &p) != nil || p.Event != "PermissionRequest" || p.ToolName != AskTool || p.AgentID != "" {
		return Questions{}, errors.New("no question dialog of the agent's own is open")
	}
	_ = json.Unmarshal(event, &p.fullPayload)
	q := Questions{Ask: p.ask(), input: p.Input, name: answerName(latest)}
	if json.Unmarshal(p.Input["questions"], &q.raw) != nil || len(q.raw) == 0 {
		return Questions{}, errors.New("the dialog asks no questions")
	}
	return q, nil
}

// answerName names the answer's file for the dialog whose report is the
// state file data: by the POSIX cksum of the file and its length, which
// the hook takes of its payload as it wrote it, the same bytes.
func answerName(data []byte) string {
	return fmt.Sprintf("%s%d.%d", answerPrefix, cksum(data), len(data))
}

// cksum is the CRC of POSIX cksum: polynomial 0x04C11DB7, the length
// appended, complemented.
func cksum(data []byte) uint32 {
	var crc uint32
	add := func(b byte) {
		crc ^= uint32(b) << 24
		for range 8 {
			if crc&0x80000000 != 0 {
				crc = crc<<1 ^ 0x04c11db7
			} else {
				crc <<= 1
			}
		}
	}
	for _, b := range data {
		add(b)
	}
	for n := len(data); n > 0; n >>= 8 {
		add(byte(n))
	}
	return ^crc
}

// Decision is the hook's decision that gives answers, one per question in
// the dialog's order, and what Claude then reports it got for each
// question, by its text: an allow whose updated input is the tool's input
// with the answers added, keyed by question text, a label, a list of
// labels for a multi-select question, which Claude reports joined with
// ", ", or free text, stripped as a message is (CleanMessage) (#89). An answer that does not fit the
// dialog is refused whole.
func (q Questions) Decision(answers []Answer) (decision []byte, sent map[string]string, err error) {
	if len(answers) != len(q.raw) {
		return nil, nil, fmt.Errorf("the dialog has %s, %s given: one per question, in its order", count(len(q.raw), "question"), count(len(answers), "answer"))
	}
	given := map[string]any{}
	sent = map[string]string{}
	for i, a := range answers {
		rq := q.raw[i]
		name := Clean(rq.Question)
		switch {
		case len(a.Options) > 0 && a.Text != "":
			return nil, nil, fmt.Errorf("%s: an option or text, not both", name)
		case len(a.Options) == 0:
			// Stripped as a message is: it crosses into the sandbox.
			a.Text = CleanMessage(a.Text)
			if a.Text == "" {
				return nil, nil, fmt.Errorf("%s: no answer", name)
			}
			if len(a.Text) > MaxMessage {
				return nil, nil, fmt.Errorf("%s: text too long: at most %d bytes", name, MaxMessage)
			}
			given[rq.Question], sent[rq.Question] = a.Text, a.Text
			continue
		case len(a.Options) > 1 && !rq.MultiSelect:
			return nil, nil, fmt.Errorf("%s takes one option, %d given", name, len(a.Options))
		}
		labels := []string{}
		seen := map[int]bool{}
		for _, n := range a.Options {
			if n < 1 || n > len(rq.Options) {
				return nil, nil, fmt.Errorf("%s has no option %d: it has %d", name, n, len(rq.Options))
			}
			if seen[n] {
				return nil, nil, fmt.Errorf("%s: option %d twice", name, n)
			}
			seen[n] = true
			labels = append(labels, rq.Options[n-1].Label)
		}
		sent[rq.Question] = strings.Join(labels, ", ")
		if rq.MultiSelect {
			given[rq.Question] = labels
		} else {
			given[rq.Question] = labels[0]
		}
	}
	input := map[string]any{}
	for k, v := range q.input {
		input[k] = v
	}
	input["answers"] = given
	return encodeDecision(map[string]any{"behavior": "allow", "updatedInput": input}), sent, nil
}

// count is n things, a thing for one.
func count(n int, thing string) string {
	if n == 1 {
		return "1 " + thing
	}
	return fmt.Sprintf("%d %ss", n, thing)
}

// Cancel is the hook's decision that cancels the dialog as Esc does: a
// deny that interrupts the turn, which Claude ends with "User declined to
// answer questions" and no Stop (#89, case 5).
func (q Questions) Cancel() []byte {
	return encodeDecision(map[string]any{"behavior": "deny", "message": "The user cancelled the questions through hq", "interrupt": true})
}

// encodeDecision is the hook output of a PermissionRequest decision, on
// one line, <, > and & as they are.
func encodeDecision(decision map[string]any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "PermissionRequest", "decision": decision}})
	return bytes.TrimSpace(b.Bytes())
}

// ErrAnswerWaiting is LeaveAnswer's error while an answer to the same
// dialog waits in the inbox: another hq process answers it.
var ErrAnswerWaiting = errors.New("another answer to this dialog is on its way")

// LeaveAnswer leaves decision, Decision's or Cancel's, as the answer to the
// dialog q for agent id of the repository at root, for its hook to take.
// It is written beside the inbox's files and linked into place, so a hook
// never reads half of it and one already waiting is never replaced
// (ErrAnswerWaiting). Answers left for other dialogs, which no hook takes
// any more, go.
func LeaveAnswer(root, id string, q Questions, decision []byte) error {
	if !idRE.MatchString(id) || q.name == "" {
		return errors.New("no dialog to answer")
	}
	dir := InboxDir(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if old, _ := filepath.Glob(filepath.Join(dir, answerPrefix+"*")); len(old) > 0 {
		for _, o := range old {
			if filepath.Base(o) != q.name {
				_ = os.Remove(o)
			}
		}
	}
	r := make([]byte, 4)
	_, _ = rand.Read(r)
	tmp := filepath.Join(dir, "."+q.name+"."+hex.EncodeToString(r))
	defer os.Remove(tmp)
	// One line, as the hook prints it.
	if err := os.WriteFile(tmp, append(decision, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Link(tmp, filepath.Join(dir, q.name)); err != nil {
		if errors.Is(err, os.ErrExist) {
			return ErrAnswerWaiting
		}
		return err
	}
	return nil
}

// AnswerTaken reports whether the answer to q is no longer in the inbox of
// agent id: its hook has taken it.
func AnswerTaken(root, id string, q Questions) bool {
	if !idRE.MatchString(id) || q.name == "" {
		return false
	}
	_, err := os.Lstat(filepath.Join(InboxDir(root, id), q.name))
	return errors.Is(err, os.ErrNotExist)
}

// WithdrawAnswer takes the answer to q back out of the inbox of agent id,
// and reports whether it did: false when its hook took it first.
func WithdrawAnswer(root, id string, q Questions) bool {
	if !idRE.MatchString(id) || q.name == "" {
		return false
	}
	dir := InboxDir(root, id)
	r := make([]byte, 4)
	_, _ = rand.Read(r)
	back := filepath.Join(dir, ".withdrawn-"+hex.EncodeToString(r))
	if os.Rename(filepath.Join(dir, q.name), back) != nil {
		return false
	}
	_ = os.Remove(back)
	return true
}

// ReadAnswers returns what Claude reports it got for a question dialog of
// agent id, when the agent's state file is the end of the dialog's tool
// (ParseAnswers).
func ReadAnswers(root, id string) (map[string]string, bool) {
	if !idRE.MatchString(id) {
		return nil, false
	}
	data, _, err := readData(filepath.Join(Dir(root), id))
	if err != nil {
		return nil, false
	}
	return ParseAnswers(data)
}

// ParseAnswers reads the state file latest as the end of AskUserQuestion,
// which the hook writes only when it closes the dialog, and returns the
// answers Claude reports, by question text, each one string; ok is false
// for any other event.
func ParseAnswers(latest []byte) (map[string]string, bool) {
	_, event := splitHeader(latest)
	var p struct {
		Event    string `json:"hook_event_name"`
		ToolName string `json:"tool_name"`
		Input    struct {
			Answers map[string]string `json:"answers"`
		} `json:"tool_input"`
	}
	if json.Unmarshal(event, &p) != nil || p.Event != "PostToolUse" || p.ToolName != AskTool {
		return nil, false
	}
	if p.Input.Answers == nil {
		p.Input.Answers = map[string]string{}
	}
	return p.Input.Answers, true
}
