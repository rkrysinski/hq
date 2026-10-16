// Command elicit is a stdio MCP server for capturing Claude Code's screens
// (tools/capture/README.md). Its one tool, ask_color, asks the user to pick
// a color through an MCP elicitation (elicitation/create), then works on for
// -work before it returns, so the screen after the answer shows Claude at
// work on the tool. It is how #70 captured an elicitation open, answered
// and cancelled; tools/capture/capture.sh mcp builds it and writes the
// --mcp-config for it.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"os"
	"sync"
	"time"
)

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
}

type server struct {
	work    time.Duration
	mu      sync.Mutex
	out     *json.Encoder
	next    int
	pending map[string]chan json.RawMessage
}

func main() {
	work := flag.Duration("work", 20*time.Second, "how long the tool works on after the answer")
	flag.Parse()
	s := &server{work: *work, out: json.NewEncoder(os.Stdout), next: 1000, pending: map[string]chan json.RawMessage{}}
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 1<<20)
	for in.Scan() {
		var m struct {
			message
			Result json.RawMessage `json:"result"`
		}
		if json.Unmarshal(in.Bytes(), &m) != nil {
			continue
		}
		if m.Method == "" {
			s.answered(string(m.ID), m.Result)
			continue
		}
		s.handle(m.message)
	}
}

func (s *server) send(m message) {
	m.JSONRPC = "2.0"
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.out.Encode(m)
}

// ask sends a request to Claude and waits for its answer.
func (s *server) ask(method string, params any) json.RawMessage {
	s.mu.Lock()
	s.next++
	id, _ := json.Marshal(s.next)
	ch := make(chan json.RawMessage, 1)
	s.pending[string(id)] = ch
	s.mu.Unlock()
	p, _ := json.Marshal(params)
	s.send(message{ID: id, Method: method, Params: p})
	return <-ch
}

func (s *server) answered(id string, result json.RawMessage) {
	s.mu.Lock()
	ch, ok := s.pending[id]
	delete(s.pending, id)
	s.mu.Unlock()
	if ok {
		ch <- result
	}
}

func (s *server) handle(m message) {
	switch m.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(m.Params, &p)
		if p.ProtocolVersion == "" {
			p.ProtocolVersion = "2025-06-18"
		}
		s.send(message{ID: m.ID, Result: map[string]any{
			"protocolVersion": p.ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "elicit", "version": "0.1"},
		}})
	case "tools/list":
		s.send(message{ID: m.ID, Result: map[string]any{"tools": []any{map[string]any{
			"name":        "ask_color",
			"description": "Asks the user to pick a color (via an elicitation form) and returns it.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		}}}})
	case "tools/call":
		go s.askColor(m.ID)
	default:
		if m.ID != nil {
			s.send(message{ID: m.ID, Result: map[string]any{}})
		}
	}
}

func (s *server) askColor(id json.RawMessage) {
	answer := s.ask("elicitation/create", map[string]any{
		"message": "Which color should I use?",
		"requestedSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{"color": map[string]any{
				"type": "string", "title": "Color", "enum": []string{"red", "blue"}, "description": "Pick one",
			}},
			"required": []string{"color"},
		},
	})
	time.Sleep(s.work)
	s.send(message{ID: id, Result: map[string]any{"content": []any{map[string]any{
		"type": "text", "text": "elicitation answer: " + string(answer),
	}}}})
}
