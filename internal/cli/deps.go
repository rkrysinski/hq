package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rkrysinski/hq/internal/dash"
	"github.com/rkrysinski/hq/internal/dialog"
	"github.com/rkrysinski/hq/internal/gh"
	"github.com/rkrysinski/hq/internal/iterm"
	"github.com/rkrysinski/hq/internal/platform"
	"github.com/rkrysinski/hq/internal/prefs"
	"github.com/rkrysinski/hq/internal/proc"
	"github.com/rkrysinski/hq/internal/repo"
	"github.com/rkrysinski/hq/internal/sbx"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
	"github.com/rkrysinski/hq/internal/update"
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
	KeepFirst(id, key, prefix, value string) (string, error)
	Screens(panes []string) (map[string]string, error)
	Paste(pane, text string) error
	Submit(pane string) error
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
	Create(workspace string) error
	RunArgv(sandbox string, agentArgs ...string) []string
	Exec(sandbox string, args ...string) error
	Stop(sandbox string) error
	Remove(sandbox string) error
}

// Releases is the seam to hq's GitHub releases (design §3.9).
type Releases interface {
	Latest() (string, error)
	Download(tag, dir string, files ...string) error
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
	// readState reads an agent's state file (design §3.4).
	readState func(root, id string) (state.Report, bool)
	// removeState deletes an agent's state files once it is gone (design §3.4).
	removeState func(root, id string) error
	// postMessage leaves a message in an agent's inbox, takeMessages takes
	// what waits there for hq to type it in, and pending counts it (hq
	// send, ADR 0012).
	postMessage  func(root, id, text string, now bool) error
	takeMessages func(root, id string) ([]string, error)
	pending      func(root, id string) int
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
	// itermProfile adds the iTerm2 profile hq when iTerm2 is present on
	// macOS and reports whether it wrote it (design §3.7, §3.9).
	itermProfile func() (bool, error)
	loadPrefs    func() prefs.Prefs
	savePrefs    func(prefs.Prefs) error
}

// ghTimeout bounds gh pr list, which goes to GitHub.
const ghTimeout = 30 * time.Second

// sbxPollTimeout bounds sbx ls when it only tells which sandboxes run;
// sbx ls usually answers within a second.
const sbxPollTimeout = 5 * time.Second

func defaultDeps() deps {
	run := proc.Exec{}
	plat := platform.Detect(os.Getenv, os.ReadFile, run)
	var placeholder, session []string
	if exe, err := executable(); err == nil {
		placeholder, session = []string{exe, slotCommand}, []string{exe, sessionCommand}
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
			_, err := run.Run(argv[0], argv[1:]...)
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

		readState:   state.Read,
		removeState: state.Remove,
		postMessage: func(root, id, text string, now bool) error {
			return state.Post(root, id, text, now, time.Now())
		},
		takeMessages:  state.Take,
		pending:       state.Pending,
		pollSandboxes: sbx.Client{Run: proc.Exec{Timeout: sbxPollTimeout}, Platform: plat}.List,
		notify:        plat.NotifySequence(),

		runList:   dash.Run,
		runDialog: dialog.Run,
		pid:       os.Getpid(),
		alive:     func(pid int) bool { return syscall.Kill(pid, 0) == nil },

		releases:   update.Releases{Run: run, Bin: "gh"},
		asset:      update.Asset(runtime.GOOS, runtime.GOARCH),
		executable: executable,
		itermProfile: func() (bool, error) {
			home, _ := os.UserHomeDir()
			return iterm.Install(runtime.GOOS, home, "/")
		},
		loadPrefs: func() prefs.Prefs { return prefs.Load(prefs.Path(os.Getenv)) },
		savePrefs: func(p prefs.Prefs) error { return prefs.Save(prefs.Path(os.Getenv), p) },
	}
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
		return envErr("sbx not found; install Docker Sandboxes")
	}
	return envErr("%v", err)
}

// tmuxErr turns a failed tmux call into an environment error.
func tmuxErr(err error) error { return envErr("%s", fmt.Sprint(err)) }
