package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/rkrysinski/hq/internal/proc"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
)

func codeFakes() *fakes {
	f := newFakes()
	f.tmux.windows = []tmux.Window{
		{ID: "@0", Name: "hq", Options: map[string]string{}},
		agentWindow("@4", "a", "/w/app", f.now, false),
	}
	return f
}

func TestCodeOpensTheAgentsWorktree(t *testing.T) {
	for _, tc := range []struct{ cwd, want string }{
		{`F:\w\app\.claude\worktrees\x`, "/w/app/.claude/worktrees/x"},     // a worktree, as the sandbox sees it
		{`F:\w\app\.claude\worktrees\x\sub`, "/w/app/.claude/worktrees/x"}, // Claude in a subdirectory of it
		{`F:\w\app\sub`, "/w/app"},                                         // the main checkout
		{"", "/w/app"},                                                     // not reported yet
		{`F:\w\lib`, "/w/app"},                                             // outside the agent's repository
		{`F:\gone`, "/w/app"},                                              // no longer there
	} {
		f := codeFakes()
		f.states["id-a"] = state.Report{State: state.Working, Cwd: tc.cwd}
		code, out, errOut := f.run("code", "a")
		if code != 0 || strings.Join(f.edited, " ") != tc.want || out != "opened "+tc.want+" in VS Code\n" {
			t.Errorf("%q: exit %d %q %q, opened %q", tc.cwd, code, out, errOut, f.edited)
		}
	}
}

func TestCodeNeedsAnAgent(t *testing.T) {
	f := codeFakes()
	if code, _, errOut := f.run("code", "missing"); code != ExitNotFound || errOut != "hq: no agent 'missing' (see hq ls)\n" {
		t.Fatalf("exit %d %q", code, errOut)
	}
	if code, _, _ := f.run("code"); code != ExitUsage {
		t.Fatalf("no name: exit %d", code)
	}
	if len(f.edited) != 0 {
		t.Fatalf("opened %q", f.edited)
	}
}

func TestCodeWithoutVSCodeSaysHowToGetIt(t *testing.T) {
	f := codeFakes()
	f.editorErr = &proc.Error{Name: "code", Msg: "not found", NotFound: true}
	if code, _, errOut := f.run("code", "a"); code != ExitEnvironment || !strings.Contains(errOut, "Install 'code' command in PATH") {
		t.Fatalf("exit %d %q", code, errOut)
	}
	f.editorErr = errors.New("code: broken")
	if code, _, errOut := f.run("code", "a"); code != ExitEnvironment || errOut != "hq: code: broken\n" {
		t.Fatalf("exit %d %q", code, errOut)
	}
}

func TestHelpListsCode(t *testing.T) {
	if _, out, _ := runCLI("help"); !strings.Contains(out, "code NAME") {
		t.Fatalf("help:\n%s", out)
	}
}

func TestCInTheListOpensTheEditor(t *testing.T) {
	f := codeFakes()
	if err := listSource(f.deps(), "%1").Code("a"); err != nil || strings.Join(f.edited, " ") != "/w/app" {
		t.Fatalf("%v, opened %q", err, f.edited)
	}
	if err := listSource(f.deps(), "%1").Code("missing"); err == nil || err.Error() != "no agent 'missing' (see hq ls)" {
		t.Fatalf("missing: %v", err)
	}
}
