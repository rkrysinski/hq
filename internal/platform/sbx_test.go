package platform_test

import (
	"errors"
	"os"
	"reflect"
	"slices"
	"testing"

	"github.com/rkrysinski/hq/internal/platform"
	"github.com/rkrysinski/hq/internal/platform/platformtest"
)

func TestNativeSbxKeepsTheContract(t *testing.T) { platformtest.SbxContract(t, platform.NativeSbx{}) }

func TestWindowsSbxKeepsTheContract(t *testing.T) {
	platformtest.SbxContract(t, platform.WindowsSbx{Run: &fakeWslpath{}})
}

// The native sbx sees hq's paths, and starts hq's gateway command line
// itself, its environment set by env(1): sbx mcp add sets none (#141).
func TestNativeSbxRunsSbxWithPathsUnchangedAndStartsHqThroughEnv(t *testing.T) {
	s := platform.NativeSbx{}
	if s.SbxCommand() != "sbx" {
		t.Fatalf("sbx command %q", s.SbxCommand())
	}
	if p, err := s.ToSbx("/home/dev/app"); err != nil || p != "/home/dev/app" {
		t.Fatalf("ToSbx %q %v", p, err)
	}
	if p, err := s.FromSbx("/home/dev/app"); err != nil || p != "/home/dev/app" {
		t.Fatalf("FromSbx %q %v", p, err)
	}
	cmd, args := s.Gateway([]string{"/u/hq", "mcp", "sbx"}, []string{"PATH=/usr/bin", "HQ_TMUX_SOCKET=s"})
	if cmd != "/usr/bin/env" || !slices.Equal(args, []string{"PATH=/usr/bin", "HQ_TMUX_SOCKET=s", "/u/hq", "mcp", "sbx"}) {
		t.Fatalf("gateway %q %q", cmd, args)
	}
	if got := s.Missing(); got != "sbx not found" {
		t.Fatalf("missing %q", got)
	}
}

// sbx.exe on Windows sees Windows paths, through wslpath, and starts hq as
// Claude Desktop does, through wsl.exe; when it is missing, so was sbx.
func TestWindowsSbxRunsSbxExeThroughInterop(t *testing.T) {
	w := &fakeWslpath{}
	s := platform.WindowsSbx{Run: w, Distro: "Ubuntu"}
	if s.SbxCommand() != "sbx.exe" {
		t.Fatalf("sbx command %q", s.SbxCommand())
	}
	if p, err := s.ToSbx("/home/dev/app"); err != nil || p != `\\wsl.localhost\Ubuntu\home\dev\app` {
		t.Fatalf("ToSbx %q %v", p, err)
	}
	if p, err := s.FromSbx(`C:\Users\dev\app`); err != nil || p != "/mnt/c/Users/dev/app" {
		t.Fatalf("FromSbx %q %v", p, err)
	}
	cmd, args := s.Gateway([]string{"/u/hq", "mcp", "sbx"}, []string{"PATH=/usr/bin"})
	if cmd != "wsl.exe" || !slices.Equal(args, []string{"-d", "Ubuntu", "--exec", "/usr/bin/env", "PATH=/usr/bin", "/u/hq", "mcp", "sbx"}) {
		t.Fatalf("gateway %q %q", cmd, args)
	}
	if got := s.Missing(); got != "neither sbx nor sbx.exe found" {
		t.Fatalf("missing %q", got)
	}
}

// On WSL the sbx in PATH is the Linux sbx, and hq uses it; without one, the
// Windows sbx.exe, as before (#153). The rest of WSL stays WSL's.
func TestDetectOnWSLTakesTheLinuxSbxWhenOneIsInPath(t *testing.T) {
	env := func(k string) string { return map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}[k] }
	noFile := func(string) ([]byte, error) { return nil, os.ErrNotExist }
	found := func(name string) (string, error) {
		if name != "sbx" {
			t.Fatalf("looked for %q", name)
		}
		return "/usr/local/bin/sbx", nil
	}
	none := func(string) (string, error) { return "", errors.New("not found") }

	w := &fakeWslpath{}
	p := platform.Detect(env, noFile, found, w)
	if _, ok := p.(platform.WSL); !ok {
		t.Fatalf("got %T", p)
	}
	if p.SbxCommand() != "sbx" {
		t.Fatalf("sbx command %q", p.SbxCommand())
	}
	if s, err := p.ToSbx("/home/dev/app"); err != nil || s != "/home/dev/app" {
		t.Fatalf("ToSbx %q %v", s, err)
	}
	if s, err := p.FromSbx("/home/dev/app"); err != nil || s != "/home/dev/app" || len(w.calls) != 0 {
		t.Fatalf("FromSbx %q %v, calls %q", s, err, w.calls)
	}
	if cmd, _ := p.Gateway([]string{"/u/hq", "mcp", "sbx"}, nil); cmd != "/usr/bin/env" {
		t.Fatalf("gateway %q", cmd)
	}
	// WSL's own: the bell, the window raised by PowerShell, the app
	// started through wsl.exe.
	if p.NotifySequence() != `\u0007` || p.Raise("")[0] != "powershell.exe" {
		t.Fatalf("not WSL's: %q %q", p.NotifySequence(), p.Raise(""))
	}
	if cmd, _, _ := p.AppServer([]string{"/u/hq", "mcp"}, nil); cmd != "wsl.exe" {
		t.Fatalf("app server %q", cmd)
	}

	p = platform.Detect(env, noFile, none, w)
	if p.SbxCommand() != "sbx.exe" {
		t.Fatalf("without sbx: %q", p.SbxCommand())
	}
	if s, err := p.ToSbx("/home/dev/app"); err != nil || s != `\\wsl.localhost\Ubuntu\home\dev\app` {
		t.Fatalf("without sbx: ToSbx %q %v", s, err)
	}
	if cmd, args := p.Gateway([]string{"/u/hq", "mcp", "sbx"}, nil); cmd != "wsl.exe" || !slices.Equal(args[:2], []string{"-d", "Ubuntu"}) {
		t.Fatalf("without sbx: gateway %q %q", cmd, args)
	}
}

// macOS and a plain Linux always run the native sbx, never looking.
func TestDetectOffWSLRunsTheNativeSbx(t *testing.T) {
	noFile := func(string) ([]byte, error) { return nil, os.ErrNotExist }
	look := func(string) (string, error) { t.Fatal("looked for sbx"); return "", nil }
	p := platform.Detect(func(string) string { return "" }, noFile, look, &fakeWslpath{})
	if !reflect.DeepEqual(p, platform.Native{}) || p.SbxCommand() != "sbx" {
		t.Fatalf("got %#v", p)
	}
}

// HQ_PLATFORM=wsl is the WSL adapter as the tests stub it, with sbx.exe:
// a test's PATH may hold the user's own sbx (design §7.2).
func TestDetectToldWSLRunsSbxExe(t *testing.T) {
	env := func(k string) string { return map[string]string{"HQ_PLATFORM": "wsl"}[k] }
	found := func(string) (string, error) { return "/usr/local/bin/sbx", nil }
	if p := platform.Detect(env, nil, found, &fakeWslpath{}); p.SbxCommand() != "sbx.exe" {
		t.Fatalf("got %q", p.SbxCommand())
	}
}
