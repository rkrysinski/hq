//go:build integration

package update

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/testutil"
)

// Contract against a server that answers as GitHub's public release URLs do.
func TestReleasesLatestAndDownload(t *testing.T) {
	base, dir := testutil.ReleaseServer(t)
	r := Releases{Base: base, Agent: "hq/test"}
	if _, err := r.Latest(); err == nil || !strings.Contains(err.Error(), "no release at") {
		t.Fatalf("no release yet must be an error: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "v0.2.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"latest": "v0.2.0\n", "v0.2.0/hq-linux-amd64": "bin", "v0.2.0/SHA256SUMS": "sums"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if tag, err := r.Latest(); err != nil || tag != "v0.2.0" {
		t.Fatalf("%q %v", tag, err)
	}
	out := t.TempDir()
	if err := r.Download("v0.2.0", out, "hq-linux-amd64", SumsFile); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"hq-linux-amd64": "bin", SumsFile: "sums"} {
		if data, _ := os.ReadFile(filepath.Join(out, name)); string(data) != want {
			t.Fatalf("%s: %q", name, data)
		}
	}
	if err := r.Download("v0.2.0", out, "hq-darwin-arm64"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("a missing asset must be an error: %v", err)
	}
	// A repository that is not there, or private: 404.
	other := strings.TrimSuffix(base, testutil.ReleasePath) + "/someone/else/releases"
	if _, err := (Releases{Base: other}).Latest(); err == nil || !strings.Contains(err.Error(), "no release at") {
		t.Fatalf("a repository that is not there must be an error: %v", err)
	}
	if err := r.Download("v0.2.0", filepath.Join(out, "missing"), SumsFile); err == nil {
		t.Fatal("a directory that is gone must be an error")
	}
	if _, err := (Releases{Base: "http://[::1"}).Latest(); err == nil {
		t.Fatal("a bad URL must be an error")
	}
}

// A server that does not answer holds a lookup up to its timeout, no longer.
func TestReleasesGiveUpOnAServerThatHangs(t *testing.T) {
	hang := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-hang }))
	defer srv.Close()
	defer close(hang)
	r := Releases{Base: srv.URL, LookupTimeout: 200 * time.Millisecond, DownloadTimeout: 200 * time.Millisecond}
	start := time.Now()
	if _, err := r.Latest(); err == nil {
		t.Fatal("a hanging server must be an error")
	}
	if err := r.Download("v1.0.0", t.TempDir(), "x"); err == nil {
		t.Fatal("a hanging download must be an error")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("took %s", d)
	}
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	defer srv2.Close()
	if _, err := (Releases{Base: srv2.URL}).Latest(); err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("a failing server must be an error naming it: %v", err)
	}
}
