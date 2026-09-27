package update

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewer(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"v0.2.0", "v0.1.0", true},
		{"v0.10.0", "v0.9.9", true},
		{"v1.0.0", "v0.99.99", true},
		{"v0.1.0", "v0.1.0", false},
		{"v0.1.0", "v0.2.0", false},
		{"v0.1.0", "dev", true},
		{"", "v0.1.0", false},
		{"dev", "v0.1.0", false},
		{"v1.2", "v0.1.0", false},
		{"v1.x.0", "v0.1.0", false},
	} {
		if got := Newer(tc.a, tc.b); got != tc.want {
			t.Errorf("Newer(%q, %q) = %v", tc.a, tc.b, got)
		}
	}
	if IsRelease("dev") || !IsRelease("v1.2.3") {
		t.Fatal("IsRelease")
	}
}

func TestVerify(t *testing.T) {
	// sha256("new")
	sums := []byte("11507a0e2f5e69d5dfa40a62a1bd7b6ee57e6bcd85c67c9b8431b36fff21c437  hq-linux-amd64\n" +
		"0000000000000000000000000000000000000000000000000000000000000000 *install.sh\n")
	if err := Verify(sums, "hq-linux-amd64", []byte("new")); err != nil {
		t.Fatal(err)
	}
	if err := Verify(sums, "hq-linux-amd64", []byte("tampered")); err == nil {
		t.Fatal("tampered binary accepted")
	}
	if err := Verify(sums, "install.sh", []byte("x")); err == nil {
		t.Fatal("wrong sum accepted (binary-mode line)")
	}
	if err := Verify(sums, "hq-darwin-arm64", []byte("new")); err == nil {
		t.Fatal("unlisted file accepted")
	}
}

func TestReplaceSwapsTheFileInOneStep(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hq")
	if err := os.WriteFile(path, []byte("old"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Replace(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	fi, _ := os.Stat(path)
	entries, _ := os.ReadDir(dir)
	if string(data) != "new" || fi.Mode().Perm() != 0o755 || len(entries) != 1 {
		t.Fatalf("%q %v %d files", data, fi.Mode(), len(entries))
	}
	if err := Replace(filepath.Join(dir, "missing", "hq"), []byte("x")); err == nil {
		t.Fatal("replacing in a missing directory must fail")
	}
}

func TestAsset(t *testing.T) {
	if Asset("darwin", "arm64") != "hq-darwin-arm64" {
		t.Fatal(Asset("darwin", "arm64"))
	}
}
