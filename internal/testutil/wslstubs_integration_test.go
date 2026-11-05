//go:build integration

package testutil

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWindowsStubsNamesEachListedStubThatIsThere(t *testing.T) {
	dir := t.TempDir()
	list := "# comment\n\nwslpath wslpath-stub\ncmd.exe cmd-stub\npowershell.exe powershell-stub\n"
	for name, body := range map[string]string{"wsl-stubs": list, "wslpath-stub": "", "cmd-stub": ""} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got, err := WindowsStubs(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"wslpath": filepath.Join(dir, "wslpath-stub"), "cmd.exe": filepath.Join(dir, "cmd-stub")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestWindowsStubsRefusesALineThatIsNotACommandAndItsStub(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "wsl-stubs"), []byte("wslpath\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := WindowsStubs(dir); err == nil {
		t.Fatal("no error")
	}
}

func TestWSLStubsPutsTheListedStubsOnPath(t *testing.T) {
	WSLStubs(t)
	for _, name := range []string{"sbx.exe", "wslpath", "cmd.exe"} {
		found := false
		for _, d := range filepath.SplitList(os.Getenv("PATH")) {
			if _, err := os.Stat(filepath.Join(d, name)); err == nil {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s not on PATH", name)
		}
	}
}
