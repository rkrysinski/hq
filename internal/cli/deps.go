package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/rkrysinski/hq/internal/platform"
	"github.com/rkrysinski/hq/internal/prefs"
	"github.com/rkrysinski/hq/internal/proc"
	"github.com/rkrysinski/hq/internal/repo"
	"github.com/rkrysinski/hq/internal/sbx"
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
	KillWindow(id string) error
	SocketPath() (string, error)
	Enter(id string) error
	Attach(id string, t tmux.Terminal) error
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
	samePath func(a, b string) bool
	isDir    func(path string) bool
	getwd    func() (string, error)
	now      func() time.Time
	getenv   func(key string) string
	terminal tmux.Terminal
	sleep    func(time.Duration)
	canAsk   func(stdin io.Reader) bool // stdin is a terminal to confirm on

	releases   Releases
	asset      string // this platform's binary in a release
	executable func() (string, error)
	loadPrefs  func() prefs.Prefs
	savePrefs  func(prefs.Prefs) error
}

func defaultDeps() deps {
	run := proc.Exec{}
	plat := platform.Detect(os.Getenv, os.ReadFile, run)
	return deps{
		tmux:     tmux.Client{Run: run, Socket: os.Getenv("HQ_TMUX_SOCKET")},
		sbx:      sbx.Client{Run: run, Platform: plat},
		repoRoot: func(dir string) (string, bool) { return repo.Root(run, dir) },
		samePath: repo.Same,
		isDir: func(path string) bool {
			fi, err := os.Stat(path)
			return err == nil && fi.IsDir()
		},
		getwd:    os.Getwd,
		now:      time.Now,
		getenv:   os.Getenv,
		terminal: run,
		sleep:    time.Sleep,
		canAsk:   isTerminal,

		releases: update.Releases{Run: run, Bin: "gh"},
		asset:    update.Asset(runtime.GOOS, runtime.GOARCH),
		executable: func() (string, error) {
			exe, err := os.Executable()
			if err != nil {
				return "", err
			}
			return filepath.EvalSymlinks(exe)
		},
		loadPrefs: func() prefs.Prefs { return prefs.Load(prefs.Path(os.Getenv)) },
		savePrefs: func(p prefs.Prefs) error { return prefs.Save(prefs.Path(os.Getenv), p) },
	}
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
