package platform

import (
	"slices"

	"github.com/rkrysinski/hq/internal/proc"
)

// SbxSide is where sbx runs as hq reaches it, apart from the desktop it
// runs on (design §3.10): the native sbx, on hq's own system, or the
// Windows sbx.exe, which hq on WSL reaches through interop. Detect puts it
// together with the platform: macOS and Linux always run the native sbx;
// WSL the Linux sbx when one is in PATH, otherwise sbx.exe (ADR 0017).
type SbxSide interface {
	// SbxCommand is the command that runs sbx.
	SbxCommand() string
	// ToSbx turns a path as hq sees it into the path sbx understands.
	ToSbx(path string) (string, error)
	// FromSbx turns a path sbx reports into the path hq sees.
	FromSbx(path string) (string, error)
	// Gateway is the command line sbx's MCP gateway starts hq's command
	// line argv with, setting the environment env ("KEY=value") for it:
	// sbx mcp add sets none itself (#141).
	Gateway(argv, env []string) (command string, args []string)
	// Missing says what was not found when the sbx command is not there.
	Missing() string
}

// NativeSbx is sbx on hq's own system: it sees hq's paths and starts hq
// itself.
type NativeSbx struct{}

func (NativeSbx) SbxCommand() string                  { return "sbx" }
func (NativeSbx) ToSbx(path string) (string, error)   { return path, nil }
func (NativeSbx) FromSbx(path string) (string, error) { return path, nil }
func (NativeSbx) Missing() string                     { return "sbx not found" }

// Gateway is env(1) setting the environment, then hq.
func (NativeSbx) Gateway(argv, env []string) (string, []string) {
	return "/usr/bin/env", append(slices.Clone(env), argv...)
}

// WindowsSbx is sbx.exe on Windows, reached from WSL through interop: paths
// cross between Linux and Windows with wslpath, and it starts hq in the
// distribution as Claude Desktop does, through wsl.exe.
type WindowsSbx struct {
	Run    proc.Runner
	Distro string // the WSL distribution hq runs in, when known
}

func (WindowsSbx) SbxCommand() string { return "sbx.exe" }

func (s WindowsSbx) ToSbx(path string) (string, error)   { return wslpath(s.Run, "-w", path) }
func (s WindowsSbx) FromSbx(path string) (string, error) { return wslpath(s.Run, "-u", path) }

// Missing names both: hq on WSL takes sbx.exe only when there is no sbx.
func (WindowsSbx) Missing() string { return "neither sbx nor sbx.exe found" }

// Gateway is Claude Desktop's command line (WSL.AppServer), which carries
// the environment itself.
func (s WindowsSbx) Gateway(argv, env []string) (string, []string) {
	cmd, args, _ := WSL{Distro: s.Distro}.AppServer(argv, env)
	return cmd, args
}

// wslSbx is the sbx hq on WSL runs: the Linux sbx when lookPath finds sbx,
// which on WSL is only ever the Linux one (Windows programs keep their
// .exe), otherwise sbx.exe.
func wslSbx(lookPath func(string) (string, error), run proc.Runner, distro string) SbxSide {
	if _, err := lookPath("sbx"); err == nil {
		return NativeSbx{}
	}
	return WindowsSbx{Run: run, Distro: distro}
}
