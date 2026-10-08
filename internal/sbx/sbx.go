// Package sbx talks to Docker Sandboxes (design §3.6).
package sbx

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/rkrysinski/hq/internal/platform"
	"github.com/rkrysinski/hq/internal/proc"
)

// Sandbox is one sandbox as sbx ls reports it.
type Sandbox struct {
	Name       string   `json:"name"`
	Agent      string   `json:"agent"`
	Status     string   `json:"status"`
	Workspaces []string `json:"workspaces"`
}

// Running reports whether the sandbox is running.
func (s Sandbox) Running() bool { return s.Status == "running" }

// Client runs the sbx command. Paths cross the platform at this boundary:
// workspaces come back from sbx as hq sees them, and the workspace hq gives
// goes to sbx as sbx understands it (design §3.10).
type Client struct {
	Run      proc.Runner
	Platform platform.Platform
}

// List returns every sandbox.
func (c Client) List() ([]Sandbox, error) {
	out, err := c.run("ls", "--json")
	if err != nil {
		return nil, err
	}
	var v struct {
		Sandboxes []Sandbox `json:"sandboxes"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return nil, fmt.Errorf("sbx ls: %w", err)
	}
	for _, s := range v.Sandboxes {
		for i, ws := range s.Workspaces {
			if s.Workspaces[i], err = c.Platform.FromSbx(ws); err != nil {
				return nil, err
			}
		}
	}
	return v.Sandboxes, nil
}

// Options are what a sandbox is created with besides its workspace; the
// zero value is sbx's defaults.
type Options struct {
	Template  string   // the image, sbx create --template
	StaticMCP []string // MCP servers registered with sbx mcp add, --static-mcp
}

// Create creates a Claude sandbox for the workspace under sbx's default name.
func (c Client) Create(workspace string, o Options) error {
	ws, err := c.Platform.ToSbx(workspace)
	if err != nil {
		return err
	}
	args := []string{"create", "--quiet"}
	if o.Template != "" {
		args = append(args, "--template", o.Template)
	}
	if len(o.StaticMCP) > 0 {
		args = append(args, "--static-mcp", strings.Join(o.StaticMCP, ","))
	}
	_, err = c.run(append(args, "claude", ws)...)
	return err
}

// Stop stops a sandbox, ending every session in it; its state is kept.
func (c Client) Stop(sandbox string) error {
	_, err := c.run("stop", sandbox)
	return err
}

// Remove deletes a sandbox and its state without sbx asking again (hq has
// asked).
func (c Client) Remove(sandbox string) error {
	_, err := c.run("rm", "--force", sandbox)
	return err
}

// Exec runs a command inside a running sandbox.
func (c Client) Exec(sandbox string, args ...string) error {
	_, err := c.run(append([]string{"exec", sandbox}, args...)...)
	return err
}

// RunArgv is the command that runs one more Claude session in a sandbox,
// starting the sandbox when it is stopped; agentArgs go to Claude.
func (c Client) RunArgv(sandbox string, agentArgs ...string) []string {
	return append([]string{c.bin(), "run", "--name", sandbox, "--"}, agentArgs...)
}

func (c Client) bin() string { return c.Platform.SbxCommand() }

// run runs sbx; a failure carries sbx's own error and remedy (Failure).
func (c Client) run(args ...string) ([]byte, error) {
	out, err := c.Run.Run(c.bin(), args...)
	var pe *proc.Error
	if errors.As(err, &pe) {
		if msg := Failure(pe.Stderr); msg != "" {
			failed := *pe
			failed.Msg = msg
			return out, &failed
		}
	}
	return out, err
}

// Failure is what sbx's stderr says went wrong, as one line: its "error:"
// line, not a warning or progress printed before it, followed by the remedy
// sbx prints under it ("  try: sbx login"). Without an "error:" line it is
// the first line that is not a warning; "" when stderr is empty.
func Failure(stderr string) string {
	lines := strings.Split(strings.ReplaceAll(stderr, "\r", ""), "\n")
	at := -1
	for i, l := range lines {
		l = strings.TrimSpace(l)
		if hasPrefixFold(l, "error:") {
			at = i
			break
		}
		if at < 0 && l != "" && !hasPrefixFold(l, "warn") {
			at = i
		}
	}
	if at < 0 {
		for i, l := range lines {
			if strings.TrimSpace(l) != "" {
				return strings.TrimSpace(lines[i])
			}
		}
		return ""
	}
	msg := strings.TrimSpace(lines[at])
	if !hasPrefixFold(msg, "error:") {
		return msg
	}
	// The remedy: the lines sbx indents under its error.
	for _, l := range lines[at+1:] {
		if l == strings.TrimLeft(l, " \t") || strings.TrimSpace(l) == "" {
			break
		}
		msg += "; " + strings.TrimSpace(l)
	}
	return msg
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// ByWorkspace returns the sandbox whose primary workspace is path, compared
// with same (which may resolve symlinks), or false.
func ByWorkspace(all []Sandbox, path string, same func(a, b string) bool) (Sandbox, bool) {
	for _, s := range all {
		if len(s.Workspaces) > 0 && same(s.Workspaces[0], path) {
			return s, true
		}
	}
	return Sandbox{}, false
}
