package platform

import (
	"errors"
	"reflect"
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

// windows answers cmd.exe with appdata and wslpath -u by mapping C:\ to
// /mnt/c.
type windows struct {
	appdata string
	err     error
	calls   [][]string
}

func (w *windows) Run(name string, args ...string) ([]byte, error) {
	w.calls = append(w.calls, append([]string{name}, args...))
	if name == "cmd.exe" {
		return []byte(w.appdata), w.err
	}
	if len(args) == 2 && args[1] == `C:\Users\dev\AppData\Roaming` {
		return []byte("/mnt/c/Users/dev/AppData/Roaming\n"), nil
	}
	return nil, errors.New("wslpath: bad path")
}

func TestClaudeDesktopsConfigurationOnWindowsIsInAppData(t *testing.T) {
	w := &windows{appdata: "C:\\Users\\dev\\AppData\\Roaming\r\n"}
	got, err := WSL{Run: w}.DesktopConfig("/home/dev")
	if err != nil || got != "/mnt/c/Users/dev/AppData/Roaming/Claude/claude_desktop_config.json" {
		t.Fatalf("%q %v", got, err)
	}
	want := [][]string{{"cmd.exe", "/d", "/c", "echo %APPDATA%"}, {"wslpath", "-u", `C:\Users\dev\AppData\Roaming`}}
	if !reflect.DeepEqual(w.calls, want) {
		t.Fatalf("calls %q", w.calls)
	}
	for _, w := range []*windows{
		{err: errors.New("cmd.exe: not found")},
		{appdata: "\r\n"},
		{appdata: "%APPDATA%\r\n"},
		{appdata: `D:\elsewhere`},
	} {
		if got, err := (WSL{Run: w}).DesktopConfig("/home/dev"); err == nil {
			t.Errorf("%q: %q, want an error", w.appdata, got)
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
