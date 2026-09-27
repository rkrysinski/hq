//go:build integration

package sbx

import (
	"os"
	"testing"

	"github.com/rkrysinski/hq/internal/proc"
	"github.com/rkrysinski/hq/internal/testutil"
)

// Contract against the stub, which mirrors the real sbx's JSON (sbx v0.45).
func TestCreateThenListFindsSandboxByWorkspace(t *testing.T) {
	bin, _ := testutil.SbxStub(t)
	c := Client{Run: proc.Exec{}, Bin: bin}
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
	c := Client{Run: proc.Exec{}, Bin: bin}
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
	c := Client{Run: proc.Exec{}, Bin: bin}
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
