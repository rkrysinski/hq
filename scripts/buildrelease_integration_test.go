//go:build integration

package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/rkrysinski/hq/internal/update"
	"github.com/rkrysinski/hq/internal/version"
)

// The release build publishes the generation of the hq it built beside the
// binaries, and SHA256SUMS covers it, as hq update and install.sh read it
// (ADR 0020).
func TestReleaseBuildPublishesTheGeneration(t *testing.T) {
	out := t.TempDir()
	cmd := exec.Command("sh", "build-release.sh", "v9.9.9", out, runtime.GOOS+"/"+runtime.GOARCH)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, b)
	}
	sums, err := os.ReadFile(filepath.Join(out, update.SumsFile))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(out, update.GenerationFile))
	if err != nil {
		t.Fatal(err)
	}
	if gen, err := update.ReadGeneration(sums, data); err != nil || gen != version.Generation {
		t.Fatalf("generation %d %v, want %d", gen, err, version.Generation)
	}
	asset := "hq-" + runtime.GOOS + "-" + runtime.GOARCH
	bin, err := os.ReadFile(filepath.Join(out, asset))
	if err != nil {
		t.Fatal(err)
	}
	if err := update.Verify(sums, asset, bin); err != nil {
		t.Fatal(err)
	}
}
