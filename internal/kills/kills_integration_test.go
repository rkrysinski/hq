//go:build integration

package kills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppendedKillsReadBackOldestFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hq", "kills.log")
	if ks, err := Read(path); err != nil || ks != nil {
		t.Fatalf("no trace: %v %v", ks, err)
	}
	at := time.Date(2026, 11, 3, 14, 2, 11, 0, time.UTC)
	a := Kill{At: at, Name: "coolify", ID: "0123456789ab", Repo: "/r/app", Sandbox: "claude-app", By: ByCLI, Command: "kill"}
	b := Kill{At: at.Add(time.Minute), Name: "sup", ID: "ba9876543210", Repo: "/r/app", Sandbox: "claude-app", By: ByMCP, Client: "sbx", App: "claude-code 2.1.30", Caller: "lead"}
	for _, k := range []Kill{a, b} {
		if err := Append(path, k); err != nil {
			t.Fatal(err)
		}
	}
	// A line hq cannot read (a newer hq's, a torn write) is skipped.
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("not json\n")
	f.Close()
	ks, err := Read(path)
	if err != nil || len(ks) != 2 || ks[0] != a || ks[1] != b {
		t.Fatalf("read: %+v %v", ks, err)
	}
}

func TestTheOldestKillsGoPastTheLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kills.log")
	var size int64
	n := 0
	for ; ; n++ {
		if err := Append(path, Kill{Name: fmt.Sprintf("a%d", n), By: ByCLI, Repo: strings.Repeat("r", 200)}); err != nil {
			t.Fatal(err)
		}
		info, _ := os.Stat(path)
		if info.Size() > maxSize {
			t.Fatalf("the trace grew to %d bytes", info.Size())
		}
		if info.Size() < size {
			break // trimmed
		}
		size = info.Size()
	}
	ks, _ := Read(path)
	if len(ks) == 0 || ks[len(ks)-1].Name != fmt.Sprintf("a%d", n) || ks[0].Name == "a0" {
		t.Fatalf("after trimming at a%d: %d kills", n, len(ks))
	}
}
