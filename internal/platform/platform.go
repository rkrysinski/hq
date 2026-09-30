// Package platform is the only code that knows whether hq runs on macOS or
// on Windows through WSL (design §3.10). Everything else asks it.
package platform

import (
	"encoding/base64"
	"fmt"
	"strings"
	"unicode/utf16"

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

// OverrideEnv names the variable that decides the platform instead of
// Detect's look at the machine: "native" or "wsl". It is for tests and QA,
// so that a suite run on WSL can still run hq as it runs on macOS, with the
// stub sbx, and never reaches the Windows side (design §7.2).
const OverrideEnv = "HQ_PLATFORM"

// Detect chooses the platform at startup: WSL when WSL_DISTRO_NAME is set or
// the kernel release names Microsoft, otherwise Native.
// OverrideEnv, when set to one of its values, decides instead.
func Detect(getenv func(string) string, readFile func(string) ([]byte, error), run proc.Runner) Platform {
	switch getenv(OverrideEnv) {
	case "native":
		return Native{}
	case "wsl":
		return WSL{Run: run, BrowserSet: getenv("BROWSER") != "", Distro: getenv("WSL_DISTRO_NAME")}
	}
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

// Raise is PowerShell bringing the window titled hq - agents, which tmux sets
// while the dashboard is attached, to the front; the client's terminal means
// nothing to Windows. The script goes encoded, so no quoting is lost on the
// way through WSL's interop. It fails when no such window is open or when
// Windows kept another window in front (RaiseScript).
func (WSL) Raise(string) []string {
	return []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-OutputFormat", "Text", "-EncodedCommand", encodedCommand(RaiseScript)}
}

// RaiseScript is the PowerShell of WSL's Raise. Windows lets only the
// process that received the last input change the foreground window (the
// foreground lock): asked by any other, such as hq started by Claude Desktop,
// it flashes the window's taskbar button and reports success (#9). So the
// script looks at which window is in front afterwards instead of trusting
// the answer, and when it is another one, taps Alt, which makes this process
// the one with the last input, and asks again. It exits 1 with one line on
// stderr when no window has the title or another window stayed in front.
const RaiseScript = `$ErrorActionPreference = 'Stop'; $ProgressPreference = 'SilentlyContinue'
$title = '` + tmux.TerminalTitle + `'
$w = Add-Type -PassThru -Namespace Hq -Name Win -MemberDefinition '
[DllImport("user32.dll", CharSet = CharSet.Unicode)] public static extern IntPtr FindWindow(IntPtr cls, string title);
[DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
[DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
[DllImport("user32.dll")] public static extern bool IsIconic(IntPtr h);
[DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr h, int cmd);
[DllImport("user32.dll")] public static extern void keybd_event(byte vk, byte scan, uint flags, UIntPtr extra);'
$h = $w::FindWindow([IntPtr]::Zero, $title)
if ($h -eq [IntPtr]::Zero) { [Console]::Error.WriteLine("no window titled $title"); exit 1 }
function InFront { Start-Sleep -Milliseconds 100; $w::GetForegroundWindow() -eq $h }
if ($w::IsIconic($h)) { [void]$w::ShowWindow($h, 9) }
[void]$w::SetForegroundWindow($h)
if (InFront) { exit 0 }
$w::keybd_event(0x12, 0, 0, [UIntPtr]::Zero); $w::keybd_event(0x12, 0, 2, [UIntPtr]::Zero)
[void]$w::SetForegroundWindow($h)
if (InFront) { exit 0 }
[Console]::Error.WriteLine("Windows kept another window in front; the window titled $title flashes on the taskbar")
exit 1
`

// encodedCommand is a script as powershell.exe -EncodedCommand takes it:
// UTF-16LE in base64.
func encodedCommand(script string) string {
	b := make([]byte, 0, 2*len(script))
	for _, u := range utf16.Encode([]rune(script)) {
		b = append(b, byte(u), byte(u>>8))
	}
	return base64.StdEncoding.EncodeToString(b)
}

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
