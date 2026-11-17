package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// hookJSON is what Claude Code hands a PreToolUse hook for a Bash command
// run in cwd.
func hookJSON(t *testing.T, cwd, command string) *strings.Reader {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"session_id":      "s",
		"cwd":             cwd,
		"hook_event_name": "PreToolUse",
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": command, "description": "d"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return strings.NewReader(string(b))
}

// inTime returns what f returns and fails the test when f has not returned
// within five seconds: a reader that stops advancing fails instead of
// hanging the run.
func inTime[T any](t *testing.T, f func() T) T {
	t.Helper()
	done := make(chan T, 1)
	go func() { done <- f() }()
	select {
	case v := <-done:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("did not return within 5s")
		var zero T
		return zero
	}
}

// checkInTime runs checkPost within five seconds.
func checkInTime(t *testing.T, list string, input *strings.Reader, home string) (string, []string, error) {
	t.Helper()
	type result struct {
		summary  string
		problems []string
		err      error
	}
	r := inTime(t, func() result {
		summary, problems, err := checkPost(list, input, home)
		return result{summary, problems, err}
	})
	return r.summary, r.problems, r.err
}

// post runs checkPost on command, run in cwd, with the list "acme" and the
// home directory /home/u.
func post(t *testing.T, cwd, command string) (string, []string) {
	t.Helper()
	summary, problems, err := checkInTime(t, "acme\n", hookJSON(t, cwd, command), "/home/u")
	if err != nil {
		t.Fatalf("checkPost(%q): %v", command, err)
	}
	return summary, problems
}

func TestACommandThatPostsNothingPassesSilentlyEvenWithoutAList(t *testing.T) {
	for _, c := range []string{"ls -la", "gh pr view 12 --json body", "gh issue list --search acme", "echo acme"} {
		summary, problems, err := checkInTime(t, "", hookJSON(t, "/w", c), "/home/u")
		if err != nil || summary != "" || problems != nil {
			t.Errorf("%q: summary %q problems %v err %v", c, summary, problems, err)
		}
	}
}

func TestInlineTextOfAPostingCommandIsChecked(t *testing.T) {
	summary, problems := post(t, "/w", `gh pr create --title "Fix it" --body "line one
we ship for ACME"`)
	want := []string{"gh pr create --body:2 matches private pattern 1"}
	if summary != "prepost: 1 patterns, 2 texts, 1 matching lines" || !reflect.DeepEqual(problems, want) {
		t.Fatalf("summary %q problems %v", summary, problems)
	}
}

func TestAHeredocBodyTheWayClaudeWritesItIsChecked(t *testing.T) {
	_, problems := post(t, "/w", "gh pr create --title \"Fix it\" --body \"$(cat <<'EOF'\n## Summary\n\nFor Acme.\nEOF\n)\"")
	want := []string{"gh pr create --body:3 matches private pattern 1"}
	if !reflect.DeepEqual(problems, want) {
		t.Fatalf("problems %v, want %v", problems, want)
	}
}

func TestABodyFileIsReadFromTheCommandsDirectory(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "body.md", "fine\nacme\n")
	write(t, dir, "sub/clean.md", "fine\n")
	summary, problems := post(t, dir, "gh pr edit 12 --body-file body.md && cd -P sub && gh issue comment 3 -F clean.md")
	if summary != "prepost: 1 patterns, 2 texts, 1 matching lines" || !reflect.DeepEqual(problems, []string{"body.md:2 matches private pattern 1"}) {
		t.Fatalf("summary %q problems %v", summary, problems)
	}
}

func TestATextTheHookCannotSeeIsRefused(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "old.md", "fine\n")
	write(t, dir, "locked.md", "fine\n")
	if err := os.Chmod(filepath.Join(dir, "locked.md"), 0); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ command, want string }{
		{`gh pr create --title t --body "$BODY"`, "gh pr create --body: holds $"},
		{`gh pr comment 1 --body "$(cat body.md)"`, "gh pr comment --body: holds $"},
		{"gh pr comment 1 --body \"`cat body.md`\"", "gh pr comment --body: holds $"},
		{"gh pr comment 1 --body \"$(cat <<EOF\nfor $USER\nEOF\n)\"", "gh pr comment --body: holds $"},
		{`gh pr comment 1 --body-file "$F"`, "$F: names its file with"},
		{"gh pr comment 1 --body-file missing.md", "missing.md: does not exist yet"},
		{"cat > old.md <<'EOF'\nnew text\nEOF\ngh pr comment 1 --body-file old.md", "old.md: is written by this same command"},
		{"echo hi | tee -a old.md && gh pr comment 1 -F old.md", "old.md: is written by this same command"},
		{"cd - && gh pr comment 1 -F old.md", "old.md: is a relative path in a directory the hook cannot tell"},
		{"cd \"$X\" && gh pr comment 1 -F old.md", "old.md: is a relative path in a directory the hook cannot tell"},
		{"gh pr comment 1 -F ~bob/x.md", "~bob/x.md: is a relative path"},
		{"cat old.md | gh pr comment 1 --body-file -", "gh pr comment stdin: reads its text from standard input"},
		{"gh pr comment 1 --body-file . ", ".: is not a regular file"},
		{"gh pr comment 1 --body-file locked.md", "locked.md: cannot be read"},
		{"cp new.md old.md && gh pr comment 1 -F old.md", "old.md: is written by this same command"},
		{"mv -f new.md old.md; gh pr comment 1 -F old.md", "old.md: is written by this same command"},
		{"sed -i.bak 's/a/b/' old.md && gh pr comment 1 -F old.md", "old.md: is written by this same command"},
		{"perl -pi -e 's/a/b/' old.md && gh pr comment 1 -F old.md", "old.md: is written by this same command"},
		{"sed --in-place=.bak 's/a/b/' old.md && gh pr comment 1 -F old.md", "old.md: is written by this same command"},
	} {
		summary, problems := post(t, dir, c.command)
		if len(problems) != 1 || !strings.HasPrefix(problems[0], c.want) || !strings.HasSuffix(summary, "0 matching lines") {
			t.Errorf("%q: summary %q problems %v, want one starting %q", c.command, summary, problems, c.want)
		}
	}
}

func TestWithoutAListAPostingCommandIsRefused(t *testing.T) {
	summary, problems, err := checkInTime(t, "# no patterns\n", hookJSON(t, "/w", `gh issue comment 3 --body "fine"`), "/home/u")
	if err != nil || summary != "prepost: no pattern list, refused" || !reflect.DeepEqual(problems, []string{noPostList}) {
		t.Fatalf("summary %q problems %v err %v", summary, problems, err)
	}
}

func TestABadListOrInputIsAnError(t *testing.T) {
	if _, _, err := checkInTime(t, "(unclosed\n", hookJSON(t, "/w", `gh issue comment 3 --body "fine"`), ""); err == nil || strings.Contains(err.Error(), "unclosed") {
		t.Errorf("bad pattern: err %v, want one that does not quote it", err)
	}
	if _, _, err := checkInTime(t, "acme\n", strings.NewReader("not json"), ""); err == nil {
		t.Error("input that is not JSON: no error")
	}
}

func TestGhAPIFieldsFilesAndStandardInputAreChecked(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "body.md", "acme\n")
	write(t, dir, "req.json", "{\"body\": \"fine\"}\n")
	_, problems := post(t, dir, "gh api --method PATCH repos/{owner}/{repo}/pulls/5 -F body=@body.md -f title='for Acme' "+
		"&& gh api repos/{owner}/{repo}/issues/5/comments --field n=3 --raw-field=body=acme --input req.json "+
		"&& gh api graphql --input - <<'EOF'\n{\"query\": \"acme\"}\nEOF\n"+
		"gh api -X GET search/issues -f q=acme")
	want := []string{"body.md:1 matches private pattern 1", "gh api -f:1 matches private pattern 1",
		"gh api --raw-field:1 matches private pattern 1", "gh api stdin:1 matches private pattern 1"}
	if !reflect.DeepEqual(problems, want) {
		t.Fatalf("problems %v, want %v", problems, want)
	}
}

func TestStandardInputFromAHereStringOrAFileIsChecked(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "body.md", "acme\n")
	_, problems := post(t, dir, "gh pr comment 1 --body-file - <<< 'for acme' ; gh pr comment 1 -F - < body.md")
	want := []string{"gh pr comment stdin:1 matches private pattern 1", "body.md:1 matches private pattern 1"}
	if !reflect.DeepEqual(problems, want) {
		t.Fatalf("problems %v, want %v", problems, want)
	}
}

func TestEveryPostingSubcommandIsCheckedAndNoOther(t *testing.T) {
	posting := []string{
		"gh pr create -t acme", "gh pr new -b acme", "gh pr edit 1 --title acme", "gh pr comment 1 -b acme",
		"gh pr review 1 --comment -b acme", "gh pr merge 1 --squash --subject acme", "gh pr merge 1 -t acme", "gh pr close 1 -c acme",
		"gh issue create --title acme", "gh issue new -t acme", "gh issue edit 1 --body acme", "gh issue comment 1 --body acme",
		"gh issue close 1 --comment acme", "gh release create v1 --notes acme", "gh release new v1 -n acme", "gh release edit v1 -t acme",
		"/usr/local/bin/gh pr comment 1 -b acme",
	}
	for _, c := range posting {
		if _, problems := post(t, "/w", c); len(problems) != 1 || !strings.HasSuffix(problems[0], ":1 matches private pattern 1") {
			t.Errorf("%q: problems %v, want one hit", c, problems)
		}
	}
	for _, c := range []string{"gh pr view 1 -c acme", "gh issue list -t acme", "gh run list -b acme", "gh pr", "gh", "gh repo edit -t acme", "ghx pr comment 1 -b acme"} {
		if summary, problems := post(t, "/w", c); summary != "" || problems != nil {
			t.Errorf("%q: summary %q problems %v, want nothing checked", c, summary, problems)
		}
	}
}

func TestFlagsAreReadInEveryFormGhTakes(t *testing.T) {
	_, problems := post(t, "/w", `gh pr create --body=acme1 -bacme2 -db acme3 --fill -t "a" -- -b acme4`)
	want := []string{"gh pr create --body:1 matches private pattern 1", "gh pr create -b:1 matches private pattern 1",
		"gh pr create -b:1 matches private pattern 1"}
	if !reflect.DeepEqual(problems, want) {
		t.Fatalf("problems %v, want %v", problems, want)
	}
	if summary, problems := post(t, "/w", "gh pr create --title"); summary != "" || problems != nil {
		t.Fatalf("a flag without its value: summary %q problems %v", summary, problems)
	}
}

func TestGhIsFoundInsideSubstitutionsShellsAndEval(t *testing.T) {
	for _, c := range []string{
		`PR=$(gh pr create --title acme --body-file -  <<< fine)`,
		`echo "$(gh issue comment 1 -b acme)"`,
		"x=`gh issue comment 1 -b acme`",
		`bash -lc "gh issue comment 1 --body 'for acme'"`,
		`sh -o pipefail -c 'gh issue comment 1 -b acme'`,
		`eval gh issue comment 1 -b acme`,
		"(cd /w && gh issue comment 1 -b acme)",
		"if true; then gh issue comment 1 -b acme; fi",
		"timeout 30 gh issue comment 1 \\\n  -b acme",
		"true || gh issue comment 1 -b acme 2>&1 >/dev/null &",
	} {
		if _, problems := post(t, "/w", c); len(problems) != 1 || !strings.HasSuffix(problems[0], ":1 matches private pattern 1") {
			t.Errorf("%q: problems %v, want one hit", c, problems)
		}
	}
	for _, c := range []string{"# gh issue comment 1 -b acme", "bash -c", "bash --norc script.sh gh", "sh -e run.sh -c x", `git commit -m "gh issue comment 1 -b acme"`} {
		if summary, problems := post(t, "/w", c); summary != "" || problems != nil {
			t.Errorf("%q: summary %q problems %v, want nothing checked", c, summary, problems)
		}
	}
}

func TestAPathFromHomeIsReadFromThere(t *testing.T) {
	home := t.TempDir()
	write(t, home, "b.md", "acme\n")
	summary, problems, err := checkInTime(t, "acme\n", hookJSON(t, "/w", "gh pr comment 1 -F ~/b.md && cd && gh pr comment 1 -F b.md"), home)
	want := []string{"~/b.md:1 matches private pattern 1", "b.md:1 matches private pattern 1"}
	if err != nil || !reflect.DeepEqual(problems, want) {
		t.Fatalf("summary %q problems %v err %v", summary, problems, err)
	}
	if _, problems, _ := checkInTime(t, "acme\n", hookJSON(t, "/w", "gh pr comment 1 -F ~/b.md"), ""); len(problems) != 1 || !strings.Contains(problems[0], "cannot tell") {
		t.Fatalf("without a home: problems %v", problems)
	}
}

func TestTheEdgesOfAPIMethodsStandardInputAndRedirections(t *testing.T) {
	_, problems := post(t, "/w", "gh api -XGET search/issues -f q=acme; gh api repos/{owner}/{repo}/issues -f body=acme -X\n"+
		"> out.log; gh pr comment 1 -F - <<'EOF' 2>err.log\nacme\nEOF\n")
	want := []string{"gh api -f:1 matches private pattern 1", "gh pr comment stdin:1 matches private pattern 1"}
	if !reflect.DeepEqual(problems, want) {
		t.Fatalf("problems %v, want %v", problems, want)
	}
}

func TestWhatOnlyReadsAFileOrNamesItDoesNotCountAsWritingIt(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "body.md", "acme\n")
	for _, c := range []string{
		"go run ./tools/testgate private body.md && gh pr comment 1 -F body.md",
		"cp body.md /tmp/copy.md && sed 's/a/b/' body.md && gh pr comment 1 -F body.md",
		"cp; ln -s x y; gh pr comment 1 -F body.md 2>&1",
	} {
		if _, problems := post(t, dir, c); !reflect.DeepEqual(problems, []string{"body.md:1 matches private pattern 1"}) {
			t.Errorf("%q: problems %v, want body.md checked", c, problems)
		}
	}
}

func TestStandardInputByAnyOfItsNamesAndARepoBeforeTheSubcommandAreChecked(t *testing.T) {
	for _, c := range []string{
		"gh pr create -t x --body-file /dev/stdin <<'EOF'\nfor acme\nEOF\n",
		"gh api repos/{owner}/{repo}/issues/1/comments --input /dev/fd/0 <<< acme",
		"gh issue comment 1 -F /proc/self/fd/0 <<< acme",
		"gh pr -R owner/repo create --body acme",
		"gh issue --repo owner/repo comment 1 --body acme",
		"gh issue --repo=owner/repo -Rowner/repo comment 1 --body acme",
		"bash <<'EOF'\ngh issue comment 1 --body acme\nEOF\n",
	} {
		if _, problems := post(t, "/w", c); len(problems) != 1 || !strings.HasSuffix(problems[0], ":1 matches private pattern 1") {
			t.Errorf("%q: problems %v, want one hit", c, problems)
		}
	}
	for _, c := range []string{"gh pr -R owner/repo view 1 -b acme", "gh pr -R", "gh issue --repo owner/repo", "bash < script.sh"} {
		if summary, problems := post(t, "/w", c); summary != "" || problems != nil {
			t.Errorf("%q: summary %q problems %v, want nothing checked", c, summary, problems)
		}
	}
}
