package cli

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rkrysinski/hq/internal/desktop"
	"github.com/rkrysinski/hq/internal/state"
	"github.com/rkrysinski/hq/internal/tmux"
)

// mcpSession connects an MCP client in memory to hq's MCP server on the
// fakes, as Claude Desktop connects over stdio. There is no terminal.
func mcpSession(t *testing.T, f *fakes) *mcp.ClientSession {
	t.Helper()
	f.tty = false
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := newMCPServer(f.deps()).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close(); _ = ss.Wait() })
	return cs
}

// call calls a tool and returns its text and whether it is an error.
func call(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any) (string, bool) {
	t.Helper()
	r, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	var text strings.Builder
	for _, c := range r.Content {
		text.WriteString(c.(*mcp.TextContent).Text)
	}
	return text.String(), r.IsError
}

func TestMCPOffersTheSupervisorsToolsAndTeachesTheProtocol(t *testing.T) {
	cs := mcpSession(t, newLsFakes())
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string]*mcp.Tool{}
	var names []string
	for _, tl := range res.Tools {
		tools[tl.Name] = tl
		names = append(names, tl.Name)
	}
	slices.Sort(names)
	if strings.Join(names, " ") != "go kill list new read send wait" {
		t.Fatalf("tools %q; stop and sandbox are never offered", names)
	}
	for name, tl := range tools {
		a := tl.Annotations
		destructive := a != nil && !a.ReadOnlyHint && (a.DestructiveHint == nil || *a.DestructiveHint)
		if destructive != (name == "kill") {
			t.Errorf("%s: destructive %v", name, destructive)
		}
		if a == nil || a.Title == "" || tl.Description == "" {
			t.Errorf("%s: no title or description", name)
		}
	}
	for _, name := range []string{"list", "read", "wait"} {
		if !tools[name].Annotations.ReadOnlyHint {
			t.Errorf("%s changes something?", name)
		}
	}
	// The descriptions carry the protocol.
	for name, want := range map[string]string{
		"list": "never ask an agent for its status",
		"read": "what it asks",
		"wait": "since is required: the next_since of the most recent list, read, send or wait result",
		"send": "wait for the agent with that next_since as since",
		"kill": "without confirmed set to true it ends nothing",
	} {
		if !strings.Contains(tools[name].Description, want) {
			t.Errorf("%s: no %q in %q", name, want, tools[name].Description)
		}
	}
	for _, want := range []string{"Status comes from hq, never from asking an agent", "wait for the agent with send's next_since as since, then read it", "Always pass the next_since of the most recent of them as wait's since", "call list first", "Never try to answer it"} {
		if !strings.Contains(cs.InitializeResult().Instructions, want) {
			t.Errorf("instructions have no %q", want)
		}
	}
	// What each tool must be given.
	for name, want := range map[string][]string{"new": {"name", "dir"}, "send": {"name", "text"}, "read": {"name"}, "go": {"name"}, "kill": {"name"}, "wait": {"since"}, "list": nil} {
		b, _ := json.Marshal(tools[name].InputSchema)
		var schema struct{ Required []string }
		_ = json.Unmarshal(b, &schema)
		if !slices.Equal(schema.Required, want) {
			t.Errorf("%s requires %q, want %q", name, schema.Required, want)
		}
	}
}

func TestMCPListAndReadGiveWhatHqLsAndHqReadPrint(t *testing.T) {
	f := newLsFakes()
	f.tmux.windows = []tmux.Window{{ID: "@0", Name: "hq", Options: map[string]string{}}, agentWindow("@1", "a", "/w/app", f.now.Add(-time.Hour), false)}
	f.states["id-a"] = state.Report{State: state.Done, Since: f.now.Add(-time.Minute), Last: "PR #58 opened", Branch: "feat/58"}
	f.details["id-a"] = state.Detail{Reply: "Opened PR #58.\n\nAll tests pass."}
	cs := mcpSession(t, f)
	_, ls, _ := f.run("ls", "--json")
	if out, isErr := call(t, cs, "list", nil); isErr || out != ls {
		t.Fatalf("list %v:\n%s\nwant\n%s", isErr, out, ls)
	}
	_, read, _ := f.run("read", "a", "--json")
	if out, isErr := call(t, cs, "read", map[string]any{"name": "a"}); isErr || out != read || !strings.Contains(out, "All tests pass.") {
		t.Fatalf("read %v:\n%s\nwant\n%s", isErr, out, read)
	}
	// Errors are the CLI's, as a tool error.
	if out, isErr := call(t, cs, "read", map[string]any{"name": "nobody"}); !isErr || out != "hq: no agent 'nobody' (see hq ls)\n" {
		t.Fatalf("unknown agent %v %q", isErr, out)
	}
	// A missing argument is refused before hq runs.
	if r, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "read", Arguments: map[string]any{}}); err == nil && !r.IsError {
		t.Fatal("read without a name")
	}
}

func TestMCPWaitInALoopWithSinceReturnsEachChangeOnce(t *testing.T) {
	f := newWaitFakes()
	cs := mcpSession(t, f)
	out, _ := call(t, cs, "list", nil)
	var ls waitOutput
	if err := json.Unmarshal([]byte(out), &ls); err != nil || len(ls.Agents) != 2 {
		t.Fatalf("list %v: %s", err, out)
	}
	start := f.now
	out, isErr := call(t, cs, "wait", map[string]any{"since": ls.NextSince.Format(time.RFC3339Nano)})
	first := waitJSON(t, out)
	if isErr || len(first.Agents) != 0 || f.now.Sub(start) < DefaultWaitTimeout || f.now.Sub(start) > DefaultWaitTimeout+time.Second {
		t.Fatalf("first call after %v: %s", f.now.Sub(start), out)
	}
	// Between the calls a asks a question.
	f.now = f.now.Add(5 * time.Second)
	f.states["id-a"] = state.Report{State: state.Question, Since: f.now, Last: "Shall I go on?"}
	f.now = f.now.Add(5 * time.Second)
	out, _ = call(t, cs, "wait", map[string]any{"since": first.NextSince.Format(time.RFC3339Nano)})
	second := waitJSON(t, out)
	if len(second.Agents) != 1 || second.Agents[0].Name != "a" || second.Agents[0].State != state.Question || second.Agents[0].Last != "Shall I go on?" {
		t.Fatalf("second call: %s", out)
	}
	// Once only; the names narrow it and the timeout shortens it.
	start = f.now
	out, _ = call(t, cs, "wait", map[string]any{"since": second.NextSince.Format(time.RFC3339Nano), "names": []string{"a", "b"}, "timeout": 3})
	if third := waitJSON(t, out); len(third.Agents) != 0 || f.now.Sub(start) < 3*time.Second || f.now.Sub(start) > 4*time.Second {
		t.Fatalf("third call after %v: %s", f.now.Sub(start), out)
	}
	if out, isErr := call(t, cs, "wait", map[string]any{"since": "0s", "names": []string{"nobody"}}); !isErr || out != "hq: no agent 'nobody' (see hq ls)\n" {
		t.Fatalf("unknown agent %v %q", isErr, out)
	}
	if out, isErr := call(t, cs, "wait", map[string]any{"since": "yesterday"}); !isErr || !strings.HasPrefix(out, "hq: --since 'yesterday'") {
		t.Fatalf("bad since %v %q", isErr, out)
	}
	if out, isErr := call(t, cs, "wait", map[string]any{"since": ""}); !isErr || !strings.HasPrefix(out, "hq: since is required") {
		t.Fatalf("no since %v %q", isErr, out)
	}
	if r, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "wait", Arguments: map[string]any{}}); err == nil && !r.IsError {
		t.Fatal("wait without since")
	}
}

func TestMCPWaitNeverOutlastsClaudeDesktopsToolCall(t *testing.T) {
	for _, timeout := range []int{0, -5, 51, 600} {
		f := newWaitFakes()
		cs := mcpSession(t, f)
		start := f.now
		args := map[string]any{"since": "0s"}
		if timeout != 0 {
			args["timeout"] = timeout
		}
		if out, _ := call(t, cs, "wait", args); len(waitJSON(t, out).Agents) != 0 {
			t.Fatalf("%d: %s", timeout, out)
		}
		if took := f.now.Sub(start); took < maxWaitTimeout || took > maxWaitTimeout+time.Second {
			t.Errorf("timeout %d waited %v", timeout, took)
		}
	}
	if maxWaitTimeout >= 60*time.Second {
		t.Fatal("Claude Desktop gives a tool call 60 s")
	}
}

func TestMCPSendLeavesTheMessageAsHqSendDoes(t *testing.T) {
	f := sendFakes(state.Working)
	cs := mcpSession(t, f)
	if out, isErr := call(t, cs, "send", map[string]any{"name": "a", "text": "Any blockers? Answer briefly when you finish."}); isErr || out != "{\n  \"delivery\": \"queued: a is working, delivered when it stops\",\n  \"next_since\": \"2026-09-27T11:59:59.75Z\"\n}\n" {
		t.Fatalf("%v %q", isErr, out)
	}
	if out, _ := call(t, cs, "send", map[string]any{"name": "a", "text": "use the v2 API", "now": true}); !strings.Contains(out, `"delivery": "queued: a is working, delivered after its next tool call, or when it stops"`) {
		t.Fatalf("now: %q", out)
	}
	if got := strings.Join(f.inbox["id-a"], "|"); got != "Any blockers? Answer briefly when you finish.|use the v2 API (now)" {
		t.Fatalf("inbox %q", got)
	}
	if out, isErr := call(t, cs, "send", map[string]any{"name": "a", "text": " "}); !isErr || !strings.HasPrefix(out, "hq: empty message") {
		t.Fatalf("empty %v %q", isErr, out)
	}
}

// The owner's case: an agent waiting at its prompt gets the message typed in
// and answers at once, before the supervisor calls wait. wait from send's
// next_since returns the answer at once; from the moment wait is called it
// would never come.
func TestMCPWaitFromSendsNextSinceReturnsAnAnswerGivenBeforeWaitWasCalled(t *testing.T) {
	f := sendFakes(state.Question)
	cs := mcpSession(t, f)
	out, isErr := call(t, cs, "send", map[string]any{"name": "a", "text": "No"})
	var sent sendOutput
	if err := json.Unmarshal([]byte(out), &sent); isErr || err != nil || sent.Delivery != "delivered: typed into a as its next prompt" || !sent.NextSince.Before(f.now) {
		t.Fatalf("send %v %v: %s", isErr, err, out)
	}
	// a answers within two seconds; the supervisor calls wait a minute later.
	f.states["id-a"] = state.Report{State: state.Done, Since: f.now.Add(2 * time.Second), Last: "Understood, I will not."}
	f.now = f.now.Add(time.Minute)
	start := f.now
	out, _ = call(t, cs, "wait", map[string]any{"since": sent.NextSince.Format(time.RFC3339Nano), "names": []string{"a"}})
	got := waitJSON(t, out)
	if len(got.Agents) != 1 || got.Agents[0].State != state.Done || got.Agents[0].Last != "Understood, I will not." || f.now.Sub(start) > time.Second {
		t.Fatalf("wait after %v: %s", f.now.Sub(start), out)
	}
	// From the moment of the call (0s back), as hq wait without --since, the
	// answer is lost: the call times out empty.
	out, _ = call(t, cs, "wait", map[string]any{"since": "0s", "names": []string{"a"}, "timeout": 3})
	if len(waitJSON(t, out).Agents) != 0 {
		t.Fatalf("from now: %s", out)
	}
}

func TestMCPNewStartsAnAgentInTheRepositoryGiven(t *testing.T) {
	f := newFakes()
	f.sbx.sandboxes = sandboxesFor("/w/lib")
	cs := mcpSession(t, f)
	out, isErr := call(t, cs, "new", map[string]any{"name": "42", "dir": "/w/lib", "prompt": "work on issue #42"})
	if isErr || out != "started 42 in lib (sandbox claude-lib); enter it with: hq go 42\n" {
		t.Fatalf("%v %q", isErr, out)
	}
	w := f.tmux.windows[len(f.tmux.windows)-1]
	if argv := f.tmux.argv[w.ID]; w.Options["repo"] != "/w/lib" || argv[len(argv)-1] != "work on issue #42" {
		t.Fatalf("window %+v argv %q", w, argv)
	}
	// Without a prompt the agent waits for one.
	if out, isErr := call(t, cs, "new", map[string]any{"name": "idle", "dir": "/w/lib/"}); isErr {
		t.Fatalf("no prompt: %q", out)
	}
	w = f.tmux.windows[len(f.tmux.windows)-1]
	if argv := f.tmux.argv[w.ID]; w.Name != "idle" || strings.HasPrefix(argv[len(argv)-1], "/w/") {
		t.Fatalf("no prompt: %q", argv)
	}
	for _, tc := range []struct{ dir, want string }{
		{"lib", "hq: dir 'lib' is not an absolute path\n"},
		{"/w/missing", "hq: no directory /w/missing\n"},
		{"/w/plain", "hq: /w/plain is not in a git repository\n"},
	} {
		if out, isErr := call(t, cs, "new", map[string]any{"name": "x", "dir": tc.dir, "prompt": "hi"}); !isErr || out != tc.want {
			t.Errorf("%s: %v %q", tc.dir, isErr, out)
		}
	}
	if out, isErr := call(t, cs, "new", map[string]any{"name": "42", "dir": "/w/lib"}); !isErr || !strings.Contains(out, "already exists") {
		t.Errorf("duplicate: %v %q", isErr, out)
	}
}

func TestAbsDirTakesTildeAsTheHome(t *testing.T) {
	f := newFakes()
	f.dirs["/home/dev/work/app"] = true
	d := f.deps()
	for dir, want := range map[string]string{"~/work/app": "/home/dev/work/app", "/w/app/../lib/": "/w/lib"} {
		if got, err := absDir(d, dir); err != nil || got != want {
			t.Errorf("%s: %q %v", dir, got, err)
		}
	}
	d.home = func() (string, error) { return "", errors.New("$HOME is not defined") }
	if _, err := absDir(d, "~"); err == nil || asError(err).Code != ExitEnvironment {
		t.Errorf("no home: %v", err)
	}
}

func TestMCPGoDocksTheAgentAndShowsTheDashboardWhereItIsOpen(t *testing.T) {
	f := goFakes()
	cs := mcpSession(t, f)
	// No terminal shows the dashboard: the agent waits docked in it.
	out, isErr := call(t, cs, "go", map[string]any{"name": "a"})
	if isErr || out != "docked a; no terminal shows the dashboard: run hq in a terminal to see it\n" || f.tmux.docked != "@4" || f.tmux.attached != "" {
		t.Fatalf("%v %q docked %q attached %q", isErr, out, f.tmux.docked, f.tmux.attached)
	}
	// Claude Desktop's directory means nothing: the New agent dialog
	// starts from the home.
	if f.tmux.session[startedValue] != "/home/dev" {
		t.Errorf("started from %q", f.tmux.session[startedValue])
	}
	// A terminal shows it: that window comes to the front.
	f.tmux.clients = []string{"/dev/ttys004"}
	if out, _ := call(t, cs, "go", map[string]any{"name": "a"}); out != "docked a in the open dashboard\n" || !slices.Equal(f.raised, []string{"/dev/ttys004"}) {
		t.Fatalf("%q raised %q", out, f.raised)
	}
}

func TestGoWithoutATerminalLeavesTheAgentDocked(t *testing.T) {
	f := goFakes()
	f.tty = false
	if code, out, _ := f.run("go", "a"); code != 0 || f.tmux.attached != "" || f.tmux.docked != "@4" || !strings.Contains(out, "run hq in a terminal") {
		t.Fatalf("exit %d %q attached %q", code, out, f.tmux.attached)
	}
}

func TestMCPKillEndsTheAgentOnlyOnTheUsersWord(t *testing.T) {
	f := goFakes()
	cs := mcpSession(t, f)
	// Without the user's word nothing ends, and the supervisor is told to ask.
	for _, args := range []map[string]any{{"name": "a"}, {"name": "a", "confirmed": false}} {
		out, isErr := call(t, cs, "kill", args)
		if !isErr || out != "hq: a not killed: ask the user whether to end it, then call kill with confirmed set to true\n" || len(f.tmux.windows) != 2 {
			t.Fatalf("%v: %v %q, windows %+v", args, isErr, out, f.tmux.windows)
		}
	}
	if out, isErr := call(t, cs, "kill", map[string]any{"name": "a", "confirmed": true}); isErr || out != "killed a; the sandbox stays\n" {
		t.Fatalf("%v %q", isErr, out)
	}
	if len(f.tmux.windows) != 1 {
		t.Fatalf("windows left %+v", f.tmux.windows)
	}
	if out, isErr := call(t, cs, "kill", map[string]any{"name": "a", "confirmed": true}); !isErr || out != "hq: no agent 'a' (see hq ls)\n" {
		t.Fatalf("again: %v %q", isErr, out)
	}
}

func TestMCPToolsReportAMissingTmux(t *testing.T) {
	f := newLsFakes()
	f.tmux.missing = true
	cs := mcpSession(t, f)
	if out, isErr := call(t, cs, "list", nil); !isErr || !strings.HasPrefix(out, "hq: tmux not found") {
		t.Fatalf("%v %q", isErr, out)
	}
}

func TestToolResultOfAnErrorOfNoKind(t *testing.T) {
	r, _, _ := toolResult("partial\n", errors.New("boom"))
	if !r.IsError || r.Content[0].(*mcp.TextContent).Text != "partial\nhq: boom\n" {
		t.Fatalf("%+v", r)
	}
	r, _, _ = toolResult("", &Error{Code: ExitUsage})
	if !r.IsError || r.Content[0].(*mcp.TextContent).Text != "" {
		t.Fatalf("%+v", r)
	}
}

func TestMCPServesOnlyAClient(t *testing.T) {
	f := newFakes()
	if code, _, errOut := f.run("mcp"); code != ExitUsage || f.served != nil || !strings.Contains(errOut, "hq mcp install") {
		t.Fatalf("from a terminal: exit %d %q", code, errOut)
	}
	f.tty = false
	if code, _, errOut := f.run("mcp"); code != 0 || f.served == nil {
		t.Fatalf("exit %d %q", code, errOut)
	}
	f.serveErr = errors.New("broken pipe")
	if code, _, errOut := f.run("mcp"); code != ExitEnvironment || errOut != "hq: mcp: broken pipe\n" {
		t.Fatalf("exit %d %q", code, errOut)
	}
	if code, _, errOut := f.run("mcp", "install", "now"); code != ExitUsage || errOut != "hq: unexpected argument 'now' (usage: hq mcp [install])\n" {
		t.Fatalf("exit %d %q", code, errOut)
	}
	if code, _, errOut := f.run("mcp", "serve"); code != ExitUsage || errOut != "hq: unexpected argument 'serve' (usage: hq mcp [install])\n" {
		t.Fatalf("exit %d %q", code, errOut)
	}
}

func TestMCPInstallSetsHqUpInClaudeDesktop(t *testing.T) {
	f := newFakes()
	f.exe = "/Users/dev/.local/bin/hq"
	f.desktop = "/Users/dev/Library/Application Support/Claude/claude_desktop_config.json"
	f.env = map[string]string{"PATH": "/opt/homebrew/bin:/usr/bin", "HQ_TMUX_SOCKET": "hq-dev", "HOME": "/Users/dev", "GITHUB_TOKEN": "secret"}
	f.installResult = desktop.Result{Outcome: desktop.Added, Backup: f.desktop + ".hq-backup-20260927-120000"}
	code, out, errOut := f.run("mcp", "install")
	want := "added hq to Claude Desktop (" + f.desktop + ")\nthe file as it was: " + f.desktop + ".hq-backup-20260927-120000\nquit and reopen Claude Desktop to load it\n"
	if code != 0 || out != want {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	s := f.installed[0]
	if s.Command != f.exe || !slices.Equal(s.Args, []string{"mcp"}) || len(s.Env) != 2 || s.Env["PATH"] != "/opt/homebrew/bin:/usr/bin" || s.Env["HQ_TMUX_SOCKET"] != "hq-dev" {
		t.Fatalf("server %+v", s)
	}
	f.installResult = desktop.Result{Outcome: desktop.Unchanged}
	if _, out, _ := f.run("mcp", "install"); out != "hq is already set up in Claude Desktop ("+f.desktop+")\n" {
		t.Fatalf("again %q", out)
	}
	f.installResult = desktop.Result{Outcome: desktop.Updated, Backup: "/b"}
	if _, out, _ := f.run("mcp", "install"); out != "updated hq in Claude Desktop ("+f.desktop+")\nthe file as it was: /b\nquit and reopen Claude Desktop to load it\n" {
		t.Fatalf("update %q", out)
	}
}

func TestMCPInstallPrintsTheEntryWhenItCannotEditTheFile(t *testing.T) {
	f := newFakes()
	f.exe = "/Users/dev/.local/bin/hq"
	f.env = map[string]string{"PATH": "/usr/bin"}
	f.desktop = "/c.json"
	f.installErr = errors.New("not valid JSON")
	code, out, errOut := f.run("mcp", "install")
	var snippet map[string]map[string]desktop.Server
	if code != ExitEnvironment || json.Unmarshal([]byte(out), &snippet) != nil || snippet["mcpServers"]["hq"].Command != f.exe ||
		errOut != "hq: cannot edit /c.json safely (not valid JSON); add the entry above to it by hand, then quit and reopen Claude Desktop\n" {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	f.desktop = "/Users/dev/Library/Application Support/Claude/claude_desktop_config.json"
	f.installErr = desktop.ErrNoFolder
	code, out, errOut = f.run("mcp", "install")
	if code != ExitEnvironment || json.Unmarshal([]byte(out), &snippet) != nil || snippet["mcpServers"]["hq"].Command != f.exe ||
		errOut != "hq: Claude Desktop not found: there is no /Users/dev/Library/Application Support/Claude; install Claude Desktop and open it once, then run hq mcp install again (or add the entry above to its configuration by hand)\n" {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	f.desktopErr = errors.New("Windows gave no %APPDATA%")
	if code, out, errOut := f.run("mcp", "install"); code != ExitEnvironment || !strings.Contains(out, `"mcpServers"`) || !strings.HasPrefix(errOut, "hq: cannot find Claude Desktop's configuration (Windows gave no %APPDATA%)") {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	f.exe = ""
	d := f.deps()
	d.executable = func() (string, error) { return "", errors.New("no /proc") }
	if err := runMCPInstall(Env{Stdout: new(strings.Builder)}, d); asError(err).Code != ExitEnvironment {
		t.Fatalf("%v", err)
	}
}
