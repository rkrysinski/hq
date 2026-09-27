// Package update finds, downloads and verifies hq's releases on GitHub and
// replaces the running binary (design §3.9, §7.3).
package update

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rkrysinski/hq/internal/proc"
)

// Repo is where hq's releases are published.
const Repo = "rkrysinski/hq"

// SumsFile lists the SHA-256 of every file of a release.
const SumsFile = "SHA256SUMS"

// Releases reads hq's releases through the GitHub CLI, which works on the
// private repository with the user's own login.
type Releases struct {
	Run proc.Runner
	Bin string // gh
}

// Latest returns the tag of the latest release.
func (r Releases) Latest() (string, error) {
	out, err := r.Run.Run(r.Bin, "release", "view", "-R", Repo, "--json", "tagName", "-q", ".tagName")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Download saves the named files of a release into dir.
func (r Releases) Download(tag, dir string, files ...string) error {
	args := []string{"release", "download", tag, "-R", Repo, "-D", dir}
	for _, f := range files {
		args = append(args, "-p", f)
	}
	_, err := r.Run.Run(r.Bin, args...)
	return err
}

// Asset is the name of the binary for a platform in a release.
func Asset(goos, goarch string) string { return "hq-" + goos + "-" + goarch }

// parse reads vX.Y.Z; ok is false for anything else, such as a dev build.
func parse(v string) (n [3]int, ok bool) {
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if !strings.HasPrefix(v, "v") || len(parts) != 3 {
		return n, false
	}
	for i, p := range parts {
		x, err := strconv.Atoi(p)
		if err != nil || x < 0 {
			return n, false
		}
		n[i] = x
	}
	return n, true
}

// IsRelease reports whether v is a release version (vX.Y.Z).
func IsRelease(v string) bool {
	_, ok := parse(v)
	return ok
}

// Newer reports whether release a is newer than version b. A version that is
// not a release (a dev build) is older than every release.
func Newer(a, b string) bool {
	na, ok := parse(a)
	if !ok {
		return false
	}
	nb, ok := parse(b)
	if !ok {
		return true
	}
	for i := range na {
		if na[i] != nb[i] {
			return na[i] > nb[i]
		}
	}
	return false
}

// Verify checks data against the line for name in a SHA256SUMS file.
func Verify(sums []byte, name string, data []byte) error {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			got := sha256.Sum256(data)
			if hex.EncodeToString(got[:]) != strings.ToLower(f[0]) {
				return fmt.Errorf("checksum mismatch for %s", name)
			}
			return nil
		}
	}
	return fmt.Errorf("%s is not listed in %s", name, SumsFile)
}

// Replace puts data in place of the executable at path in one step: it is
// written next to it and renamed over it, so hq is never half-written.
func Replace(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".hq-update-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
