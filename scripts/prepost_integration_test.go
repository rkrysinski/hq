//go:build integration

package scripts_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
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
	return runHook(t, string(input), env...)
}

// runHook runs the hook with input on stdin.
func runHook(t *testing.T, input string, env ...string) (int, string) {
	t.Helper()
	cmd := exec.Command("claude-hooks/prepost")
	cmd.Stdin = strings.NewReader(input)
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
	if code, out := runHook(t, `gh pr comment 3 --body fine`, "HQ_PRIVATE_PATTERNS=acme"); code != 2 || !strings.Contains(out, "refused") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

// binWith is a directory of links to the named tools, for a PATH that holds
// only them.
func binWith(t *testing.T, tools ...string) string {
	t.Helper()
	bin := t.TempDir()
	for _, tool := range tools {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Skip(tool + " not installed")
		}
		if err := os.Symlink(p, filepath.Join(bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	return bin
}

// Without go only the commands that may post reach the gate, which refuses
// them: the hook tells them by the subcommands of postingSubcommands in
// tools/testgate/prepost.go, so every one of them is refused here.
func TestWithoutGoTheClaudeHookRefusesAPostAndPassesAnythingElse(t *testing.T) {
	path := "PATH=" + binWith(t, "sh", "cat", "grep", "dirname")
	for _, c := range []string{
		"gh pr create", "gh pr new", "gh pr edit", "gh pr comment", "gh pr review", "gh pr merge", "gh pr close",
		"gh issue create", "gh issue new", "gh issue edit", "gh issue comment", "gh issue close",
		"gh release create", "gh release new", "gh release edit", "gh api",
		"gh pr -R o/r create", "gh issue --repo o/r comment", "gh issue --repo=o/r -Ro/r close", "cd /w &&\n  gh \\\n pr\tcomment", "true\ngh issue comment",
	} {
		if code, out := runPrepost(t, t.TempDir(), c+" -b fine", path, "HQ_PRIVATE_PATTERNS=acme"); code != 2 || !strings.Contains(out, "go is not installed") {
			t.Errorf("%q: exit %d\n%s", c, code, out)
		}
	}
	for _, c := range []string{"ls -la", "gh pr view 3 --json body", "gh issue list -R o/r", "git log --oneline -3", "echo high api"} {
		if code, out := runPrepost(t, t.TempDir(), c, path); code != 0 || out != "" {
			t.Errorf("%q: exit %d\n%s", c, code, out)
		}
	}
}

func TestWhenGrepFailsTheClaudeHookStillChecks(t *testing.T) {
	needGo(t)
	bin := binWith(t, "sh", "cat", "dirname", "go")
	if err := os.WriteFile(filepath.Join(bin, "grep"), []byte("#!/bin/sh\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	code, out := runPrepost(t, t.TempDir(), "gh pr comment 3 --body acme", "PATH="+bin, "HQ_PRIVATE_PATTERNS=acme")
	if code != 2 || !strings.Contains(out, "gh pr comment --body:1 matches private pattern 1") {
		t.Fatalf("exit %d\n%s", code, out)
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

// The test above reads .claude/settings.json, so a pull request that
// changes only that file gets the Pull request tests run: no pattern of a
// skip list (paths-ignore) of that workflow matches the file (#301).
func TestAPullRequestThatChangesOnlyTheProjectSettingsGetsATestRun(t *testing.T) {
	const workflow, file = ".github/workflows/pull-request-tests.yml", ".claude/settings.json"
	body, err := os.ReadFile("../" + workflow)
	if err != nil {
		t.Fatal(err)
	}
	var covering []string
	for _, p := range skipPatterns(t, string(body)) {
		if matchesPathFilter(t, p, file) {
			covering = append(covering, p)
		}
	}
	if len(covering) > 0 {
		t.Fatalf("the skip list of %s covers %s with %q: a pull request that changes only that file gets no run, though TestTheProjectSettingsRunTheHookOnEveryBashCommand reads it", workflow, file, covering)
	}
}

// skipPatterns returns the patterns of every paths-ignore list of a
// workflow. hq has no YAML library, so it reads each list as a block
// sequence of one pattern per line, and fails on a list written otherwise
// rather than find no pattern in it.
func skipPatterns(t *testing.T, workflow string) []string {
	t.Helper()
	var patterns []string
	list := -1 // the indentation of the paths-ignore key whose entries follow, or -1
	for n, line := range strings.Split(workflow, "\n") {
		text := strings.TrimSpace(line)
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if entry, ok := strings.CutPrefix(text, "- "); ok && list >= 0 && indent >= list {
			patterns = append(patterns, yamlScalar(t, n+1, strings.TrimSpace(entry)))
			continue
		}
		list = -1
		if key, value, _ := strings.Cut(text, ":"); key == "paths-ignore" {
			if value = strings.TrimSpace(value); value != "" && !strings.HasPrefix(value, "#") {
				t.Fatalf("line %d: paths-ignore: %s: write the list one pattern per line", n+1, value)
			}
			list = indent
		}
	}
	return patterns
}

// yamlScalar is the value of a YAML scalar written on line n: a single- or
// double-quoted one without its quotes, a plain one up to its comment.
func yamlScalar(t *testing.T, n int, s string) string {
	t.Helper()
	switch {
	case strings.HasPrefix(s, "'"):
		var v strings.Builder
		for i := 1; i < len(s); i++ {
			if s[i] != '\'' {
				v.WriteByte(s[i])
			} else if i+1 < len(s) && s[i+1] == '\'' {
				v.WriteByte('\'')
				i++
			} else {
				return v.String()
			}
		}
	case strings.HasPrefix(s, `"`):
		if q, err := strconv.QuotedPrefix(s); err == nil {
			if v, err := strconv.Unquote(q); err == nil {
				return v
			}
		}
	default:
		v, _, _ := strings.Cut(s, " #")
		return strings.TrimSpace(v)
	}
	t.Fatalf("line %d: cannot read the pattern %s", n, s)
	return ""
}

// matchesPathFilter reports whether a pattern of a workflow's paths filter
// matches the whole of path, by GitHub's filter pattern syntax: * is any
// characters but /, ** any characters, and **/ any directories or none; ?
// and + make the character before them optional or repeatable; [...] is one
// character of a class; \ takes the next character as it is. A leading dot
// is a character like any other, the stricter reading for a skip list.
func matchesPathFilter(t *testing.T, pattern, path string) bool {
	t.Helper()
	var re strings.Builder
	for i := 0; i < len(pattern); i++ {
		switch rest := pattern[i:]; {
		case strings.HasPrefix(rest, "**/"):
			re.WriteString("(.*/)?")
			i += 2
		case strings.HasPrefix(rest, "**"):
			re.WriteString(".*")
			i++
		case rest[0] == '*':
			re.WriteString("[^/]*")
		case rest[0] == '?', rest[0] == '+':
			re.WriteByte(rest[0])
		case rest[0] == '[':
			end := strings.IndexByte(rest, ']')
			if end < 0 {
				t.Fatalf("the pattern %q has [ without ]", pattern)
			}
			re.WriteString(rest[:end+1])
			i += end
		case rest[0] == '\\' && len(rest) > 1:
			re.WriteString(regexp.QuoteMeta(rest[1:2]))
			i++
		default:
			re.WriteString(regexp.QuoteMeta(rest[:1]))
		}
	}
	matched, err := regexp.MatchString("^(?:"+re.String()+")$", path)
	if err != nil {
		t.Fatalf("the pattern %q: %v", pattern, err)
	}
	return matched
}
