package state

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
)

// maxFile caps what hq reads from a file the sandbox writes (design §7.3): a bound
// against a planted file, not a limit on what an agent may say, so a long
// reply is read whole (#64).
const maxFile = 16 << 20

// tooLargeError is the error of a regular file over maxFile: hq does not
// read it, but an event file that size is the agent's report all the same,
// which hq says (TooLarge).
type tooLargeError struct{ size int64 }

func (e tooLargeError) Error() string { return fmt.Sprintf("file of %d bytes", e.size) }

// TooLarge is what hq shows of a report too large to read: as the last
// message and as the reply (spec §4.1, design §7.3).
func TooLarge(size int64) string {
	return fmt.Sprintf("[report too large to read: %.1f MiB]", float64(size)/(1<<20))
}

var idRE = regexp.MustCompile(`^[0-9a-f]{1,64}$`)

// Dir is where, under the repository at root, the hook of each of its
// agents keeps the working files it needs inside the sandbox (hookSource),
// where hq leaves its word to the hook (Announce) and keeps its record of
// each agent (WriteRecord). hq on the host reads none of the hook's files:
// an agent's state comes from its journal alone (ADR 0018).
func Dir(root string) string { return filepath.Join(root, ".git", "hq", "agents") }

// noteSuffix names, in Dir, the hook's note of the latest event it took
// for the agent's state: of that event only what its own rules read (its
// kind, the turn's id, the tool, the MCP server, whether a subagent
// reported it), and the cksum the hook of kind hold knows its dialog's
// report by (hookAwk, K=note).
const noteSuffix = ".last"

// endedSuffix names, in Dir, the hook's note of the latest turn end it
// reported, as noteSuffix keeps it: what tells a queued prompt Claude has
// taken at a turn end (#82).
const endedSuffix = ".ended"

// keptSuffix names, in Dir, the hook's mark that it kept an event without
// taking it for the agent's state: a turn end while background work runs,
// or Claude's wake-up while the agent works.
const keptSuffix = ".on"

// owedSuffix names, in Dir, the hook's own list of the background work it
// follows: the tasks Claude still owes a turn for, the shell commands the
// agent's subagents started, and the watches the agent keeps with the
// Monitor tool (hookAwk).
const owedSuffix = ".owe"

// supervisedSuffix names, in Dir, the hook's mark that the agent's turn is
// a supervised turn: every prompt of it came from the supervisor, so its
// end notifies nobody (spec §5, design §3.5).
const supervisedSuffix = ".sup"

// announcedSuffix names, in Dir, hq's word to the hook that the agent's
// next prompt is the supervisor's (Announce).
const announcedSuffix = ".next"

// actedSuffix names, in Dir, the hook's mark that a tool of the agent's
// own has ended in its current turn, holding the turn's prompt_id.
const actedSuffix = ".acted"

// queuedSuffix names, in Dir, the hook's copy of a prompt Claude has
// queued while the agent works, until Claude takes it into the turn at
// work, or, when it takes it as a turn of its own after that turn's end,
// until that turn reports (#82).
const queuedSuffix = ".queued"

// askedSuffix names, in Dir, the hook's copy of its note of the latest
// dialog it reported, with that report's cksum, made once the report is
// the note and before its event, so the hook of kind hold that first looks after its
// dialog was answered, the note no longer its dialog's, knows the dialog
// has closed and ends at once, not after the bound it gives a report that
// never comes (#209). The hook of kind hold removes it as it ends when it
// is its own dialog's, so the same questions asked again in the turn, a
// report of the same cksum, are waited for.
const askedSuffix = ".asked"

// agentFiles are the files Remove deletes in Dir for an agent: the hook's
// working files, hq's word to it and record of it, and the state file,
// the latest turn end and the report before a resumed session that the
// hook of an earlier generation wrote ("", .stop, .prev), so killing an
// outdated agent leaves none of them behind.
var agentFiles = [...]string{"", ".stop", ".prev", noteSuffix, endedSuffix, keptSuffix, owedSuffix, supervisedSuffix, announcedSuffix, actedSuffix, queuedSuffix, askedSuffix, recordSuffix}

// Remove deletes the files in Dir and the inbox of agent id of the
// repository at root, once the agent is gone (design §3.4); files already
// gone are no error. Only that agent's files are touched, never another
// agent's: agents of other tmux servers keep theirs in the same directory.
func Remove(root, id string) error {
	if !idRE.MatchString(id) {
		return errors.New("not an agent id")
	}
	path := filepath.Join(Dir(root), id)
	var errs []error
	for _, suffix := range agentFiles {
		if err := os.Remove(path + suffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	// Messages still waiting go with the agent (#135).
	if err := removeInbox(root, id); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// ReadFile reads a file the sandbox writes as hq reads such a file (design
// §7.3): a regular file of at most maxFile bytes (16 MiB), never following
// a symbolic link, so a link planted in the repository cannot make hq read
// and show another file. A larger regular file is a tooLargeError, with
// its information (TooLargeSize).
func ReadFile(path string) ([]byte, os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, errors.New("not a regular file")
	}
	if info.Size() > maxFile {
		return nil, info, tooLargeError{info.Size()}
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	if info, err = f.Stat(); err != nil || !info.Mode().IsRegular() {
		return nil, nil, errors.New("not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxFile+1))
	if err != nil {
		return nil, nil, err
	}
	if len(data) > maxFile {
		return nil, info, tooLargeError{max(info.Size(), int64(len(data)))}
	}
	return data, info, nil
}
