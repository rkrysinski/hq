package cli

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/dialog"
	"github.com/rkrysinski/hq/internal/prefs"
	"github.com/rkrysinski/hq/internal/sbx"
	"github.com/rkrysinski/hq/internal/tmux"
)

// newArgs is hq new's arguments after parsing.
type newArgs struct {
	name, dir, prompt string
}

// parseNew applies spec §4.1: the second argument is DIR when it is an
// existing directory, otherwise it is the PROMPT; DIR defaults to cwd.
func parseNew(args []string, isDir func(string) bool, cwd string) (newArgs, error) {
	for _, a := range args {
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			return newArgs{}, usageErr("unknown option '%s' (see hq help)", a)
		}
	}
	if len(args) == 0 {
		return newArgs{}, usageErr("missing NAME (usage: hq new NAME [DIR] [PROMPT])")
	}
	n := newArgs{name: args[0], dir: cwd}
	rest := args[1:]
	if len(rest) > 0 && isDir(rest[0]) {
		n.dir, rest = rest[0], rest[1:]
	}
	switch len(rest) {
	case 0:
	case 1:
		n.prompt = rest[0]
	default:
		return newArgs{}, usageErr("too many arguments; quote the prompt (usage: hq new NAME [DIR] [PROMPT])")
	}
	if !filepath.IsAbs(n.dir) {
		n.dir = filepath.Join(cwd, n.dir)
	}
	if err := checkName(n.name); err != nil {
		return newArgs{}, err
	}
	return n, nil
}

// checkName says what is wrong with an agent name, naming the remedy (spec
// §4.2): characters come first, so a long name with a bad character learns
// about the character.
func checkName(name string) error {
	switch {
	case !agent.NameChars(name):
		return usageErr("invalid name '%s': use letters, digits, - and _", name)
	case len(name) > agent.MaxName:
		return usageErr("name '%s' is too long: at most %d characters", name, agent.MaxName)
	}
	return nil
}

// claudeArgs are the arguments hq gives Claude: settings carrying the agent's
// identity and the hooks that report its state and notify with the terminal's
// sequence, the Claude session to resume if any, then the first prompt if
// any. The settings are a reference that hq __session writes out as the
// session starts (settingsArgs): written out, they would be part of the
// agent's tmux command, which tmux bounds, and the hook script would take
// its room from the prompt (#40).
func claudeArgs(name, id, resume, prompt string) []string {
	args := []string{"--settings", settingsRef + name + ":" + id}
	if resume != "" {
		args = append(args, "--resume", resume)
	}
	if prompt != "" {
		args = append(args, prompt)
	}
	return args
}

// inboxHooks is the inbox option of every agent hq starts or relaunches
// from now on, the generation of its hooks: from 1 they deliver messages
// (ADR 0012), from 2 they also tell a supervised turn (spec §5,
// agent.Supervisable).
const inboxHooks = "2"

func runNew(env Env, d deps, args []string) error {
	cwd, err := d.getwd()
	if err != nil {
		return envErr("%v", err)
	}
	n, err := parseNew(args, d.isDir, cwd)
	if err != nil {
		return err
	}
	if err := checkTmux(d.tmux); err != nil {
		return err
	}
	root, sandbox, err := createAgent(env.Stdout, d, n)
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "started %s in %s (sandbox %s); enter it with: hq go %s\n", n.name, filepath.Base(root), sandbox, n.name)
	return nil
}

// createAgent starts a new agent, as hq new and the New agent dialog do,
// marked new for the list (S2). What is wrong with the name or the dir is a
// dialog.FieldError, so the dialog shows it under that field.
func createAgent(out io.Writer, d deps, n newArgs) (root, sandbox string, err error) {
	if err := checkName(n.name); err != nil {
		return "", "", dialog.FieldError{Field: dialog.Name, Err: err}
	}
	root, ok := d.repoRoot(n.dir)
	if !ok {
		return "", "", dialog.FieldError{Field: dialog.Dir, Err: notFoundErr("%s is not in a git repository", n.dir)}
	}
	ws, err := d.tmux.Windows()
	if err != nil {
		return "", "", tmuxErr(err)
	}
	if _, exists := agent.Find(agent.FromWindows(ws), n.name); exists {
		return "", "", dialog.FieldError{Field: dialog.Name, Err: duplicateErr(n.name)}
	}
	sb, err := findOrCreateSandbox(out, d, root)
	if err != nil {
		return "", "", err
	}
	if err := startAgent(d, n.name, root, sb.Name, "", n.prompt, true); err != nil {
		return "", "", err
	}
	return root, sb.Name, nil
}

func duplicateErr(name string) error {
	return usageErr("agent '%s' already exists (see hq ls; hq kill %s frees the name)", name, name)
}

// startAgent opens the agent's home window running a new Claude session in
// the sandbox and releases it, resuming the Claude session resume when it
// is set; fresh marks the agent new (S2).
func startAgent(d deps, name, root, sandbox, resume, prompt string, fresh bool) error {
	if err := d.tmux.EnsureSession(root); err != nil {
		return tmuxErr(err)
	}
	id := agent.NewID()
	opts := map[string]string{
		"id": id, "name": name, "repo": root, "sandbox": sandbox,
		"started": agent.Stamp(d.now()), "inbox": inboxHooks,
	}
	if fresh {
		opts["new"] = "1"
	}
	win, err := d.tmux.NewWindow(name, root, opts, d.sbx.RunArgv(sandbox, claudeArgs(name, id, resume, prompt)...))
	var long tmux.CommandTooLong
	if errors.As(err, &long) {
		return dialog.FieldError{Field: dialog.Prompt, Err: usageErr("the prompt is %d bytes too long for tmux; shorten it, or send the rest with hq send once the agent runs", long.Over)}
	}
	if err != nil {
		return tmuxErr(err)
	}
	// tmux cannot create a window only if its name is free, so two hq new of
	// one name can both get here; the later window gives way (design §7.1).
	if lost, err := lostNameRace(d, name, win); err != nil || lost {
		_ = d.tmux.KillWindow(win)
		if err != nil {
			return tmuxErr(err)
		}
		return dialog.FieldError{Field: dialog.Name, Err: duplicateErr(name)}
	}
	// The first prompt of an agent the supervisor starts is the
	// supervisor's (spec §5); without the word it notifies, as the user's.
	supervised := d.supervisor && prompt != "" && d.announce(root, id) == nil
	if err := d.tmux.Start(win); err != nil {
		if supervised {
			_ = d.withdraw(root, id)
		}
		_ = d.tmux.KillWindow(win)
		return tmuxErr(err)
	}
	return nil
}

func findOrCreateSandbox(out io.Writer, d deps, root string) (sbx.Sandbox, error) {
	all, err := d.sbx.List()
	if err != nil {
		return sbx.Sandbox{}, sbxErr(err)
	}
	if s, ok := sbx.ByWorkspace(all, root, d.samePath); ok {
		return s, nil
	}
	fmt.Fprintf(out, "creating a sandbox for %s (first time only; log in to Claude when the session asks)\n", filepath.Base(root))
	set := sandboxSettings(d)
	if err := d.sbx.Create(root, set.Options); err != nil {
		if set.from != "" {
			err = fmt.Errorf("%w; created with %s", err, set.from)
		}
		return sbx.Sandbox{}, sbxErr(err)
	}
	if all, err = d.sbx.List(); err != nil {
		return sbx.Sandbox{}, sbxErr(err)
	}
	if s, ok := sbx.ByWorkspace(all, root, d.samePath); ok {
		return s, nil
	}
	return sbx.Sandbox{}, envErr("sbx created no sandbox for %s (see sbx ls)", root)
}

// The environment variables that override the preferences file's sandbox
// settings, for scripts and tests.
const (
	templateEnv  = "HQ_SBX_TEMPLATE"
	staticMCPEnv = "HQ_SBX_STATIC_MCP" // comma-separated
)

// sandboxSetup is what hq creates a sandbox with; from names the settings
// in effect and where they were set, for sbx's error about them.
type sandboxSetup struct {
	sbx.Options
	from string
}

// sandboxSettings reads the settings hq creates a sandbox with (#58), each
// from its environment variable when set, else from the preferences file.
func sandboxSettings(d deps) sandboxSetup {
	file := prefs.Path(d.getenv)
	p := d.loadPrefs().Sandbox
	var r sandboxSetup
	var named []string // setting, where; where "" is the file
	if r.Template = strings.TrimSpace(d.getenv(templateEnv)); r.Template != "" {
		named = append(named, "sandbox.template "+r.Template, templateEnv)
	} else if r.Template = strings.TrimSpace(p.Template); r.Template != "" {
		named = append(named, "sandbox.template "+r.Template, "")
	}
	if r.StaticMCP = names(strings.Split(d.getenv(staticMCPEnv), ",")); r.StaticMCP != nil {
		named = append(named, "sandbox.staticMcp "+strings.Join(r.StaticMCP, ","), staticMCPEnv)
	} else if r.StaticMCP = names(p.StaticMCP); r.StaticMCP != nil {
		named = append(named, "sandbox.staticMcp "+strings.Join(r.StaticMCP, ","), "")
	}
	// Settings from the same place share its name: "a, b (where)".
	for i := 0; i < len(named); i += 2 {
		if r.from != "" {
			r.from += ", "
		}
		r.from += named[i]
		if i+2 >= len(named) || named[i+3] != named[i+1] {
			where := named[i+1]
			if where == "" {
				where = file
			}
			r.from += " (" + where + ")"
		}
	}
	return r
}

// names is the list without blanks, trimmed; nil when nothing is left.
func names(list []string) []string {
	var out []string
	for _, n := range list {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// lostNameRace reports whether another window with this name was created
// before ours (lower window id).
func lostNameRace(d deps, name, ours string) (bool, error) {
	ws, err := d.tmux.Windows()
	if err != nil {
		return false, err
	}
	for _, w := range ws {
		if w.ID != ours && w.Name == name && windowNum(w.ID) < windowNum(ours) {
			return true, nil
		}
	}
	return false, nil
}

func windowNum(id string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(id, "@"))
	if err != nil {
		return -1
	}
	return n
}
