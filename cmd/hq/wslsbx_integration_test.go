//go:build integration

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rkrysinski/hq/internal/testutil"
)

// On WSL with the Linux sbx in PATH, hq runs it, not sbx.exe, and gives it
// hq's own paths (#153): hq new makes the agent's sandbox on the
// repository as hq sees it, hq ls shows the agent, and hq mcp install sbx
// registers the native command line, the one a user who ran hq with
// HQ_PLATFORM=native already has, so that user's install leaves it, and
// hears that the workaround can go.
func TestOnWSLHqRunsTheLinuxSbxInPath(t *testing.T) {
	s := supervisorEnv(t)
	dir := os.Getenv("SBX_STUB_DIR")
	// WSL as hq finds it, by looking: no HQ_PLATFORM. No sbx.exe, wslpath
	// or cmd.exe is in PATH, so none is reached.
	wsl := append(s.env, "WSL_DISTRO_NAME=Stub", "HQ_PLATFORM=")
	hq := func(env []string, args ...string) (string, string) {
		t.Helper()
		cmd := exec.Command(s.bin, args...)
		cmd.Env = env
		var out, errOut strings.Builder
		cmd.Stdout, cmd.Stderr = &out, &errOut
		if err := cmd.Run(); err != nil {
			t.Fatalf("hq %q: %v %s %s", args, err, out.String(), errOut.String())
		}
		return out.String(), errOut.String()
	}

	app := testutil.GitRepo(t, "app")
	hq(wsl, "new", "a", app, "hello")
	state, err := os.ReadFile(filepath.Join(dir, "sandboxes", "claude-app"))
	if err != nil || strings.Split(string(state), "\n")[1] != app {
		t.Fatalf("sbx got %q %v, want the workspace %s", state, err, app)
	}
	if out, _ := hq(wsl, "ls"); !strings.Contains(out, "a ") {
		t.Fatalf("ls: %s", out)
	}

	if out, errOut := hq(wsl, "mcp", "install", "sbx"); !strings.HasPrefix(out, "added hq to sbx") || errOut != "" {
		t.Fatalf("install: %q %q", out, errOut)
	}
	var registered struct {
		Command []string `json:"command"`
	}
	b, _ := os.ReadFile(filepath.Join(dir, "mcp", "hq"))
	// hq's own path, symlinks resolved (macOS's /var is /private/var).
	bin, _ := filepath.EvalSymlinks(s.bin)
	if err := json.Unmarshal(b, &registered); err != nil || registered.Command[0] != "/usr/bin/env" || registered.Command[len(registered.Command)-3] != bin {
		t.Fatalf("registered %v %s", err, b)
	}

	out, errOut := hq(append(wsl, "HQ_PLATFORM=native"), "mcp", "install", "sbx")
	if !strings.HasPrefix(out, "hq is already set up in sbx") || !strings.HasPrefix(errOut, "hq: HQ_PLATFORM=native is no longer needed on WSL") {
		t.Fatalf("with the workaround: %q %q", out, errOut)
	}
}
