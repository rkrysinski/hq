// Package repo resolves a directory to its repository.
package repo

import (
	"path/filepath"
	"strings"

	"github.com/rkrysinski/hq/internal/proc"
)

// Root returns the main repository's root for any directory inside it,
// including a subdirectory or one of Claude's worktrees: the parent of git's
// common directory. ok is false when dir is not in a git repository.
func Root(r proc.Runner, dir string) (root string, ok bool) {
	out, err := r.Run("git", "-C", dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", false
	}
	common := strings.TrimSpace(string(out))
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common), true
	}
	// A bare repository or an unusual layout: fall back to the work tree.
	out, err = r.Run("git", "-C", dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// Top returns the top of the work tree dir is in: a worktree's own root,
// or the main checkout's. ok is false when dir is not in a git repository.
func Top(r proc.Runner, dir string) (top string, ok bool) {
	out, err := r.Run("git", "-C", dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// Same reports whether two paths name the same directory, resolving symlinks
// (e.g. /tmp and /private/tmp on macOS).
func Same(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
}
