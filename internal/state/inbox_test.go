package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestCleanMessageKeepsLinesAndDropsWhatCouldDriveTheTerminal(t *testing.T) {
	for in, want := range map[string]string{
		"  run the tests\n":                 "run the tests",
		"one\r\ntwo\rthree":                 "one\ntwo\nthree",
		"a\tb":                              "a\tb",
		"\x1b[31mred\x1b[0m \x1b]0;title\a": "red",
		"bell\a nul\x00 del\x7f":            "bell nul del",
		"line sep":                          "linesep",
		"bad \xff byte":                     "bad  byte",
		"zażółć 👍":                          "zażółć 👍",
		"\x1b[200~pasted\x1b[201~":          "pasted",
	} {
		if got := CleanMessage(in); got != want {
			t.Errorf("CleanMessage(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAMessageIsStoredAsTheInsideOfAJSONString(t *testing.T) {
	for _, text := range []string{`say "hi" \ bye`, "two\nlines\tand a tab", "<b>&amp;</b>", "zażółć 👍", "}'; rm -rf /; '"} {
		enc := encodeMessage(text)
		if strings.ContainsAny(string(enc), "\n\r\t") {
			t.Errorf("%q: raw control characters in %q", text, enc)
		}
		// The hook puts it between quotes of its JSON output.
		var s string
		if err := json.Unmarshal([]byte(`"`+string(enc)+`"`), &s); err != nil || s != text {
			t.Errorf("%q: %q decodes to %q, %v", text, enc, s, err)
		}
		if got, err := decodeMessage(enc); err != nil || got != text {
			t.Errorf("%q: decodeMessage %q, %v", text, got, err)
		}
	}
	// HTML stays as typed: Claude reads it, no browser.
	if got := string(encodeMessage("<a&b>")); got != "<a&b>" {
		t.Errorf("%q", got)
	}
	for _, bad := range []string{`unterminated \`, `"`, "raw\nnewline"} {
		if _, err := decodeMessage([]byte(bad)); err == nil {
			t.Errorf("%q decoded", bad)
		}
	}
}

func TestMessageNamesSortInTheOrderTheyWereSent(t *testing.T) {
	at := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	names := []string{
		messageName(at.Add(time.Second), "00000000", false, false),
		messageName(at, "ffffffff", false, true),
		messageName(at.Add(time.Nanosecond), "00000000", false, false),
		messageName(time.Unix(1, 0), "abcdef01", false, false),
	}
	sort.Strings(names)
	if names[0] != "0000000001000000000-abcdef01" || !strings.HasSuffix(names[1], "-ffffffff.now") || !strings.HasSuffix(names[3], "-00000000") ||
		!strings.HasPrefix(names[3], "17") {
		t.Fatalf("%v", names)
	}
	for _, n := range names {
		if !isMessage(n) {
			t.Errorf("%q is a message", n)
		}
	}
	// Files being written, and the directories deliveries take messages
	// into, are not.
	for _, n := range []string{"", "." + names[0], ".taken-123", ".taken.42"} {
		if isMessage(n) {
			t.Errorf("%q is no message", n)
		}
	}
}

func TestAMessageNameSaysWhoSentItAndStillAsksForNow(t *testing.T) {
	at := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		supervisor, now bool
		suffix          string
	}{
		{false, false, "-0a0b0c0d"},
		{false, true, "-0a0b0c0d.now"},
		{true, false, "-0a0b0c0d.sup"},
		// The hook finds a message that asks for now by the end of its
		// name, so that stays last.
		{true, true, "-0a0b0c0d.sup.now"},
	} {
		n := messageName(at, "0a0b0c0d", tc.supervisor, tc.now)
		if !strings.HasSuffix(n, tc.suffix) || !isMessage(n) || fromSupervisor(n) != tc.supervisor {
			t.Errorf("supervisor %v, now %v: %q, the supervisor's %v", tc.supervisor, tc.now, n, fromSupervisor(n))
		}
	}
	// Whoever sent them, messages sort in the order they were sent.
	if first, later := messageName(at, "ffffffff", true, true), messageName(at.Add(time.Nanosecond), "00000000", false, false); first >= later {
		t.Errorf("%q sorts after %q", first, later)
	}
}

func TestAMessageFitsWhatHqReadsBack(t *testing.T) {
	// The worst case: every byte escaped as \u00XX would be 6x, but
	// CleanMessage leaves only newlines and tabs, 2 bytes each.
	worst := strings.Repeat("\n\"", MaxMessage/2)
	if n := len(encodeMessage(worst)); n > maxFile {
		t.Fatalf("%d bytes encoded", n)
	}
}

func TestInboxDirIsBesideTheStateFiles(t *testing.T) {
	if got := InboxDir("/w/app", "0123abcd"); got != "/w/app/.git/hq/inbox/0123abcd" {
		t.Fatal(got)
	}
	if Pending("/w/app", "../x") != 0 {
		t.Fatal("not an agent id")
	}
	if err := Post("/w/app", "../x", Message{Text: "hi"}, false, time.Now()); err == nil {
		t.Fatal("posted for a bad id")
	}
	if _, err := Take("/w/app", "../x"); err == nil {
		t.Fatal("took for a bad id")
	}
}

func TestOnlyAnAgentsIdIsAnnouncedFor(t *testing.T) {
	root := t.TempDir()
	for _, id := range []string{"", "../x", "ABC", "abc/../def"} {
		if Announce(root, id) == nil || Withdraw(root, id) == nil || Announced(root, id) {
			t.Errorf("announced for the invalid id %q", id)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); !os.IsNotExist(err) {
		t.Fatal("wrote something for an invalid id")
	}
}
