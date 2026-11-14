//go:build integration

package scripts_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stubGh stands in for GitHub: it answers the reads scripts/release.sh makes
// from files in $STUB_DIR (pulls.json, issue-<n>.json), has no release, no
// milestone and no comment yet, and logs every write to $STUB_DIR/writes, a
// release's notes to $STUB_DIR/notes.
const stubGh = `#!/bin/sh
d=$STUB_DIR
case "$1 $2" in
"api repos/o/r") exit 0 ;;
"api repos/o/r/pulls?state=closed&per_page=100") cat "$d/pulls.json"; exit 0 ;;
"issue view") cat "$d/issue-$3.json" 2>/dev/null; exit $? ;;
"release view") exit 1 ;;
"release create")
	while [ $# -gt 0 ]; do [ "$1" = --notes-file ] && cp "$2" "$d/notes"; shift; done
	echo "release create" >> "$d/writes"; exit 0 ;;
"issue comment") echo "comment #$3: $7" >> "$d/writes"; exit 0 ;;
esac
case "$*" in
"api repos/o/r/milestones?state=all"*) exit 0 ;;
"api repos/o/r/milestones -f "*) echo "milestone: $4 / $6" >> "$d/writes"; echo 7; exit 0 ;;
"api repos/o/r/milestones/7 -q"*) echo open; exit 0 ;;
"api repos/o/r/milestones/7 -X PATCH"*) echo "milestone closed" >> "$d/writes"; exit 0 ;;
*" -X PATCH -F milestone=7") exit 0 ;;
"api repos/o/r/issues/"*"/comments"*) exit 0 ;;
"api repos/o/r/issues/"*) exit 0 ;;
esac
echo "stub gh: unexpected call: $*" >&2
exit 3
`

// pull is a merged pull request as the GitHub API lists it.
func pull(number int, title, body, mergeSha string) string {
	return fmt.Sprintf(`{"number":%d,"title":%q,"body":%q,"merged_at":"2026-11-14T10:00:00Z","merge_commit_sha":%q,"head":{"ref":"issue-%d"}}`,
		number, title, body, mergeSha, number)
}

func issue(number int, title string, labels ...string) string {
	names := make([]string, len(labels))
	for i, l := range labels {
		names[i] = fmt.Sprintf(`{"name":%q}`, l)
	}
	return fmt.Sprintf(`{"number":%d,"title":%q,"labels":[%s]}`, number, title, strings.Join(names, ","))
}

func TestReleaseWritesItsNotesMilestoneAndCommentsInEnglish(t *testing.T) {
	root := repoRoot(t)
	repo := t.TempDir()
	for _, f := range []string{"scripts/release.sh", "scripts/version.sh", "scripts/lib/release-notes.sh", "scripts/lib/repo-from-url.sh"} {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(repo, f), string(b), 0o755)
	}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	shas := make([]string, 4)
	for i := range shas {
		git("commit", "-q", "--allow-empty", "-m", fmt.Sprintf("change %d", i))
		shas[i] = git("rev-parse", "HEAD")
	}
	git("tag", "v1.0.0")

	stub := t.TempDir()
	writeFile(t, filepath.Join(stub, "gh"), stubGh, 0o755)
	writeFile(t, filepath.Join(stub, "pulls.json"), "["+strings.Join([]string{
		pull(10, "Show the queue", "Closes #1\n\n## Before deployment\n\n- [ ] Grant the token.\n\n## Rollback\n\n- Revoke the token.\n", shas[0]),
		pull(11, "Keep the list sorted", "Closes #2\n\n## After deployment\n\n- [ ] Restart hq.\n\n## Rollback\n\nNone: one-way door\n", shas[1]),
		pull(12, "Name the agent", "Closes #3\n", shas[2]),
		pull(13, "Tidy the scripts", "No issue.\n", shas[3]),
	}, ",")+"]", 0o644)
	writeFile(t, filepath.Join(stub, "issue-1.json"), issue(1, "A queue view", "enhancement"), 0o644)
	writeFile(t, filepath.Join(stub, "issue-2.json"), issue(2, "The list jumps", "bug"), 0o644)
	writeFile(t, filepath.Join(stub, "issue-3.json"), issue(3, "Agent names"), 0o644)

	cmd := exec.Command(filepath.Join(repo, "scripts", "release.sh"), "v1.0.0", "--repo", "o/r")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "PATH="+stub+string(os.PathListSeparator)+os.Getenv("PATH"), "STUB_DIR="+stub)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("release.sh: %v\n%s", err, out)
	}

	notes, err := os.ReadFile(filepath.Join(stub, "notes"))
	if err != nil {
		t.Fatal(err)
	}
	wantNotes := `<!-- generated -->

<a name="new-features"></a>
## New features

- A queue view (#1)

<a name="fixes"></a>
## Fixes

- The list jumps (#2)

<a name="other"></a>
## Other

- Agent names (#3)

<a name="internal"></a>
## Internal

- Tidy the scripts (#13)

<a name="deployment"></a>
## Deployment

<a name="before-deployment"></a>
### Before deployment

To do before you deploy this version:

**Show the queue (#10)**

- [ ] Grant the token.

<a name="command"></a>

<a name="after-deployment"></a>
### After deployment

To do after deploying this version:

**Keep the list sorted (#11)**

- [ ] Restart hq.

<!-- /generated -->
`
	if string(notes) != wantNotes {
		t.Errorf("notes:\n%s\nwant:\n%s", notes, wantNotes)
	}

	writes, err := os.ReadFile(filepath.Join(stub, "writes"))
	if err != nil {
		t.Fatal(err)
	}
	wantWrites := strings.Join([]string{
		"release create",
		"milestone: title=v1.0.0 / description=Release v1.0.0",
		"comment #1: Released in v1.0.0 - https://github.com/o/r/releases/tag/v1.0.0",
		"comment #2: Released in v1.0.0 - https://github.com/o/r/releases/tag/v1.0.0",
		"comment #3: Released in v1.0.0 - https://github.com/o/r/releases/tag/v1.0.0",
		"milestone closed",
	}, "\n") + "\n"
	if string(writes) != wantWrites {
		t.Errorf("writes:\n%s\nwant:\n%s", writes, wantWrites)
	}
}
