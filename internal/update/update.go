// Package update finds, downloads and verifies hq's releases on GitHub and
// replaces the running binary (design §3.9, §7.3).
package update

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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

// GenerationFile holds the generation of a release's hq (version.Generation),
// published beside the binaries and listed in SumsFile (ADR 0020). A release
// from before generations has none.
const GenerationFile = "GENERATION"

// maxGenerationFile bounds what hq reads of a release's GenerationFile: a
// number and a newline.
const maxGenerationFile = 64

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

func (r Releases) fileURL(tag, file string) string {
	return r.Base + "/download/" + url.PathEscape(tag) + "/" + url.PathEscape(file)
}

func (r Releases) download(tag, dir, file string) error {
	u := r.fileURL(tag, file)
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

// Generation returns the generation a release publishes, verified against
// its SumsFile; published is false for a release that publishes none (one
// from before generations), which counts as another generation.
//
// Only a release whose SumsFile is there and does not list GenerationFile
// publishes none: one without its SumsFile, or whose GenerationFile the
// SumsFile lists but that is not there, has files still on their way (the
// release workflow publishes the notes before it attaches the files).
func (r Releases) Generation(tag string) (gen int, published bool, err error) {
	sums, sumsFound, err := r.small(tag, SumsFile, maxSumsFile)
	if err != nil || !sumsFound {
		return generationOf(tag, sums, sumsFound, nil, false, err)
	}
	data, found, err := r.small(tag, GenerationFile, maxGenerationFile)
	return generationOf(tag, sums, true, data, found, err)
}

// generationOf is Generation's answer for release tag from what it read:
// its SumsFile, sums, when found, and its GenerationFile, data, when found;
// err is the error of the reads.
func generationOf(tag string, sums []byte, sumsFound bool, data []byte, found bool, err error) (int, bool, error) {
	switch {
	case err != nil:
		return 0, false, err
	case !sumsFound:
		return 0, false, fmt.Errorf("%s has no %s yet; try again in a minute", tag, SumsFile)
	case !found && Lists(sums, GenerationFile):
		return 0, false, fmt.Errorf("%s has no %s yet, which its %s lists; try again in a minute", tag, GenerationFile, SumsFile)
	case !found:
		return 0, false, nil
	}
	gen, err := ReadGeneration(sums, data)
	return gen, err == nil, err
}

// Lists reports whether a release's SumsFile, sums, has a line for name.
func Lists(sums []byte, name string) bool {
	_, ok := sumOf(sums, name)
	return ok
}

// maxSumsFile bounds what hq reads of a release's SumsFile.
const maxSumsFile = 64 << 10

// small reads a small file of a release, at most limit bytes; found is false
// when the release has no such file.
func (r Releases) small(tag, file string, limit int64) (data []byte, found bool, err error) {
	u := r.fileURL(tag, file)
	resp, err := r.get(u, r.LookupTimeout, LookupTimeout, true)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, false, nil
	default:
		return nil, false, fmt.Errorf("%s: %s", u, resp.Status)
	}
	data, err = io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, false, fmt.Errorf("%s: %v", u, err)
	}
	if int64(len(data)) > limit {
		return nil, false, fmt.Errorf("%s: larger than %d bytes", u, limit)
	}
	return data, true, nil
}

// ReadGeneration reads a release's GenerationFile, data, once its line in
// the release's SumsFile, sums, vouches for it: a number above zero.
func ReadGeneration(sums, data []byte) (int, error) {
	if err := Verify(sums, GenerationFile, data); err != nil {
		return 0, err
	}
	gen, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || gen < 1 {
		return 0, fmt.Errorf("%s is not a generation: %q", GenerationFile, data)
	}
	return gen, nil
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
	want, ok := sumOf(sums, name)
	if !ok {
		return unverified(fmt.Sprintf("%s is not listed in %s", name, SumsFile))
	}
	got := sha256.Sum256(data)
	if hex.EncodeToString(got[:]) != strings.ToLower(want) {
		return unverified("checksum mismatch for " + name)
	}
	return nil
}

// sumOf is the checksum a release's SumsFile, sums, gives name, and whether
// it lists name.
func sumOf(sums []byte, name string) (string, bool) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			return f[0], true
		}
	}
	return "", false
}

// ErrUnverified is what every error of Verify wraps: a file its release's
// SumsFile does not vouch for, which hq refuses to install (exit code 1).
var ErrUnverified = errors.New("not vouched for by " + SumsFile)

// unverified is an error of Verify, saying why.
type unverified string

func (u unverified) Error() string { return string(u) }

func (u unverified) Unwrap() error { return ErrUnverified }

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
