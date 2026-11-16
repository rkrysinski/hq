// Package version holds the version hq was built as.
package version

// Version is the release tag (vX.Y.Z) of a release build, set at build time:
//
//	go build -ldflags "-X github.com/rkrysinski/hq/internal/version.Version=v0.1.0" ./cmd/hq
//
// A local build stays "dev".
var Version = "dev"

// Generation is the generation of this hq (GLOSSARY.md): the form of what
// agents report and of how hq keeps it, which every agent hq starts or
// relaunches carries on its home window and in its record. An agent of
// another generation is an outdated agent, whose state hq cannot read: it
// is listed as outdated and can only be opened or killed. It changes
// exactly when one of the two forms changes: 4 since the hook writes no
// state file and no copy of its latest turn end beside the repository, and
// hq keeps no records on the home windows (#199).
const Generation = 4
