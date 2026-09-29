//go:build integration || e2e

package testutil

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ReleasePath is where the stub release server serves releases, as GitHub
// does for the repository.
const ReleasePath = "/rkrysinski/hq/releases"

// releases answers as GitHub's public release URLs do, from dir:
// dir/latest holds the latest tag and dir/<tag>/ the files of that release.
// /latest redirects to /tag/TAG (to the releases page when there is none),
// and /download/TAG/FILE redirects once more, as GitHub does to its storage,
// before the file.
func releases(dir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rest, ok := strings.CutPrefix(r.URL.Path, ReleasePath)
		if !ok {
			http.NotFound(w, r)
			return
		}
		latest := func() string {
			b, _ := os.ReadFile(filepath.Join(dir, "latest"))
			return strings.TrimSpace(string(b))
		}
		switch {
		case rest == "/latest":
			if tag := latest(); tag != "" {
				http.Redirect(w, r, ReleasePath+"/tag/"+tag, http.StatusFound)
			} else {
				http.Redirect(w, r, ReleasePath, http.StatusFound)
			}
		case strings.HasPrefix(rest, "/download/"):
			http.Redirect(w, r, "/storage/"+strings.TrimPrefix(rest, "/download/"), http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	})
}

// storage serves the release files the download URLs redirect to.
func storage(dir string) http.Handler {
	return http.StripPrefix("/storage/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Join(dir, filepath.FromSlash(filepath.Clean("/"+r.URL.Path)))
		if fi, err := os.Stat(p); err != nil || fi.IsDir() {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, p)
	}))
}

// ReleaseMux serves the release URLs of the releases in dir and the storage
// they redirect to (the QA stub server serves it too).
func ReleaseMux(dir string) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/storage/", storage(dir))
	mux.Handle("/", releases(dir))
	return mux
}

// ReleaseServer starts a stub release server for this test and returns the
// base URL to give hq and install.sh (HQ_RELEASES_URL) and its release
// directory.
func ReleaseServer(t *testing.T) (base, dir string) {
	t.Helper()
	dir = t.TempDir()
	srv := httptest.NewServer(ReleaseMux(dir))
	t.Cleanup(srv.Close)
	return srv.URL + ReleasePath, dir
}
