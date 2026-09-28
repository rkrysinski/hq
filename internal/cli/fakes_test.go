package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rkrysinski/hq/internal/dash"
	"github.com/rkrysinski/hq/internal/desktop"
	"github.com/rkrysinski/hq/internal/gh"
	"github.com/rkrysinski/hq/internal/platform/platformtest"
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
	entered      string   // window shown with Enter
	attached     string   // window shown with Attach
	clients      []string // terminals of the clients attached to hq's session
	shown        string   // window shown to them
	windowsErr   error

	dash       tmux.Dash // the dashboard window, once made
	dashList   []string  // the list pane's program
	respawned  int       // times the list program was started again
	height     int       // the height of the terminal showing the dashboard
	resized    []int     // heights given to the list pane, in order
	margins    int       // times the margins beside the slot were set back
	footer     string
	listPID    int // @hq_list_pid on the list pane
	session    map[string]string
	docked     string // window docked last, with its frame title
	dockTitle  string
	titles     []string // frame titles set with SetTitle, "pane=title"
	dockErr    error
	popups     []string             // popups opened: "pane dir WxH argv..."
	chords     string               // what BindChords was given: "argv... | hints"
	messages   []string             // shown on the status line
	screens    map[string]string    // what panes show, by pane id
	onScreens  func()               // called after each Screens, e.g. to move the screens on
	onKeep     func(id, key string) // called as KeepFirst starts, e.g. to have another process store first
	onSet      func(id, key string) // called after SetOption stored, e.g. to look as another process
	screenErr  error
	focused    int      // times the keys were put on the list
	left       []string // Leave calls, by message
	leaveErr   error
	onLeave    func()
	onAttach   func()
	respawns   []string // agent panes given a new program, in order
	respawnErr error
	// lastScreens is what shows each pane's latest output again, by pane.
	lastScreens   map[string]string
	lastScreenErr error
	// pasted is what was pasted into panes ("pane text"), submitted the
	// panes Enter was pressed in; pasteErr fails the paste.
	pasted    []string
	submitted []string
	pasteErr  error
}

func (f *fakeTmux) LastScreen(pane string) (string, error) {
	return f.lastScreens[pane], f.lastScreenErr
}

func (f *fakeTmux) FocusList() error { f.focused++; return nil }

func (f *fakeTmux) Paste(pane, text string) error {
	if f.pasteErr != nil {
		return f.pasteErr
	}
	f.pasted = append(f.pasted, pane+" "+text)
	return nil
}

func (f *fakeTmux) Submit(pane string) error {
	f.submitted = append(f.submitted, pane)
	return nil
}

func (f *fakeTmux) Leave(message string) error {
	if f.onLeave != nil {
		f.onLeave()
	}
	f.left = append(f.left, message)
	return f.leaveErr
}

func (f *fakeTmux) BindChords(argv []string, hints string) error {
	f.chords = strings.Join(argv, " ") + " | " + hints
	return nil
}

func (f *fakeTmux) Message(text string) error { f.messages = append(f.messages, text); return nil }

func (f *fakeTmux) Popup(pane, dir string, w, h int, argv []string) error {
	f.popups = append(f.popups, fmt.Sprintf("%s %s %dx%d %s", pane, dir, w, h, strings.Join(argv, " ")))
	return nil
}

func (f *fakeTmux) Dock(window, title string) error {
	if f.dockErr != nil {
		return f.dockErr
	}
	f.docked, f.dockTitle = window, title
	for i := range f.windows {
		f.windows[i].Docked = f.windows[i].ID == window
		if f.windows[i].Docked {
			f.windows[i].Title = title
		}
	}
	return nil
}

func (f *fakeTmux) SetTitle(pane, title string) error {
	f.titles = append(f.titles, pane+"="+title)
	return nil
}

func (f *fakeTmux) SessionValue(key string) (string, error) { return f.session[key], nil }

func (f *fakeTmux) SetSessionValue(key, value string) error {
	if f.session == nil {
		f.session = map[string]string{}
	}
	f.session[key] = value
	return nil
}

func (f *fakeTmux) Dashboard(dir string, list []string) (tmux.Dash, error) {
	_ = f.EnsureSession(dir)
	f.dashList = list
	if f.dash.Window == "" {
		f.dash = tmux.Dash{Window: f.windows[0].ID, List: "%1", Slot: "%0", Started: true}
	} else {
		f.dash.Started = false
	}
	d := f.dash
	d.ListPID = f.listPID
	return d, nil
}

func (f *fakeTmux) RespawnList(pane string, list []string) error {
	f.respawned++
	f.dashList = list
	return nil
}

func (f *fakeTmux) TerminalHeight(string) (int, error) { return f.height, nil }

func (f *fakeTmux) ResizeHeight(_ string, lines int) error {
	f.resized = append(f.resized, lines)
	return nil
}

func (f *fakeTmux) KeepMargins(string) error { f.margins++; return nil }

func (f *fakeTmux) SetFooter(text string) error { f.footer = text; return nil }

func (f *fakeTmux) MarkList(_ string, pid int) error { f.listPID = pid; return nil }

func (f *fakeTmux) SocketPath() (string, error) { return f.socket, nil }

func (f *fakeTmux) Enter(id string) error { f.entered = id; return nil }

func (f *fakeTmux) Attach(id string, _ tmux.Terminal) error {
	f.attached = id
	if f.onAttach != nil {
		f.onAttach()
	}
	return nil
}

func (f *fakeTmux) ShowAttached(id string) ([]string, error) {
	if len(f.clients) > 0 {
		f.shown = id
	}
	return f.clients, nil
}

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

// Respawn runs argv in the window whose pane is pane: the pane lives again.
func (f *fakeTmux) Respawn(pane, _ string, argv []string) error {
	if f.respawnErr != nil {
		return f.respawnErr
	}
	for i, w := range f.windows {
		if w.Pane == pane {
			f.windows[i].PaneDead = false
			f.argv[w.ID] = argv
			f.respawns = append(f.respawns, pane)
			return nil
		}
	}
	return errors.New("no pane " + pane)
}

func (f *fakeTmux) StyledScreen(pane string) (string, error) {
	if f.screenErr != nil {
		return "", f.screenErr
	}
	return f.screens[pane], nil
}

func (f *fakeTmux) Screens(panes []string) (map[string]string, error) {
	if f.screenErr != nil {
		return nil, f.screenErr
	}
	out := map[string]string{}
	for _, p := range panes {
		if s, ok := f.screens[p]; ok {
			out[p] = s
		}
	}
	if f.onScreens != nil {
		f.onScreens()
	}
	return out, nil
}

func (f *fakeTmux) KeepFirst(id, key, prefix, value string) (string, error) {
	if f.onKeep != nil {
		f.onKeep(id, key)
	}
	for _, w := range f.windows {
		if w.ID == id {
			if !strings.HasPrefix(w.Options[key], prefix) {
				w.Options[key] = value
			}
			return w.Options[key], nil
		}
	}
	return "", errors.New("no window " + id)
}

func (f *fakeTmux) SetOption(id, key, value string) error {
	for _, w := range f.windows {
		if w.ID == id {
			w.Options[key] = value
			if f.onSet != nil {
				f.onSet(id, key)
			}
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
	// onExec is what the command does; its error is the command's.
	onExec func(args []string) error
	// orphans are the Claude sessions that run in a sandbox although their
	// pane died (its host-side sbx run ended), by agent id.
	orphans map[string]bool
	calls   []string // stop, rm and exec, in order
	onStop  func(sandbox string)
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
		return f.onExec(args)
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
	tops  map[string]string // dir -> top of its work tree
	// edited are the directories VS Code opened on; editorErr fails it.
	edited    []string
	editorErr error
	// browsed are the URLs opened; prs is gh's answer by repository.
	browsed []string
	// raised are the terminals whose window was brought to the front;
	// raiseErr fails it.
	raised   []string
	raiseErr error
	prs      map[string]map[string]gh.PR
	dirs     map[string]bool
	cwd      string
	now      time.Time
	onSleep  func() // called after each sleep, e.g. to move the agents on
	env      map[string]string
	tty      bool   // stdin is a terminal
	stdin    string // what the user types

	states  map[string]state.Report // agent id -> its state file
	details map[string]state.Detail // agent id -> what its state files hold in full
	// inbox is what waits for each agent id: the text, with " (now)" when
	// sent with --now; postErr and takeErr fail posting and taking.
	inbox   map[string][]string
	postErr error
	takeErr error
	removed []string // "repo id" of each agent whose state files were removed

	releases *fakeReleases
	exe      string // the running hq, for hq update
	// profiles counts the asks for the iTerm2 profile; profileWrote and
	// profileErr are the answer.
	profiles     int
	profileWrote bool
	profileErr   error
	prefs        prefs.Prefs

	rawOn, rawOff int                     // terminals put in raw mode, and restored
	alive         map[int]bool            // processes that exist
	listRan       func(dash.Source) error // the list program; returns at q
	dialog        tea.Model               // the dialog run last

	// ran are the programs run on the terminal (foreground); ranCode and
	// ranErr are how they end.
	ran     [][]string
	ranCode int
	ranErr  error

	// desktop is Claude Desktop's configuration file (desktopErr: it
	// cannot be found); installed are the servers set up in it, and
	// installResult and installErr the answer; served is the MCP server
	// run over stdio, serveErr how it ends.
	desktop       string
	desktopErr    error
	installed     []desktop.Server
	installResult desktop.Result
	installErr    error
	served        *mcp.Server
	serveErr      error
}

func newFakes() *fakes {
	f := &fakes{
		tmux:  &fakeTmux{version: "3.5a", argv: map[string][]string{}, started: map[string]bool{}, socket: "/tmp/tmux-501/default"},
		env:   map[string]string{},
		sbx:   &fakeSbx{},
		repos: map[string]string{"/w/app": "/w/app", "/w/app/sub": "/w/app", "/w/app/.claude/worktrees/x": "/w/app", "/w/lib": "/w/lib"},
		tops: map[string]string{"/w/app": "/w/app", "/w/app/sub": "/w/app", "/w/app/.claude/worktrees/x": "/w/app/.claude/worktrees/x",
			"/w/app/.claude/worktrees/x/sub": "/w/app/.claude/worktrees/x", "/w/lib": "/w/lib"},
		dirs: map[string]bool{"/w/app": true, "/w/app/sub": true, "/w/lib": true, "/w/plain": true, "/w/app/.claude/worktrees/x": true},
		cwd:  "/w/app",
		now:  time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC),
		tty:  true,

		states:  map[string]state.Report{},
		inbox:   map[string][]string{},
		details: map[string]state.Detail{},

		releases: &fakeReleases{files: map[string]map[string][]byte{}},
		exe:      "/nonexistent/hq",
	}
	// pkill -f 'HQ_ID":"<id>"' ends the session: its pane dies, or the
	// orphan goes. pgrep -f finds it while it runs. Either fails when
	// nothing matches.
	f.sbx.onExec = func(args []string) error {
		if args[0] != "pkill" && args[0] != "pgrep" {
			return nil
		}
		kill, found := args[0] == "pkill", false
		for i, w := range f.tmux.windows {
			if !w.PaneDead && strings.Contains(args[len(args)-1], `"`+w.Options["id"]+`"`) {
				found = true
				f.tmux.windows[i].PaneDead = kill
			}
		}
		for id := range f.sbx.orphans {
			if strings.Contains(args[len(args)-1], `"`+id+`"`) {
				found = true
				if kill {
					delete(f.sbx.orphans, id)
				}
			}
		}
		if !found {
			return errors.New("exit status 1")
		}
		return nil
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
		worktreeTop: func(dir string) (string, bool) {
			t, ok := f.tops[dir]
			return t, ok
		},
		fromSbx: platformtest.Fake{}.FromSbx,
		browse:  func(url string) error { f.browsed = append(f.browsed, url); return nil },
		raise: func(tty string) error {
			f.raised = append(f.raised, tty)
			return f.raiseErr
		},
		pullRequests: func(repo string) (map[string]gh.PR, error) {
			return f.prs[repo], nil
		},
		editor: func(dir string) error {
			if f.editorErr != nil {
				return f.editorErr
			}
			f.edited = append(f.edited, dir)
			return nil
		},
		isDir:  func(p string) bool { return f.dirs[p] || f.dirs["/w/app/"+p] },
		getwd:  func() (string, error) { return f.cwd, nil },
		now:    func() time.Time { return f.now },
		getenv: func(k string) string { return f.env[k] },
		foreground: func(argv []string) (int, error) {
			f.ran = append(f.ran, argv)
			return f.ranCode, f.ranErr
		},
		sleep: func(d time.Duration) {
			f.now = f.now.Add(d)
			if f.onSleep != nil {
				f.onSleep()
			}
		},
		canAsk: func(io.Reader) bool { return f.tty },
		rawTerminal: func(io.Reader) func() {
			f.rawOn++
			return func() { f.rawOff++ }
		},
		notify:     "[notify %s]",
		readDetail: func(_, id string) state.Detail { return f.details[id] },
		readState: func(_, id string) (state.Report, bool) {
			r, ok := f.states[id]
			return r, ok
		},
		removeState: func(root, id string) error {
			f.removed = append(f.removed, root+" "+id)
			delete(f.states, id)
			delete(f.inbox, id)
			return nil
		},
		pollSandboxes: f.sbx.List,
		postMessage: func(_, id, text string, now bool) error {
			if f.postErr != nil {
				return f.postErr
			}
			if now {
				text += " (now)"
			}
			f.inbox[id] = append(f.inbox[id], text)
			return nil
		},
		takeMessages: func(_, id string) ([]string, error) {
			if f.takeErr != nil {
				return nil, f.takeErr
			}
			texts := f.inbox[id]
			delete(f.inbox, id)
			return texts, nil
		},
		pending: func(_, id string) int { return len(f.inbox[id]) },

		releases:   f.releases,
		asset:      "hq-testos-testarch",
		executable: func() (string, error) { return f.exe, nil },
		itermProfile: func() (bool, error) {
			f.profiles++
			return f.profileWrote, f.profileErr
		},
		loadPrefs: func() prefs.Prefs { return f.prefs },
		savePrefs: func(p prefs.Prefs) error { f.prefs = p; return nil },

		runList: func(src dash.Source) error {
			if f.listRan == nil {
				return nil
			}
			return f.listRan(src)
		},
		runDialog: func(m tea.Model) error {
			f.dialog = m
			return nil
		},
		pid:   4242,
		alive: func(pid int) bool { return f.alive[pid] },

		home:          func() (string, error) { return "/home/dev", nil },
		desktopConfig: func() (string, error) { return f.desktop, f.desktopErr },
		desktopServer: func(hq string, env []string) desktop.Server {
			cmd, args, e := platformtest.Fake{}.DesktopServer(hq, env)
			return desktop.Server{Command: cmd, Args: args, Env: e}
		},
		installDesktop: func(path string, s desktop.Server) (desktop.Result, error) {
			f.installed = append(f.installed, s)
			return f.installResult, f.installErr
		},
		serveMCP: func(s *mcp.Server) error { f.served = s; return f.serveErr },
	}
}
