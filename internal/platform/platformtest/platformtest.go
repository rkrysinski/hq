// Package platformtest holds the contract every platform adapter keeps, and
// a fake adapter that keeps it (design §3.10).
package platformtest

import (
	"strings"
	"testing"

	"github.com/rkrysinski/hq/internal/platform"
)

// Contract checks what hq relies on from any platform: an sbx command, and
// paths that survive the trip to sbx and back.
func Contract(t *testing.T, p platform.Platform) {
	t.Helper()
	if p.SbxCommand() == "" {
		t.Error("no sbx command")
	}
	for _, path := range []string{"/home/dev/app", "/Users/dev/work/hq", "/w/repo with space"} {
		s, err := p.ToSbx(path)
		if err != nil || s == "" {
			t.Errorf("ToSbx(%q) = %q, %v", path, s, err)
			continue
		}
		back, err := p.FromSbx(s)
		if err != nil || back != path {
			t.Errorf("FromSbx(ToSbx(%q)) = %q, %v", path, back, err)
		}
	}
}

// Fake is a platform whose sbx sees Windows-like paths (F:\home\dev\app for
// /home/dev/app), so a test shows which paths went through the adapter.
type Fake struct{ Sbx string }

func (f Fake) SbxCommand() string {
	if f.Sbx == "" {
		return "sbx"
	}
	return f.Sbx
}

func (Fake) ToSbx(path string) (string, error) {
	return `F:` + strings.ReplaceAll(path, "/", `\`), nil
}

func (Fake) FromSbx(path string) (string, error) {
	return strings.ReplaceAll(strings.TrimPrefix(path, "F:"), `\`, "/"), nil
}

var _ platform.Platform = Fake{}
