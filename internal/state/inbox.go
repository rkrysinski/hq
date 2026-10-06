package state

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// The inbox holds the messages hq send leaves for an agent until its hooks,
// or hq typing them as its next prompt, deliver them (design §3.4, ADR
// 0012): one file per message in the main repository's .git/hq/inbox/ID,
// named so that the names sort in the order the messages were sent. The
// file holds the message already encoded as the inside of a JSON string,
// so the hook in the sandbox only copies it into its output. A message
// hq send gave --now ends in nowSuffix. Whoever delivers a message first
// renames it out of the inbox (a rename happens once), so a message is
// delivered once, by the hook or by hq.

// MaxMessage is the longest message hq send takes, in bytes: encoded, it
// stays within what hq reads back from a file (maxFile).
const MaxMessage = 8 << 10

// nowSuffix ends the name of a message sent with --now: the hook delivers
// it after Claude's next tool call instead of when Claude stops.
const nowSuffix = ".now"

// supervisorSuffix marks the name of a message the supervisor sent (hq
// mcp), before nowSuffix when it has both: the hook tells by it whether a
// message of the user's reached a turn (design §3.5).
const supervisorSuffix = ".sup"

// Message is a message for an agent.
type Message struct {
	Text string
	// Supervisor says the supervisor sent it, through hq mcp, not the user.
	Supervisor bool
}

// MessageLabel starts each message the hooks deliver, so Claude, and the
// user watching its session, can tell it from its own work.
const MessageLabel = "Message from the user (hq send): "

// InboxDir is where messages for agent id of the repository at root wait.
func InboxDir(root, id string) string { return filepath.Join(root, ".git", "hq", "inbox", id) }

// CleanMessage makes text safe to type into Claude's prompt box and to
// show: escape sequences and control characters go, except newlines and
// tabs; CR LF becomes LF; surrounding whitespace goes. Unlike Clean it
// keeps the lines.
func CleanMessage(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var b strings.Builder
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		switch {
		case r == 0x1b:
			i += escapeLen(text[i:])
			continue
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r == '\r':
			b.WriteByte('\n')
		case r == utf8.RuneError && size == 1, unicode.IsControl(r), r == 0x2028, r == 0x2029:
		default:
			b.WriteRune(r)
		}
		i += size
	}
	return strings.TrimSpace(b.String())
}

// encodeMessage is text as the inside of a JSON string: what the hook
// copies between the quotes of its output.
func encodeMessage(text string) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(text)
	s := bytes.TrimSpace(b.Bytes())
	return s[1 : len(s)-1]
}

// decodeMessage is the text of a message file.
func decodeMessage(data []byte) (string, error) {
	var s string
	if err := json.Unmarshal(append(append([]byte{'"'}, data...), '"'), &s); err != nil {
		return "", errors.New("not a message")
	}
	return CleanMessage(s), nil
}

// messageName names a message sent at at: nanoseconds, zero-padded so that
// names sort as times do, a random part so that two messages sent in the
// same nanosecond do not collide, supervisorSuffix when the supervisor sent
// it and nowSuffix when now.
func messageName(at time.Time, random string, supervisor, now bool) string {
	name := fmt.Sprintf("%019d-%s", at.UnixNano(), random)
	if supervisor {
		name += supervisorSuffix
	}
	if now {
		name += nowSuffix
	}
	return name
}

// isMessage reports whether a file in an inbox is a message: temporary
// files and the directories deliveries take messages into start with a dot.
func isMessage(name string) bool { return name != "" && name[0] >= '0' && name[0] <= '9' }

// fromSupervisor reports whether the message named name is the
// supervisor's.
func fromSupervisor(name string) bool {
	return strings.HasSuffix(strings.TrimSuffix(name, nowSuffix), supervisorSuffix)
}

// Post leaves m for agent id of the repository at root, sent at at; now
// asks for delivery after Claude's next tool call. The message is written
// beside the others and renamed into place, so a hook never reads half of
// it.
func Post(root, id string, m Message, now bool, at time.Time) error {
	if !idRE.MatchString(id) {
		return errors.New("not an agent id")
	}
	dir := InboxDir(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	r := make([]byte, 4)
	_, _ = rand.Read(r)
	name := messageName(at, hex.EncodeToString(r), m.Supervisor, now)
	tmp := filepath.Join(dir, "."+name)
	if err := os.WriteFile(tmp, encodeMessage(CleanMessage(m.Text)), 0o644); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// messages lists the messages waiting in dir, oldest first.
func messages(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if isMessage(e.Name()) && !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// Pending is how many messages wait for agent id of the repository at root.
func Pending(root, id string) int {
	if !idRE.MatchString(id) {
		return 0
	}
	return len(messages(InboxDir(root, id)))
}

// Take takes every message waiting for agent id of the repository at root
// out of its inbox, oldest first, for hq to deliver itself. A message a
// hook took first is not among them. A file that is no message (a link, a
// file too big, one that does not decode) is taken and dropped.
func Take(root, id string) ([]Message, error) {
	if !idRE.MatchString(id) {
		return nil, errors.New("not an agent id")
	}
	dir := InboxDir(root, id)
	names := messages(dir)
	if len(names) == 0 {
		return nil, nil
	}
	taken, err := os.MkdirTemp(dir, ".taken-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(taken)
	var msgs []Message
	for _, n := range names {
		p := filepath.Join(taken, n)
		if os.Rename(filepath.Join(dir, n), p) != nil {
			continue // a hook took it first
		}
		data, _, err := readData(p)
		if err != nil {
			continue
		}
		if text, err := decodeMessage(data); err == nil && text != "" {
			msgs = append(msgs, Message{Text: text, Supervisor: fromSupervisor(n)})
		}
	}
	return msgs, nil
}

// removeInbox deletes the inbox of agent id with whatever waits in it.
func removeInbox(root, id string) error {
	err := os.RemoveAll(InboxDir(root, id))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
