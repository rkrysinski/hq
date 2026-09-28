package state

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Detail is what hq read shows of an agent beyond its report (spec §4.1):
// its last reply in full and what its latest event asks.
type Detail struct {
	Reply string // the last reply in full, its lines kept, stripped (design §7.3)
	Ask   *Ask   // what the latest event asks: an open dialog or a Notification; nil when nothing
}

// Ask is what an agent is asked: the questions of a question dialog, the
// tool and command of a permission prompt, or a message.
type Ask struct {
	Tool        string        `json:"tool,omitempty"`
	Description string        `json:"description,omitempty"`
	Command     string        `json:"command,omitempty"` // the command, file or URL the tool would use
	Questions   []AskQuestion `json:"questions,omitempty"`
	Message     string        `json:"message,omitempty"`
}

// AskQuestion is one question of a question dialog (AskUserQuestion).
type AskQuestion struct {
	Header      string      `json:"header,omitempty"`
	Question    string      `json:"question"`
	Options     []AskOption `json:"options"`
	MultiSelect bool        `json:"multi_select"`
}

// AskOption is one answer an AskQuestion offers.
type AskOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// ReadDetail returns the detail of agent id of the repository at root:
// empty until the agent's first event.
func ReadDetail(root, id string) Detail {
	if !idRE.MatchString(id) {
		return Detail{}
	}
	path := filepath.Join(Dir(root), id)
	latest, _, _ := readData(path)
	lastStop, _, _ := readData(path + ".stop")
	return ParseDetail(latest, lastStop)
}

// ParseDetail derives the detail from the latest event and the latest Stop
// event (each empty when there is none).
func ParseDetail(latest, lastStop []byte) Detail {
	var d Detail
	_, latest = splitHeader(latest)
	var p fullPayload
	if json.Unmarshal(latest, &p) != nil {
		return d
	}
	var stop payload
	if _, lastStop = splitHeader(lastStop); json.Unmarshal(lastStop, &stop) == nil {
		d.Reply = CleanText(stop.AssistantMessage)
	}
	switch p.Event {
	case "Stop":
		d.Reply = CleanText(p.AssistantMessage)
	case "PermissionRequest":
		d.Ask = p.ask()
	case "Notification":
		if m := Clean(p.Message); m != "" {
			d.Ask = &Ask{Message: m}
		}
	}
	return d
}

// fullPayload is a hook event with what an open dialog shows in full; its
// tool_input shadows the payload's.
type fullPayload struct {
	payload
	Input struct {
		Description string `json:"description"`
		Command     string `json:"command"`
		FilePath    string `json:"file_path"`
		URL         string `json:"url"`
		Questions   []struct {
			Question    string `json:"question"`
			Header      string `json:"header"`
			MultiSelect bool   `json:"multiSelect"`
			Options     []struct {
				Label       string `json:"label"`
				Description string `json:"description"`
			} `json:"options"`
		} `json:"questions"`
	} `json:"tool_input"`
}

// ask is what an open dialog asks: every question with its options, or the
// tool a permission prompt is for with what it is about to do.
func (p fullPayload) ask() *Ask {
	a := &Ask{Tool: Clean(p.ToolName)}
	if p.ToolName == askTool && len(p.Input.Questions) > 0 {
		for _, q := range p.Input.Questions {
			out := AskQuestion{Header: Clean(q.Header), Question: Clean(q.Question), MultiSelect: q.MultiSelect, Options: []AskOption{}}
			for _, o := range q.Options {
				out.Options = append(out.Options, AskOption{Label: Clean(o.Label), Description: Clean(o.Description)})
			}
			a.Questions = append(a.Questions, out)
		}
		return a
	}
	in := p.Input
	a.Description = Clean(in.Description)
	for _, s := range []string{in.Command, in.FilePath, in.URL} {
		if s = Clean(s); s != "" {
			a.Command = s
			break
		}
	}
	return a
}

// CleanText is Clean for text shown in full: its lines are kept, each
// without escape sequences, control characters and trailing blanks, tabs
// as spaces, blank lines at either end dropped.
func CleanText(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRightFunc(cleanLine(l), unicode.IsSpace)
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}

// cleanLine strips one line as Clean does, keeping its spacing.
func cleanLine(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == 0x1b:
			i += escapeLen(s[i:])
			continue
		case r == '\t':
			b.WriteString("    ")
		case r == utf8.RuneError && size == 1, unicode.IsControl(r), r == 0x2028, r == 0x2029:
		default:
			b.WriteRune(r)
		}
		i += size
	}
	return b.String()
}
