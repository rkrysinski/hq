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
	"strconv"
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
// delivered once, by the hook or by hq. Messages hq took to type in but
// holds for the agent's next prompt wait in the inbox's heldDir, where the
// hook does not look (Hold).

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
	// Name is the name it was left under in the inbox, for a message Take
	// took: Hold, and Post putting it back, keep it, so every delivery of
	// it names it as it was first left (spec #165, story 46); Post drops
	// its nowSuffix, which only the first delivery honours.
	Name string
}

// MessageLabel starts each message the hooks deliver, so Claude, and the
// user watching its session, can tell it from its own work.
const MessageLabel = "Message from the user (hq send): "

// InboxDir is where messages for agent id of the repository at root wait.
func InboxDir(root, id string) string { return filepath.Join(root, ".git", "hq", "inbox", id) }

// inboxDir is InboxDir(root, id), or sub in it, refused when it or a
// directory above it under .git/hq is a link (LinkedAt): hq lists, reads,
// writes and removes nothing in an inbox through a link (#215).
func inboxDir(root, id string, sub ...string) (string, error) {
	dir := filepath.Join(append([]string{InboxDir(root, id)}, sub...)...)
	if at := LinkedAt(root, dir); at != "" {
		return "", LinkedErr(at)
	}
	return dir, nil
}

// heldDir is the directory in an inbox where held messages wait (Hold):
// the hook takes only the messages in the inbox itself, whose names start
// with a digit.
const heldDir = "held"

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

// keptName reports whether name is one a message can keep: a message's
// name, of no other directory.
func keptName(name string) bool {
	return isMessage(name) && filepath.Base(name) == name && len(name) <= 100
}

// isMessage reports whether a file in an inbox is a message: temporary
// files and the directories deliveries take messages into start with a dot.
func isMessage(name string) bool { return name != "" && name[0] >= '0' && name[0] <= '9' }

// fromSupervisor reports whether the message named name is the
// supervisor's.
func fromSupervisor(name string) bool {
	return strings.HasSuffix(strings.TrimSuffix(name, nowSuffix), supervisorSuffix)
}

// Hold puts msgs, taken out of the inbox of agent id of the repository at
// root by Take, back into it for hq alone: the hooks do not deliver them,
// and Take, Pending and Oldest see them as sent when they were (#173). hq
// holds the messages it cannot type in with the prompt it types now: a
// slash command is a prompt of its own, and Claude Code delivers a prompt
// typed in while it works ahead of a slash command queued before it.
func Hold(root, id string, msgs []Message) error {
	if !idRE.MatchString(id) {
		return errors.New("not an agent id")
	}
	dir, err := inboxDir(root, id, heldDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var written []string
	for _, m := range msgs {
		var path string
		err := fmt.Errorf("a message held keeps the name it was left under: %q has none", m.Text)
		if keptName(m.Name) {
			// The name says when it was sent; no time is needed.
			path, err = write(dir, m, false, time.Time{})
		}
		if errors.Is(err, fs.ErrExist) {
			err = fmt.Errorf("a message named %s is held already", m.Name)
		}
		if err != nil {
			// None of them held, so that a caller putting them back
			// delivers none twice.
			for _, p := range written {
				os.Remove(p)
			}
			return err
		}
		written = append(written, path)
	}
	return nil
}

// Held is how many messages wait held for agent id of the repository at
// root (Hold): Pending counts them too, but the hooks do not deliver them.
func Held(root, id string) int {
	if !idRE.MatchString(id) {
		return 0
	}
	dir, err := inboxDir(root, id, heldDir)
	if err != nil {
		return 0
	}
	return len(messages(dir))
}

// Post leaves m for agent id of the repository at root, sent at at; now
// asks for delivery after Claude's next tool call. The message is written
// beside the others and renamed into place, so a hook never reads half of
// it. A message Take took is put back under its name, which says when it
// was sent and by whom (at aside), less nowSuffix unless now asks for it
// again: put back after hq failed to type it, it goes with the agent's
// next prompt, as it did before messages kept their names. It returns
// the name the message waits under, which the deliveries in the agent's
// journal name it by (SameMessage).
func Post(root, id string, m Message, now bool, at time.Time) (string, error) {
	if !idRE.MatchString(id) {
		return "", errors.New("not an agent id")
	}
	m.Name = backName(m.Name, now)
	dir, err := inboxDir(root, id)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path, err := write(dir, m, now, at)
	if err != nil {
		return "", err
	}
	return filepath.Base(path), nil
}

// SameMessage reports whether the names a and b are those of one message:
// a message put back after hq failed to type it loses nowSuffix (Post), so
// a later delivery names it without it.
func SameMessage(a, b string) bool {
	return strings.TrimSuffix(a, nowSuffix) == strings.TrimSuffix(b, nowSuffix)
}

// backName is the name a message Take took under name is put back under
// (Post): name less nowSuffix, unless now asks for it again.
func backName(name string, now bool) string {
	if now {
		return name
	}
	return strings.TrimSuffix(name, nowSuffix)
}

// write writes m, sent at at, into the directory dir of an inbox, beside
// the others, and renames it into place; it returns its path. A message
// with a name (Message.Name) keeps it, and is not written over another of
// that name (fs.ErrExist).
func write(dir string, m Message, now bool, at time.Time) (string, error) {
	name := m.Name
	if !keptName(name) {
		r := make([]byte, 4)
		_, _ = rand.Read(r)
		name = messageName(at, hex.EncodeToString(r), m.Supervisor, now)
	}
	tmp := filepath.Join(dir, "."+name)
	if err := os.WriteFile(tmp, encodeMessage(CleanMessage(m.Text)), 0o644); err != nil {
		os.Remove(tmp)
		return "", err
	}
	path := filepath.Join(dir, name)
	if keptName(m.Name) {
		// A link fails where the name is taken, as a rename would not.
		err := os.Link(tmp, path)
		os.Remove(tmp)
		if err != nil {
			return "", err
		}
		return path, nil
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return path, nil
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

// waiting lists the messages waiting for agent id of the repository at
// root, held ones included, as paths relative to its inbox, in the order
// they were sent.
func waiting(root, id string) []string {
	dir, err := inboxDir(root, id)
	if err != nil {
		return nil
	}
	if _, err := inboxDir(root, id, heldDir); err != nil {
		return nil
	}
	names := messages(dir)
	for _, n := range messages(filepath.Join(dir, heldDir)) {
		names = append(names, filepath.Join(heldDir, n))
	}
	sort.Slice(names, func(i, j int) bool { return filepath.Base(names[i]) < filepath.Base(names[j]) })
	return names
}

// Pending is how many messages wait for agent id of the repository at root,
// held ones included.
func Pending(root, id string) int {
	if !idRE.MatchString(id) {
		return 0
	}
	return len(waiting(root, id))
}

// Oldest is the moment the oldest message waiting for agent id of the
// repository at root was sent, as its name says; false when none waits.
func Oldest(root, id string) (time.Time, bool) {
	if !idRE.MatchString(id) {
		return time.Time{}, false
	}
	names := waiting(root, id)
	if len(names) == 0 {
		return time.Time{}, false
	}
	return sentAt(filepath.Base(names[0]))
}

// sentAt is the moment the message named name was sent (messageName).
func sentAt(name string) (time.Time, bool) {
	n, err := strconv.ParseInt(name[:max(0, strings.IndexByte(name, '-'))], 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(0, n), true
}

// Take takes every message waiting for agent id of the repository at root
// out of its inbox, held ones included, oldest first, for hq to deliver
// itself, each with the name it was left under (Message.Name). A message a hook took first is not among them. A file that is no
// message (a link, a file too big, one that does not decode) is taken and
// dropped.
func Take(root, id string) ([]Message, error) {
	if !idRE.MatchString(id) {
		return nil, errors.New("not an agent id")
	}
	dir, err := inboxDir(root, id)
	if err != nil {
		return nil, err
	}
	if _, err := inboxDir(root, id, heldDir); err != nil {
		return nil, err
	}
	names := waiting(root, id)
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
		base := filepath.Base(n)
		p := filepath.Join(taken, base)
		if os.Rename(filepath.Join(dir, n), p) != nil {
			continue // a hook took it first
		}
		data, _, err := ReadFile(p)
		if err != nil {
			continue
		}
		if text, err := decodeMessage(data); err == nil && text != "" {
			msgs = append(msgs, Message{Text: text, Supervisor: fromSupervisor(base), Name: base})
		}
	}
	return msgs, nil
}

// removeInbox deletes the inbox of agent id with whatever waits in it.
// An inbox that is itself a link loses the link alone; one under a linked
// directory is not removed (LinkedAt).
func removeInbox(root, id string) error {
	dir := InboxDir(root, id)
	var err error
	switch at := LinkedAt(root, dir); at {
	case "":
		err = os.RemoveAll(dir)
	case dir:
		err = os.Remove(dir)
	default:
		err = LinkedErr(at)
	}
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
