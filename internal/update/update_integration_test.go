//go:build integration

package update

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rkrysinski/hq/internal/proc"
	"github.com/rkrysinski/hq/internal/testutil"
)

// Contract against the stub, which answers like gh 2.x.
func TestReleasesLatestAndDownload(t *testing.T) {
	bin, dir := testutil.GhStub(t)
	r := Releases{Run: proc.Exec{}, Bin: bin}
	if _, err := r.Latest(); err == nil {
		t.Fatal("no release yet must be an error")
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
	if data, _ := os.ReadFile(filepath.Join(out, "hq-linux-amd64")); string(data) != "bin" {
		t.Fatalf("%q", data)
	}
	if err := r.Download("v0.2.0", out, "hq-darwin-arm64"); err == nil {
		t.Fatal("a missing asset must be an error")
	}
}
