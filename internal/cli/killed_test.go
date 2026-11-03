package cli

import (
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/kills"
)

func TestKilledListsTheTraceOldestFirstWithWhoAskedForIt(t *testing.T) {
	f := newFakes()
	at := time.Date(2026, 11, 3, 14, 2, 11, 0, time.UTC)
	f.kills = []kills.Kill{
		{At: at, Name: "coolify", ID: "id-c", Repo: "/w/app", Sandbox: "claude-app", By: kills.ByCLI, Command: "kill"},
		{At: at.Add(time.Minute), Name: "b", Repo: "/w/lib", Sandbox: "claude-lib", By: kills.ByCLI, Command: "stop"},
		{At: at.Add(2 * time.Minute), Name: "c", Repo: "/w/lib", Sandbox: "claude-lib", By: kills.ByDashboard},
		{At: at.Add(3 * time.Minute), Name: "coolify", Repo: "/w/app", Sandbox: "claude-app", By: kills.ByMCP, Client: "sbx", App: "claude-code 2.1.30", Caller: "sup"},
		{At: at.Add(4 * time.Minute), Name: "d", Repo: "/w/app", Sandbox: "claude-app", By: kills.ByMCP, Client: "claude-desktop"},
	}
	code, out, errOut := f.run("killed")
	want := "2026-11-03 14:02:11  coolify in app (sandbox claude-app), by hq kill\n" +
		"2026-11-03 14:03:11  b in lib (sandbox claude-lib), by hq stop\n" +
		"2026-11-03 14:04:11  c in lib (sandbox claude-lib), by the dashboard\n" +
		"2026-11-03 14:05:11  coolify in app (sandbox claude-app), by MCP: client sbx (claude-code 2.1.30), agent sup\n" +
		"2026-11-03 14:06:11  d in app (sandbox claude-app), by MCP: client claude-desktop\n"
	if code != 0 || out != want {
		t.Fatalf("exit %d %q\n%s", code, errOut, out)
	}
	code, out, _ = f.run("killed", "coolify")
	if code != 0 || out != "2026-11-03 14:02:11  coolify in app (sandbox claude-app), by hq kill\n2026-11-03 14:05:11  coolify in app (sandbox claude-app), by MCP: client sbx (claude-code 2.1.30), agent sup\n" {
		t.Fatalf("killed coolify: exit %d\n%s", code, out)
	}
	if code, out, _ := f.run("killed", "nobody"); code != 0 || out != "no kill of 'nobody' in the trace\n" {
		t.Fatalf("killed nobody: exit %d %q", code, out)
	}
}

func TestKilledWithNoTraceAndBadArguments(t *testing.T) {
	f := newFakes()
	if code, out, _ := f.run("killed"); code != 0 || out != "no kills in the trace\n" {
		t.Fatalf("exit %d %q", code, out)
	}
	for _, args := range [][]string{{"killed", "a", "b"}, {"killed", "--json"}} {
		if code, _, errOut := f.run(args...); code != ExitUsage || errOut != "hq: usage: hq killed [NAME]\n" {
			t.Fatalf("%q: exit %d %q", args, code, errOut)
		}
	}
}
