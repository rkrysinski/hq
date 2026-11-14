//go:build integration

package main

import (
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const zero = "0000000000000000000000000000000000000000"

// gitRepo is a repository with a bare remote, origin, that already holds one
// commit naming acme: what a push sends must leave it out.
type gitRepo struct {
	t   *testing.T
	dir string
}

func newGitRepo(t *testing.T) *gitRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := t.TempDir()
	r := &gitRepo{t, filepath.Join(root, "work")}
	r.git(root, "init", "-q", "--bare", "-b", "main", "remote.git")
	r.git(root, "init", "-q", "-b", "main", "work")
	r.git(r.dir, "config", "user.email", "t@example.com")
	r.git(r.dir, "config", "user.name", "t")
	r.git(r.dir, "remote", "add", "origin", filepath.Join(root, "remote.git"))
	r.commit("old.txt", "acme, already public\n", "Old")
	r.git(r.dir, "push", "-q", "origin", "main")
	return r
}

func (r *gitRepo) git(dir string, args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *gitRepo) commit(file, body, msg string) string {
	r.t.Helper()
	write(r.t, r.dir, file, body)
	r.git(r.dir, "add", "-A")
	r.git(r.dir, "commit", "-q", "-m", msg)
	return r.git(r.dir, "rev-parse", "HEAD")
}

func (r *gitRepo) check(list, refs string) (string, []string) {
	r.t.Helper()
	summary, problems, err := checkPush(list, r.dir, strings.NewReader(refs))
	if err != nil {
		r.t.Fatal(err)
	}
	return summary, problems
}

func TestPushChecksEveryCommitItSendsToABranch(t *testing.T) {
	r := newGitRepo(t)
	base := r.git(r.dir, "rev-parse", "HEAD")
	added := r.commit("a.txt", "fine\nacme slipped in\n", "Add a")
	r.commit("a.txt", "fine\n", "Take acme out again")
	tip := r.git(r.dir, "rev-parse", "HEAD")
	summary, problems := r.check("acme\n", "refs/heads/main "+tip+" refs/heads/main "+base+"\n")
	want := []string{added[:7] + " a.txt:2 matches private pattern 1", tip[:7] + " message:1 matches private pattern 1"}
	if summary != "private: 1 patterns, 2 commits, 2 matching lines" || !reflect.DeepEqual(problems, want) {
		t.Fatalf("summary %q problems %v, want %v", summary, problems, want)
	}
}

func TestANewBranchSendsOnlyCommitsNoRemoteHas(t *testing.T) {
	r := newGitRepo(t)
	r.git(r.dir, "checkout", "-q", "-b", "topic")
	tip := r.commit("b.txt", "fine\n", "Add b")
	summary, problems := r.check("acme\n", "refs/heads/topic "+tip+" refs/heads/topic "+zero+"\n")
	if summary != "private: 1 patterns, 1 commits, 0 matching lines" || len(problems) != 0 {
		t.Fatalf("summary %q problems %v", summary, problems)
	}
}

func TestARemoteTipUnknownHereFallsBackToWhatNoRemoteHas(t *testing.T) {
	r := newGitRepo(t)
	tip := r.commit("b.txt", "fine\n", "Add b")
	summary, _ := r.check("acme\n", "refs/heads/main "+tip+" refs/heads/main 1234567890123456789012345678901234567890\n")
	if summary != "private: 1 patterns, 1 commits, 0 matching lines" {
		t.Fatalf("summary %q", summary)
	}
}

func TestTheBranchNameIsCheckedAndADeletionSendsNothing(t *testing.T) {
	r := newGitRepo(t)
	tip := r.git(r.dir, "rev-parse", "HEAD")
	summary, problems := r.check("acme\n",
		"refs/heads/main "+tip+" refs/heads/acme-fix "+zero+"\n"+
			"(delete) "+zero+" refs/heads/old "+tip+"\n")
	want := []string{"ref refs/heads/acme-fix matches private pattern 1"}
	if summary != "private: 1 patterns, 0 commits, 1 matching lines" || !reflect.DeepEqual(problems, want) {
		t.Fatalf("summary %q problems %v", summary, problems)
	}
}

func TestAnAnnotatedTagsMessageIsChecked(t *testing.T) {
	r := newGitRepo(t)
	r.git(r.dir, "tag", "-a", "v1", "-m", "Release for acme")
	tag := r.git(r.dir, "rev-parse", "v1")
	summary, problems := r.check("acme\n", "refs/tags/v1 "+tag+" refs/tags/v1 "+zero+"\n")
	want := []string{tag[:7] + " tag message:1 matches private pattern 1"}
	if summary != "private: 1 patterns, 0 commits, 1 matching lines" || !reflect.DeepEqual(problems, want) {
		t.Fatalf("summary %q problems %v", summary, problems)
	}
}
