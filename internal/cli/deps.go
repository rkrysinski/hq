package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rkrysinski/hq/internal/agent"
	"github.com/rkrysinski/hq/internal/dash"
	"github.com/rkrysinski/hq/internal/dialog"
	"github.com/rkrysinski/hq/internal/gh"
	"github.com/rkrysinski/hq/internal/iterm"
	"github.com/rkrysinski/hq/internal/journal"
	"github.com/rkrysinski/hq/internal/kills"
	"github.com/rkrysinski/hq/internal/mcpconfig"
	"github.com/rkrysinski/hq/internal/platform"
	"github.com/rkrysinski/hq/internal/prefs"
	"github.com/rkrysinski/hq/internal/proc"
	"github.com/rkrysinski/hq/internal/repo"
	"github.com/rkrysinski/hq/internal/sbx"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
	"github.com/rkrysinski/hq/internal/update"
	"github.com/rkrysinski/hq/internal/version"
	"golang.org/x/term"
)

// Tmux is the seam to the tmux server (design §7.2).
type Tmux interface {
	Version() (string, error)
	Windows() ([]tmux.Window, error)
	EnsureSession(dir string) error
	NewWindow(name, dir string, options map[string]string, argv []string) (string, error)
	Start(id string) error
	Respawn(pane, dir string, argv []string) error
	SetOption(id, key, value string) error
	Screens(panes []string) (map[string]string, error)
	StyledScreen(pane string) (string, error)
	Paste(pane, text string) error
	Type(pane, text string) error
	Submit(pane string) error
	Press(pane, key string) error
	LastScreen(pane string) (string, error)
	KillWindow(id string) error
	SocketPath() (string, error)
	Enter(id string) error
	Attach(id string, t tmux.Terminal) error
	ShowAttached(id string) ([]string, error)
	Dashboard(dir string, list []string) (tmux.Dash, error)
	RespawnList(pane string, list []string) error
	TerminalHeight(pane string) (int, error)
	ResizeHeight(pane string, lines int) error
	KeepMargins(pane string) error
	SetFooter(text string) error
	MarkList(pane string, pid int) error
	SessionValue(key string) (string, error)
	Dock(window, title string) error
	Show(window, title string) error
	FitHomes() error
	SetTitle(pane, title string) error
	SetSessionValue(key, value string) error
	Popup(pane, dir string, w, h int, argv []string) error
	BindChords(argv []string, hints string) error
	Message(text string) error
	FocusList() error
	Leave(message string) error
}

// Sandboxes is the seam to sbx (design §7.2).
type Sandboxes interface {
	List() ([]sbx.Sandbox, error)
	Create(workspace string, o sbx.Options) error
	RunArgv(sandbox string, agentArgs ...string) []string
	Exec(sandbox string, args ...string) error
	Output(sandbox string, args ...string) ([]byte, error)
	Stop(sandbox string) error
	Remove(sandbox string) error
	// MCPCommand, MCPAdd and MCPLoad are sbx's MCP servers: the command line
	// of one registered, registering one, and loading one into a sandbox
	// (hq mcp install sbx, hq mcp load, design §3.12).
	MCPCommand(name string) ([]string, bool, error)
	MCPAdd(name, command string, args []string) error
	MCPLoad(name, sandbox string) error
}

// Releases is the seam to hq's GitHub releases (design §3.9).
type Releases interface {
	Latest() (string, error)
	Download(tag, dir string, files ...string) error
	// Generation is the generation a release publishes, verified against
	// its checksums; published is false when it publishes none.
	Generation(tag string) (gen int, published bool, err error)
}

// deps is everything a command needs from outside hq.
type deps struct {
	tmux     Tmux
	sbx      Sandboxes
	repoRoot func(dir string) (string, bool)
	// worktreeTop is the top of the work tree dir is in (a worktree's own
	// root); fromSbx turns a path the sandbox sees into hq's.
	worktreeTop func(dir string) (string, bool)
	fromSbx     func(path string) (string, error)
	// editor opens VS Code on a directory, browse a URL (design §3.10).
	editor func(dir string) error
	browse func(url string) error
	// raise brings the terminal window of the client on tty to the front
	// (design §3.11).
	raise func(tty string) error
	// pullRequests asks gh for a repository's pull requests (design §5.2).
	pullRequests func(repo string) (map[string]gh.PR, error)
	samePath     func(a, b string) bool
	isDir        func(path string) bool
	getwd        func() (string, error)
	now          func() time.Time
	getenv       func(key string) string
	terminal     tmux.Terminal
	sleep        func(time.Duration)
	canAsk       func(stdin io.Reader) bool // stdin is a terminal to confirm on
	// rawTerminal puts stdin, when it is a terminal, in raw mode, so every
	// key reaches hq as it is typed and none is echoed; the function it
	// returns restores it.
	rawTerminal func(stdin io.Reader) func()
	// foreground runs a program on this terminal until it ends and returns
	// its exit status; err only when it could not start.
	foreground func(argv []string) (int, error)
	// removeState deletes what an agent keeps beside its repository once
	// it is gone: its hook's working files, hq's record of it and its
	// inbox (design §3.4).
	removeState func(root, id string) error
	// journal is the agents' journals, where an agent's state comes from:
	// every look ingests the event files an agent's hook left into its
	// journal, appends what it sees and folds it, and a kill removes it
	// (ADR 0018, design §3.4).
	journal journal.Store
	// folds keeps the journals folded for this process, so a long-running
	// one (the list, hq wait) folds only the events new since its last
	// look (design §5.1).
	folds *agent.Folds
	// killer says who asks for the kills this command makes, for the
	// trace (#147): the way it came, and in hq mcp the client and the
	// calling agent when known. traceKill adds a kill to the trace and
	// readKills reads it (kills.Path).
	killer    kills.Kill
	traceKill func(k kills.Kill) error
	readKills func() ([]kills.Kill, error)
	// writeRecord keeps hq's record of an agent beside its repository, and
	// records reads those of a repository, for hq restore (design §3.3).
	writeRecord func(root, id string, r state.Record) error
	records     func(root string) ([]state.Record, error)
	// postMessage leaves a message in an agent's inbox and returns the
	// name it waits under, takeMessages takes what waits there for hq to
	// type it in, holdMessages puts back what hq types in later, out of the
	// hooks' reach, pending counts it, held names what is held, and
	// oldestMessage says when the oldest of it was sent (hq send, ADR 0012,
	// #173).
	postMessage   func(root, id string, m state.Message, now bool) (string, error)
	takeMessages  func(root, id string) ([]state.Message, error)
	holdMessages  func(root, id string, msgs []state.Message) error
	held          func(root, id string) []string
	pending       func(root, id string) int
	oldestMessage func(root, id string) (time.Time, bool)
	// questions reads the question dialog an agent's journal holds as the
	// latest its hook reported; leaveAnswer leaves the answer to it in the agent's inbox for
	// the hook that holds on it, answerTaken tells whether that hook took
	// it, withdrawAnswer takes it back unless the hook took it first, and
	// answersGot reads from the journal what Claude reports it got once the
	// dialog's tool ended (hq answer, design §3.4, ADR 0013).
	questions      func(store journal.Store, id string) (state.Questions, error)
	leaveAnswer    func(root, id string, q state.Questions, decision []byte) error
	answerTaken    func(root, id string, q state.Questions) bool
	withdrawAnswer func(root, id string, q state.Questions) bool
	answersGot     func(store journal.Store, id string) (map[string]string, bool)
	// supervisor says the commands run for the supervisor, in hq mcp: the
	// prompts and the messages they give agents are its own, not the
	// user's, and a turn made only of them notifies nobody (spec §5).
	supervisor bool
	// announce tells an agent's hooks that its next prompt is the
	// supervisor's, announced whether that word still waits for the
	// prompt, and withdraw takes it back (design §3.5).
	announce  func(root, id string) error
	announced func(root, id string) bool
	withdraw  func(root, id string) error
	// pollSandboxes is sbx ls within a time limit, for the state of agents
	// (design §5.1, §7.1); a slow sbx must not hold up hq ls.
	pollSandboxes func() ([]sbx.Sandbox, error)
	// notify is the terminal's desktop notification sequence (design §3.5).
	notify string

	// runList runs the list program on this terminal until q (design §3.8).
	runList func(dash.Source) error
	// runDialog runs a dialog on this terminal, a popup's, until it closes.
	runDialog func(tea.Model) error
	pid       int                // this process
	alive     func(pid int) bool // a process with that pid exists

	releases   Releases
	asset      string // this platform's binary in a release
	executable func() (string, error)
	// stampFile tells one state of a file from another, askGeneration asks
	// the hq at a path its generation, and replaceProcess runs a program in
	// place of this one: how the list and hq mcp notice an update to
	// another generation and step aside (design §3.11, §3.12).
	stampFile      func(path string) (fileStamp, error)
	askGeneration  func(exe string) (int, error)
	replaceProcess func(argv []string) error
	// itermProfile adds the iTerm2 profile hq when iTerm2 is present on
	// macOS and reports whether it wrote it (design §3.7, §3.9).
	itermProfile func() (bool, error)
	loadPrefs    func() prefs.Prefs
	// updatePrefs changes the preferences file under its lock (prefs.Update).
	updatePrefs func(change func(*prefs.Prefs) bool) error
	// detach starts a program that outlives hq, without waiting for it
	// (proc.Exec.Detach): the update check, so the command that found it due
	// never waits on GitHub (design §3.9).
	detach func(argv []string) error
	// stderrTerminal reports whether w, a command's stderr, is a terminal.
	stderrTerminal func(w io.Writer) bool

	// home is the user's home directory.
	home func() (string, error)
	// desktopConfig is Claude Desktop's configuration file and whether it
	// runs,
	// windowsCodeConfig Claude Code's on Windows seen from WSL (ok false
	// elsewhere), appServer how such an app starts hq's command line with an
	// environment, and installConfig sets hq up in an MCP client's
	// configuration file (hq mcp install, design §3.12).
	desktopConfig     func() (platform.Desktop, error)
	windowsCodeConfig func() (path string, ok bool, err error)
	appServer         func(argv, env []string) mcpconfig.Server
	// sbxGateway is the command line sbx's MCP gateway starts hq's command
	// line argv with, environment env (platform.SbxSide.Gateway).
	sbxGateway    func(argv, env []string) (command string, args []string)
	installConfig func(f mcpconfig.File, s mcpconfig.Server) (mcpconfig.Result, error)
	// serveMCP runs an MCP server over stdin and stdout until the client
	// closes them.
	serveMCP func(*mcp.Server) error
}

// ghTimeout bounds gh pr list, which goes to GitHub.
const ghTimeout = 30 * time.Second

// sbxPollTimeout bounds sbx ls when it only tells which sandboxes run, as
// for the list and before a kill (endAgents); sbx ls usually answers within
// a second.
const sbxPollTimeout = 5 * time.Second

func defaultDeps() deps {
	run := proc.Exec{}
	plat := platform.Detect(os.Getenv, os.ReadFile, exec.LookPath, run)
	var placeholder, session []string
	if exe, err := executable(); err == nil {
		placeholder, session = []string{exe, slotCommand}, []string{exe, sessionCommand}
	} else {
		// An agent's window refers to its settings, which only hq __session
		// writes out (claudeArgs): hq as it was called stands in.
		session = []string{os.Args[0], sessionCommand}
	}
	return deps{
		tmux:        tmux.Client{Run: run, Socket: os.Getenv("HQ_TMUX_SOCKET"), Placeholder: placeholder, Session: session},
		sbx:         sbx.Client{Run: run, Platform: plat},
		repoRoot:    func(dir string) (string, bool) { return repo.Root(run, dir) },
		samePath:    repo.Same,
		worktreeTop: func(dir string) (string, bool) { return repo.Top(run, dir) },
		fromSbx:     plat.FromSbx,
		editor: func(dir string) error {
			argv := plat.Editor(dir)
			// Not a pipe for its stderr: VS Code's launcher on WSL 1 opens
			// nothing with one, and says it worked (#7).
			_, err := proc.Exec{StderrFile: true}.Run(argv[0], argv[1:]...)
			return err
		},
		browse: func(url string) error {
			argv := plat.Browser(url)
			_, err := run.Run(argv[0], argv[1:]...)
			return err
		},
		raise: func(tty string) error {
			argv := plat.Raise(tty)
			_, err := run.Run(argv[0], argv[1:]...)
			return err
		},
		pullRequests: func(repo string) (map[string]gh.PR, error) {
			return gh.PullRequests(proc.Exec{Dir: repo, Timeout: ghTimeout})
		},
		isDir: func(path string) bool {
			fi, err := os.Stat(path)
			return err == nil && fi.IsDir()
		},
		getwd:       os.Getwd,
		now:         time.Now,
		getenv:      os.Getenv,
		terminal:    run,
		sleep:       time.Sleep,
		canAsk:      isTerminal,
		rawTerminal: rawTerminal,
		foreground:  func(argv []string) (int, error) { return run.Foreground(argv[0], argv[1:]...) },

		removeState: state.Remove,
		journal:     journal.Files{Dir: journal.Dir(os.Getenv)},
		folds:       agent.NewFolds(),
		killer:      kills.Kill{By: kills.ByCLI},
		traceKill:   func(k kills.Kill) error { return kills.Append(kills.Path(os.Getenv), k) },
		readKills:   func() ([]kills.Kill, error) { return kills.Read(kills.Path(os.Getenv)) },
		writeRecord: state.WriteRecord,
		records:     state.Records,
		postMessage: func(root, id string, m state.Message, now bool) (string, error) {
			return state.Post(root, id, m, now, time.Now())
		},
		takeMessages:  state.Take,
		holdMessages:  state.Hold,
		held:          state.HeldNames,
		pending:       state.Pending,
		oldestMessage: state.Oldest,

		questions:      journalQuestions,
		leaveAnswer:    state.LeaveAnswer,
		answerTaken:    state.AnswerTaken,
		withdrawAnswer: state.WithdrawAnswer,
		answersGot:     journalAnswers,

		announce:      state.Announce,
		announced:     state.Announced,
		withdraw:      state.Withdraw,
		pollSandboxes: sbx.Client{Run: proc.Exec{Timeout: sbxPollTimeout}, Platform: plat}.List,
		notify:        plat.NotifySequence(),

		runList:   dash.Run,
		runDialog: dialog.Run,
		pid:       os.Getpid(),
		alive:     func(pid int) bool { return syscall.Kill(pid, 0) == nil },

		releases:   update.Releases{Base: update.Base(os.Getenv), Agent: "hq/" + version.Version},
		asset:      update.Asset(platform.OS, runtime.GOARCH),
		executable: executable,
		stampFile:  stampFile,
		askGeneration: func(exe string) (int, error) {
			return generationOf(exe, generationTimeout)
		},
		replaceProcess: replaceProcess,
		itermProfile: func() (bool, error) {
			home, _ := os.UserHomeDir()
			return iterm.Install(platform.OS, home, "/")
		},
		loadPrefs: func() prefs.Prefs { return prefs.Load(prefs.Path(os.Getenv)) },
		updatePrefs: func(change func(*prefs.Prefs) bool) error {
			return prefs.Update(prefs.Path(os.Getenv), change)
		},
		detach:         func(argv []string) error { return run.Detach(argv[0], argv[1:]...) },
		stderrTerminal: isTerminalOut,

		home: os.UserHomeDir,
		desktopConfig: func() (platform.Desktop, error) {
			home, err := os.UserHomeDir()
			if err != nil {
				return platform.Desktop{}, err
			}
			return plat.DesktopConfig(home)
		},
		windowsCodeConfig: plat.WindowsCodeConfig,
		appServer: func(argv, env []string) mcpconfig.Server {
			cmd, args, e := plat.AppServer(argv, env)
			return mcpconfig.Server{Command: cmd, Args: args, Env: e}
		},
		sbxGateway: plat.Gateway,
		installConfig: func(f mcpconfig.File, s mcpconfig.Server) (mcpconfig.Result, error) {
			return mcpconfig.Install(f, s, time.Now())
		},
		serveMCP: func(s *mcp.Server) error { return s.Run(context.Background(), &mcp.StdioTransport{}) },
	}
}

// lookSources are where a look reads and appends (agent.Sources): the
// journals' store, tmux for the windows and the screens, and hq mcp's
// word to an agent's hook that its next prompt is the supervisor's
// (ID.next).
type lookSources struct {
	journal.Store
	windows  func() ([]tmux.Window, error)
	screens  func(panes []string) (map[string]string, error)
	withdraw func(root, id string) error
}

func (s lookSources) Screens(panes []string) (map[string]string, error) { return s.screens(panes) }

func (s lookSources) Withdraw(root, id string) error { return s.withdraw(root, id) }

// Stands says whether home window w is among the windows tmux lists now.
// When tmux cannot list them it does not: the look then writes no journal
// that is not there, which a kill under way would leave for good, and the
// next look writes it once tmux answers.
func (s lookSources) Stands(w string) bool {
	ws, err := s.windows()
	return err == nil && slices.ContainsFunc(ws, func(x tmux.Window) bool { return x.ID == w })
}

// sources are the look's sources of these dependencies.
func (d deps) sources() agent.Sources {
	return lookSources{d.journal, d.tmux.Windows, d.tmux.Screens, d.withdraw}
}

// looker takes the look of a command that takes one and returns
// (agent.Looker), from these dependencies.
func (d deps) looker() agent.Looker {
	return agent.Looker{Windows: d.tmux.Windows, Sources: d.sources(), Sandboxes: d.pollSandboxes, Now: d.now, Sleep: d.sleep, Folds: d.folds}
}

// lookErr is the error of a look that failed (agent.Looker.Look) as a
// command's: sbx's or tmux's, as an environment error.
func lookErr(err error) error {
	var s *agent.SbxError
	if errors.As(err, &s) {
		return sbxErr(s)
	}
	var t *agent.TmuxError
	if errors.As(err, &t) {
		return tmuxErr(t)
	}
	return err
}

// refusalErr is the look's refusal of an agent by name as a command's:
// none of that name (exit 2), or an outdated one (agent.NotFoundError,
// agent.OutdatedError).
func refusalErr(err error) error {
	var none *agent.NotFoundError
	if errors.As(err, &none) {
		return notFoundErr("%s", err)
	}
	return usageErr("%s", err)
}

// look is a look at the agents of the home windows ws (agent.Look), with
// sbx's answer running, as the list and hq wait take it.
func (d deps) look(ws []tmux.Window, running agent.Sandboxes) []agent.Agent {
	return d.withoutKilled(agent.Look(ws, running, d.now(), d.sources(), d.folds))
}

// withoutKilled are the agents of a look but those whose journal it found
// gone (agent.Agent.JournalGone) and whose home windows are gone by now:
// hq kill kills an agent's window before it removes its journal, so
// windows listed after a look that found the journal of an agent it was
// removing gone show its window gone, and the agent is left out as the
// next look leaves it out (#239). tmux is asked again only when the look
// found a journal gone; when it cannot say, every agent stays.
func (d deps) withoutKilled(as []agent.Agent) []agent.Agent {
	if !slices.ContainsFunc(as, agent.Agent.JournalGone) {
		return as
	}
	ws, err := d.tmux.Windows()
	if err != nil {
		return as
	}
	return slices.DeleteFunc(as, func(a agent.Agent) bool {
		return a.JournalGone() && !slices.ContainsFunc(ws, func(w tmux.Window) bool { return w.ID == a.Window })
	})
}

// fromJournals are the agents of the home windows ws with their state
// from their journals, no screen read (agent.FromJournals).
func (d deps) fromJournals(ws []tmux.Window) []agent.Agent {
	return agent.FromJournals(ws, d.sources(), d.now(), d.folds)
}

// removeAgentFiles deletes what an agent leaves once it is gone: its hook's
// working files, its record, its inbox, its journal and its event files
// (design §3.4).
func removeAgentFiles(d deps, root, id string) {
	_ = d.removeState(root, id)
	_ = d.journal.Remove(root, id)
}

// executable is the path of the running hq, links resolved.
func executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// rawTerminal puts r in raw mode when it is a terminal and returns what
// restores it.
func rawTerminal(r io.Reader) func() {
	f, ok := r.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return func() {}
	}
	old, err := term.MakeRaw(int(f.Fd()))
	if err != nil {
		return func() {}
	}
	return func() { _ = term.Restore(int(f.Fd()), old) }
}

// isTerminalOut reports whether w is a terminal, not a pipe or a file.
func isTerminalOut(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// isTerminal reports whether r is a terminal that a confirmation can be
// asked on (not a pipe, a file or /dev/null).
func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// checkTmux fails with exit 3 when tmux is missing or too old (design §3.2).
func checkTmux(t Tmux) error {
	v, err := t.Version()
	if err != nil {
		var pe *proc.Error
		if errors.As(err, &pe) && pe.NotFound {
			return envErr("tmux not found; install tmux %s or newer", tmux.MinVersion)
		}
		return envErr("tmux: %v", err)
	}
	if !tmux.AtLeast(v, tmux.MinVersion) {
		return envErr("tmux %s is too old, %s or newer needed (Ubuntu 24.04 ships it)", v, tmux.MinVersion)
	}
	return nil
}

// sbxErr turns a failed sbx call into an environment error.
func sbxErr(err error) error {
	var pe *proc.Error
	if errors.As(err, &pe) && pe.NotFound {
		// sbx.Client says what was not found, as the platform names it.
		return envErr("%s; install Docker Sandboxes", pe.Msg)
	}
	return envErr("%v", err)
}

// tmuxErr turns a failed tmux call into an environment error.
func tmuxErr(err error) error { return envErr("%s", fmt.Sprint(err)) }
