// Package update finds, downloads and verifies hq's releases on GitHub and
// replaces the running binary (design §3.9, §7.3).
package update

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DefaultBase is where hq's releases are published: GitHub's public release
// URLs, read without a login (design §3.9).
const DefaultBase = "https://github.com/rkrysinski/hq/releases"

// BaseEnv names another place to read releases from, one that answers as
// GitHub's release URLs do; for tests and QA.
const BaseEnv = "HQ_RELEASES_URL"

// Base is the release URL hq reads: $HQ_RELEASES_URL, else DefaultBase.
func Base(getenv func(string) string) string {
	if b := strings.TrimRight(getenv(BaseEnv), "/"); b != "" {
		return b
	}
	return DefaultBase
}

// SumsFile lists the SHA-256 of every file of a release.
const SumsFile = "SHA256SUMS"

// Timeouts of Releases: a lookup is one small answer, a download a binary of
// some megabytes.
const (
	LookupTimeout   = 15 * time.Second
	DownloadTimeout = 5 * time.Minute
)

// Releases reads hq's releases over HTTPS from their public URLs, as
// install.sh does: Base/latest redirects to Base/tag/TAG, and a release's
// files are at Base/download/TAG/FILE.
type Releases struct {
	Base string
	// Agent is the User-Agent sent, e.g. hq/v0.2.0.
	Agent string
	// LookupTimeout and DownloadTimeout bound a request; zero takes the
	// defaults above.
	LookupTimeout, DownloadTimeout time.Duration
}

func (r Releases) get(url string, timeout, fallback time.Duration, follow bool) (*http.Response, error) {
	if timeout == 0 {
		timeout = fallback
	}
	c := &http.Client{Timeout: timeout}
	if !follow {
		c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if r.Agent != "" {
		req.Header.Set("User-Agent", r.Agent)
	}
	return c.Do(req)
}

// Latest returns the tag of the latest release, from where Base/latest
// redirects.
func (r Releases) Latest() (string, error) {
	resp, err := r.get(r.Base+"/latest", r.LookupTimeout, LookupTimeout, false)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	if tag, ok := TagFrom(resp.Header.Get("Location")); ok && isRedirect(resp.StatusCode) {
		return tag, nil
	}
	if resp.StatusCode == http.StatusNotFound || isRedirect(resp.StatusCode) {
		return "", fmt.Errorf("no release at %s", r.Base)
	}
	return "", fmt.Errorf("%s/latest: %s", r.Base, resp.Status)
}

func isRedirect(code int) bool { return code >= 300 && code < 400 }

// TagFrom reads the tag in the address a latest-release lookup redirects to
// (.../releases/tag/v1.2.3); a repository without releases redirects to its
// releases page instead.
func TagFrom(location string) (string, bool) {
	i := strings.LastIndex(location, "/tag/")
	if i < 0 {
		return "", false
	}
	tag, err := url.PathUnescape(location[i+len("/tag/"):])
	if err != nil || tag == "" || strings.ContainsAny(tag, "/?#") {
		return "", false
	}
	return tag, true
}

// Download saves the named files of a release into dir.
func (r Releases) Download(tag, dir string, files ...string) error {
	for _, f := range files {
		if err := r.download(tag, dir, f); err != nil {
			return err
		}
	}
	return nil
}

func (r Releases) download(tag, dir, file string) error {
	u := r.Base + "/download/" + url.PathEscape(tag) + "/" + url.PathEscape(file)
	resp, err := r.get(u, r.DownloadTimeout, DownloadTimeout, true)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", u, resp.Status)
	}
	out, err := os.Create(filepath.Join(dir, file))
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		return fmt.Errorf("%s: %v", u, err)
	}
	return out.Close()
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
