// Package platform is the only code that knows whether hq runs on macOS or
// on Windows through WSL (design §3.10). Everything else asks it.
package platform

import (
	"fmt"
	"strings"

	"github.com/rkrysinski/hq/internal/proc"
)

// Platform is what differs between the platforms. A later issue adds
// raising the window.
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
}

// Detect chooses the platform at startup: WSL when WSL_DISTRO_NAME is set or
// the kernel release names Microsoft, otherwise Native.
func Detect(getenv func(string) string, readFile func(string) ([]byte, error), run proc.Runner) Platform {
	if getenv("WSL_DISTRO_NAME") != "" {
		return WSL{Run: run, BrowserSet: getenv("BROWSER") != ""}
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

func ghView(url string) []string { return []string{"gh", "pr", "view", "--web", url} }

// WSL is Windows through WSL: sbx is the Windows sbx.exe, reached through
// interop, and paths cross between Linux and Windows with wslpath.
type WSL struct {
	Run        proc.Runner
	BrowserSet bool // the user set BROWSER
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
