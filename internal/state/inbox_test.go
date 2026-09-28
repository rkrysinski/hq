package state

import (
	"encoding/json"
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
		messageName(at.Add(time.Second), "00000000", false),
		messageName(at, "ffffffff", true),
		messageName(at.Add(time.Nanosecond), "00000000", false),
		messageName(time.Unix(1, 0), "abcdef01", false),
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
	if err := Post("/w/app", "../x", "hi", false, time.Now()); err == nil {
		t.Fatal("posted for a bad id")
	}
	if _, err := Take("/w/app", "../x"); err == nil {
		t.Fatal("took for a bad id")
	}
}
