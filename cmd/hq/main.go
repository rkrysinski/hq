// Command hq is one console for many Claude Code agents running in Docker
// Sandboxes and tmux. See docs/requirements/spec.md and docs/design/hq.md.
package main

import (
	"os"

	"github.com/rkrysinski/hq/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], cli.Env{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}))
}
