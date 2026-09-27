package platform_test

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/rkrysinski/hq/internal/platform"
	"github.com/rkrysinski/hq/internal/platform/platformtest"
)

// fakeWslpath answers like wslpath on a distribution named Ubuntu: Windows
// drives are under /mnt, the rest of Linux under \\wsl.localhost\Ubuntu.
type fakeWslpath struct {
	calls [][]string
	err   error
	out   string // when set, the answer to every call
}

func (f *fakeWslpath) Run(name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	if f.err != nil {
		return nil, f.err
	}
	if f.out != "" || len(args) != 2 {
		return []byte(f.out), nil
	}
	const unc = `\\wsl.localhost\Ubuntu`
	p := args[1]
	switch args[0] {
	case "-w":
		if rest, ok := strings.CutPrefix(p, "/mnt/c/"); ok {
			return []byte(`C:\` + strings.ReplaceAll(rest, "/", `\`) + "\n"), nil
		}
		return []byte(unc + strings.ReplaceAll(p, "/", `\`) + "\n"), nil
	case "-u":
		if rest, ok := strings.CutPrefix(p, `C:\`); ok {
			return []byte("/mnt/c/" + strings.ReplaceAll(rest, `\`, "/") + "\n"), nil
		}
		return []byte(strings.ReplaceAll(strings.TrimPrefix(p, unc), `\`, "/") + "\n"), nil
	}
	return nil, errors.New("wslpath: bad flag")
}

func TestNativeKeepsTheContract(t *testing.T) { platformtest.Contract(t, platform.Native{}) }

func TestWSLKeepsTheContract(t *testing.T) {
	platformtest.Contract(t, platform.WSL{Run: &fakeWslpath{}})
}

func TestNotificationSequencePerPlatform(t *testing.T) {
	for p, want := range map[platform.Platform]string{platform.Native{}: "\x1b]9;%s\a", platform.WSL{}: "\a"} {
		var got string
		if err := json.Unmarshal([]byte(`"`+p.NotifySequence()+`"`), &got); err != nil || got != want {
			t.Errorf("%T: %q %v, want %q", p, got, err, want)
		}
	}
}

func TestFakeKeepsTheContract(t *testing.T) { platformtest.Contract(t, platformtest.Fake{}) }

func TestNativeRunsSbxWithPathsUnchanged(t *testing.T) {
	p := platform.Native{}
	if p.SbxCommand() != "sbx" {
		t.Fatalf("sbx command %q", p.SbxCommand())
	}
	if s, _ := p.ToSbx("/Users/dev/hq"); s != "/Users/dev/hq" {
		t.Fatalf("ToSbx %q", s)
	}
}

func TestWSLRunsSbxExeAndMapsPathsWithWslpath(t *testing.T) {
	w := &fakeWslpath{}
	p := platform.WSL{Run: w}
	if p.SbxCommand() != "sbx.exe" {
		t.Fatalf("sbx command %q", p.SbxCommand())
	}
	if s, err := p.ToSbx("/home/dev/app"); err != nil || s != `\\wsl.localhost\Ubuntu\home\dev\app` {
		t.Fatalf("ToSbx %q %v", s, err)
	}
	if s, err := p.FromSbx(`C:\Users\dev\app`); err != nil || s != "/mnt/c/Users/dev/app" {
		t.Fatalf("FromSbx %q %v", s, err)
	}
	want := [][]string{{"wslpath", "-w", "/home/dev/app"}, {"wslpath", "-u", `C:\Users\dev\app`}}
	if !reflect.DeepEqual(w.calls, want) {
		t.Fatalf("calls %q", w.calls)
	}
}

func TestWSLReportsWslpathFailures(t *testing.T) {
	if _, err := (platform.WSL{Run: &fakeWslpath{err: errors.New("wslpath: not found")}}).ToSbx("/a"); err == nil {
		t.Fatal("want the wslpath error")
	}
	if _, err := (platform.WSL{Run: &fakeWslpath{out: "\r\n"}}).FromSbx(`C:\a`); err == nil {
		t.Fatal("want an error for an empty answer")
	}
}

func TestDetectFindsWSLFromTheEnvironmentOrTheKernel(t *testing.T) {
	noFile := func(string) ([]byte, error) { return nil, os.ErrNotExist }
	kernel := func(release string) func(string) ([]byte, error) {
		return func(path string) ([]byte, error) {
			if path != "/proc/sys/kernel/osrelease" {
				t.Fatalf("read %s", path)
			}
			return []byte(release), nil
		}
	}
	env := func(distro string) func(string) string {
		return func(k string) string {
			if k == "WSL_DISTRO_NAME" {
				return distro
			}
			return ""
		}
	}
	for _, tc := range []struct {
		name     string
		getenv   func(string) string
		readFile func(string) ([]byte, error)
		wsl      bool
	}{
		{"WSL_DISTRO_NAME set", env("Ubuntu"), noFile, true},
		{"WSL 2 kernel", env(""), kernel("5.15.167.4-microsoft-standard-WSL2\n"), true},
		{"WSL 1 kernel", env(""), kernel("4.4.0-19041-Microsoft\n"), true},
		{"Linux kernel", env(""), kernel("6.8.0-45-generic\n"), false},
		{"macOS, no /proc", env(""), noFile, false},
	} {
		p := platform.Detect(tc.getenv, tc.readFile, &fakeWslpath{})
		if _, wsl := p.(platform.WSL); wsl != tc.wsl {
			t.Errorf("%s: got %T", tc.name, p)
		}
	}
}

func TestBrowserIsGhOpeningThePullRequest(t *testing.T) {
	url := "https://github.com/o/r/pull/7"
	for _, tc := range []struct {
		p    platform.Platform
		want string
	}{
		{platform.Native{}, "gh pr view --web " + url},
		{platform.WSL{}, "env BROWSER=explorer.exe gh pr view --web " + url},
		{platform.WSL{BrowserSet: true}, "gh pr view --web " + url},
	} {
		if got := strings.Join(tc.p.Browser(url), " "); got != tc.want {
			t.Errorf("%T: %q, want %q", tc.p, got, tc.want)
		}
	}
	wsl := platform.Detect(func(k string) string { return map[string]string{"WSL_DISTRO_NAME": "U", "BROWSER": "wslview"}[k] }, nil, nil)
	if !wsl.(platform.WSL).BrowserSet {
		t.Error("BROWSER not seen")
	}
}

func TestRaiseFindsTheWindowPerPlatform(t *testing.T) {
	// macOS: iTerm2's session on the client's terminal, never starting
	// iTerm2.
	r := platform.Native{}.Raise("/dev/ttys004")
	if len(r) != 4 || r[0] != "osascript" || r[1] != "-e" || r[3] != "/dev/ttys004" {
		t.Fatalf("native %q", r)
	}
	for _, want := range []string{"is not running then error", "tty of s is t", "activate"} {
		if !strings.Contains(r[2], want) {
			t.Errorf("the script has no %q:\n%s", want, r[2])
		}
	}
	// WSL: the window titled hq - agents; a refusal is an error.
	w := platform.WSL{}.Raise("/dev/pts/3")
	if w[0] != "powershell.exe" || !strings.Contains(w[len(w)-1], "AppActivate('hq - agents')") || !strings.Contains(w[len(w)-1], "exit 1") {
		t.Fatalf("wsl %q", w)
	}
}
