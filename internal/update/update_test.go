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

func TestTagFromTheLatestReleaseRedirect(t *testing.T) {
	for loc, want := range map[string]string{
		"https://github.com/rkrysinski/hq/releases/tag/v0.2.0": "v0.2.0",
		"/rkrysinski/hq/releases/tag/v1.10.3":                  "v1.10.3",
		"https://github.com/o/r/releases/tag/v1%2E0%2E0":       "v1.0.0",
		"https://github.com/rkrysinski/hq/releases":            "",
		"https://github.com/o/r/releases/tag/":                 "",
		"https://github.com/o/r/releases/tag/v1/x":             "",
		"https://github.com/o/r/releases/tag/%zz":              "",
		"": "", // no redirect
	} {
		got, ok := TagFrom(loc)
		if got != want || ok != (want != "") {
			t.Errorf("TagFrom(%q) = %q %v", loc, got, ok)
		}
	}
}

func TestBase(t *testing.T) {
	env := map[string]string{}
	getenv := func(k string) string { return env[k] }
	if Base(getenv) != "https://github.com/rkrysinski/hq/releases" {
		t.Fatal(Base(getenv))
	}
	env[BaseEnv] = "http://127.0.0.1:8080/o/r/releases/"
	if Base(getenv) != "http://127.0.0.1:8080/o/r/releases" {
		t.Fatal(Base(getenv))
	}
}
