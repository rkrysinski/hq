//go:build integration

package sbx

import (
	"testing"

	"github.com/rkrysinski/hq/internal/proc"
	"github.com/rkrysinski/hq/internal/testutil"
)

// Contract against the stub, which mirrors the real sbx's JSON (sbx v0.45).
func TestCreateThenListFindsSandboxByWorkspace(t *testing.T) {
	bin, _ := testutil.SbxStub(t)
	c := Client{Run: proc.Exec{}, Bin: bin}
	if all, err := c.List(); err != nil || len(all) != 0 {
		t.Fatalf("%v %v", all, err)
	}
	if err := c.Create("/w/app"); err != nil {
		t.Fatal(err)
	}
	all, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	s, ok := ByWorkspace(all, "/w/app", func(a, b string) bool { return a == b })
	if !ok || s.Name != "claude-app" || s.Running() {
		t.Fatalf("%+v %v", s, ok)
	}
}
