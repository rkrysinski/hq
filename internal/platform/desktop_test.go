package platform

import (
	"errors"
	"path"
	"reflect"
	"strings"
	"testing"
)

func TestClaudeDesktopsConfigurationOnMacOSAndLinux(t *testing.T) {
	for goos, want := range map[string]string{
		"darwin": "/Users/dev/Library/Application Support/Claude/claude_desktop_config.json",
		"linux":  "/Users/dev/.config/Claude/claude_desktop_config.json",
	} {
		if got, err := nativeDesktopConfig(goos, "/Users/dev"); err != nil || got != want {
			t.Errorf("%s: %q %v, want %q", goos, got, err, want)
		}
	}
	if _, err := nativeDesktopConfig("darwin", ""); err == nil {
		t.Error("no home is no error")
	}
}

// windows answers cmd.exe with its folders, powershell.exe with the
// command lines of the claude.exe processes and wslpath -u by mapping C:\ to
// /mnt/c; glob answers with the folders in found that match a pattern.
type windows struct {
	folders   string
	err       error
	processes string
	psErr     error
	found     []string
	calls     [][]string
	globbed   []string
}

func (w *windows) Run(name string, args ...string) ([]byte, error) {
	w.calls = append(w.calls, append([]string{name}, args...))
	switch name {
	case "cmd.exe":
		return []byte(w.folders), w.err
	case "powershell.exe":
		return []byte(w.processes), w.psErr
	}
	if len(args) == 2 && strings.HasPrefix(args[1], `C:\Users\dev`) {
		return []byte("/mnt/c/" + strings.ReplaceAll(strings.TrimPrefix(args[1], `C:\`), `\`, "/") + "\n"), nil
	}
	return nil, errors.New("wslpath: bad path")
}

// on has DesktopConfig look for Claude Desktop's folders on w for this test.
func on(t *testing.T, w *windows) *windows {
	old := glob
	glob = func(pattern string) ([]string, error) {
		w.globbed = append(w.globbed, pattern)
		var m []string
		for _, f := range w.found {
			if ok, _ := path.Match(pattern, f); ok {
				m = append(m, f)
			}
		}
		return m, nil
	}
	t.Cleanup(func() { glob = old })
	return w
}

const (
	devFolders = "C:\\Users\\dev\\AppData\\Roaming\r\nC:\\Users\\dev\\AppData\\Local\r\n"
	local      = "/mnt/c/Users/dev/AppData/Local"
	appData    = "/mnt/c/Users/dev/AppData/Roaming/Claude"
	dataDir    = local + "/Claude-Data/claude_2f7a1c"
	pkg        = local + "/Packages/Claude_pzs8sxrjxfjjc/LocalCache/Roaming/Claude"
	// left is the marker Claude Desktop leaves in the package's folder
	// when it moves its configuration to Claude-Data (#154).
	left = pkg + "/.user-data-left.1761040000"
)

func file(dir string) string { return dir + "/claude_desktop_config.json" }

func TestClaudeDesktopsConfigurationOnWindowsIsInAppData(t *testing.T) {
	w := on(t, &windows{folders: devFolders})
	got, err := WSL{Run: w}.DesktopConfig("/home/dev")
	if err != nil || got != (Desktop{Path: file(appData)}) {
		t.Fatalf("%+v %v", got, err)
	}
	want := [][]string{{"cmd.exe", "/d", "/c", "echo %APPDATA%&echo %LOCALAPPDATA%"}, {"wslpath", "-u", `C:\Users\dev\AppData\Local`}, {"wslpath", "-u", `C:\Users\dev\AppData\Roaming`},
		{"powershell.exe", "-NoProfile", "-NonInteractive", "-OutputFormat", "Text", "-EncodedCommand", encodedCommand(desktopProcesses)}}
	if !reflect.DeepEqual(w.calls, want) {
		t.Fatalf("calls %q", w.calls)
	}
	for _, w := range []*windows{
		{err: errors.New("cmd.exe: not found")},
		{folders: "\r\n"},
		{folders: "%APPDATA%\r\n%LOCALAPPDATA%\r\n"},
		{folders: `D:\elsewhere`},
	} {
		if got, err := (WSL{Run: on(t, w)}).DesktopConfig("/home/dev"); err == nil {
			t.Errorf("%q: %+v, want an error", w.folders, got)
		}
	}
}

// Claude Desktop not running, hq looks in Claude-Data first, then in the
// Store package's folder unless Claude Desktop left it, then in %APPDATA%
// (#25, #154).
func TestClaudeDesktopsConfigurationIsLookedForInOrder(t *testing.T) {
	for _, c := range []struct {
		name  string
		found []string
		want  string
	}{
		{"the Store's Claude-Data", []string{dataDir, local + "/Claude-Data/claude_9b", pkg, left}, file(dataDir)},
		{"Claude-Data before the package it left", []string{pkg, dataDir}, file(dataDir)},
		{"the Store package's folder", []string{pkg, local + "/Packages/Claude_zzz/LocalCache/Roaming/Claude"}, file(pkg)},
		{"not a package Claude Desktop left", []string{pkg, left}, file(appData)},
		{"the installer from claude.ai", nil, file(appData)},
	} {
		w := on(t, &windows{folders: devFolders, found: c.found})
		if got, err := (WSL{Run: w}).DesktopConfig("/home/dev"); err != nil || got != (Desktop{Path: c.want}) {
			t.Errorf("%s: %+v %v, want %q", c.name, got, err, c.want)
		}
	}
	w := on(t, &windows{folders: devFolders})
	WSL{Run: w}.DesktopConfig("/home/dev")
	if want := []string{local + "/Claude-Data/claude_*", local + "/Packages/Claude_*/LocalCache/Roaming/Claude"}; !reflect.DeepEqual(w.globbed, want) {
		t.Fatalf("looked for %q", w.globbed)
	}
	// Without a %LOCALAPPDATA%, or one wslpath cannot map: %APPDATA% as before.
	for _, folders := range []string{"C:\\Users\\dev\\AppData\\Roaming\r\n", "C:\\Users\\dev\\AppData\\Roaming\r\n%LOCALAPPDATA%\r\n", "C:\\Users\\dev\\AppData\\Roaming\r\nD:\\elsewhere\r\n"} {
		w := on(t, &windows{folders: folders, found: []string{dataDir, pkg}})
		if got, err := (WSL{Run: w}).DesktopConfig("/home/dev"); err != nil || got != (Desktop{Path: file(appData)}) || len(w.globbed) != 0 {
			t.Errorf("%q: %+v %v, globbed %q", folders, got, err, w.globbed)
		}
	}
}

// A running Claude Desktop reads the configuration in the folder its helper
// processes are given with --user-data-dir; the main process has none. When
// that is another file than the order finds, hq says which (#154).
func TestARunningClaudeDesktopsConfigurationIsTheOneItReads(t *testing.T) {
	const main = `"C:\Program Files\WindowsApps\Claude_1.0.0_x64__pzs8sxrjxfjjc\app\claude.exe"`
	helper := main + ` --type=renderer --user-data-dir="C:\Users\dev\AppData\Local\Claude-Data\claude_2f7a1c" --app-path="C:\Program Files\WindowsApps\x" --lang=pl`
	w := on(t, &windows{folders: devFolders, found: []string{pkg}, processes: main + "\r\n" + helper + "\r\n"})
	got, err := WSL{Run: w}.DesktopConfig("/home/dev")
	if err != nil || got != (Desktop{Path: file(dataDir), Running: true, Searched: file(pkg)}) {
		t.Fatalf("%+v %v", got, err)
	}
	// The order finds the file it reads: nothing more to say.
	w = on(t, &windows{folders: devFolders, found: []string{dataDir}, processes: helper + "\r\n"})
	if got, err := (WSL{Run: w}).DesktopConfig("/home/dev"); err != nil || got != (Desktop{Path: file(dataDir), Running: true}) {
		t.Fatalf("same %+v %v", got, err)
	}
	// Windows' names ignore case: the same file, however it is written.
	w = on(t, &windows{folders: devFolders, found: []string{dataDir}, processes: main + ` --type=renderer --user-data-dir="C:\Users\dev\AppData\Local\Claude-Data\CLAUDE_2F7A1C\\"`})
	if got, err := (WSL{Run: w}).DesktopConfig("/home/dev"); err != nil || got != (Desktop{Path: file(dataDir), Running: true}) {
		t.Fatalf("case %+v %v", got, err)
	}
	// The installer from claude.ai gives its helpers no --user-data-dir:
	// the order's file, Claude Desktop running.
	w = on(t, &windows{folders: devFolders, processes: main + "\r\n" + main + " --type=gpu-process --field-trial-handle=1\r\n"})
	if got, err := (WSL{Run: w}).DesktopConfig("/home/dev"); err != nil || got != (Desktop{Path: file(appData), Running: true}) {
		t.Fatalf("installer %+v %v", got, err)
	}
	// Claude Code on Windows is a claude.exe too, with no helpers: not
	// Claude Desktop. No PowerShell to ask: as though it were not running.
	for _, w := range []*windows{
		{folders: devFolders, processes: `C:\Users\dev\.local\bin\claude.exe --resume` + "\r\n"},
		{folders: devFolders, processes: helper, psErr: errors.New("powershell.exe: not found")},
	} {
		if got, err := (WSL{Run: on(t, w)}).DesktopConfig("/home/dev"); err != nil || got != (Desktop{Path: file(appData)}) {
			t.Errorf("%q %v: %+v %v", w.processes, w.psErr, got, err)
		}
	}
	// A --user-data-dir wslpath cannot map: the order's file.
	w = on(t, &windows{folders: devFolders, processes: main + ` --type=renderer --user-data-dir=D:\elsewhere`})
	if got, err := (WSL{Run: w}).DesktopConfig("/home/dev"); err != nil || got != (Desktop{Path: file(appData), Running: true}) {
		t.Fatalf("unmapped %+v %v", got, err)
	}
}

func TestUserDataDirIsReadOffACommandLine(t *testing.T) {
	for line, want := range map[string]string{
		`claude.exe --type=renderer --user-data-dir="C:\Users\Jan Kowalski\AppData\Local\Claude-Data\claude_1" --lang=pl`: `C:\Users\Jan Kowalski\AppData\Local\Claude-Data\claude_1`,
		`claude.exe --type=utility "--user-data-dir=C:\Users\Jan Kowalski\Claude-Data\claude_1" --x`:                      `C:\Users\Jan Kowalski\Claude-Data\claude_1`,
		`claude.exe --type=gpu-process --user-data-dir=C:\Users\dev\Claude-Data\claude_1 --x`:                             `C:\Users\dev\Claude-Data\claude_1`,
		`claude.exe --type=gpu-process --user-data-dir=C:\Users\dev\Claude-Data\claude_1`:                                 `C:\Users\dev\Claude-Data\claude_1`,
		`claude.exe --type=utility --user-data-dir="C:\Users\dev\Claude-Data\claude_1\\" --x`:                             `C:\Users\dev\Claude-Data\claude_1`,
		`claude.exe --type=gpu-process --user-data-dir=`:                                                                  ``,
		`claude.exe --type=gpu-process`: ``,
	} {
		if got := userDataDir(line); got != want {
			t.Errorf("%s: %q, want %q", line, got, want)
		}
	}
}

func TestClaudeDesktopStartsHq(t *testing.T) {
	env := []string{"PATH=/opt/homebrew/bin:/usr/bin", "HQ_TMUX_SOCKET=dev"}
	cmd, args, e := Native{}.AppServer([]string{"/Users/dev/.local/bin/hq", "mcp"}, env)
	if cmd != "/Users/dev/.local/bin/hq" || !reflect.DeepEqual(args, []string{"mcp"}) ||
		!reflect.DeepEqual(e, map[string]string{"PATH": "/opt/homebrew/bin:/usr/bin", "HQ_TMUX_SOCKET": "dev"}) {
		t.Fatalf("native %q %q %q", cmd, args, e)
	}
	cmd, args, e = WSL{Distro: "Ubuntu"}.AppServer([]string{"/home/dev/.local/bin/hq", "mcp"}, env)
	want := []string{"-d", "Ubuntu", "--exec", "/usr/bin/env", "PATH=/opt/homebrew/bin:/usr/bin", "HQ_TMUX_SOCKET=dev", "/home/dev/.local/bin/hq", "mcp"}
	if cmd != "wsl.exe" || !reflect.DeepEqual(args, want) || e != nil {
		t.Fatalf("wsl %q %q %q", cmd, args, e)
	}
	// Without the distribution's name, wsl.exe's default one.
	if _, args, _ := (WSL{}).AppServer([]string{"/hq", "mcp"}, nil); !reflect.DeepEqual(args, []string{"--exec", "/usr/bin/env", "/hq", "mcp"}) {
		t.Fatalf("default distribution %q", args)
	}
}

// Claude Code on Windows reads .claude.json in %CLAUDE_CONFIG_DIR%, or in
// the Windows user's profile (#115).
func TestClaudeCodesConfigurationOnWindowsIsInTheProfile(t *testing.T) {
	w := &windows{folders: "C:\\Users\\dev\r\n%CLAUDE_CONFIG_DIR%\r\n"}
	got, ok, err := WSL{Run: w}.WindowsCodeConfig()
	if err != nil || !ok || got != "/mnt/c/Users/dev/.claude.json" {
		t.Fatalf("%q %v %v", got, ok, err)
	}
	if want := [][]string{{"cmd.exe", "/d", "/c", "echo %USERPROFILE%&echo %CLAUDE_CONFIG_DIR%"}, {"wslpath", "-u", `C:\Users\dev`}}; !reflect.DeepEqual(w.calls, want) {
		t.Fatalf("calls %q", w.calls)
	}
	w = &windows{folders: "C:\\Users\\dev\r\nC:\\Users\\dev\\AppData\\claude\r\n"}
	if got, _, err := (WSL{Run: w}).WindowsCodeConfig(); err != nil || got != "/mnt/c/Users/dev/AppData/claude/.claude.json" {
		t.Fatalf("CLAUDE_CONFIG_DIR: %q %v", got, err)
	}
	for _, w := range []*windows{
		{err: errors.New("cmd.exe: not found")},
		{folders: "\r\n"},
		{folders: "%USERPROFILE%\r\n%CLAUDE_CONFIG_DIR%\r\n"},
		{folders: `D:\elsewhere`},
	} {
		if got, ok, err := (WSL{Run: w}).WindowsCodeConfig(); err == nil || !ok {
			t.Errorf("%q: %q %v, want an error", w.folders, got, ok)
		}
	}
	if _, ok, err := (Native{}).WindowsCodeConfig(); ok || err != nil {
		t.Fatalf("native %v %v", ok, err)
	}
}
