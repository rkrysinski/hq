package journal

import (
	"os"
	"syscall"
)

// KillAfterAppend makes the process kill itself once an ingest has
// appended its events, before it removes their files.
func KillAfterAppend() {
	afterAppend = func() { _ = syscall.Kill(os.Getpid(), syscall.SIGKILL) }
}
