package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/rkrysinski/hq/internal/proc"
	"github.com/rkrysinski/hq/internal/repo"
	"github.com/rkrysinski/hq/internal/sbx"
	"github.com/rkrysinski/hq/internal/tmux"
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
}

func defaultDeps() deps {
	run := proc.Exec{}
	return deps{
		tmux:     tmux.Client{Run: run, Socket: os.Getenv("HQ_TMUX_SOCKET")},
		sbx:      sbx.Client{Run: run, Bin: "sbx"},
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
