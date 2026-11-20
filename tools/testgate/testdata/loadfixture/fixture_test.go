// Package loadfixture holds the tests the integration tests of testgate load
// run (tools/testgate/load_integration_test.go): one that passes, one that
// fails, one that skips, one that ends its process, and one that waits.
package loadfixture

import (
	"os"
	"strconv"
	"testing"
	"time"
)

func TestPasses(t *testing.T) {}

func TestFails(t *testing.T) { t.Fatal("fails as asked") }

func TestSkips(t *testing.T) { t.Skip("skips as asked") }

// TestSub has a subtest that passes and one that skips.
func TestSub(t *testing.T) {
	t.Run("passes", func(t *testing.T) {})
	t.Run("skips", func(t *testing.T) { t.Skip("skips as asked") })
}

// TestExits ends its process in its first run.
func TestExits(t *testing.T) { os.Exit(3) }

// TestWaits writes its process id to the file $LOAD_FIXTURE_STARTED names,
// then waits a minute, until the load run stops it.
func TestWaits(t *testing.T) {
	file := os.Getenv("LOAD_FIXTURE_STARTED")
	if file == "" {
		t.Skip("run by the tests of testgate load only")
	}
	if err := os.WriteFile(file, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Minute)
}
