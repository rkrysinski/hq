// Package sbx talks to Docker Sandboxes (design §3.6).
package sbx

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
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
	Name      string   // the sandbox's name, --name; sbx's own when empty
	Template  string   // the image, sbx create --template
	StaticMCP []string // MCP servers registered with sbx mcp add, --static-mcp
	// Model and Provider are the model sbx serves the agent's API calls
	// from, --model and --provider (#135).
	Model, Provider string
}

// Create creates a Claude sandbox for the workspace. sbx create takes no
// model, so one with a model or provider is created by sbx run --detached,
// which starts it without a session; given to a sandbox that exists,
// --model would recreate it, so it is only ever given here.
func (c Client) Create(workspace string, o Options) error {
	ws, err := c.Platform.ToSbx(workspace)
	if err != nil {
		return err
	}
	withModel := o.Model != "" || o.Provider != ""
	args := []string{"create", "--quiet"}
	if withModel {
		args = []string{"run", "--detached"}
	}
	if o.Name != "" {
		args = append(args, "--name", o.Name)
	}
	if o.Template != "" {
		args = append(args, "--template", o.Template)
	}
	if len(o.StaticMCP) > 0 {
		args = append(args, "--static-mcp", strings.Join(o.StaticMCP, ","))
	}
	if o.Model != "" {
		args = append(args, "--model", o.Model)
	}
	if o.Provider != "" {
		args = append(args, "--provider", o.Provider)
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

// Output runs a command inside a running sandbox and returns what it
// printed on its standard output.
func (c Client) Output(sandbox string, args ...string) ([]byte, error) {
	return c.run(append([]string{"exec", sandbox}, args...)...)
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

// DefaultSetup is the setup whose sandbox has sbx's own name.
const DefaultSetup = "default"

// setupPrefix starts the name of every sandbox of a setup but the default.
const setupPrefix = "hq-"

// Find returns the sandbox of a setup for the repository at path: among
// the sandboxes whose primary workspace is path, compared with same (which
// may resolve symlinks), the default's is the first whose name does not
// start with hq-, whatever its name, so one made by hand is adopted; another
// setup's is the one named SandboxName(setup, base of path) (#135).
func Find(all []Sandbox, path, setup string, same func(a, b string) bool) (Sandbox, bool) {
	want := SandboxName(setup, filepath.Base(path))
	for _, s := range all {
		if len(s.Workspaces) == 0 || !same(s.Workspaces[0], path) {
			continue
		}
		if want == "" && !strings.HasPrefix(s.Name, setupPrefix) || want != "" && s.Name == want {
			return s, true
		}
	}
	return Sandbox{}, false
}

// SandboxName is the name hq gives the sandbox of a setup for a repository
// in directory dir: hq-<setup>-<dir>, dir cleaned as sbx cleans it for the
// names it gives (lowercased, anything but letters, digits, hyphens and
// periods a hyphen). It is "" for the default setup, whose sandbox has
// sbx's own name.
func SandboxName(setup, dir string) string {
	if setup == DefaultSetup {
		return ""
	}
	return setupPrefix + setup + "-" + cleanDir(dir)
}

// SetupOf is the setup of the sandbox named name for a repository in
// directory dir: what stands between hq- and -<dir>, the default for any
// other name.
func SetupOf(name, dir string) string {
	rest, ok := strings.CutPrefix(name, setupPrefix)
	if !ok {
		return DefaultSetup
	}
	setup, ok := strings.CutSuffix(rest, "-"+cleanDir(dir))
	if !ok || setup == "" {
		return DefaultSetup
	}
	return setup
}

func cleanDir(dir string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '.':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 'a' - 'A'
		}
		return '-'
	}, dir)
}
