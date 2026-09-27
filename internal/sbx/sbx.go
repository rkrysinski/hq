// Package sbx talks to Docker Sandboxes (design §3.6).
package sbx

import (
	"encoding/json"
	"fmt"

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
	out, err := c.Run.Run(c.bin(), "ls", "--json")
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

// Create creates a Claude sandbox for the workspace under sbx's default name.
func (c Client) Create(workspace string) error {
	ws, err := c.Platform.ToSbx(workspace)
	if err != nil {
		return err
	}
	_, err = c.Run.Run(c.bin(), "create", "--quiet", "claude", ws)
	return err
}

// Stop stops a sandbox, ending every session in it; its state is kept.
func (c Client) Stop(sandbox string) error {
	_, err := c.Run.Run(c.bin(), "stop", sandbox)
	return err
}

// Remove deletes a sandbox and its state without sbx asking again (hq has
// asked).
func (c Client) Remove(sandbox string) error {
	_, err := c.Run.Run(c.bin(), "rm", "--force", sandbox)
	return err
}

// Exec runs a command inside a running sandbox.
func (c Client) Exec(sandbox string, args ...string) error {
	_, err := c.Run.Run(c.bin(), append([]string{"exec", sandbox}, args...)...)
	return err
}

// RunArgv is the command that runs one more Claude session in a sandbox,
// starting the sandbox when it is stopped; agentArgs go to Claude.
func (c Client) RunArgv(sandbox string, agentArgs ...string) []string {
	return append([]string{c.bin(), "run", "--name", sandbox, "--"}, agentArgs...)
}

func (c Client) bin() string { return c.Platform.SbxCommand() }

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
