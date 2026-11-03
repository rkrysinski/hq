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

// newArgs is hq new's arguments after parsing; setup "" is the default.
type newArgs struct {
	name, dir, prompt, setup string
}

const newUsage = "hq new NAME [DIR] [PROMPT] [--setup NAME]"

// parseNew applies spec §4.1: the second argument is DIR when it is an
// existing directory, otherwise it is the PROMPT; DIR defaults to cwd;
// --setup NAME, anywhere, is the setup, the default without it.
func parseNew(args []string, isDir func(string) bool, cwd string) (newArgs, error) {
	args, setup, _, err := parseSetup(args, "usage: "+newUsage)
	if err != nil {
		return newArgs{}, err
	}
	for _, a := range args {
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			return newArgs{}, usageErr("unknown option '%s' (see hq help)", a)
		}
	}
	if len(args) == 0 {
		return newArgs{}, usageErr("missing NAME (usage: %s)", newUsage)
	}
	n := newArgs{name: args[0], dir: cwd, setup: setup}
	rest := args[1:]
	if len(rest) > 0 && isDir(rest[0]) {
		n.dir, rest = rest[0], rest[1:]
	}
	switch len(rest) {
	case 0:
	case 1:
		n.prompt = rest[0]
	default:
		return newArgs{}, usageErr("too many arguments; quote the prompt (usage: %s)", newUsage)
	}
	if !filepath.IsAbs(n.dir) {
		n.dir = filepath.Join(cwd, n.dir)
	}
	if err := checkName(n.name); err != nil {
		return newArgs{}, err
	}
	return n, nil
}

// parseSetup takes --setup NAME (or --setup=NAME), anywhere, out of args:
// setup is NAME, given whether the flag was there; without it the setup is
// the default. The flag without a name is refused, usage in brackets after
// the reason.
func parseSetup(args []string, usage string) (rest []string, setup string, given bool, err error) {
	setup = prefs.DefaultSetup
	for i := 0; i < len(args); i++ {
		a := args[i]
		v, isSetup := strings.CutPrefix(a, "--setup=")
		if a == "--setup" {
			isSetup, v = true, ""
			if i+1 < len(args) {
				i++
				v = args[i]
			}
		}
		switch {
		case isSetup && v == "":
			return nil, "", false, usageErr("--setup needs a setup's name (%s)", usage)
		case isSetup:
			setup, given = v, true
		default:
			rest = append(rest, a)
		}
	}
	return rest, setup, given, nil
}

// checkName says what is wrong with an agent name, naming the remedy (spec
// §4.2): characters come first, so a long name with a bad character learns
// about the character.
func checkName(name string) error {
	switch {
	case !agent.NameChars(name):
		return usageErr("invalid name '%s': use letters, digits, '-', '_' and '.'", name)
	case !agent.NameHasLetterOrDigit(name):
		return usageErr("invalid name '%s': it needs a letter or a digit", name)
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
// agent.Supervisable), from 3 they hold on a question dialog for hq's
// answer (hq answer, agent.Answerable).
const inboxHooks = "3"

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
// marked new for the list (S2), in the repository's sandbox of its setup:
// the default when n.setup is "", as the dialog without its setup field
// gives it, the one place a setup may be "". What is wrong with the name or the dir
// is a dialog.FieldError, so the dialog shows it under that field.
func createAgent(out io.Writer, d deps, n newArgs) (root, sandbox string, err error) {
	if err := checkName(n.name); err != nil {
		return "", "", dialog.FieldError{Field: dialog.Name, Err: err}
	}
	if n.setup == "" {
		n.setup = prefs.DefaultSetup
	}
	spec, err := sandboxSpecOf(d, n.setup)
	if err != nil {
		return "", "", err
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
	all, err := d.sbx.List()
	if err != nil {
		return "", "", sbxErr(err)
	}
	// A lost agent holds its name too, until it is restored or killed.
	if _, lost := agent.Find(lostAgents(d, ws, append(knownRepositories(d, all), root)), n.name); lost {
		return "", "", dialog.FieldError{Field: dialog.Name, Err: usageErr("agent '%s' was lost with tmux (hq restore brings it back; hq kill %s forgets it)", n.name, n.name)}
	}
	spec.Name = sbx.SandboxName(spec.setup, filepath.Base(root))
	sb, err := findOrCreateSandbox(out, d, all, root, spec)
	if err != nil {
		return "", "", err
	}
	if err := startAgent(d, n.name, root, sb.Name, n.prompt); err != nil {
		return "", "", err
	}
	return root, sb.Name, nil
}

func duplicateErr(name string) error {
	return usageErr("agent '%s' already exists (see hq ls; hq kill %s frees the name)", name, name)
}

// startAgent opens the home window of a new agent, marked new (S2),
// running a new Claude session in the sandbox, and releases it.
func startAgent(d deps, name, root, sandbox, prompt string) error {
	return launchAgent(d, agent.Agent{ID: agent.NewID(), Name: name, RepoPath: root, Sandbox: sandbox}, "", prompt, true)
}

// launchAgent opens a's home window running a Claude session in its
// sandbox under its id, resuming the Claude session resume when it is set,
// keeps hq's record of it for hq restore, and releases it; fresh marks
// the agent new (S2).
func launchAgent(d deps, a agent.Agent, resume, prompt string, fresh bool) error {
	name, root, sandbox, id := a.Name, a.RepoPath, a.Sandbox, a.ID
	if err := d.tmux.EnsureSession(root); err != nil {
		return tmuxErr(err)
	}
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
	// Without its record, an agent lost with tmux could not be restored.
	if err := keepRecord(d, a); err != nil {
		_ = d.tmux.KillWindow(win)
		return err
	}
	// The first prompt of an agent the supervisor starts is the
	// supervisor's (spec §5); without the word it notifies, as the user's.
	supervised := d.supervisor && prompt != "" && d.announce(root, id) == nil
	if err := d.tmux.Start(win); err != nil {
		if supervised {
			_ = d.withdraw(root, id)
		}
		_ = d.tmux.KillWindow(win)
		// A new agent that never ran leaves no record to restore; a
		// restored one keeps its own.
		if fresh {
			_ = d.removeState(root, id)
		}
		return tmuxErr(err)
	}
	return nil
}

// findOrCreateSandbox is the repository's sandbox of spec's setup among
// all, created as spec says when it has none (#135).
func findOrCreateSandbox(out io.Writer, d deps, all []sbx.Sandbox, root string, spec sandboxSpec) (sbx.Sandbox, error) {
	var err error
	if s, ok := sbx.Find(all, root, spec.setup, d.samePath); ok {
		return s, nil
	}
	if spec.Name == "" {
		fmt.Fprintf(out, "creating a sandbox for %s (first time only; log in to Claude when the session asks)\n", filepath.Base(root))
	} else {
		fmt.Fprintf(out, "creating the %s sandbox %s for %s (first time only; log in to Claude when the session asks)\n", spec.setup, spec.Name, filepath.Base(root))
	}
	if err := d.sbx.Create(root, spec.Options); err != nil {
		if spec.from != "" {
			err = fmt.Errorf("%w; created with %s", err, spec.from)
		}
		return sbx.Sandbox{}, sbxErr(err)
	}
	if all, err = d.sbx.List(); err != nil {
		return sbx.Sandbox{}, sbxErr(err)
	}
	if s, ok := sbx.Find(all, root, spec.setup, d.samePath); ok {
		return s, nil
	}
	return sbx.Sandbox{}, envErr("sbx created no sandbox for %s (see sbx ls)", root)
}

// The environment variables that override the preferences file's settings
// of the default setup, for scripts and tests.
const (
	templateEnv  = "HQ_SBX_TEMPLATE"
	staticMCPEnv = "HQ_SBX_STATIC_MCP" // comma-separated
)

// sandboxSpec is what hq creates the sandbox of a setup with: the sbx
// options, the sandbox's name included (none for the default: sbx's own);
// setup is the setup's name; from names the settings in effect and where
// they were set, for sbx's error about them.
type sandboxSpec struct {
	sbx.Options
	setup string
	from  string
}

// sandboxSpecOf reads the settings hq creates a sandbox of the setup with
// (#58, #135), from the preferences file, those of the default each from
// its environment variable when set. The setup's sandbox name is left for
// the caller, who knows the repository. An unknown setup is refused, and
// so is any while the file's setups are wrong (prefs.InFile), naming it.
func sandboxSpecOf(d deps, setup string) (sandboxSpec, error) {
	file := prefs.Path(d.getenv)
	p, err := d.loadPrefs().Setup(setup)
	if err != nil {
		if prefs.InFile(err) {
			return sandboxSpec{}, usageErr("%s: %v", file, err)
		}
		return sandboxSpec{}, usageErr("%v", err)
	}
	r := sandboxSpec{setup: setup}
	key := "sandboxes." + setup + "."
	if setup == prefs.DefaultSetup {
		key = "sandbox."
	}
	var named []string // setting, where; where "" is the file
	// The default's settings are each overridden by their variable.
	env := func(name string) string {
		if setup != prefs.DefaultSetup {
			return ""
		}
		return d.getenv(name)
	}
	if r.Template = strings.TrimSpace(env(templateEnv)); r.Template != "" {
		named = append(named, key+"template "+r.Template, templateEnv)
	} else if r.Template = strings.TrimSpace(p.Template); r.Template != "" {
		named = append(named, key+"template "+r.Template, "")
	}
	if r.StaticMCP = names(strings.Split(env(staticMCPEnv), ",")); r.StaticMCP != nil {
		named = append(named, key+"staticMcp "+strings.Join(r.StaticMCP, ","), staticMCPEnv)
	} else if r.StaticMCP = names(p.StaticMCP); r.StaticMCP != nil {
		named = append(named, key+"staticMcp "+strings.Join(r.StaticMCP, ","), "")
	}
	if r.Model = strings.TrimSpace(p.Model); r.Model != "" {
		named = append(named, key+"model "+r.Model, "")
	}
	if r.Provider = strings.TrimSpace(p.Provider); r.Provider != "" {
		named = append(named, key+"provider "+r.Provider, "")
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
	return r, nil
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
