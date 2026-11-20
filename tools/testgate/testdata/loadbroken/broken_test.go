// Package loadbroken does not build: testgate load refuses it before any
// load (tools/testgate/load_integration_test.go).
package loadbroken

import "testing"

func TestDoesNotBuild(t *testing.T) {
	var n int = "not a number"
	_ = n
}
