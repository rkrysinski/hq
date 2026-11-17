//go:build integration

package scripts_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runPrepost runs the Claude Code hook scripts/claude-hooks/prepost (#231)
// on a Bash command run in cwd, the way Claude Code does, with env added to
// the environment and no list of the machine's own, and returns its exit
// code and its output.
func runPrepost(t *testing.T, cwd, command string, env ...string) (int, string) {
	t.Helper()
	input, err := json.Marshal(map[string]any{
		"session_id": "s", "cwd": cwd, "hook_event_name": "PreToolUse", "tool_name": "Bash",
		"tool_input": map[string]any{"command": command, "description": "d"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("claude-hooks/prepost")
	cmd.Stdin = strings.NewReader(string(input))
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HQ_PRIVATE_PATTERNS=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), string(out)
	}
	if err != nil {
		t.Fatal(err)
	}
	return 0, string(out)
}

func needGo(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not installed")
	}
}

func TestTheClaudeHookLetsACleanPostThrough(t *testing.T) {
	needGo(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "body.md"), []byte("fine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := runPrepost(t, dir, "gh pr create --title Fix --body-file body.md", "HQ_PRIVATE_PATTERNS=acme")
	if code != 0 || !strings.Contains(out, "prepost: 1 patterns, 2 texts, 0 matching lines") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestTheClaudeHookRefusesAPostWithAPrivateDetail(t *testing.T) {
	needGo(t)
	code, out := runPrepost(t, t.TempDir(), "cd /w && gh issue comment 3 --body \"$(cat <<'EOF'\nfine\nfor Acme\nEOF\n)\"", "HQ_PRIVATE_PATTERNS=acme")
	if code != 2 || !strings.Contains(out, "FAIL: gh issue comment --body:2 matches private pattern 1") ||
		!strings.Contains(out, "refused: take the private detail out") || strings.Contains(strings.ToLower(out), "for acme") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestTheClaudeHookRefusesAPostWithoutTheList(t *testing.T) {
	needGo(t)
	code, out := runPrepost(t, t.TempDir(), "gh pr comment 3 --body fine", "HQ_PRIVATE_PATTERNS=# no patterns")
	if code != 2 || !strings.Contains(out, "no pattern list") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestTheClaudeHookRefusesInputItCannotRead(t *testing.T) {
	needGo(t)
	cmd := exec.Command("claude-hooks/prepost")
	cmd.Stdin = strings.NewReader(`gh pr comment 3 --body fine`)
	cmd.Env = append(os.Environ(), "HQ_PRIVATE_PATTERNS=acme")
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 2 || !strings.Contains(string(out), "refused") {
		t.Fatalf("err %v\n%s", err, out)
	}
}

// pathWithoutGo is a PATH that holds the tools the hook uses but no go.
func pathWithoutGo(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	for _, tool := range []string{"sh", "cat", "grep", "dirname"} {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Skip(tool + " not installed")
		}
		if err := os.Symlink(p, filepath.Join(bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	return "PATH=" + bin
}

func TestWithoutGoTheClaudeHookRefusesAPostAndPassesAnythingElse(t *testing.T) {
	path := pathWithoutGo(t)
	code, out := runPrepost(t, t.TempDir(), "gh api repos/{owner}/{repo}/issues/3/comments -f body=fine", path, "HQ_PRIVATE_PATTERNS=acme")
	if code != 2 || !strings.Contains(out, "go is not installed") {
		t.Fatalf("a post: exit %d\n%s", code, out)
	}
	for _, c := range []string{"ls -la", "gh pr view 3 --json body", "git log --oneline -3"} {
		if code, out := runPrepost(t, t.TempDir(), c, path); code != 0 || out != "" {
			t.Errorf("%q: exit %d\n%s", c, code, out)
		}
	}
}

func TestTheProjectSettingsRunTheHookOnEveryBashCommand(t *testing.T) {
	body, err := os.ReadFile("../.claude/settings.json")
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Hooks struct {
			PreToolUse []struct {
				Matcher string
				Hooks   []struct{ Type, Command string }
			}
		}
	}
	if err := json.Unmarshal(body, &settings); err != nil {
		t.Fatal(err)
	}
	for _, m := range settings.Hooks.PreToolUse {
		for _, h := range m.Hooks {
			if m.Matcher == "Bash" && h.Type == "command" && h.Command == `"$CLAUDE_PROJECT_DIR"/scripts/claude-hooks/prepost` {
				if info, err := os.Stat("claude-hooks/prepost"); err != nil || info.Mode()&0o111 == 0 {
					t.Fatalf("the hook is not an executable file: %v %v", info, err)
				}
				return
			}
		}
	}
	t.Fatalf("no PreToolUse hook on Bash runs scripts/claude-hooks/prepost:\n%s", body)
}
