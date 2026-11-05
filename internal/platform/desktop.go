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
func (Native) DesktopConfig(home string) (Desktop, error) {
	p, err := nativeDesktopConfig(runtime.GOOS, home)
	return Desktop{Path: p}, err
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

// AppServer is hq itself, given the environment: a program started by the
// Dock gets only the system's PATH, which has no tmux, sbx or gh.
func (Native) AppServer(argv, env []string) (string, []string, map[string]string) {
	m := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return argv[0], argv[1:], m
}

// WindowsCodeConfig: there is no Windows side.
func (Native) WindowsCodeConfig() (string, bool, error) { return "", false, nil }

// glob lists the paths matching a pattern; the tests put their own in.
var glob = filepath.Glob

// Under the Windows user's %LOCALAPPDATA%, where Claude Desktop installed
// from the Microsoft Store keeps its configuration: dataDirs since late
// 2026, one folder per installation, given to the app with --user-data-dir
// (#154); before, storeConfigDirs, the package's own %APPDATA%, which
// Windows gives a packaged app there (#25). Claude Desktop leaves
// leftMarker in the package's folder when it moves to Claude-Data, and no
// longer reads the file there. The package's name ends in its publisher's
// id.
var (
	dataDirs        = []string{"Claude-Data", "claude_*"}
	storeConfigDirs = []string{"Packages", "Claude_*", "LocalCache", "Roaming", "Claude"}
)

const leftMarker = ".user-data-left.*"

// DesktopConfig is, when Claude Desktop is not running, the first there of:
// a folder in %LOCALAPPDATA%\Claude-Data, the Store package's folder under
// %LOCALAPPDATA% unless Claude Desktop left it, and %APPDATA%\Claude (the
// installer from claude.ai); more than one folder of a kind (a leftover):
// the first by name. A running Claude Desktop reads the one its processes
// are given with --user-data-dir instead, when they are. WSL reaches the
// folders through cmd.exe and wslpath, the processes through PowerShell.
func (w WSL) DesktopConfig(string) (Desktop, error) {
	out, err := w.Run.Run("cmd.exe", "/d", "/c", "echo %APPDATA%&echo %LOCALAPPDATA%")
	if err != nil {
		return Desktop{}, err
	}
	appdata, local, _ := strings.Cut(strings.ReplaceAll(string(out), "\r", ""), "\n")
	appdata, local = strings.TrimSpace(appdata), strings.TrimSpace(local)
	if appdata == "" || strings.Contains(appdata, "%") {
		return Desktop{}, errors.New("Windows gave no %APPDATA%")
	}
	d := Desktop{}
	if local != "" && !strings.Contains(local, "%") {
		if dir, err := w.wslpath("-u", local); err == nil {
			d.Path = storeConfig(dir)
		}
	}
	if d.Path == "" {
		dir, err := w.wslpath("-u", appdata)
		if err != nil {
			return Desktop{}, err
		}
		d.Path = path.Join(dir, "Claude", desktopFile)
	}
	running, reads := w.runningDesktop()
	if !running {
		return d, nil
	}
	d.Running = true
	if reads == "" {
		return d, nil
	}
	// Windows' names ignore case: C:\Users\Dev is C:\Users\dev.
	if dir, err := w.wslpath("-u", reads); err == nil && !strings.EqualFold(path.Join(dir, desktopFile), d.Path) {
		d.Path, d.Searched = path.Join(dir, desktopFile), d.Path
	}
	return d, nil
}

// storeConfig is the configuration file of Claude Desktop from the Store
// under local, the Windows user's %LOCALAPPDATA% as hq sees it, or "".
func storeConfig(local string) string {
	if dirs, _ := glob(path.Join(append([]string{local}, dataDirs...)...)); len(dirs) > 0 {
		return path.Join(dirs[0], desktopFile)
	}
	dirs, _ := glob(path.Join(append([]string{local}, storeConfigDirs...)...))
	for _, dir := range dirs {
		if left, _ := glob(path.Join(dir, leftMarker)); len(left) == 0 {
			return path.Join(dir, desktopFile)
		}
	}
	return ""
}

// desktopProcesses is the PowerShell that prints the command line of every
// claude.exe process, one a line, in UTF-8 so that a user's name in the
// path survives.
const desktopProcesses = `$ErrorActionPreference = 'Stop'; $ProgressPreference = 'SilentlyContinue'
[Console]::OutputEncoding = [Text.Encoding]::UTF8
Get-CimInstance Win32_Process -Filter "Name = 'claude.exe'" | ForEach-Object { $_.CommandLine }
`

// runningDesktop reports whether Claude Desktop runs on Windows, and the
// folder, as Windows names it, its processes are given with
// --user-data-dir, or "". Claude Desktop is an Electron app: its helper
// processes have a --type, which its main process and Claude Code (a
// claude.exe too) have not. When Windows cannot be asked, it is taken as
// not running.
func (w WSL) runningDesktop() (running bool, dir string) {
	out, err := w.Run.Run("powershell.exe", "-NoProfile", "-NonInteractive", "-OutputFormat", "Text", "-EncodedCommand", encodedCommand(desktopProcesses))
	if err != nil {
		return false, ""
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(out), "\r", ""), "\n") {
		if !strings.Contains(line, " --type=") {
			continue
		}
		running = true
		if dir = userDataDir(line); dir != "" {
			return true, dir
		}
	}
	return running, ""
}

// userDataDir is the value of --user-data-dir on a Windows command line,
// quoted (--user-data-dir="C:\a b" or "--user-data-dir=C:\a b") or not, or "".
func userDataDir(line string) string {
	const flag = "--user-data-dir="
	i := strings.Index(line, flag)
	if i < 0 {
		return ""
	}
	rest := line[i+len(flag):]
	end := " "
	switch {
	case strings.HasPrefix(rest, `"`):
		rest, end = rest[1:], `"`
	case i > 0 && line[i-1] == '"':
		end = `"`
	}
	v, _, _ := strings.Cut(rest, end)
	// A folder quoted with its trailing backslash doubled ("C:\a b\\").
	return strings.TrimRight(v, `\`)
}

// AppServer is wsl.exe running hq in this distribution directly, with no
// shell, its environment set by env(1): Windows starts wsl.exe, and an
// environment given to it stays on the Windows side.
func (w WSL) AppServer(argv, env []string) (string, []string, map[string]string) {
	var args []string
	if w.Distro != "" {
		args = append(args, "-d", w.Distro)
	}
	args = append(args, "--exec", "/usr/bin/env")
	args = append(args, env...)
	return "wsl.exe", append(args, argv...), nil
}

// codeFile is the name of Claude Code's user configuration file.
const codeFile = ".claude.json"

// WindowsCodeConfig is .claude.json in the Windows user's
// %CLAUDE_CONFIG_DIR% when that is set, as Claude Code on Windows reads it,
// otherwise in %USERPROFILE%; WSL reaches it through cmd.exe and wslpath.
func (w WSL) WindowsCodeConfig() (string, bool, error) {
	out, err := w.Run.Run("cmd.exe", "/d", "/c", "echo %USERPROFILE%&echo %CLAUDE_CONFIG_DIR%")
	if err != nil {
		return "", true, err
	}
	profile, configDir, _ := strings.Cut(strings.ReplaceAll(string(out), "\r", ""), "\n")
	dir := strings.TrimSpace(configDir)
	if dir == "" || strings.Contains(dir, "%") {
		dir = strings.TrimSpace(profile)
	}
	if dir == "" || strings.Contains(dir, "%") {
		return "", true, errors.New("Windows gave no %USERPROFILE%")
	}
	if dir, err = w.wslpath("-u", dir); err != nil {
		return "", true, err
	}
	return path.Join(dir, codeFile), true, nil
}
