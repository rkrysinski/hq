// Package version holds the version hq was built as.
package version

// Version is the release tag (vX.Y.Z) of a release build, set at build time:
//
//	go build -ldflags "-X github.com/rkrysinski/hq/internal/version.Version=v0.1.0" ./cmd/hq
//
// A local build stays "dev".
var Version = "dev"
