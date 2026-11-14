//go:build integration

package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// pushRepo is a repository with a bare remote whose pushes run the hooks in
// scripts/git-hooks (#185), with env as the push's environment.
type pushRepo struct {
	t   *testing.T
	dir string
	env []string
}

func newPushRepo(t *testing.T, env ...string) *pushRepo {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not installed")
	}
	hooks, err := filepath.Abs("git-hooks")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	r := &pushRepo{t: t, dir: filepath.Join(root, "work")}
	// Whatever list the machine has, the test sets its own.
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HQ_PRIVATE_PATTERNS=") {
			r.env = append(r.env, kv)
		}
	}
	r.env = append(r.env, env...)
	r.git(root, "init", "-q", "--bare", "-b", "main", "remote.git")
	r.git(root, "init", "-q", "-b", "main", "work")
	for _, kv := range [][2]string{{"user.email", "t@example.com"}, {"user.name", "t"}, {"core.hooksPath", hooks}} {
		r.git(r.dir, "config", kv[0], kv[1])
	}
	r.git(r.dir, "remote", "add", "origin", filepath.Join(root, "remote.git"))
	return r
}

func (r *pushRepo) git(dir string, args ...string) string {
	r.t.Helper()
	out, err := r.run(dir, args...)
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return out
}

func (r *pushRepo) run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = r.env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (r *pushRepo) commit(file, body string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.dir, file), []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}
	r.git(r.dir, "add", "-A")
	r.git(r.dir, "commit", "-q", "-m", "Add "+file)
}

// pushed reports whether the remote's main is the local one.
func (r *pushRepo) pushed() bool {
	remote, _ := r.run(r.dir, "ls-remote", "origin", "refs/heads/main")
	local := r.git(r.dir, "rev-parse", "HEAD")
	return strings.HasPrefix(remote, strings.TrimSpace(local))
}

func TestTheHookLetsACleanPushThrough(t *testing.T) {
	r := newPushRepo(t, "HQ_PRIVATE_PATTERNS=acme")
	r.commit("a.txt", "fine\n")
	out, err := r.run(r.dir, "push", "origin", "main")
	if err != nil || !r.pushed() || !strings.Contains(out, "private: 1 patterns, 1 commits, 0 matching lines") {
		t.Fatalf("err %v pushed %v\n%s", err, r.pushed(), out)
	}
}

func TestTheHookRefusesAPushWithAPrivateDetail(t *testing.T) {
	r := newPushRepo(t, "HQ_PRIVATE_PATTERNS=acme")
	r.commit("a.txt", "for ACME\n")
	out, err := r.run(r.dir, "push", "origin", "main")
	if err == nil || r.pushed() || !strings.Contains(out, "a.txt:1 matches private pattern 1") {
		t.Fatalf("err %v pushed %v\n%s", err, r.pushed(), out)
	}
}

func TestTheHookRefusesAPushWithoutTheList(t *testing.T) {
	r := newPushRepo(t)
	r.commit("a.txt", "fine\n")
	out, err := r.run(r.dir, "push", "origin", "main")
	if err == nil || r.pushed() || !strings.Contains(out, "no pattern list") {
		t.Fatalf("err %v pushed %v\n%s", err, r.pushed(), out)
	}
}

func TestTheHookReadsTheListFromTheRepositorysGitDirectory(t *testing.T) {
	r := newPushRepo(t)
	if err := os.WriteFile(filepath.Join(r.dir, ".git/info/hq-private-patterns"), []byte("acme\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.commit("a.txt", "for acme\n")
	out, err := r.run(r.dir, "push", "origin", "main")
	if err == nil || r.pushed() || !strings.Contains(out, "a.txt:1 matches private pattern 1") {
		t.Fatalf("err %v pushed %v\n%s", err, r.pushed(), out)
	}
}
