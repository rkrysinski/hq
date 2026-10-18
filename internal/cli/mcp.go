package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rkrysinski/hq/internal/desktop"
	"github.com/rkrysinski/hq/internal/state"
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
- To ask an agent something or give it feedback, send it a message. hq delivers it when the agent is ready, without interrupting it; its answer is its next reply: the next wait returns the agent when it answers, then read it. Use now only for a course correction that must reach it within the turn it is working on.
- Watch from one moment: the next_since of your most recent wait or list result. Always pass it as wait's since: wait then returns every change after that moment, even one that happened before wait was called, such as an agent that answered your message at once, and none twice. Never take since from read, send or answer: their next_since is that one agent's, later than what the others did meanwhile, so a wait from it misses that. Name every agent you follow in each wait (or none, for every agent): a wait's next_since stands for the agents it waited on. To start watching with no wait or list result yet, call list first.
- The user is not notified when work you sent finishes or asks a question: the end of a turn whose prompts all came from you (new with a prompt, send) raises no desktop notification, whether or not you are waiting, because its result is for you. So wait for the agents you gave work to, read their replies and tell the user what matters. A turn the user prompted themselves, or sent a message into, still notifies them.
- To watch agents, call wait in a loop, each time with the next_since of the most recent wait or list result: it returns as soon as an agent is done, asks a question, needs input or ends, with every other that does so within a moment of it, or returns no agents after its timeout; then call it again.
- An agent done or in question with own_tasks: a shell command or watch of its own still runs, which it started and left running at its turn end. Claude takes another turn when one ends, so wait for it again if you need that result; a dev server may never end. The list is that turn end's and may be stale until its next turn end.
- An agent that needs input has a dialog open (a permission, questions with options, or an MCP server's form; read gives them, with the dialog's id). Never answer a permission or any other dialog, with send or otherwise: tell the user which agent waits and what it asks; they answer it in hq (go shows them the agent). A question dialog (asks with questions) is the exception: answer it with answer, only on the user's word. Tell the user which agent asks what, and pass on what they pick, or your pick when they told you to choose for them; never on your own initiative. hq notifies them of the dialog itself, also on a turn you started.
- kill ends an agent's session for good: only when the user wants it. Ask the user first, naming the agent, unless they have just told you to end it; then call kill with confirmed set to true.`

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
	mcpReadIn struct {
		Name   string `json:"name" jsonschema:"the agent's name, as list shows it"`
		Offset int    `json:"offset,omitempty" jsonschema:"where the reply's page starts: the next_offset of the read before; 0 or absent for the first page"`
		Since  string `json:"since,omitempty" jsonschema:"with offset: the next_since of the read that gave that next_offset, so the page is of the same reply"`
	}
	mcpWaitIn struct {
		Names   []string `json:"names,omitempty" jsonschema:"the agents to wait for; every agent when empty"`
		Since   string   `json:"since" jsonschema:"the next_since of your most recent wait or list result (e.g. 2026-09-28T10:25:01.25Z), never of read, send or answer, or a duration back from now (10m); wait returns what changed after it"`
		Timeout int      `json:"timeout,omitempty" jsonschema:"seconds to wait before returning no agents; default and at most 50"`
	}
	mcpKillIn struct {
		Name      string `json:"name" jsonschema:"the agent's name, as list shows it"`
		Confirmed bool   `json:"confirmed,omitempty" jsonschema:"true only when the user has said, in this conversation, to end this agent; otherwise ask them first, naming the agent, and call kill again once they agree"`
	}
	mcpAnswerIn struct {
		Name      string      `json:"name" jsonschema:"the agent's name, as list shows it"`
		Dialog    string      `json:"dialog" jsonschema:"the open dialog's id, as read and list give it (dialog)"`
		Answers   []mcpAnswer `json:"answers,omitempty" jsonschema:"one answer per question, in the dialog's order (the order read lists them)"`
		Cancel    bool        `json:"cancel,omitempty" jsonschema:"cancel the dialog as Esc does instead of answering: the agent's turn ends"`
		Confirmed bool        `json:"confirmed,omitempty" jsonschema:"true only when the user has said, in this conversation, what to pick, or told you to choose for them; otherwise ask them first, naming the agent and its question, and call answer again with their choice"`
	}
	mcpAnswer struct {
		Options []int  `json:"options,omitempty" jsonschema:"the numbers of the options picked, from 1, as read lists them; several only where the question is multi_select"`
		Text    string `json:"text,omitempty" jsonschema:"an answer in the user's own words, in place of the options"`
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
	// What its tools give agents is the supervisor's, not the user's.
	d.supervisor = true
	s := mcp.NewServer(&mcp.Implementation{Name: "hq", Title: "hq", Version: version.Version}, &mcp.ServerOptions{Instructions: mcpInstructions})
	no, yes := false, true

	mcp.AddTool(s, &mcp.Tool{
		Name: "new",
		Description: "Start an agent: a Claude Code session named name, in the repository at dir, in that repository's sandbox, with prompt as its first prompt. " +
			"It returns once the agent is starting; the agent then works on its own. Follow it with wait and read: " +
			"the user is not notified when the work you gave it with prompt finishes or asks a question, so wait for it and tell them.",
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
		Description: "List every agent as JSON (agents), those that need the user first: name, repo, branch, state, since and age_seconds (in that state), last (its last message, one line), sandbox, pending (messages waiting for it), dialog (while it needs input: the open dialog's id, for answer), own_tasks (while it is done or in question: the shell commands and watches of its own still running at that turn end, each with its type, shell or monitor, and text, the command or the watch's description; [] when none) and asks (what it asks: while it needs input the open dialog's questions and options, a permission's tool and command, or an MCP server's form (server, message and fields: each with its name, title, type, whether it is required, options and default; or the url it sends the user to); the question while in question; null otherwise); and next_since, the moment of the list, to pass to wait as since. " +
			"States: starting, working, question (its reply ends with a question), needs input (a dialog is open), done (its turn ended), ended (its session is gone). " +
			"This is where status comes from: never ask an agent for its status.",
		Annotations: &mcp.ToolAnnotations{Title: "List agents", ReadOnlyHint: true, OpenWorldHint: &no},
	}, func(context.Context, *mcp.CallToolRequest, mcpListIn) (*mcp.CallToolResult, any, error) {
		return callCommand(d, runLs, "--json")
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "read",
		Description: "One agent in full, as JSON: what list gives, what it asks and the open dialog's id included (asks: while it needs input the open dialog's questions and options, a permission's tool and command, or an MCP server's form (server, message and fields: each with its name, title, type, whether it is required, options and default; or the url it sends the user to); the question while in question; null otherwise), its worktree, its last reply, and next_since, the moment of the read, for its next page (below): never pass it to wait as since, it is this agent's alone. " +
			"A reply longer than 32 KiB comes in pages: reply is the page, reply_size the whole reply's size in bytes, and next_offset where the next page starts, null on the last page. " +
			"To read on, call read again with offset set to next_offset and since set to this read's next_since; if the agent has replied anew in between, it says the reply changed: read it again from its start. " +
			"Read an agent when wait returns it, and to see its answer to a message you sent. " +
			"An agent done or in question with own_tasks still runs a shell command or watch of its own: Claude takes another turn when one ends, so wait for it again if you need that result (a dev server may never end); the list is that turn end's and may be stale.",
		Annotations: &mcp.ToolAnnotations{Title: "Read an agent", ReadOnlyHint: true, OpenWorldHint: &no},
	}, func(_ context.Context, _ *mcp.CallToolRequest, in mcpReadIn) (*mcp.CallToolResult, any, error) {
		args := []string{in.Name, "--json"}
		if in.Offset != 0 {
			args = append(args, "--offset", strconv.Itoa(in.Offset))
		}
		if in.Since != "" {
			args = append(args, "--since", in.Since)
		}
		return callCommand(d, runRead, args...)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "wait",
		Description: "Wait until one of the named agents (every agent when none is named) is done, asks a question, needs input or ends, and return it, with every other that does so within a moment of it, as list gives them, with next_since, as JSON. " +
			"After timeout seconds (default and at most 50) it returns no agents, which is not an error. " +
			"since is required: the next_since of your most recent wait or list result, so a change between that result and this call is not missed; never that of read, send or answer, which is one agent's and later than what the others did meanwhile. " +
			"To watch agents, call it in a loop, passing each result's next_since as the next call's since and naming every agent you follow each time (or none, for every agent): no change is then missed or returned twice. React to the agents it returns (read them), then call it again. " +
			"An agent returned done or in question with own_tasks still runs a shell command or watch of its own: Claude takes another turn when one ends, so wait for it again if you need that result (a dev server may never end); the list is that turn end's and may be stale.",
		Annotations: &mcp.ToolAnnotations{Title: "Wait for agents", ReadOnlyHint: true, OpenWorldHint: &no},
	}, func(_ context.Context, _ *mcp.CallToolRequest, in mcpWaitIn) (*mcp.CallToolResult, any, error) {
		timeout := time.Duration(in.Timeout) * time.Second
		if timeout <= 0 || timeout > maxWaitTimeout {
			timeout = maxWaitTimeout
		}
		if in.Since == "" {
			return toolResult("", usageErr("since is required: the next_since of your most recent wait or list result; with none yet, call list first"))
		}
		args := append(append([]string{}, in.Names...), "--json", "--timeout", timeout.String(), "--since", in.Since)
		return callCommand(d, runWait, args...)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "send",
		Description: "Leave a message for an agent: a question or feedback, as the user would type it. hq delivers it when the agent is ready, never interrupting it: " +
			"an agent at work gets it when it would end its turn and goes on with it; one waiting at its prompt gets it as its next prompt; one with a dialog open gets it once the user has closed the dialog. " +
			"With now, an agent at work gets it after its next tool call, within the running turn. " +
			"It returns, as JSON, how the message goes (delivery: queued or delivered) and next_since, the moment before the message was left. The agent's answer is its next reply: your next wait, from your watch's since (the next_since of your most recent wait or list result), returns the agent when it answers, even if it answered before wait was called; then read it. This next_since is for a wait on this agent alone, never your watch's since. " +
			"The user is not notified when a turn your message started finishes or asks a question, so wait for it and tell them. " +
			"Never use it to answer an agent's dialog (needs input): for a question dialog use answer, on the user's word; for any other, tell the user.",
		Annotations: &mcp.ToolAnnotations{Title: "Send a message", DestructiveHint: &no, OpenWorldHint: &no},
	}, func(_ context.Context, _ *mcp.CallToolRequest, in mcpSendIn) (*mcp.CallToolResult, any, error) {
		args := []string{in.Name, in.Text, "--json"}
		if in.Now {
			args = append(args, "--now")
		}
		return callCommand(d, runSend, args...)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "answer",
		Description: "Answer an agent's question dialog (it needs input, and read's asks has questions with options), or cancel it as Esc does. " +
			"Only on the user's word: without confirmed set to true it answers nothing and says to ask the user. Never on your own initiative: tell the user which agent asks what, and pass on what they pick; when they tell you to choose for them, whether you may pick your own recommendation is part of what they told you. " +
			"Name the dialog by its id (read and list give it as dialog), so an answer never lands on a dialog that opened after you read; an answer for a dialog no longer open is refused with the agent's state. " +
			"answers holds one entry per question in the dialog's order: options, the numbers read lists (several only for a multi_select question), or text, an answer in the user's words. " +
			"Permission prompts, MCP elicitations and dialogs of Claude's own are refused: tell the user, who answers them at the agent (go shows it). " +
			"It returns once the agent has left the dialog, as JSON: its state and next_since, the moment before the answer was left: for a wait on this agent alone, never your watch's since.",
		Annotations: &mcp.ToolAnnotations{Title: "Answer a question dialog", DestructiveHint: &no, OpenWorldHint: &no},
	}, func(_ context.Context, _ *mcp.CallToolRequest, in mcpAnswerIn) (*mcp.CallToolResult, any, error) {
		return callCommand(d, func(env Env, d deps, _ []string) error {
			switch {
			// The user's word comes with the call, as for kill: the
			// supervisor never answers for them on its own.
			case !in.Confirmed:
				return usageErr("%s not answered: ask the user what to pick, naming the agent and its question, then call answer with confirmed set to true", in.Name)
			case in.Cancel && len(in.Answers) > 0:
				return usageErr("%s not answered: answers or cancel, not both", in.Name)
			case !in.Cancel && len(in.Answers) == 0:
				return usageErr("%s not answered: no answers; give one per question, or cancel", in.Name)
			}
			r := answerRequest{name: in.Name, dialog: in.Dialog, cancel: in.Cancel, asJSON: true}
			for _, a := range in.Answers {
				r.answers = append(r.answers, state.Answer{Options: a.Options, Text: a.Text})
			}
			return answer(env, d, r)
		})
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
			"Only when the user wants it: without confirmed set to true it ends nothing and says to ask the user.",
		Annotations: &mcp.ToolAnnotations{Title: "Kill an agent", DestructiveHint: &yes, OpenWorldHint: &no},
	}, func(_ context.Context, _ *mcp.CallToolRequest, in mcpKillIn) (*mcp.CallToolResult, any, error) {
		// A client may run a destructive tool without asking the user
		// (Claude Desktop did, #26), so the user's word comes with the call:
		// it is the confirmation -y stands for.
		if !in.Confirmed {
			return callCommand(d, func(Env, deps, []string) error {
				return usageErr("%s not killed: ask the user whether to end it, then call kill with confirmed set to true", in.Name)
			})
		}
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
