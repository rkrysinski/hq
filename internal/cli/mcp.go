package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rkrysinski/hq/internal/desktop"
	"github.com/rkrysinski/hq/internal/version"
)

// maxWaitTimeout is the longest a wait tool call waits: Claude Desktop gives
// a local MCP server's tool call 60 s and does not extend it for progress
// notifications (design §3.12), so a longer wait would fail there instead
// of returning "nothing yet".
const maxWaitTimeout = DefaultWaitTimeout

// desktopEnv are the variables hq mcp install passes on to Claude Desktop's
// hq when they are set: PATH, which finds tmux, sbx and gh, and those that
// choose hq's tmux server and preferences.
var desktopEnv = []string{"PATH", "HQ_TMUX_SOCKET", "TMUX_TMPDIR", "XDG_CONFIG_HOME"}

// runMCP serves hq's tools to an MCP client over stdio, or with install
// adds hq to Claude Desktop (spec §4.1, design §3.12).
func runMCP(env Env, d deps, args []string) error {
	switch {
	case len(args) == 1 && args[0] == "install":
		return runMCPInstall(env, d)
	case len(args) != 0:
		extra := args[0]
		if extra == "install" {
			extra = args[1]
		}
		return usageErr("unexpected argument '%s' (usage: hq mcp [install])", extra)
	case d.canAsk(env.Stdin):
		return usageErr("hq mcp is started by Claude Desktop, not typed; add it there with: hq mcp install")
	}
	if err := d.serveMCP(newMCPServer(d)); err != nil {
		return envErr("mcp: %v", err)
	}
	return nil
}

// runMCPInstall sets hq up in Claude Desktop's configuration; when Claude
// Desktop's folder is not there or the file cannot be edited safely, it
// changes nothing, prints hq's entry for the user to add and fails with the
// reason.
func runMCPInstall(env Env, d deps) error {
	exe, err := d.executable()
	if err != nil {
		return envErr("cannot find the running hq: %v", err)
	}
	var vars []string
	for _, k := range desktopEnv {
		if v := d.getenv(k); v != "" {
			vars = append(vars, k+"="+v)
		}
	}
	server := d.desktopServer(exe, vars)
	path, err := d.desktopConfig()
	if err != nil {
		fmt.Fprint(env.Stdout, desktop.Snippet(server))
		return envErr("cannot find Claude Desktop's configuration (%v); add the entry above to it by hand, then quit and reopen Claude Desktop", err)
	}
	r, err := d.installDesktop(path, server)
	if errors.Is(err, desktop.ErrNoFolder) {
		fmt.Fprint(env.Stdout, desktop.Snippet(server))
		return envErr("Claude Desktop not found: there is no %s; install Claude Desktop and open it once, then run hq mcp install again (or add the entry above to its configuration by hand)", filepath.Dir(path))
	}
	if err != nil {
		fmt.Fprint(env.Stdout, desktop.Snippet(server))
		return envErr("cannot edit %s safely (%v); add the entry above to it by hand, then quit and reopen Claude Desktop", path, err)
	}
	switch r.Outcome {
	case desktop.Unchanged:
		fmt.Fprintf(env.Stdout, "hq is already set up in Claude Desktop (%s)\n", path)
		return nil
	case desktop.Added:
		fmt.Fprintf(env.Stdout, "added hq to Claude Desktop (%s)\n", path)
	default:
		fmt.Fprintf(env.Stdout, "updated hq in Claude Desktop (%s)\n", path)
	}
	if r.Backup != "" {
		fmt.Fprintf(env.Stdout, "the file as it was: %s\n", r.Backup)
	}
	fmt.Fprintln(env.Stdout, "quit and reopen Claude Desktop to load it")
	return nil
}

// mcpInstructions teach a supervisor how to work with hq's agents; the
// tools' descriptions repeat what each needs.
const mcpInstructions = `hq runs Claude Code agents, each working on one task in its repository, for the user. You supervise them: start them, follow them and give them feedback.

- Status comes from hq, never from asking an agent: list for every agent, read for one in full (its whole last reply, what it asks, messages waiting for it).
- To ask an agent something or give it feedback, send it a message. hq delivers it when the agent is ready, without interrupting it; its answer is its next reply: wait for the agent with send's next_since as since, then read it. Use now only for a course correction that must reach it within the turn it is working on.
- Every list, read, send and wait result carries next_since, the moment it was taken. Always pass the next_since of the most recent of them as wait's since: wait then returns every change after that moment, even one that happened before wait was called, and none twice. A wait from a later moment misses what happened in between, such as an agent that answered at once; to start watching with no result yet, call list first.
- To watch agents, call wait in a loop, each time with the next_since of the most recent result: it returns as soon as an agent is done, asks a question, needs input or ends, or returns no agents after its timeout; then call it again.
- An agent that needs input has a dialog open (a permission, or questions with options). Never try to answer it, with send or otherwise: tell the user which agent waits and what it asks; they answer it in hq (go shows them the agent).
- kill ends an agent's session for good: only when the user wants it.`

// Tool inputs.
type (
	mcpNewIn struct {
		Name   string `json:"name" jsonschema:"the agent's name: letters, digits, - and _, at most 32 characters, e.g. the issue number (42) or a slug (bok-17); free until that agent is killed"`
		Dir    string `json:"dir" jsonschema:"the git repository to work in: an absolute path on the user's machine (~/ is their home), e.g. /Users/me/work/app"`
		Prompt string `json:"prompt,omitempty" jsonschema:"the agent's first prompt: the task; without it the agent waits for a prompt (send one)"`
	}
	mcpListIn struct{}
	mcpNameIn struct {
		Name string `json:"name" jsonschema:"the agent's name, as list shows it"`
	}
	mcpWaitIn struct {
		Names   []string `json:"names,omitempty" jsonschema:"the agents to wait for; every agent when empty"`
		Since   string   `json:"since" jsonschema:"the next_since of the most recent list, read, send or wait result (e.g. 2026-09-28T10:25:01.25Z), or a duration back from now (10m); wait returns what changed after it"`
		Timeout int      `json:"timeout,omitempty" jsonschema:"seconds to wait before returning no agents; default and at most 50"`
	}
	mcpSendIn struct {
		Name string `json:"name" jsonschema:"the agent's name, as list shows it"`
		Text string `json:"text" jsonschema:"the message, as the user would type it to the agent; at most 8 KiB"`
		Now  bool   `json:"now,omitempty" jsonschema:"deliver within the turn it is working on, after its next tool call: for a course correction"`
	}
)

// newMCPServer is hq's MCP server: each tool runs the CLI command of its
// name, in this process, and returns what the command prints, or its error
// line as a tool error (design §3.12). Its commands run from the user's
// home: the directory Claude Desktop starts hq in means nothing to them.
func newMCPServer(d deps) *mcp.Server {
	d.getwd = d.home
	s := mcp.NewServer(&mcp.Implementation{Name: "hq", Title: "hq", Version: version.Version}, &mcp.ServerOptions{Instructions: mcpInstructions})
	no, yes := false, true

	mcp.AddTool(s, &mcp.Tool{
		Name: "new",
		Description: "Start an agent: a Claude Code session named name, in the repository at dir, in that repository's sandbox, with prompt as its first prompt. " +
			"It returns once the agent is starting; the agent then works on its own. Follow it with wait and read.",
		Annotations: &mcp.ToolAnnotations{Title: "Start an agent", DestructiveHint: &no, OpenWorldHint: &no},
	}, func(_ context.Context, _ *mcp.CallToolRequest, in mcpNewIn) (*mcp.CallToolResult, any, error) {
		dir, err := absDir(d, in.Dir)
		if err != nil {
			return toolResult("", err)
		}
		args := []string{in.Name, dir}
		if in.Prompt != "" {
			args = append(args, in.Prompt)
		}
		return callCommand(d, runNew, args...)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "list",
		Description: "List every agent as JSON (agents), those that need the user first: name, repo, branch, state, since and age_seconds (in that state), last (its last message, one line), sandbox and pending (messages waiting for it); and next_since, the moment of the list, to pass to wait as since. " +
			"States: starting, working, question (its reply ends with a question), needs input (a dialog is open), done (its turn ended), ended (its session is gone). " +
			"This is where status comes from: never ask an agent for its status.",
		Annotations: &mcp.ToolAnnotations{Title: "List agents", ReadOnlyHint: true, OpenWorldHint: &no},
	}, func(context.Context, *mcp.CallToolRequest, mcpListIn) (*mcp.CallToolResult, any, error) {
		return callCommand(d, runLs, "--json")
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "read",
		Description: "One agent in full, as JSON: what list gives, its worktree, its whole last reply, what it asks (asks: the open dialog's questions and options while it needs input, the question while in question; null otherwise), and next_since, the moment of the read, to pass to wait as since. " +
			"Read an agent when wait returns it, and to see its answer to a message you sent.",
		Annotations: &mcp.ToolAnnotations{Title: "Read an agent", ReadOnlyHint: true, OpenWorldHint: &no},
	}, func(_ context.Context, _ *mcp.CallToolRequest, in mcpNameIn) (*mcp.CallToolResult, any, error) {
		return callCommand(d, runRead, in.Name, "--json")
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "wait",
		Description: "Wait until one of the named agents (every agent when none is named) is done, asks a question, needs input or ends, and return those agents as list gives them, with next_since, as JSON. " +
			"After timeout seconds (default and at most 50) it returns no agents, which is not an error. " +
			"since is required: the next_since of the most recent list, read, send or wait result, so a change between that result and this call is not missed. " +
			"To watch agents, call it in a loop, passing each result's next_since as the next call's since: no change is then missed or returned twice. React to the agents it returns (read them), then call it again.",
		Annotations: &mcp.ToolAnnotations{Title: "Wait for agents", ReadOnlyHint: true, OpenWorldHint: &no},
	}, func(_ context.Context, _ *mcp.CallToolRequest, in mcpWaitIn) (*mcp.CallToolResult, any, error) {
		timeout := time.Duration(in.Timeout) * time.Second
		if timeout <= 0 || timeout > maxWaitTimeout {
			timeout = maxWaitTimeout
		}
		if in.Since == "" {
			return toolResult("", usageErr("since is required: the next_since of the most recent list, read, send or wait result; with none yet, call list first"))
		}
		args := append(append([]string{}, in.Names...), "--json", "--timeout", timeout.String(), "--since", in.Since)
		return callCommand(d, runWait, args...)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "send",
		Description: "Leave a message for an agent: a question or feedback, as the user would type it. hq delivers it when the agent is ready, never interrupting it: " +
			"an agent at work gets it when it would end its turn and goes on with it; one waiting at its prompt gets it as its next prompt; one with a dialog open gets it once the user has closed the dialog. " +
			"With now, an agent at work gets it after its next tool call, within the running turn. " +
			"It returns, as JSON, how the message goes (delivery: queued or delivered) and next_since, the moment before the message was left. The agent's answer is its next reply: wait for the agent with that next_since as since (it returns the answer even if the agent answered before wait was called), then read it. " +
			"Never use it to answer an agent's dialog (needs input): tell the user instead.",
		Annotations: &mcp.ToolAnnotations{Title: "Send a message", DestructiveHint: &no, OpenWorldHint: &no},
	}, func(_ context.Context, _ *mcp.CallToolRequest, in mcpSendIn) (*mcp.CallToolResult, any, error) {
		args := []string{in.Name, in.Text, "--json"}
		if in.Now {
			args = append(args, "--now")
		}
		return callCommand(d, runSend, args...)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "go",
		Description: "Show an agent to the user: dock its session in hq's dashboard and bring the dashboard's terminal window to the front. " +
			"For when the user wants to see or talk to the agent themselves, such as to answer its dialog. When no terminal shows the dashboard, the user opens it by running hq in one.",
		Annotations: &mcp.ToolAnnotations{Title: "Show an agent", DestructiveHint: &no, IdempotentHint: true, OpenWorldHint: &no},
	}, func(_ context.Context, _ *mcp.CallToolRequest, in mcpNameIn) (*mcp.CallToolResult, any, error) {
		return callCommand(d, runGo, in.Name)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "kill",
		Description: "End an agent's Claude session and remove it from hq; its conversation cannot be continued. Its sandbox, worktree and commits stay. " +
			"Only when the user wants it.",
		Annotations: &mcp.ToolAnnotations{Title: "Kill an agent", DestructiveHint: &yes, OpenWorldHint: &no},
	}, func(_ context.Context, _ *mcp.CallToolRequest, in mcpNameIn) (*mcp.CallToolResult, any, error) {
		// The client asks the user before a destructive tool runs, which
		// is the confirmation -y stands for.
		return callCommand(d, runKill, in.Name, "-y")
	})
	return s
}

// absDir is dir as hq new takes it from a tool: absolute, with ~/ the
// user's home, and existing, since there is no current directory to be
// relative to and a missing DIR would be taken for the PROMPT.
func absDir(d deps, dir string) (string, error) {
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		home, err := d.home()
		if err != nil {
			return "", envErr("no home directory: %v", err)
		}
		dir = filepath.Join(home, strings.TrimPrefix(dir, "~"))
	}
	if !filepath.IsAbs(dir) {
		return "", usageErr("dir '%s' is not an absolute path", dir)
	}
	if dir = filepath.Clean(dir); !d.isDir(dir) {
		return "", notFoundErr("no directory %s", dir)
	}
	return dir, nil
}

// callCommand runs a CLI command as hq would from a shell, with no terminal:
// what it prints is the tool's result, its error line a tool error.
func callCommand(d deps, run func(Env, deps, []string) error, args ...string) (*mcp.CallToolResult, any, error) {
	var out strings.Builder
	err := run(Env{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &out}, d, args)
	return toolResult(out.String(), err)
}

// toolResult is a command's output and outcome as a tool's result: an
// error adds its hq: line, as on stderr, and marks the result an error.
func toolResult(out string, err error) (*mcp.CallToolResult, any, error) {
	r := &mcp.CallToolResult{}
	if err != nil {
		if e := asError(err); e.Msg != "" {
			out += "hq: " + e.Msg + "\n"
		}
		r.IsError = true
	}
	r.Content = []mcp.Content{&mcp.TextContent{Text: out}}
	return r, nil, nil
}
