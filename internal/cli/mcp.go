package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/kills"
	"github.com/rkrysinski/hq/internal/mcpconfig"
	"github.com/rkrysinski/hq/internal/platform"
	"github.com/rkrysinski/hq/internal/prefs"
	"github.com/rkrysinski/hq/internal/sbx"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/version"
)

// mcpClient is an MCP client hq mcp serves and hq mcp install sets hq up
// for (spec §4.1, design §3.12).
type mcpClient struct {
	name  string // as hq mcp takes it
	title string // as the user knows it
	// maxWait is the longest a wait tool call waits: within the time the
	// client gives a tool call, so that a wait returns "nothing yet"
	// instead of failing.
	maxWait time.Duration
	// updated is what every tool call answers once the hq on disk is of
	// another generation, and remedy what the supervisor tells the user
	// to do about it, each naming what that client's user has (#249).
	updated, remedy string
}

var (
	// Claude Desktop gives a local MCP server's tool call 60 s and does not
	// extend it for progress notifications.
	claudeDesktop = mcpClient{name: "claude-desktop", title: "Claude Desktop", maxWait: DefaultWaitTimeout, updated: hqUpdated, remedy: restartInClient}
	// Claude Code gives a tool call the timeout of the server's entry,
	// which hq mcp install sets a minute above maxWait.
	claudeCode = mcpClient{name: "claude-code", title: "Claude Code", maxWait: time.Hour, updated: hqUpdated, remedy: restartInClient}
	// A Claude Code inside a sandbox reaches hq through sbx's MCP gateway,
	// which gives a tool call 60 s, as Claude Desktop does (#141). The
	// gateway starts hq on the host, where restarting the Claude Code in
	// the sandbox does not replace it, and hq sandbox restart does (#249).
	sbxGateway = mcpClient{name: "sbx", title: "sbx", maxWait: DefaultWaitTimeout, updated: hqUpdatedInSandbox, remedy: restartTheSandbox}
	mcpClients = []mcpClient{claudeDesktop, claudeCode, sbxGateway}
)

const (
	mcpUsage     = "hq mcp [install] [claude-desktop|claude-code|sbx]"
	mcpLoadUsage = "usage: hq mcp %s REPO|SANDBOX [--setup NAME|--all-setups]"
	// sbxServer is the name hq is registered with sbx under.
	sbxServer = "hq"
)

// mcpEnv are the variables hq mcp install passes on to the client's
// hq when they are set: PATH, which finds tmux, sbx and gh, and those that
// choose hq's tmux server, preferences and journals.
var mcpEnv = []string{"PATH", "HQ_TMUX_SOCKET", "TMUX_TMPDIR", "XDG_CONFIG_HOME", "XDG_STATE_HOME"}

// parseMCP reads hq mcp's arguments: whether to install, and the client,
// Claude Desktop when none is named.
func parseMCP(args []string) (install bool, c mcpClient, err error) {
	c = claudeDesktop
	if len(args) > 0 && args[0] == "install" {
		install, args = true, args[1:]
	}
	if len(args) > 0 {
		if i := slices.IndexFunc(mcpClients, func(m mcpClient) bool { return m.name == args[0] }); i >= 0 {
			c, args = mcpClients[i], args[1:]
		}
	}
	if len(args) > 0 {
		return false, c, usageErr("unexpected argument '%s' (usage: %s)", args[0], mcpUsage)
	}
	return install, c, nil
}

// runMCP serves hq's tools to an MCP client over stdio, or with install
// adds hq to the client (spec §4.1, design §3.12).
func runMCP(env Env, d deps, args []string) error {
	if len(args) > 0 && (args[0] == "load" || args[0] == "unload") {
		return runMCPLoad(env, d, args[0], args[1:])
	}
	install, client, err := parseMCP(args)
	switch {
	case err != nil:
		return err
	case install:
		return runMCPInstall(env, d, client)
	case d.canAsk(env.Stdin):
		return usageErr("hq mcp is started by an MCP client, not typed; add it to Claude Desktop with: hq mcp install, to Claude Code with: hq mcp install claude-code, or to sbx, for a Claude Code inside a sandbox, with: hq mcp install sbx")
	}
	if err := d.serveMCP(newMCPServer(d, client)); err != nil {
		return envErr("mcp: %v", err)
	}
	return nil
}

// mcpTarget is a configuration file hq mcp install sets hq up in, and
// what it tells the user about it.
type mcpTarget struct {
	title  string // the client, as the user knows it
	file   mcpconfig.File
	server mcpconfig.Server
	// missing is the folder, or the file, whose absence means the client
	// is not there.
	missing string
	// byHand is the entry, or the command that adds it, to print when hq
	// cannot set it up; fix tells the user to use it.
	byHand, fix string
}

// mcpTargets are where hq mcp install sets hq up for a client, the remedy
// when it is in none of them, and how the client loads hq.
type mcpTargets struct {
	targets       []mcpTarget
	install, load string
	// note is said before hq is set up, when there is something to say.
	note string
}

// desktopTargets is Claude Desktop's configuration, which the platform
// adapter finds, and the entry it starts hq with (design §3.10). A running
// Claude Desktop loads hq only once it is quit from the system tray; when it
// reads another file than hq looks for first, both are named (#154).
func desktopTargets(d deps, exe string, vars []string) (mcpTargets, error) {
	t := mcpTarget{
		title:  claudeDesktop.title,
		server: d.appServer([]string{exe, "mcp"}, vars),
		fix:    "add the entry above to it by hand, then quit and reopen Claude Desktop",
	}
	t.byHand = mcpconfig.Snippet(t.server)
	desktop, err := d.desktopConfig()
	t.file, t.missing = mcpconfig.File{Path: desktop.Path, Create: true}, filepath.Dir(desktop.Path)
	ts := mcpTargets{
		targets: []mcpTarget{t},
		install: "install Claude Desktop and open it once, then run hq mcp install again (or add the entry above to its configuration by hand)",
		load:    "quit and reopen Claude Desktop to load it",
	}
	if desktop.Running {
		ts.load = "quit Claude Desktop from its icon in the system tray (closing its window is not enough) and open it again to load hq"
	}
	if desktop.Searched != "" {
		ts.note = fmt.Sprintf("Claude Desktop is running and reads %s, not %s, where hq looks first; hq is set up in the one it reads", desktop.Path, desktop.Searched)
	}
	return ts, err
}

// codeTargets are Claude Code's user configuration, .claude.json in
// CLAUDE_CONFIG_DIR or the home, with the entry that starts hq beside it,
// the same on every platform; and on WSL also Claude Code's on Windows,
// whose entry starts hq as Claude Desktop's does, unless Windows cannot be
// asked where it is (design §3.12). Claude Code saves the file under its
// lock, which hq takes too.
func codeTargets(d deps, exe string, vars []string) (mcpTargets, error) {
	ts := mcpTargets{
		install: "run claude once, then hq mcp install claude-code again (or run the command above)",
		load:    "restart Claude Code to load it",
	}
	argv := []string{exe, "mcp", claudeCode.name}
	timeout := int((claudeCode.maxWait + time.Minute) / time.Millisecond)
	env := map[string]string{}
	for _, kv := range vars {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	beside := mcpTarget{
		title:  claudeCode.title,
		server: mcpconfig.Server{Type: "stdio", Command: exe, Args: argv[1:], Env: env, Timeout: timeout},
		fix:    "run the command above instead, then restart Claude Code",
	}
	beside.byHand = mcpconfig.AddJSON(beside.server)
	ts.targets = append(ts.targets, beside)
	dir := d.getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		home, err := d.home()
		if err != nil {
			return ts, err
		}
		dir = home
	}
	path := filepath.Join(dir, ".claude.json")
	ts.targets[0].file, ts.targets[0].missing = mcpconfig.File{Path: path, Lock: true}, path
	if path, ok, err := d.windowsCodeConfig(); ok && err == nil {
		windows := mcpTarget{
			title:   claudeCode.title + " on Windows",
			file:    mcpconfig.File{Path: path, Lock: true},
			server:  d.appServer(argv, vars),
			missing: path,
			fix:     "add the entry above to its mcpServers by hand, then restart Claude Code",
		}
		windows.server.Type, windows.server.Timeout = "stdio", timeout
		windows.byHand = mcpconfig.Snippet(windows.server)
		ts.targets = append(ts.targets, windows)
	}
	return ts, nil
}

// runMCPInstall sets hq up in each configuration of the client it finds;
// when the client is not there or a file cannot be edited safely, it
// changes nothing there, prints hq's entry, or the command that adds it,
// for the user to add and fails with the reason.
func runMCPInstall(env Env, d deps, c mcpClient) error {
	err := installMCP(env, d, c)
	warnHQPlatformNative(env, d)
	return err
}

// warnHQPlatformNative says, on WSL, that HQ_PLATFORM=native, the way to the
// Linux sbx before hq took it itself, is no longer needed (#153). Only hq
// mcp install says it: the tests set the variable for everything else.
func warnHQPlatformNative(env Env, d deps) {
	if platform.NativeOnWSL(d.getenv) {
		fmt.Fprintln(env.Stderr, "hq: HQ_PLATFORM=native is no longer needed on WSL: hq uses the Linux sbx when it is in PATH, and HQ_PLATFORM=native turns off the taskbar flash and hq go raising the window; remove it from ~/.bashrc, open a new terminal and run hq mcp install again")
	}
}

// installMCP is runMCPInstall's work.
func installMCP(env Env, d deps, c mcpClient) error {
	exe, err := d.executable()
	if err != nil {
		return envErr("cannot find the running hq: %v", err)
	}
	var vars []string
	for _, k := range mcpEnv {
		if v := d.getenv(k); v != "" {
			vars = append(vars, k+"="+v)
		}
	}
	if c == sbxGateway {
		return installSbx(env, d, exe, vars)
	}
	targets := desktopTargets
	if c == claudeCode {
		targets = codeTargets
	}
	ts, err := targets(d, exe, vars)
	if err != nil {
		t := ts.targets[0]
		fmt.Fprint(env.Stdout, t.byHand)
		return envErr("cannot find %s's configuration (%v); %s", t.title, err, t.fix)
	}
	if ts.note != "" {
		fmt.Fprintln(env.Stdout, ts.note)
	}
	var missing, failed []string
	set, changed := false, false
	for _, t := range ts.targets {
		r, err := d.installConfig(t.file, t.server)
		switch {
		case errors.Is(err, mcpconfig.ErrNoFolder), errors.Is(err, mcpconfig.ErrNoFile):
			missing = append(missing, t.missing)
			continue
		case err != nil:
			fmt.Fprint(env.Stdout, t.byHand)
			failed = append(failed, fmt.Sprintf("cannot edit %s safely (%v); %s", t.file.Path, err, t.fix))
			continue
		}
		set = true
		switch r.Outcome {
		case mcpconfig.Unchanged:
			fmt.Fprintf(env.Stdout, "hq is already set up in %s (%s)\n", t.title, t.file.Path)
			continue
		case mcpconfig.Added:
			fmt.Fprintf(env.Stdout, "added hq to %s (%s)\n", t.title, t.file.Path)
		default:
			fmt.Fprintf(env.Stdout, "updated hq in %s (%s)\n", t.title, t.file.Path)
		}
		changed = true
		if r.Backup != "" {
			fmt.Fprintf(env.Stdout, "the file as it was: %s\n", r.Backup)
		}
	}
	if changed {
		fmt.Fprintln(env.Stdout, ts.load)
	}
	switch {
	case len(failed) > 0:
		return envErr("%s", strings.Join(failed, "; "))
	case !set:
		fmt.Fprint(env.Stdout, ts.targets[0].byHand)
		return envErr("%s not found: there is no %s; %s", c.title, strings.Join(missing, " nor "), ts.install)
	}
	return nil
}

// installSbx registers hq with sbx, for a Claude Code inside a sandbox
// (#141): replaced when sbx has another command line under hq's name,
// left as it is when it has this one. sbx mcp add splits --args at commas,
// so a value that holds one is refused.
func installSbx(env Env, d deps, exe string, vars []string) error {
	cmd, args := d.sbxGateway([]string{exe, "mcp", sbxGateway.name}, vars)
	const comma = "cannot register hq with sbx: %s holds a comma, and sbx mcp add splits its arguments at commas; %s and run hq mcp install sbx again"
	for _, a := range args {
		k, _, isVar := strings.Cut(a, "=")
		switch {
		case !strings.Contains(a, ","):
		case a == exe:
			return envErr(comma, "the path of hq ("+exe+")", "move hq to a directory without one")
		case isVar && slices.Contains(mcpEnv, k):
			return envErr(comma, k, "take it out of "+k)
		default:
			return envErr(comma, "the argument '"+a+"'", "remove it")
		}
	}
	had, ok, err := d.sbx.MCPCommand(sbxServer)
	if err != nil {
		return sbxErr(err)
	}
	where := "sbx (sbx mcp inspect " + sbxServer + ")"
	switch want := append([]string{cmd}, args...); {
	case ok && slices.Equal(had, want):
		fmt.Fprintf(env.Stdout, "hq is already set up in %s\n", where)
	default:
		if err := d.sbx.MCPAdd(sbxServer, cmd, args); err != nil {
			return sbxErr(err)
		}
		if ok {
			fmt.Fprintf(env.Stdout, "updated hq in %s\n", where)
		} else {
			fmt.Fprintf(env.Stdout, "added hq to %s\n", where)
		}
	}
	fmt.Fprintln(env.Stdout, "load it into a sandbox with: hq mcp load REPO (every agent in that sandbox then sees hq's tools)")
	return nil
}

// parseMCPLoad reads the arguments of hq mcp load and unload: REPO and
// the flags, as hq sandbox takes them.
func parseMCPLoad(verb string, args []string) (sandboxArgs, error) {
	usage := fmt.Sprintf(mcpLoadUsage, verb)
	a, rest, err := parseSetups(flagsOf("mcp"), args, usage)
	if err != nil {
		return a, err
	}
	if len(rest) != 1 || strings.HasPrefix(rest[0], "-") {
		return a, usageErr("%s", usage)
	}
	a.repo = rest[0]
	return a, nil
}

// runMCPLoad loads hq, registered with sbx, into the sandboxes REPO and the
// flags name, as hq sandbox finds them, and records those it loaded it into
// in the preferences, so hq sandbox restart loads it again; or, with
// unload, forgets them (#141). sbx v0.47.0 has no unload, and sbx mcp rm
// would take hq from every sandbox: hq's tools stay in a sandbox until it
// restarts, which unload says.
func runMCPLoad(env Env, d deps, verb string, args []string) error {
	a, err := parseMCPLoad(verb, args)
	if err != nil {
		return err
	}
	if verb == "load" {
		if _, ok, err := d.sbx.MCPCommand(sbxServer); err != nil {
			return sbxErr(err)
		} else if !ok {
			return envErr("hq is not registered with sbx; run hq mcp install sbx first")
		}
	}
	sbs, err := resolveSandboxes(d, a)
	if verb == "unload" {
		// A sandbox removed behind hq's back (sbx rm) is forgotten by its
		// name, which sbx no longer lists.
		if err != nil && !a.flagged() && slices.Contains(d.loadPrefs().MCPLoaded, a.repo) {
			return forgetGone(env, d, a.repo)
		}
		if err != nil {
			return err
		}
		return unloadHq(env, d, sbs)
	}
	if err != nil {
		return err
	}
	var loaded []sbx.Sandbox
	var failed []string // the sandbox and sbx's error
	for _, sb := range sbs {
		if err := d.sbx.MCPLoad(sbxServer, sb.Name); err != nil {
			if len(sbs) == 1 {
				return sbxErr(err)
			}
			failed = append(failed, fmt.Sprintf("%s (%v)", sb.Name, sbxErr(err)))
			continue
		}
		loaded = append(loaded, sb)
	}
	if len(loaded) > 0 {
		if err := d.updatePrefs(func(p *prefs.Prefs) bool {
			n := len(p.MCPLoaded)
			for _, sb := range loaded {
				if !slices.Contains(p.MCPLoaded, sb.Name) {
					p.MCPLoaded = append(p.MCPLoaded, sb.Name)
				}
			}
			return len(p.MCPLoaded) > n
		}); err != nil {
			return envErr("loaded hq into %s, but cannot record it for hq sandbox restart: %v", sandboxNames(loaded), err)
		}
		it := "it"
		if len(loaded) > 1 {
			it = "them"
		}
		fmt.Fprintf(env.Stdout, "loaded hq into %s: every agent in %s sees hq's tools\n", sandboxNames(loaded), it)
	}
	if len(failed) > 0 {
		return envErr("could not load hq into %s", strings.Join(failed, "; "))
	}
	return nil
}

// forgetGone forgets a sandbox hq mcp load loaded hq into that sbx no
// longer lists.
func forgetGone(env Env, d deps, name string) error {
	if err := d.updatePrefs(func(p *prefs.Prefs) bool {
		p.MCPLoaded = slices.DeleteFunc(p.MCPLoaded, func(n string) bool { return n == name })
		return true
	}); err != nil {
		return envErr("cannot record the unload: %v", err)
	}
	fmt.Fprintf(env.Stdout, "forgot the sandbox %s, which is gone: a new sandbox of its name will not get hq\n", name)
	return nil
}

// reloadNotice names, after hq restore, each sandbox of the restored
// agents that hq mcp load loaded hq into: a host restart restarted it, and
// sbx lost the load with that.
func reloadNotice(out io.Writer, d deps, restored []agent.Agent) {
	recorded := d.loadPrefs().MCPLoaded
	var said []string
	for _, a := range restored {
		if slices.Contains(recorded, a.Sandbox) && !slices.Contains(said, a.Sandbox) {
			said = append(said, a.Sandbox)
			fmt.Fprintf(out, "hq was loaded into the sandbox %s; if it restarted, hq mcp load %s loads it again\n", a.Sandbox, a.Sandbox)
		}
	}
}

// unloadHq forgets that hq mcp load loaded hq into the sandboxes and says
// what takes the tools away: a restart of each it had loaded hq into.
func unloadHq(env Env, d deps, sbs []sbx.Sandbox) error {
	recorded := d.loadPrefs().MCPLoaded
	var loaded, not []sbx.Sandbox
	for _, sb := range sbs {
		if slices.Contains(recorded, sb.Name) {
			loaded = append(loaded, sb)
		} else {
			not = append(not, sb)
		}
	}
	if len(loaded) > 0 {
		if err := d.updatePrefs(func(p *prefs.Prefs) bool {
			p.MCPLoaded = slices.DeleteFunc(p.MCPLoaded, func(name string) bool {
				return slices.ContainsFunc(loaded, func(sb sbx.Sandbox) bool { return sb.Name == name })
			})
			return true
		}); err != nil {
			return envErr("cannot record the unload: %v", err)
		}
		for _, sb := range loaded {
			fmt.Fprintf(env.Stdout, "hq stays loaded into the sandbox %s until it restarts (sbx cannot unload it): hq sandbox restart %s takes its tools away\n", sb.Name, sb.Name)
		}
	}
	if len(not) > 0 {
		fmt.Fprintf(env.Stdout, "hq mcp load did not load hq into %s; nothing to unload\n", sandboxNames(not))
	}
	return nil
}

// mcpInstructions teach a supervisor how to work with hq's agents, every
// client alike; a client's instructions add what its updated line means.
// The tools' descriptions repeat what each needs.
var mcpInstructions = `hq runs Claude Code agents, each working on one task in its repository, for the user. You supervise them: start them, follow them and give them feedback.

- Status comes from hq, never from asking an agent: list for every agent or those you name, read for one in full (its whole last reply and its last message whole, which list and wait cut to ` + strconv.Itoa(listLastWidth) + ` characters, what it asks, messages waiting for it).
- To ask an agent something or give it feedback, send it a message. hq delivers it when the agent is ready, without interrupting it; its answer is its next reply: the next wait returns the agent when it answers, then read it. When send says the message follows as a prompt of its own, the next wait returns the turn of the earlier prompt first, with pending still counting yours; wait again for the answer. To wait for that one answer in the same call, send with wait: it returns when the turn that took your message ends (turn_ended), at once when the agent needs input or ends before taking it (not_taken), after ` + stallText + ` for a slash command that started no turn (no_turn), or at its timeout with no agents (timeout: go on with a wait that names that agent alone, from the next_since send returned; that wait's next_since is that agent's too, so your watch goes on from its own last wait or list result); it fails as stalled when the agent sits at its prompt without taking the message (read the agent before sending again: hq types nothing twice). Its next_since is that agent's alone, and your watch returns that same change once more. Use now only for a course correction that must reach it within the turn it is working on.
- next_since is an opaque string: pass it back exactly as given, never build or change one. Watch from the next_since of your most recent wait or list result. Always pass it as wait's since: wait then returns every change after it, even one that happened before wait was called, such as an agent that answered your message at once, and none twice. Your watch never takes its since from read, send or answer: their next_since is that one agent's, later than what the others did meanwhile, so a watch from it misses that. A wait that names that one agent alone may start from the next_since of its send or answer, to get the answer to that message; the next_since it returns is that agent's too, so your watch goes on from its own last wait or list result. Name every agent you follow in each wait (or none, for every agent): a wait's next_since stands for the agents it waited on. A list that names agents gives a next_since for a wait that names the same agents; a watch over every agent keeps the next_since of its own wait or of a list without names. A wait from a list's next_since returns only what changes after the list, so act on the agents the list shows done, in question, needing input or ended as on those a wait returns. To start watching with no wait or list result yet, call list first.
- The user is not notified when work you sent finishes or asks a question: the end of a turn whose prompts all came from you (new with a prompt, send) raises no desktop notification, whether or not you are waiting, because its result is for you. So wait for the agents you gave work to, read their replies and tell the user what matters. A turn the user prompted themselves, or sent a message into, still notifies them.
- To watch agents, call wait in a loop, each time with the next_since of the most recent wait or list result: it returns as soon as an agent is done, asks a question, needs input or ends, with every other that does so within a moment of it, or returns no agents after its timeout; then call it again.
- An agent done or in question with own_tasks: a shell command or watch of its own still runs, which it started and left running at its turn end. Claude takes another turn when one ends, so wait for it again if you need that result; a dev server may never end. The list is that turn end's and may be stale until its next turn end.
- An agent that needs input has a dialog open (a permission, questions with options, an MCP server's form, or a dialog of Claude Code's own such as Change effort level? after /effort; read gives them, with the dialog's id). Never answer a permission or any other dialog, with send or otherwise: tell the user which agent waits and what it asks; they answer it in hq (go shows them the agent). A question dialog (asks with questions) and a dialog of Claude Code's own are the exception: answer them with answer, only on the user's word. Once a dialog of Claude Code's own has closed, wait returns the agent once more, done or question with the completion_seq it had: no new reply. The picker of /model is not one hq answers: send /model with the model's name instead, whose Switch model? is; send it while the agent waits at its prompt, since a slash command that reaches an agent at work arrives as text and does not run (send's delivery says so). Tell the user which agent asks what, and pass on what they pick, or your pick when they told you to choose for them; never on your own initiative. hq notifies them of the dialog itself, also on a turn you started.
- An agent in the state outdated was started by another hq than the one serving you, so hq cannot read its state: read, send, answer and a wait that names it fail, and a wait on every agent leaves it out. Do not wait on it: tell the user which agent it is; they can open it (go) to finish or save its work by hand, and it has to be killed and started again.
- kill ends an agent's session for good: only when the user wants it. Ask the user first, naming the agent, unless they have just told you to end it; then call kill with confirmed set to true.`

// The remedies the instructions name for a client's updated line.
const (
	restartInClient   = "Tell the user that hq was updated and has to be restarted in this client; the tools work again after that."
	restartTheSandbox = "This server runs on the host, started by sbx's MCP gateway, so restarting the Claude Code you run in does not replace it: tell the user to run hq sandbox restart REPO on the host for this sandbox's repository, adding --setup SETUP when this sandbox is of a setup (named hq-SETUP-DIR), which starts this sandbox's hq anew; the tools work again after that."
)

// instructions are mcpInstructions with what c's updated line means and
// what the supervisor does about it.
func (c mcpClient) instructions() string {
	return mcpInstructions + "\n- A tool call that fails with `" + c.updated + "` means hq was updated to another generation while this server ran: from then on every call fails the same way and changes nothing, so do not call again. " + c.remedy
}

// Tool inputs.
type (
	mcpNewIn struct {
		Name   string `json:"name" jsonschema:"the agent's name: letters, digits, '-', '_' and '.', at least one letter or digit, at most 32 characters, e.g. the issue number (42), a slug (agent-17) or a release's version and issue (rel-1.10.5-213); free until that agent is killed"`
		Dir    string `json:"dir" jsonschema:"the git repository to work in: an absolute path on the user's machine (~/ is their home), e.g. /Users/me/work/app"`
		Prompt string `json:"prompt,omitempty" jsonschema:"the agent's first prompt: the task; without it the agent waits for a prompt (send one)"`
		Setup  string `json:"setup,omitempty" jsonschema:"the setup to start it on, as the user named it (e.g. local, a sandbox for a local model): the repository's sandbox of that setup is used, created on first use; without it, default, the repository's usual sandbox. An unknown setup is refused, listing the setups there are"`
	}
	mcpListIn struct {
		Names []string `json:"names,omitempty" jsonschema:"the agents to list; every agent when empty"`
	}
	mcpNameIn struct {
		Name string `json:"name" jsonschema:"the agent's name, as list shows it"`
	}
	mcpReadIn struct {
		Name   string `json:"name" jsonschema:"the agent's name, as list shows it"`
		Offset int    `json:"offset,omitempty" jsonschema:"where the reply's page starts: the next_offset of the read before; 0 or absent for the first page"`
		Since  string `json:"since,omitempty" jsonschema:"with offset: the next_since of the read that gave that next_offset, exactly as given, so the page is of the same reply"`
	}
	mcpWaitIn struct {
		Names   []string `json:"names,omitempty" jsonschema:"the agents to wait for; every agent when empty"`
		Since   string   `json:"since" jsonschema:"the next_since of your most recent wait or list result, exactly as given (an opaque string such as c1234), for your watch never that of read, send or answer, though a wait that names one agent alone may pass the next_since of its send or answer; wait returns what changed after it. A moment (2026-09-28T10:25:01Z) or a duration back from now (10m) are taken too, but only approximately"`
		Timeout int      `json:"timeout,omitempty" jsonschema:"seconds to wait before returning no agents; default 50, at most as the tool's description says"`
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
		Wait bool   `json:"wait,omitempty" jsonschema:"return only when the turn that took the message ends, with the agent as wait gives it, or say at once why that will not come by itself"`
		// Timeout is wait's: it needs wait.
		Timeout int `json:"timeout,omitempty" jsonschema:"with wait: seconds to wait, the delivery included, before returning no agents; default 50, at most as the tool's description says"`
	}
)

// newMCPServer is hq's MCP server for a client: each tool runs the CLI
// command of its name, in this process, and returns what the command
// prints, or its error line as a tool error (design §3.12). Its commands
// run from the user's home: the directory the client starts hq in means
// nothing to them.
func newMCPServer(d deps, client mcpClient) *mcp.Server {
	d.getwd = d.home
	// What its tools give agents is the supervisor's, not the user's.
	d.supervisor = true
	s := mcp.NewServer(&mcp.Implementation{Name: "hq", Title: "hq", Version: version.Version}, &mcp.ServerOptions{Instructions: client.instructions()})
	s.AddReceivingMiddleware(stepAside(watchOnDisk(d), client.updated))
	no, yes := false, true

	mcp.AddTool(s, &mcp.Tool{
		Name: "new",
		Description: "Start an agent: a Claude Code session named name, in the repository at dir, in that repository's sandbox of setup (the default without it), with prompt as its first prompt. " +
			"It returns once the agent is starting; the agent then works on its own. Follow it with wait and read: " +
			"the user is not notified when the work you gave it with prompt finishes or asks a question, so wait for it and tell them.",
		Annotations: &mcp.ToolAnnotations{Title: "Start an agent", DestructiveHint: &no, OpenWorldHint: &no},
	}, func(_ context.Context, _ *mcp.CallToolRequest, in mcpNewIn) (*mcp.CallToolResult, any, error) {
		dir, err := absDir(d, in.Dir)
		if err != nil {
			return toolResult("", err)
		}
		args := []string{in.Name, dir}
		if in.Setup != "" {
			args = append(args, "--setup", in.Setup)
		}
		if in.Prompt != "" {
			args = append(args, in.Prompt)
		}
		return callCommand(d, runNew, args...)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "list",
		Description: "List the agents named in names, every agent when names is empty or absent, as JSON (agents), those that need the user first: name, repo, branch, state, since (the time it entered that state: not what wait takes as since) and age_seconds (in that state), last (its last message, one line, cut to " + strconv.Itoa(listLastWidth) + " characters: read gives it whole), sandbox, setup (the setup of its sandbox, as new takes it: default, or the one it was started on), pending (messages waiting for it), dialog (while it needs input: the open dialog's id, for answer), own_tasks (while it is done or in question: the shell commands and watches of its own still running at that turn end, each with its type, shell or monitor, and text, the command or the watch's description; [] when none) and asks (what it asks: while it needs input the open dialog's questions and options, a permission's tool and command, or an MCP server's form (server, message and fields: each with its name, title, type, whether it is required, options and default; or the url it sends the user to); the question while in question; null otherwise); state_seq and completion_seq (the numbers of the change that put it in its state and of the one that ended its last completed turn; null when there is none); and next_since, an opaque string to pass back to wait as since exactly as given: a wait from it returns only what changes after the list, so act on the agents it shows done, in question, needing input or ended. " +
			"With names, next_since is for a wait that names the same agents: a watch over every agent keeps the next_since of its own wait or of a list without names. " +
			"A name no agent has is refused, and nothing is listed; an outdated agent named is listed. " +
			"States: starting, working, question (its reply ends with a question), needs input (a dialog is open), done (its turn ended), ended (its session is gone), outdated (started by another hq, so its state is unknown: tell the user; only go and kill work on it, and it has to be killed and started again). " +
			"This is where status comes from: never ask an agent for its status.",
		Annotations: &mcp.ToolAnnotations{Title: "List agents", ReadOnlyHint: true, OpenWorldHint: &no},
	}, func(_ context.Context, _ *mcp.CallToolRequest, in mcpListIn) (*mcp.CallToolResult, any, error) {
		return callCommand(d, runLs, append(append([]string{}, in.Names...), "--json")...)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "read",
		Description: "One agent in full, as JSON: what list gives, its last whole, where list and wait cut it to " + strconv.Itoa(listLastWidth) + " characters, its sandbox and setup, what it asks and the open dialog's id included (asks: while it needs input the open dialog's questions and options, a permission's tool and command, or an MCP server's form (server, message and fields: each with its name, title, type, whether it is required, options and default; or the url it sends the user to); the question while in question; null otherwise), its worktree, its last reply, and next_since, an opaque string to pass back exactly as given, for its next page (below): never pass it to wait as since, it is this agent's alone. " +
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
		Description: "Wait until one of the named agents (every agent when none is named) is done, asks a question, needs input or ends, and return it, with every other that does so within a moment of it, as list gives them (each with its sandbox and setup, its last cut as list cuts it: read gives it whole), with next_since, as JSON. " +
			"After timeout seconds (" + waitLimit(client) + ") it returns no agents, which is not an error. " +
			"since is required: the next_since of your most recent wait or list result, passed back exactly as given, so a change between that result and this call is not missed; for your watch never that of read, send or answer, which is one agent's and later than what the others did meanwhile. A wait that names one agent alone may start from the next_since of its send or answer, for the answer to that message; what it returns is then that agent's alone, so your watch goes on from its own last result. " +
			"To watch agents, call it in a loop, passing each result's next_since as the next call's since and naming every agent you follow each time (or none, for every agent): no change is then missed or returned twice. React to the agents it returns (read them), then call it again. " +
			"An agent returned done or in question with own_tasks still runs a shell command or watch of its own: Claude takes another turn when one ends, so wait for it again if you need that result (a dev server may never end); the list is that turn end's and may be stale.",
		Annotations: &mcp.ToolAnnotations{Title: "Wait for agents", ReadOnlyHint: true, OpenWorldHint: &no},
	}, func(_ context.Context, _ *mcp.CallToolRequest, in mcpWaitIn) (*mcp.CallToolResult, any, error) {
		timeout := toolTimeout(in.Timeout, client)
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
			"Without wait, it returns at once, as JSON, how the message goes (delivery: queued or delivered) and next_since, an opaque string taken before the message was left, to pass back exactly as given. The agent's answer is its next reply: your next wait, from your watch's since (the next_since of your most recent wait or list result), returns the agent when it answers, even if it answered before wait was called; then read it. When delivery says the message follows as a prompt of its own, an earlier message was typed in first: the next wait returns that prompt's turn, with pending still counting yours, and a later one your answer. A message that starts with a slash command runs only when hq types it in at the agent's prompt; when the agent's hooks deliver it, it arrives as text and does not run, and delivery says so. This next_since is for a wait on this agent alone, never your watch's since. " +
			"With wait, when you want this one answer and nothing else, it also waits, up to timeout seconds (" + waitLimit(client) + ", the delivery included), and returns delivery, outcome, the agent as wait gives it (agents) and next_since, taken after what it returned. outcome is one of: turn_ended, the agent stopped after taking the message (done, question, needs input or ended), never at a turn end that came before it took it, such as that of an earlier prompt; not_taken, the agent needs input or ended before it took the message, and message says where it is (waiting, delivered once the dialog is closed; kept for a relaunch; gone with the agent); no_turn, a slash command hq typed in left the prompt box and started no turn (a command of Claude Code's own, such as /model with a name); timeout, no agents yet: the message stays where it is, and a wait that names this agent alone, from this next_since, returns its next change, which is its answer once pending no longer counts the message; that wait's next_since is this agent's too, so your watch keeps its own since. " +
			"A stalled message is the tool's error, stalled: the agent sat at its prompt and did not take the message within " + stallText + " (the user typing in its box, or Claude leaving what hq typed in); hq does not type it again or take it back, so read the agent before sending again. " +
			"After a send with wait your watch still returns that same change once more, since the watch's since is older: it is no new answer. " +
			"The user is not notified when a turn your message started finishes or asks a question, so wait for it and tell them. " +
			"Never use it to answer an agent's dialog (needs input): for a question dialog or a dialog of Claude Code's own use answer, on the user's word; for any other, tell the user.",
		Annotations: &mcp.ToolAnnotations{Title: "Send a message", DestructiveHint: &no, OpenWorldHint: &no},
	}, func(_ context.Context, _ *mcp.CallToolRequest, in mcpSendIn) (*mcp.CallToolResult, any, error) {
		args := []string{in.Name, in.Text, "--json"}
		if in.Now {
			args = append(args, "--now")
		}
		switch {
		case in.Timeout != 0 && !in.Wait:
			return toolResult("", usageErr("timeout goes with wait"))
		case in.Wait:
			args = append(args, "--wait", "--timeout", toolTimeout(in.Timeout, client).String())
		}
		return callCommand(d, runSend, args...)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "answer",
		Description: "Answer an agent's question dialog (it needs input, and read's asks has questions with options), or cancel it as Esc does; or pick an option of a dialog of Claude Code's own in place of its prompt box, such as Change effort level? after /effort, Switch model? after /model with a name, or a question at the start of its session. " +
			"Only on the user's word: without confirmed set to true it answers nothing and says to ask the user. Never on your own initiative: tell the user which agent asks what, and pass on what they pick; when they tell you to choose for them, whether you may pick your own recommendation is part of what they told you. " +
			"Name the dialog by its id (read and list give it as dialog), so an answer never lands on a dialog that opened after you read; an answer for a dialog no longer open is refused with the agent's state. " +
			"answers holds one entry per question in the dialog's order: options, the numbers read lists (several only for a multi_select question), or text, an answer in the user's words; a dialog of Claude Code's own takes one option, no text and no cancel. " +
			"Permission prompts, MCP elicitations and a list of Claude Code's own that scrolls (the picker of /model: send /model with the model's name instead) are refused, as is a question dialog in a sandbox whose template answers it by itself (the refusal names its hook): tell the user, who answers them at the agent (go shows it). " +
			"It returns once the agent has left the dialog, as JSON: its state and next_since, an opaque string taken before the answer was left, to pass back exactly as given: for a wait on this agent alone, never your watch's since. " +
			"Once a dialog of Claude Code's own has closed, a wait returns the agent once more, back where it was (done or question) with the completion_seq it had: that is no new reply.",
		Annotations: &mcp.ToolAnnotations{Title: "Answer a dialog", DestructiveHint: &no, OpenWorldHint: &no},
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
	}, func(_ context.Context, req *mcp.CallToolRequest, in mcpKillIn) (*mcp.CallToolResult, any, error) {
		// A client may run a destructive tool without asking the user
		// (Claude Desktop did, #26), so the user's word comes with the call:
		// it is the confirmation -y stands for.
		if !in.Confirmed {
			return callCommand(d, func(Env, deps, []string) error {
				return usageErr("%s not killed: ask the user whether to end it, then call kill with confirmed set to true", in.Name)
			})
		}
		d := d
		d.killer = mcpKiller(d, client, req)
		return callCommand(d, runKill, in.Name, "-y")
	})
	return s
}

// stepAside fails every tool call with line once the hq on disk is of
// another generation than this hq mcp (design §3.12): an agent it started
// would carry a hook the installed hq cannot read. Each call looks at the
// file first; the rest of the protocol goes on, so the client can still
// say why.
func stepAside(disk *onDisk, line string) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "tools/call" && disk.otherGeneration() {
				r, _, _ := toolResult("", usageErr("%s", line))
				return r, nil
			}
			return next(ctx, method, req)
		}
	}
}

// toolTimeout is how long a tool that waits (wait, send with wait) waits
// for the client when asked for seconds: DefaultWaitTimeout when not
// asked, and never longer than the client gives a tool call (maxWait).
func toolTimeout(seconds int, c mcpClient) time.Duration {
	timeout := time.Duration(seconds) * time.Second
	if timeout <= 0 {
		timeout = DefaultWaitTimeout
	}
	return min(timeout, c.maxWait)
}

// waitLimit is how long the wait tool waits for the client, as its
// description gives it.
func waitLimit(c mcpClient) string {
	if c.maxWait == DefaultWaitTimeout {
		return fmt.Sprintf("default and at most %d", int(c.maxWait.Seconds()))
	}
	return fmt.Sprintf("default %d, at most %d", int(DefaultWaitTimeout.Seconds()), int(c.maxWait.Seconds()))
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

// mcpKiller is who asks for a kill through the kill tool, for the trace
// (#147): the client as hq mcp takes it, the name and version it gave
// itself, and the calling agent when hq mcp runs in an agent's session
// (HQ_AGENT, which hq gives every agent's session). An agent behind sbx's
// gateway calls hq mcp from outside its session, and is not known.
func mcpKiller(d deps, client mcpClient, req *mcp.CallToolRequest) kills.Kill {
	k := kills.Kill{By: kills.ByMCP, Client: client.name}
	if req != nil && req.Session != nil {
		if p := req.Session.InitializeParams(); p != nil && p.ClientInfo != nil {
			k.App = strings.TrimSpace(p.ClientInfo.Name + " " + p.ClientInfo.Version)
		}
	}
	if name := d.getenv("HQ_AGENT"); checkName(name) == nil {
		k.Caller = name
	}
	return k
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
