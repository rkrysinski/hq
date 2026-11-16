package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const linkedInboxID = "0123456789ab"

// An inbox, its held directory, or a directory above them under .git/hq,
// that is a link to another directory, as a sandbox can leave it: hq lists,
// takes, holds, writes and removes nothing there, so a file of the user's
// outside the repository, named as a message is, survives (#215).
func TestAnInboxReachedThroughALinkIsLeftAlone(t *testing.T) {
	if !idRE.MatchString(linkedInboxID) {
		t.Fatal("not an id")
	}
	for _, link := range []string{"hq", "hq/inbox", "hq/inbox/" + linkedInboxID, "hq/inbox/" + linkedInboxID + "/held"} {
		t.Run(link, func(t *testing.T) {
			root := t.TempDir()
			// The link leads to a tree that holds, where the inbox and its
			// held directory would be, files of the user's own.
			target := t.TempDir()
			rest, _ := filepath.Rel(link, filepath.Join("hq/inbox", linkedInboxID, "held"))
			mine := filepath.Join(target, rest)
			if err := os.MkdirAll(mine, 0o755); err != nil {
				t.Fatal(err)
			}
			files := []string{filepath.Join(mine, "2026-notes.txt"), filepath.Join(filepath.Dir(mine), "2026-notes.txt")}
			if rest == "." {
				files = files[:1]
			}
			for _, f := range files {
				if err := os.WriteFile(f, encodeMessage("the user's own"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			at := filepath.Join(root, ".git", link)
			if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, at); err != nil {
				t.Fatal(err)
			}
			refused := func(what string, err error) {
				t.Helper()
				if err == nil || !strings.Contains(err.Error(), "is a link") {
					t.Errorf("%s: %v, want the link refused", what, err)
				}
			}
			msgs, err := Take(root, linkedInboxID)
			refused("take", err)
			if len(msgs) != 0 {
				t.Errorf("took %+v", msgs)
			}
			if n, h := Pending(root, linkedInboxID), Held(root, linkedInboxID); n != 0 || h != 0 {
				t.Errorf("pending %d, held %d", n, h)
			}
			if _, ok := Oldest(root, linkedInboxID); ok {
				t.Error("an oldest message through the link")
			}
			_, err = Post(root, linkedInboxID, Message{Text: "hi"}, false, time.Unix(1000, 0))
			if link == "hq/inbox/"+linkedInboxID+"/held" {
				// Posting does not reach the held directory.
				if err != nil {
					t.Errorf("post beside a linked held directory: %v", err)
				}
			} else {
				refused("post", err)
			}
			refused("hold", Hold(root, linkedInboxID, []Message{{Text: "hi", Name: "0000000000000001000-0a0b0c0d"}}))
			if link != "hq/inbox/"+linkedInboxID+"/held" {
				refused("leave an answer", LeaveAnswer(root, linkedInboxID, Questions{name: "answer.1.2"}, []byte("{}")))
				if AnswerTaken(root, linkedInboxID, Questions{name: "answer.1.2"}) || WithdrawAnswer(root, linkedInboxID, Questions{name: "answer.1.2"}) {
					t.Error("an answer read through the link")
				}
			}
			err = removeInbox(root, linkedInboxID)
			if link == "hq/inbox/"+linkedInboxID {
				// The link alone goes.
				if err != nil {
					t.Errorf("remove: %v", err)
				}
				if _, err := os.Lstat(at); err == nil {
					t.Error("the link is still there")
				}
			} else if link != "hq/inbox/"+linkedInboxID+"/held" {
				refused("remove", err)
			}
			for _, f := range files {
				if _, err := os.Lstat(f); err != nil {
					t.Errorf("the file outside the repository is gone: %v", err)
				}
			}
		})
	}
}

// The same inbox without a link takes its messages: the guard refuses the
// link, not the inbox.
func TestAnInboxWithNoLinkIsTaken(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Post(root, linkedInboxID, Message{Text: "hi"}, false, time.Unix(1000, 0)); err != nil {
		t.Fatal(err)
	}
	if Pending(root, linkedInboxID) != 1 {
		t.Fatal("not pending")
	}
	msgs, err := Take(root, linkedInboxID)
	if err != nil || len(msgs) != 1 || msgs[0].Text != "hi" {
		t.Fatalf("took %+v %v", msgs, err)
	}
	if err := removeInbox(root, linkedInboxID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(InboxDir(root, linkedInboxID)); err == nil {
		t.Fatal("the inbox is still there")
	}
}
