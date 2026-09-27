//go:build integration

package platform_test

import (
	"testing"

	"github.com/rkrysinski/hq/internal/platform"
	"github.com/rkrysinski/hq/internal/platform/platformtest"
	"github.com/rkrysinski/hq/internal/proc"
	"github.com/rkrysinski/hq/internal/testutil"
)

// The WSL adapter against a wslpath program (the stub), through proc.Exec.
func TestWSLKeepsTheContractWithWslpath(t *testing.T) {
	testutil.WSLStubs(t)
	platformtest.Contract(t, platform.WSL{Run: proc.Exec{}})
}
