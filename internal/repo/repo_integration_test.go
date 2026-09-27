//go:build integration

package repo

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rkrysinski/hq/internal/proc"
	"github.com/rkrysinski/hq/internal/testutil"
)

func TestRootFromSubdirectoryAndWorktree(t *testing.T) {
	r := testutil.GitRepo(t, "app")
	sub := filepath.Join(r, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(r, ".claude", "worktrees", "x")
	testutil.Git(t, r, "worktree", "add", "-q", "-b", "x", wt)
	for _, dir := range []string{r, sub, wt} {
		if root, ok := Root(proc.Exec{}, dir); !ok || root != r {
			t.Errorf("Root(%s) = %q %v, want %q", dir, root, ok, r)
		}
	}
	if _, ok := Root(proc.Exec{}, t.TempDir()); ok {
		t.Error("a plain directory is not a repository")
	}
}

func TestSameResolvesSymlinks(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if !Same(dir, link) || Same(dir, t.TempDir()) {
		t.Fatal("Same")
	}
}
