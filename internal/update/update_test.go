package update

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

// sumsOf is a SHA256SUMS line for each file, as the release build writes it.
func sumsOf(files map[string]string) []byte {
	var b strings.Builder
	for name, content := range files {
		sum := sha256.Sum256([]byte(content))
		b.WriteString(hex.EncodeToString(sum[:]) + "  " + name + "\n")
	}
	return []byte(b.String())
}

// A release's generation counts only once its checksum vouches for it, and
// only as a number above zero (ADR 0020).
func TestReadGeneration(t *testing.T) {
	for want, data := range map[int]string{4: "4\n", 1: "1\n"} {
		if gen, err := ReadGeneration(sumsOf(map[string]string{GenerationFile: data}), []byte(data)); err != nil || gen != want {
			t.Fatalf("%q: %d %v", data, gen, err)
		}
	}
	for name, tc := range map[string]struct{ sums, data string }{
		"tampered":     {"4\n", "5\n"},
		"not listed":   {"", "4\n"},
		"not a number": {"four\n", "four\n"},
		"zero":         {"0\n", "0\n"},
		"negative":     {"-1\n", "-1\n"},
	} {
		sums := sumsOf(map[string]string{"hq-linux-amd64": "bin"})
		if tc.sums != "" {
			sums = sumsOf(map[string]string{GenerationFile: tc.sums})
		}
		if gen, err := ReadGeneration(sums, []byte(tc.data)); err == nil {
			t.Errorf("%s: generation %d accepted", name, gen)
		}
	}
}

// A release publishes no generation only when its SHA256SUMS is there and
// does not list GENERATION (a release from before generations). One whose
// files are not attached yet, its SHA256SUMS missing or listing a
// GENERATION that is not there, is an error to try again, not a release of
// another generation (#200); a read that failed stays its error.
func TestTheGenerationOfAReleaseWhoseFilesAreOnTheirWay(t *testing.T) {
	listing := sumsOf(map[string]string{GenerationFile: "4\n", "hq-linux-amd64": "bin"})
	before := sumsOf(map[string]string{"hq-linux-amd64": "bin"})
	if gen, published, err := generationOf("v1.0.0", listing, true, []byte("4\n"), true, nil); err != nil || !published || gen != 4 {
		t.Errorf("published: %d %v %v", gen, published, err)
	}
	if gen, published, err := generationOf("v0.1.0", before, true, nil, false, nil); err != nil || published || gen != 0 {
		t.Errorf("before generations: %d %v %v", gen, published, err)
	}
	for name, tc := range map[string]struct {
		sums      []byte
		sumsFound bool
		want      string
	}{
		"no checksums yet":  {nil, false, "v1.0.0 has no SHA256SUMS yet; try again in a minute"},
		"no generation yet": {listing, true, "v1.0.0 has no GENERATION yet, which its SHA256SUMS lists; try again in a minute"},
	} {
		if _, published, err := generationOf("v1.0.0", tc.sums, tc.sumsFound, nil, false, nil); err == nil || published || err.Error() != tc.want {
			t.Errorf("%s: %v %v", name, published, err)
		}
	}
	if _, _, err := generationOf("v1.0.0", nil, false, nil, false, errors.New("timeout")); err == nil || err.Error() != "timeout" {
		t.Errorf("a failed read: %v", err)
	}
	if !Lists(listing, GenerationFile) || Lists(before, GenerationFile) {
		t.Error("Lists")
	}
}

// Every error of Verify is one hq refuses an update for (exit code 1).
func TestVerifysErrorsAreUnverified(t *testing.T) {
	sums := sumsOf(map[string]string{"hq-linux-amd64": "bin"})
	for name, err := range map[string]error{
		"a mismatch": Verify(sums, "hq-linux-amd64", []byte("tampered")),
		"not listed": Verify(sums, GenerationFile, []byte("4\n")),
	} {
		if !errors.Is(err, ErrUnverified) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := ReadGeneration(sums, []byte("4\n")); !errors.Is(err, ErrUnverified) {
		t.Errorf("ReadGeneration: %v", err)
	}
}
