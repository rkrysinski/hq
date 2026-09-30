package platform

import (
	"errors"
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

// windows answers cmd.exe with its folders and wslpath -u by mapping C:\ to
// /mnt/c; glob answers with the Store packages' folders.
type windows struct {
	folders string
	err     error
	store   []string
	calls   [][]string
	globbed []string
}

func (w *windows) Run(name string, args ...string) ([]byte, error) {
	w.calls = append(w.calls, append([]string{name}, args...))
	if name == "cmd.exe" {
		return []byte(w.folders), w.err
	}
	if len(args) == 2 && strings.HasPrefix(args[1], `C:\Users\dev\AppData\`) {
		return []byte("/mnt/c/" + strings.ReplaceAll(strings.TrimPrefix(args[1], `C:\`), `\`, "/") + "\n"), nil
	}
	return nil, errors.New("wslpath: bad path")
}

// withStore has DesktopConfig find w's Store packages for this test.
func withStore(t *testing.T, w *windows) *windows {
	old := glob
	glob = func(pattern string) ([]string, error) {
		w.globbed = append(w.globbed, pattern)
		return w.store, nil
	}
	t.Cleanup(func() { glob = old })
	return w
}

const devFolders = "C:\\Users\\dev\\AppData\\Roaming\r\nC:\\Users\\dev\\AppData\\Local\r\n"

func TestClaudeDesktopsConfigurationOnWindowsIsInAppData(t *testing.T) {
	w := withStore(t, &windows{folders: devFolders})
	got, err := WSL{Run: w}.DesktopConfig("/home/dev")
	if err != nil || got != "/mnt/c/Users/dev/AppData/Roaming/Claude/claude_desktop_config.json" {
		t.Fatalf("%q %v", got, err)
	}
	want := [][]string{{"cmd.exe", "/d", "/c", "echo %APPDATA%&echo %LOCALAPPDATA%"}, {"wslpath", "-u", `C:\Users\dev\AppData\Local`}, {"wslpath", "-u", `C:\Users\dev\AppData\Roaming`}}
	if !reflect.DeepEqual(w.calls, want) {
		t.Fatalf("calls %q", w.calls)
	}
	for _, w := range []*windows{
		{err: errors.New("cmd.exe: not found")},
		{folders: "\r\n"},
		{folders: "%APPDATA%\r\n%LOCALAPPDATA%\r\n"},
		{folders: `D:\elsewhere`},
	} {
		if got, err := (WSL{Run: withStore(t, w)}).DesktopConfig("/home/dev"); err == nil {
			t.Errorf("%q: %q, want an error", w.folders, got)
		}
	}
}

// Installed from the Microsoft Store, Claude Desktop keeps its configuration
// in its package's folder, and that is the one it reads (#25).
func TestClaudeDesktopsConfigurationFromTheStoreIsInItsPackage(t *testing.T) {
	pkg := "/mnt/c/Users/dev/AppData/Local/Packages/Claude_pzs8sxrjxfjjc/LocalCache/Roaming/Claude"
	w := withStore(t, &windows{folders: devFolders, store: []string{pkg, "/mnt/c/Users/dev/AppData/Local/Packages/Claude_zzz/LocalCache/Roaming/Claude"}})
	got, err := WSL{Run: w}.DesktopConfig("/home/dev")
	if err != nil || got != pkg+"/claude_desktop_config.json" {
		t.Fatalf("%q %v", got, err)
	}
	if want := []string{"/mnt/c/Users/dev/AppData/Local/Packages/Claude_*/LocalCache/Roaming/Claude"}; !reflect.DeepEqual(w.globbed, want) {
		t.Fatalf("looked for %q", w.globbed)
	}
	// Without a %LOCALAPPDATA%, or one wslpath cannot map: %APPDATA% as before.
	for _, folders := range []string{"C:\\Users\\dev\\AppData\\Roaming\r\n", "C:\\Users\\dev\\AppData\\Roaming\r\n%LOCALAPPDATA%\r\n", "C:\\Users\\dev\\AppData\\Roaming\r\nD:\\elsewhere\r\n"} {
		w := withStore(t, &windows{folders: folders, store: []string{pkg}})
		if got, err := (WSL{Run: w}).DesktopConfig("/home/dev"); err != nil || got != "/mnt/c/Users/dev/AppData/Roaming/Claude/claude_desktop_config.json" || len(w.globbed) != 0 {
			t.Errorf("%q: %q %v, globbed %q", folders, got, err, w.globbed)
		}
	}
}

func TestClaudeDesktopStartsHq(t *testing.T) {
	env := []string{"PATH=/opt/homebrew/bin:/usr/bin", "HQ_TMUX_SOCKET=dev"}
	cmd, args, e := Native{}.DesktopServer("/Users/dev/.local/bin/hq", env)
	if cmd != "/Users/dev/.local/bin/hq" || !reflect.DeepEqual(args, []string{"mcp"}) ||
		!reflect.DeepEqual(e, map[string]string{"PATH": "/opt/homebrew/bin:/usr/bin", "HQ_TMUX_SOCKET": "dev"}) {
		t.Fatalf("native %q %q %q", cmd, args, e)
	}
	cmd, args, e = WSL{Distro: "Ubuntu"}.DesktopServer("/home/dev/.local/bin/hq", env)
	want := []string{"-d", "Ubuntu", "--exec", "/usr/bin/env", "PATH=/opt/homebrew/bin:/usr/bin", "HQ_TMUX_SOCKET=dev", "/home/dev/.local/bin/hq", "mcp"}
	if cmd != "wsl.exe" || !reflect.DeepEqual(args, want) || e != nil {
		t.Fatalf("wsl %q %q %q", cmd, args, e)
	}
	// Without the distribution's name, wsl.exe's default one.
	if _, args, _ := (WSL{}).DesktopServer("/hq", nil); !reflect.DeepEqual(args, []string{"--exec", "/usr/bin/env", "/hq", "mcp"}) {
		t.Fatalf("default distribution %q", args)
	}
}
