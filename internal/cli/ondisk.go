package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rkrysinski/hq/internal/version"
)

// hqUpdated is what every tool call of an hq mcp answers once the hq on
// disk is of another generation (spec, Phase 2; design §3.12).
const hqUpdated = "hq was updated: restart hq in this client"

// hqUpdatedInSandbox is that line for hq mcp sbx: sbx's MCP gateway
// started it on the host for a sandbox, where restarting Claude Code does
// not replace it, and hq sandbox restart does (#249). The gateway tells
// the server nothing of the sandbox it serves (it sets no environment, and
// the client is the gateway), so the line cannot name it: it names the
// repository, and --setup for a sandbox of a setup, whose REPO alone would
// be the repository's default sandbox (spec §4.1).
const hqUpdatedInSandbox = "hq was updated: on the host, run hq sandbox restart REPO for this sandbox's repository, with --setup SETUP for a sandbox of a setup"

// generationTimeout bounds asking the hq on disk its generation.
const generationTimeout = 5 * time.Second

// fileStamp tells one state of a file from another: an update renames a new
// file over hq's (another inode) or writes it anew (another time or size).
type fileStamp struct {
	dev, ino uint64
	size     int64
	modified int64 // nanoseconds since the epoch
}

// stampFile is the stamp of the file at path, links followed.
func stampFile(path string) (fileStamp, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return fileStamp{}, err
	}
	s := fileStamp{size: fi.Size(), modified: fi.ModTime().UnixNano()}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		s.dev, s.ino = uint64(st.Dev), st.Ino
	}
	return s, nil
}

// generationOf asks the hq at exe its generation, within timeout: 0 for an
// hq from before generations, which does not know the question and fails
// it, or answers something else; an error when it could not be asked (it
// did not start, or did not answer in time).
func generationOf(exe string, timeout time.Duration) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, generationCommand)
	// A child left holding its output does not hold up the answer.
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	var exit *exec.ExitError
	switch {
	case ctx.Err() != nil:
		return 0, fmt.Errorf("%s: no answer within %s", exe, timeout)
	case errors.As(err, &exit):
		return 0, nil
	case err != nil:
		return 0, err
	}
	gen, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || gen < 1 {
		return 0, nil
	}
	return gen, nil
}

// replaceProcess runs argv in place of this process, in the same pane and
// with the same pid; it returns only when that fails.
func replaceProcess(argv []string) error {
	return syscall.Exec(argv[0], argv, os.Environ())
}

// onDisk watches the hq a long-running program was started from, the list
// or hq mcp, for an update to another generation (design §3.11, §3.12):
// it looks at the file, and asks the hq on disk its generation only once
// the file changed, so while hq stays as it was no process is started.
type onDisk struct {
	exe   string // empty when the running hq could not be found
	stamp func(path string) (fileStamp, error)
	ask   func(exe string) (int, error)

	mu    sync.Mutex
	seen  fileStamp // the file as this hq started, or as last asked
	other bool
}

// watchOnDisk starts watching the hq this one was started from.
func watchOnDisk(d deps) *onDisk {
	w := &onDisk{stamp: d.stampFile, ask: d.askGeneration}
	exe, err := d.executable()
	if err != nil {
		return w
	}
	w.exe = exe
	w.seen, _ = w.stamp(exe)
	return w
}

// otherGeneration reports whether the hq on disk is of another generation
// than this one; once it is, it stays so. A file that is missing, or an hq
// that cannot be asked, says nothing yet: it is looked at again at the
// next call.
func (w *onDisk) otherGeneration() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.other || w.exe == "" {
		return w.other
	}
	now, err := w.stamp(w.exe)
	if err != nil || now == w.seen {
		return false
	}
	gen, err := w.ask(w.exe)
	if err != nil {
		return false
	}
	w.seen, w.other = now, gen != version.Generation
	return w.other
}
