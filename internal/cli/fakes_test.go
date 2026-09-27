package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rkrysinski/hq/internal/prefs"
	"github.com/rkrysinski/hq/internal/proc"
	"github.com/rkrysinski/hq/internal/sbx"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
)

// fakeTmux is an in-memory tmux server.
type fakeTmux struct {
	version  string
	missing  bool
	windows  []tmux.Window
	argv     map[string][]string
	started  map[string]bool
	next     int
	sessions int
	// onNewWindow runs after a window is created, to simulate a racing hq new.
	onNewWindow  func(f *fakeTmux)
	newWindowErr error
	socket       string
	entered      string // window shown with Enter
	attached     string // window shown with Attach
	windowsErr   error
}

func (f *fakeTmux) SocketPath() (string, error) { return f.socket, nil }

func (f *fakeTmux) Enter(id string) error { f.entered = id; return nil }

func (f *fakeTmux) Attach(id string, _ tmux.Terminal) error { f.attached = id; return nil }

func (f *fakeTmux) Version() (string, error) {
	if f.missing {
		return "", &proc.Error{Name: "tmux", Msg: "not found", NotFound: true}
	}
	return f.version, nil
}

func (f *fakeTmux) Windows() ([]tmux.Window, error) {
	if f.windowsErr != nil {
		return nil, f.windowsErr
	}
	return append([]tmux.Window(nil), f.windows...), nil
}

func (f *fakeTmux) EnsureSession(string) error {
	if f.sessions == 0 {
		f.sessions = 1
		f.add("hq", nil, nil)
	}
	return nil
}

func (f *fakeTmux) add(name string, opts map[string]string, argv []string) string {
	id := fmt.Sprintf("@%d", f.next)
	f.next++
	o := map[string]string{}
	for k, v := range opts {
		o[k] = v
	}
	f.windows = append(f.windows, tmux.Window{ID: id, Name: name, Options: o})
	f.argv[id] = argv
	return id
}

func (f *fakeTmux) NewWindow(name, _ string, opts map[string]string, argv []string) (string, error) {
	if f.newWindowErr != nil {
		return "", f.newWindowErr
	}
	id := f.add(name, opts, argv)
	if f.onNewWindow != nil {
		hook := f.onNewWindow
		f.onNewWindow = nil
		hook(f)
	}
	return id, nil
}

func (f *fakeTmux) Start(id string) error { f.started[id] = true; return nil }

func (f *fakeTmux) SetOption(id, key, value string) error {
	for _, w := range f.windows {
		if w.ID == id {
			w.Options[key] = value
			return nil
		}
	}
	return errors.New("no window " + id)
}

func (f *fakeTmux) KillWindow(id string) error {
	for i, w := range f.windows {
		if w.ID == id {
			f.windows = append(f.windows[:i], f.windows[i+1:]...)
			return nil
		}
	}
	return errors.New("no window " + id)
}

// fakeSbx holds sandboxes in memory.
type fakeSbx struct {
	sandboxes []sbx.Sandbox
	created   []string
	err       error
	createErr error
	execs     [][]string // sandbox, then the command
	onExec    func(args []string)
	calls     []string // stop, rm and exec, in order
	onStop    func(sandbox string)
}

func (f *fakeSbx) setStatus(name, status string) {
	for i := range f.sandboxes {
		if f.sandboxes[i].Name == name {
			f.sandboxes[i].Status = status
		}
	}
}

func (f *fakeSbx) Stop(sandbox string) error {
	f.calls = append(f.calls, "stop "+sandbox)
	f.setStatus(sandbox, "stopped")
	if f.onStop != nil {
		f.onStop(sandbox)
	}
	return f.err
}

func (f *fakeSbx) Remove(sandbox string) error {
	f.calls = append(f.calls, "rm "+sandbox)
	for i, s := range f.sandboxes {
		if s.Name == sandbox {
			f.sandboxes = append(f.sandboxes[:i], f.sandboxes[i+1:]...)
			break
		}
	}
	return f.err
}

func (f *fakeSbx) Exec(sandbox string, args ...string) error {
	f.execs = append(f.execs, append([]string{sandbox}, args...))
	f.calls = append(f.calls, "exec "+sandbox+" "+strings.Join(args, " "))
	f.setStatus(sandbox, "running")
	if f.onExec != nil {
		f.onExec(args)
	}
	return nil
}

func (f *fakeSbx) List() ([]sbx.Sandbox, error) { return f.sandboxes, f.err }

func (f *fakeSbx) Create(ws string) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.created = append(f.created, ws)
	parts := strings.Split(ws, "/")
	f.sandboxes = append(f.sandboxes, sbx.Sandbox{Name: "claude-" + parts[len(parts)-1], Agent: "claude", Status: "running", Workspaces: []string{ws}})
	return nil
}

func (f *fakeSbx) RunArgv(sandbox string, args ...string) []string {
	return append([]string{"sbx", "run", "--name", sandbox, "--"}, args...)
}

// fakeReleases holds hq's releases in memory: tag -> file name -> content.
type fakeReleases struct {
	latest  string
	files   map[string]map[string][]byte
	err     error
	lookups int
}

func (r *fakeReleases) Latest() (string, error) {
	r.lookups++
	return r.latest, r.err
}

func (r *fakeReleases) Download(tag, dir string, files ...string) error {
	for _, name := range files {
		data, ok := r.files[tag][name]
		if !ok {
			return fmt.Errorf("no asset %s in %s", name, tag)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

type fakes struct {
	tmux  *fakeTmux
	sbx   *fakeSbx
	repos map[string]string // dir -> repository root
	dirs  map[string]bool
	cwd   string
	now   time.Time
	env   map[string]string
	tty   bool   // stdin is a terminal
	stdin string // what the user types

	states map[string]state.Report // agent id -> its state file

	releases *fakeReleases
	exe      string // the running hq, for hq update
	prefs    prefs.Prefs
}

func newFakes() *fakes {
	f := &fakes{
		tmux:  &fakeTmux{version: "3.5a", argv: map[string][]string{}, started: map[string]bool{}, socket: "/tmp/tmux-501/default"},
		env:   map[string]string{},
		sbx:   &fakeSbx{},
		repos: map[string]string{"/w/app": "/w/app", "/w/app/sub": "/w/app", "/w/app/.claude/worktrees/x": "/w/app", "/w/lib": "/w/lib"},
		dirs:  map[string]bool{"/w/app": true, "/w/app/sub": true, "/w/lib": true, "/w/plain": true, "/w/app/.claude/worktrees/x": true},
		cwd:   "/w/app",
		now:   time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC),
		tty:   true,

		states: map[string]state.Report{},

		releases: &fakeReleases{files: map[string]map[string][]byte{}},
		exe:      "/nonexistent/hq",
	}
	// pkill -f 'HQ_ID":"<id>"' ends the session: its pane dies.
	f.sbx.onExec = func(args []string) {
		for i, w := range f.tmux.windows {
			if strings.Contains(args[len(args)-1], `"`+w.Options["id"]+`"`) {
				f.tmux.windows[i].PaneDead = true
			}
		}
	}
	// Stopping a sandbox ends every session in it.
	f.sbx.onStop = func(sandbox string) {
		for i, w := range f.tmux.windows {
			if w.Options["sandbox"] == sandbox {
				f.tmux.windows[i].PaneDead = true
			}
		}
	}
	return f
}

func (f *fakes) deps() deps {
	return deps{
		tmux: f.tmux,
		sbx:  f.sbx,
		repoRoot: func(dir string) (string, bool) {
			r, ok := f.repos[dir]
			return r, ok
		},
		samePath: func(a, b string) bool { return a == b },
		isDir:    func(p string) bool { return f.dirs[p] || f.dirs["/w/app/"+p] },
		getwd:    func() (string, error) { return f.cwd, nil },
		now:      func() time.Time { return f.now },
		getenv:   func(k string) string { return f.env[k] },
		sleep:    func(d time.Duration) { f.now = f.now.Add(d) },
		canAsk:   func(io.Reader) bool { return f.tty },
		notify:   "[notify %s]",
		readState: func(_, id string) (state.Report, bool) {
			r, ok := f.states[id]
			return r, ok
		},
		pollSandboxes: f.sbx.List,

		releases:   f.releases,
		asset:      "hq-testos-testarch",
		executable: func() (string, error) { return f.exe, nil },
		loadPrefs:  func() prefs.Prefs { return f.prefs },
		savePrefs:  func(p prefs.Prefs) error { f.prefs = p; return nil },
	}
}
