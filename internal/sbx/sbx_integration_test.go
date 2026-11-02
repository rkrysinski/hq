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
	if err := c.Create("/w/app", Options{}); err != nil {
		t.Fatal(err)
	}
	all, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	s, ok := Find(all, "/w/app", "default", func(a, b string) bool { return a == b })
	if !ok || s.Name != "claude-app" || s.Running() {
		t.Fatalf("%+v %v", s, ok)
	}
}

// The template and MCP servers reach sbx create, and sbx's error for one it
// cannot find comes back as sbx says it (#58).
func TestCreateWithTemplateAndStaticMCP(t *testing.T) {
	bin, dir := testutil.SbxStub(t)
	c := Client{Run: proc.Exec{}, Platform: platformtest.Fake{Sbx: bin}}
	if err := c.Create("/w/app", Options{Template: "sbx-image:local", StaticMCP: []string{"pencil", "docs"}}); err != nil {
		t.Fatal(err)
	}
	log, _ := os.ReadFile(dir + "/creates.log")
	if got := strings.TrimSpace(string(log)); got != `--quiet --template sbx-image:local --static-mcp pencil,docs claude F:\w\app` {
		t.Fatalf("created with %q", got)
	}
	if all, _ := c.List(); len(all) != 1 || all[0].Name != "claude-app" {
		t.Fatalf("%+v", all)
	}
	t.Setenv("SBX_STUB_CREATE_ERR", "image sbx-image:gone not found")
	err := c.Create("/w/lib", Options{Template: "sbx-image:gone"})
	if err == nil || !strings.HasSuffix(err.Error(), ": error: image sbx-image:gone not found") {
		t.Fatalf("%v", err)
	}
}

// A repository's sandboxes of several setups, on one workspace, are told
// apart by name: a setup's is created under its name, through sbx run
// --detached with a model, and found again beside the default (#135).
func TestSandboxesOfSeveralSetupsOnOneWorkspace(t *testing.T) {
	bin, dir := testutil.SbxStub(t)
	c := Client{Run: proc.Exec{}, Platform: platformtest.Fake{Sbx: bin}}
	same := func(a, b string) bool { return a == b }
	if err := c.Create("/w/app", Options{}); err != nil {
		t.Fatal(err)
	}
	if err := c.Create("/w/app", Options{Name: SandboxName("local", "app"), Model: "qwen-local", Provider: "lmstudio"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Create("/w/app", Options{Name: SandboxName("plain", "app")}); err != nil {
		t.Fatal(err)
	}
	log, _ := os.ReadFile(dir + "/creates.log")
	if got := strings.Split(strings.TrimSpace(string(log)), "\n"); len(got) != 3 ||
		got[1] != `run --detached --name hq-local-app --model qwen-local --provider lmstudio claude F:\w\app` ||
		got[2] != `--quiet --name hq-plain-app claude F:\w\app` {
		t.Fatalf("created with %q", got)
	}
	all, err := c.List()
	if err != nil || len(all) != 3 {
		t.Fatalf("%+v %v", all, err)
	}
	for setup, want := range map[string]string{"default": "claude-app", "local": "hq-local-app", "plain": "hq-plain-app"} {
		if s, ok := Find(all, "/w/app", setup, same); !ok || s.Name != want || SetupOf(s.Name, "app") != setup {
			t.Errorf("%s: %+v %v", setup, s, ok)
		}
	}
	// sbx refuses a setup's name for a second repository of the same
	// directory name, and hq passes its error on.
	err = c.Create("/x/app", Options{Name: SandboxName("local", "app")})
	if err == nil || !strings.Contains(err.Error(), "error: sandbox hq-local-app already exists and can't be given new workspaces") {
		t.Fatalf("%v", err)
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

func TestOutputReturnsWhatTheCommandPrinted(t *testing.T) {
	bin, _ := testutil.SbxStub(t)
	c := Client{Run: proc.Exec{}, Platform: platformtest.Fake{Sbx: bin}}
	out, err := c.Output("claude-app", "sh", "-c", "echo out; echo err >&2")
	if err != nil || string(out) != "out\n" {
		t.Fatalf("%q %v", out, err)
	}
	if _, err := c.Output("claude-app", "false"); err == nil {
		t.Fatal("a failing command must report an error")
	}
}

func TestStopThenExecStartsAgainAndRemoveDeletes(t *testing.T) {
	bin, _ := testutil.SbxStub(t)
	c := Client{Run: proc.Exec{}, Platform: platformtest.Fake{Sbx: bin}}
	if err := c.Create("/w/app", Options{}); err != nil {
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
	if err := c.Create("/home/dev/app", Options{}); err != nil {
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
