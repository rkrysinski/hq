//go:build integration

package iterm

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func mkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestInstallWritesTheProfileOnceWhereItermIs(t *testing.T) {
	for _, where := range []string{"Applications/iTerm.app", "home/Applications/iTerm.app", "home/Library/Application Support/iTerm2"} {
		root := t.TempDir()
		home := filepath.Join(root, "home")
		mkdir(t, filepath.Join(root, where))
		// Another dynamic profile stays as it is.
		other := filepath.Join(filepath.Dir(ProfilePath(home)), "mine.json")
		mkdir(t, filepath.Dir(other))
		if err := os.WriteFile(other, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		if ok, err := Install("darwin", home, root); !ok || err != nil {
			t.Fatalf("%s: %v %v", where, ok, err)
		}
		// Again: nothing to write.
		if ok, err := Install("darwin", home, root); ok || err != nil {
			t.Fatalf("%s again: %v %v", where, ok, err)
		}
		got, err := os.ReadFile(ProfilePath(home))
		if err != nil || !bytes.Equal(got, Profile()) {
			t.Fatalf("%s: profile %q %v", where, got, err)
		}
		es, _ := os.ReadDir(filepath.Dir(other))
		if len(es) != 2 {
			t.Fatalf("%s: %d files, want hq.json and mine.json", where, len(es))
		}
		if b, _ := os.ReadFile(other); string(b) != "{}" {
			t.Fatalf("%s: other profile changed: %q", where, b)
		}
	}
}

func TestInstallRefreshesAnOlderProfile(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	mkdir(t, filepath.Join(home, "Applications", "iTerm.app"))
	mkdir(t, filepath.Dir(ProfilePath(home)))
	if err := os.WriteFile(ProfilePath(home), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, err := Install("darwin", home, root); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if got, _ := os.ReadFile(ProfilePath(home)); !bytes.Equal(got, Profile()) {
		t.Fatalf("profile %q", got)
	}
}

func TestInstallWritesNothingWithoutItermOrOffMacOS(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if ok, err := Install("darwin", home, root); ok || err != nil {
		t.Fatalf("no iTerm2: %v %v", ok, err)
	}
	mkdir(t, filepath.Join(root, "Applications", "iTerm.app"))
	for _, goos := range []string{"linux", "windows"} {
		if ok, err := Install(goos, home, root); ok || err != nil {
			t.Fatalf("%s: %v %v", goos, ok, err)
		}
	}
	if ok, _ := Install("darwin", "", root); ok {
		t.Fatal("no home: wrote a profile")
	}
	if _, err := os.Stat(filepath.Join(home, "Library")); !os.IsNotExist(err) {
		t.Fatalf("wrote under home: %v", err)
	}
}

func TestInstallReportsAnUnwritableProfile(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	mkdir(t, filepath.Join(root, "Applications", "iTerm.app"))
	// A file where the DynamicProfiles directory should be.
	mkdir(t, filepath.Dir(filepath.Dir(ProfilePath(home))))
	if err := os.WriteFile(filepath.Dir(ProfilePath(home)), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, err := Install("darwin", home, root); ok || err == nil {
		t.Fatalf("%v %v, want an error", ok, err)
	}
}
