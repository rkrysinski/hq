// Package sbx talks to Docker Sandboxes (design §3.6).
package sbx

import (
	"encoding/json"
	"fmt"

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

// Client runs the sbx command.
type Client struct {
	Run proc.Runner
	Bin string // the sbx command: "sbx", or "sbx.exe" from WSL
}

// List returns every sandbox.
func (c Client) List() ([]Sandbox, error) {
	out, err := c.Run.Run(c.Bin, "ls", "--json")
	if err != nil {
		return nil, err
	}
	var v struct {
		Sandboxes []Sandbox `json:"sandboxes"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return nil, fmt.Errorf("sbx ls: %w", err)
	}
	return v.Sandboxes, nil
}

// Create creates a Claude sandbox for the workspace under sbx's default name.
func (c Client) Create(workspace string) error {
	_, err := c.Run.Run(c.Bin, "create", "--quiet", "claude", workspace)
	return err
}

// Exec runs a command inside a running sandbox.
func (c Client) Exec(sandbox string, args ...string) error {
	_, err := c.Run.Run(c.Bin, append([]string{"exec", sandbox}, args...)...)
	return err
}

// RunArgv is the command that runs one more Claude session in a sandbox,
// starting the sandbox when it is stopped; agentArgs go to Claude.
func (c Client) RunArgv(sandbox string, agentArgs ...string) []string {
	return append([]string{c.Bin, "run", "--name", sandbox, "--"}, agentArgs...)
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
