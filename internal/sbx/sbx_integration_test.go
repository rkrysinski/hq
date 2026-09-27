//go:build integration

package sbx

import (
	"os"
	"strings"
	"testing"

	"github.com/rkrysinski/hq/internal/platform"
	"github.com/rkrysinski/hq/internal/platform/platformtest"
	"github.com/rkrysinski/hq/internal/proc"
	"github.com/rkrysinski/hq/internal/testutil"
)

// Contract against the stub, which mirrors the real sbx's JSON (sbx v0.45).
func TestCreateThenListFindsSandboxByWorkspace(t *testing.T) {
	bin, _ := testutil.SbxStub(t)
	c := Client{Run: proc.Exec{}, Platform: platformtest.Fake{Sbx: bin}}
	if all, err := c.List(); err != nil || len(all) != 0 {
		t.Fatalf("%v %v", all, err)
	}
	if err := c.Create("/w/app"); err != nil {
		t.Fatal(err)
	}
	all, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	s, ok := ByWorkspace(all, "/w/app", func(a, b string) bool { return a == b })
	if !ok || s.Name != "claude-app" || s.Running() {
		t.Fatalf("%+v %v", s, ok)
	}
}

func TestExecRunsTheCommandInTheSandbox(t *testing.T) {
	bin, dir := testutil.SbxStub(t)
	c := Client{Run: proc.Exec{}, Platform: platformtest.Fake{Sbx: bin}}
	if err := c.Exec("claude-app", "touch", dir+"/ran"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir + "/ran"); err != nil {
		t.Fatal(err)
	}
	if err := c.Exec("claude-app", "false"); err == nil {
		t.Fatal("a failing command must report an error")
	}
}

func TestStopThenExecStartsAgainAndRemoveDeletes(t *testing.T) {
	bin, _ := testutil.SbxStub(t)
	c := Client{Run: proc.Exec{}, Platform: platformtest.Fake{Sbx: bin}}
	if err := c.Create("/w/app"); err != nil {
		t.Fatal(err)
	}
	status := func() string {
		all, err := c.List()
		if err != nil || len(all) == 0 {
			return "gone"
		}
		return all[0].Status
	}
	if err := c.Exec("claude-app", "true"); err != nil || status() != "running" {
		t.Fatalf("exec: %v, %s", err, status())
	}
	if err := c.Stop("claude-app"); err != nil || status() != "stopped" {
		t.Fatalf("stop: %v, %s", err, status())
	}
	if err := c.Remove("claude-app"); err != nil || status() != "gone" {
		t.Fatalf("rm: %v, %s", err, status())
	}
}

// On WSL, sbx.exe keeps Windows paths; hq sees them as Linux paths.
func TestWSLCreateThenListThroughSbxExe(t *testing.T) {
	dir := testutil.WSLStubs(t)
	c := Client{Run: proc.Exec{}, Platform: platform.WSL{Run: proc.Exec{}}}
	if err := c.Create("/home/dev/app"); err != nil {
		t.Fatal(err)
	}
	state, err := os.ReadFile(dir + "/sandboxes/claude-app")
	if err != nil || !strings.Contains(string(state), `\\wsl.localhost\Stub\home\dev\app`) {
		t.Fatalf("sbx.exe got %q %v", state, err)
	}
	all, err := c.List()
	if err != nil || len(all) != 1 || all[0].Name != "claude-app" || all[0].Workspaces[0] != "/home/dev/app" {
		t.Fatalf("%+v %v", all, err)
	}
}
