// Package platform is the only code that knows whether hq runs on macOS or
// on Windows through WSL (design §3.10). Everything else asks it.
package platform

import (
	"fmt"
	"strings"

	"github.com/rkrysinski/hq/internal/proc"
	"github.com/rkrysinski/hq/internal/tmux"
)

// Platform is what differs between the platforms.
type Platform interface {
	// SbxCommand is the command that runs sbx.
	SbxCommand() string
	// ToSbx turns a path as hq sees it into the path sbx understands.
	ToSbx(path string) (string, error)
	// FromSbx turns a path sbx reports into the path hq sees.
	FromSbx(path string) (string, error)
	// NotifySequence is the terminal's desktop notification as the content
	// of a JSON string, with %s where the text goes (design §3.5); without
	// %s the terminal shows no text.
	NotifySequence() string
	// Editor is the command that opens VS Code on a directory hq sees.
	Editor(dir string) []string
	// Browser is the command that opens a pull request's URL in the
	// browser (spec §6.4 pr).
	Browser(url string) []string
	// Raise is the command that brings the terminal window showing the
	// dashboard to the front (design §3.11); tty is the attached tmux
	// client's terminal, which a platform may use to find the window. It
	// fails when no window shows the dashboard or the system refuses.
	Raise(tty string) []string
	// DesktopConfig is Claude Desktop's configuration file as hq sees it,
	// home being the user's home directory (design §3.12).
	DesktopConfig(home string) (string, error)
	// DesktopServer is how Claude Desktop starts the MCP server that runs
	// the hq at path hq with the environment env ("KEY=value"): the
	// command, its arguments, and the environment Claude Desktop gives it.
	DesktopServer(hq string, env []string) (command string, args []string, environment map[string]string)
}

// Detect chooses the platform at startup: WSL when WSL_DISTRO_NAME is set or
// the kernel release names Microsoft, otherwise Native.
func Detect(getenv func(string) string, readFile func(string) ([]byte, error), run proc.Runner) Platform {
	if d := getenv("WSL_DISTRO_NAME"); d != "" {
		return WSL{Run: run, BrowserSet: getenv("BROWSER") != "", Distro: d}
	}
	if b, err := readFile("/proc/sys/kernel/osrelease"); err == nil && strings.Contains(strings.ToLower(string(b)), "microsoft") {
		return WSL{Run: run, BrowserSet: getenv("BROWSER") != ""}
	}
	return Native{}
}

// Native is macOS (and a plain Linux): sbx runs as sbx and sees hq's paths.
type Native struct{}

func (Native) SbxCommand() string                  { return "sbx" }
func (Native) ToSbx(path string) (string, error)   { return path, nil }
func (Native) FromSbx(path string) (string, error) { return path, nil }

// NotifySequence is OSC 9, which iTerm2 shows as a desktop notification.
func (Native) NotifySequence() string { return `\u001b]9;%s\u0007` }

// Editor is VS Code's code command.
func (Native) Editor(dir string) []string { return []string{"code", dir} }

// Browser is gh opening the pull request, in the browser it is set up for.
func (Native) Browser(url string) []string { return ghView(url) }

// Raise is osascript selecting the iTerm2 window, tab and session on the
// client's terminal and bringing iTerm2 to the front. The terminal finds
// the window exactly, where a title can carry iTerm2's decorations; iTerm2
// is never started when it is not running.
func (Native) Raise(tty string) []string { return []string{"osascript", "-e", raiseITerm, tty} }

// raiseITerm is the AppleScript of Raise; its argument is the tty.
const raiseITerm = `on run argv
	set t to item 1 of argv
	if application id "com.googlecode.iterm2" is not running then error "iTerm2 is not running"
	tell application id "com.googlecode.iterm2"
		repeat with w in windows
			repeat with b in tabs of w
				repeat with s in sessions of b
					if tty of s is t then
						select b
						select s
						set index of w to 1
						activate
						return
					end if
				end repeat
			end repeat
		end repeat
	end tell
	error "no iTerm2 window shows the dashboard"
end run`

func ghView(url string) []string { return []string{"gh", "pr", "view", "--web", url} }

// WSL is Windows through WSL: sbx is the Windows sbx.exe, reached through
// interop, and paths cross between Linux and Windows with wslpath.
type WSL struct {
	Run        proc.Runner
	BrowserSet bool   // the user set BROWSER
	Distro     string // the WSL distribution hq runs in, when known
}

func (WSL) SbxCommand() string                    { return "sbx.exe" }
func (w WSL) ToSbx(path string) (string, error)   { return w.wslpath("-w", path) }
func (w WSL) FromSbx(path string) (string, error) { return w.wslpath("-u", path) }

// NotifySequence is BEL, which Windows Terminal shows as a taskbar flash
// (design §3.10; what more it honours is checked on Windows, #9).
func (WSL) NotifySequence() string { return `\u0007` }

// Editor is VS Code's code command too: on WSL it is the Remote-WSL shim,
// which takes the Linux path and opens Windows VS Code on it.
func (WSL) Editor(dir string) []string { return []string{"code", dir} }

// Browser is gh too; a WSL distribution usually has no browser of its own,
// so without BROWSER gh hands the URL to Windows through explorer.exe,
// which every WSL has (wslview needs wslu).
func (w WSL) Browser(url string) []string {
	if w.BrowserSet {
		return ghView(url)
	}
	return append([]string{"env", "BROWSER=explorer.exe"}, ghView(url)...)
}

// Raise is PowerShell activating the window titled hq - agents, which tmux sets
// while the dashboard is attached; the client's terminal means nothing to
// Windows. Windows can refuse to change the foreground window, which fails
// the command.
func (WSL) Raise(string) []string {
	return []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-Command", raiseTitle}
}

// raiseTitle is the PowerShell of WSL's Raise.
const raiseTitle = `if (-not (New-Object -ComObject WScript.Shell).AppActivate('` + tmux.TerminalTitle + `')) { [Console]::Error.WriteLine('no window titled ` + tmux.TerminalTitle + `, or Windows refused to bring it to the front'); exit 1 }`

func (w WSL) wslpath(flag, path string) (string, error) {
	out, err := w.Run.Run("wslpath", flag, path)
	if err != nil {
		return "", err
	}
	p := strings.TrimRight(string(out), "\r\n")
	if p == "" {
		return "", fmt.Errorf("wslpath %s %s: no path", flag, path)
	}
	return p, nil
}
