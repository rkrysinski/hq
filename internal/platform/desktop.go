package platform

import (
	"errors"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// desktopFile is the name of Claude Desktop's configuration file.
const desktopFile = "claude_desktop_config.json"

// DesktopConfig is in the user's Application Support on macOS; on a plain
// Linux, where Claude Desktop is not published, where its unofficial builds
// keep it.
func (Native) DesktopConfig(home string) (string, error) {
	return nativeDesktopConfig(runtime.GOOS, home)
}

func nativeDesktopConfig(goos, home string) (string, error) {
	if home == "" {
		return "", errors.New("no home directory")
	}
	if goos == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "Claude", desktopFile), nil
	}
	return filepath.Join(home, ".config", "Claude", desktopFile), nil
}

// DesktopServer is hq itself, given the environment: a program started by
// the Dock gets only the system's PATH, which has no tmux, sbx or gh.
func (Native) DesktopServer(hq string, env []string) (string, []string, map[string]string) {
	m := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return hq, []string{"mcp"}, m
}

// DesktopConfig is in the Windows user's %APPDATA%, which WSL reaches
// through cmd.exe and wslpath.
func (w WSL) DesktopConfig(string) (string, error) {
	out, err := w.Run.Run("cmd.exe", "/d", "/c", "echo %APPDATA%")
	if err != nil {
		return "", err
	}
	appdata := strings.TrimSpace(string(out))
	if appdata == "" || strings.Contains(appdata, "%") {
		return "", errors.New("Windows gave no %APPDATA%")
	}
	dir, err := w.wslpath("-u", appdata)
	if err != nil {
		return "", err
	}
	return path.Join(dir, "Claude", desktopFile), nil
}

// DesktopServer is wsl.exe running hq in this distribution directly, with
// no shell, its environment set by env(1): Windows starts wsl.exe, and an
// environment given to it stays on the Windows side.
func (w WSL) DesktopServer(hq string, env []string) (string, []string, map[string]string) {
	var args []string
	if w.Distro != "" {
		args = append(args, "-d", w.Distro)
	}
	args = append(args, "--exec", "/usr/bin/env")
	args = append(args, env...)
	return "wsl.exe", append(args, hq, "mcp"), nil
}
