package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Detail is what hq read shows of an agent beyond its report (spec §4.1):
// its last reply in full and what its latest event asks.
type Detail struct {
	Reply string // the last reply in full, its lines kept, stripped (design §7.3)
	Ask   *Ask   // what the latest event asks: an open dialog or a Notification; nil when nothing
	// ReplyAt is when the reply was written: the time of the latest Stop
	// event, by which a page of the reply is tied to its report (spec
	// §4.1); zero when there is none.
	ReplyAt time.Time
}

// Ask is what an agent is asked: the questions of a question dialog, the
// tool and command of a permission prompt, the form of an MCP elicitation,
// or a message.
type Ask struct {
	Tool        string        `json:"tool,omitempty"`
	Description string        `json:"description,omitempty"`
	Command     string        `json:"command,omitempty"` // the command, file or URL the tool would use
	Questions   []AskQuestion `json:"questions,omitempty"`
	// Server is the MCP server whose elicitation asks; Message is then its
	// message, and Fields its form's fields, in the form's order, or URL the
	// page a URL-mode one sends the user to (#100).
	Server  string     `json:"server,omitempty"`
	Message string     `json:"message,omitempty"`
	Fields  []AskField `json:"fields,omitempty"`
	URL     string     `json:"url,omitempty"`
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

// AskField is one field of an MCP elicitation's form.
type AskField struct {
	Name        string           `json:"name"`
	Title       string           `json:"title,omitempty"`
	Description string           `json:"description,omitempty"`
	Type        string           `json:"type"` // string, number, integer or boolean
	Required    bool             `json:"required,omitempty"`
	Options     []AskFieldOption `json:"options,omitempty"` // none for a free field
	Default     any              `json:"default,omitempty"` // a string, a number or a boolean
}

// AskFieldOption is one value an AskField offers, with the title the form
// shows for it, if any.
type AskFieldOption struct {
	Value string `json:"value"`
	Title string `json:"title,omitempty"`
}

// ReadDetail returns the detail of agent id of the repository at root:
// empty until the agent's first event. A reply in a state file too large
// to read is TooLarge, never nothing (#64).
func ReadDetail(root, id string) Detail {
	if !idRE.MatchString(id) {
		return Detail{}
	}
	path := filepath.Join(Dir(root), id)
	latest, info, err := readData(path)
	lastStop, stopInfo, stopErr := readData(path + ".stop")
	d := ParseDetail(latest, lastStop)
	// The hook keeps every Stop it reports in .stop, so its time is the
	// reply's; before one, a Stop could only be the state file itself.
	if stopInfo == nil {
		stopInfo, stopErr = info, err
	}
	if stopInfo != nil {
		d.ReplyAt = stopInfo.ModTime()
	}
	var big tooLargeError
	// The latest event too large is the reply itself, whatever came before.
	if errors.As(err, &big) || (errors.As(stopErr, &big) && d.Reply == "") {
		d.Reply = TooLarge(big.size)
	}
	return d
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
	case "Elicitation":
		d.Ask = p.elicitation()
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
	URL    string          `json:"url"`
	Schema json.RawMessage `json:"requested_schema"`
	Input  struct {
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
	if p.ToolName == AskTool && len(p.Input.Questions) > 0 {
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

// elicitation is what an MCP elicitation asks: its server and message, and
// the fields of its form, or the URL of a URL-mode one.
func (p fullPayload) elicitation() *Ask {
	return &Ask{Server: Clean(p.Server), Message: Clean(p.Message), Fields: fields(p.Schema), URL: Clean(p.URL)}
}

// fields are the fields of a form's schema (requested_schema), in the
// order of its properties. A property hq cannot read in full, of another
// type than a string, a number, an integer or a boolean (a list, a nested
// object) or with an option that is no string, is left out; keywords it
// does not know are ignored. A schema it cannot read gives none.
func fields(schema json.RawMessage) []AskField {
	var s struct {
		Properties json.RawMessage `json:"properties"`
		Required   []string        `json:"required"`
	}
	if json.Unmarshal(schema, &s) != nil {
		return nil
	}
	names, props := ordered(s.Properties)
	var fs []AskField
	for _, name := range names {
		var p struct {
			Type        string   `json:"type"`
			Title       string   `json:"title"`
			Description string   `json:"description"`
			Enum        []string `json:"enum"`
			EnumNames   []string `json:"enumNames"`
			OneOf       []struct {
				Const string `json:"const"`
				Title string `json:"title"`
			} `json:"oneOf"`
			Default any `json:"default"`
		}
		if json.Unmarshal(props[name], &p) != nil {
			continue
		}
		switch p.Type {
		case "string", "number", "integer", "boolean":
		default:
			continue
		}
		f := AskField{Name: Clean(name), Title: Clean(p.Title), Description: Clean(p.Description), Type: p.Type, Required: slices.Contains(s.Required, name)}
		for i, v := range p.Enum {
			o := AskFieldOption{Value: Clean(v)}
			if len(p.EnumNames) == len(p.Enum) {
				o.Title = Clean(p.EnumNames[i])
			}
			f.Options = append(f.Options, o)
		}
		for _, o := range p.OneOf {
			f.Options = append(f.Options, AskFieldOption{Value: Clean(o.Const), Title: Clean(o.Title)})
		}
		switch v := p.Default.(type) {
		case string:
			f.Default = Clean(v)
		case float64, bool:
			f.Default = v
		}
		fs = append(fs, f)
	}
	return fs
}

// ordered are the keys of a JSON object in the order it gives them, with
// their values; none when it is no object.
func ordered(object json.RawMessage) ([]string, map[string]json.RawMessage) {
	dec := json.NewDecoder(bytes.NewReader(object))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, nil
	}
	var keys []string
	values := map[string]json.RawMessage{}
	for dec.More() {
		t, err := dec.Token()
		key, ok := t.(string)
		var v json.RawMessage
		if err != nil || !ok || dec.Decode(&v) != nil {
			return nil, nil
		}
		if _, seen := values[key]; !seen {
			keys = append(keys, key)
		}
		values[key] = v
	}
	return keys, values
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
