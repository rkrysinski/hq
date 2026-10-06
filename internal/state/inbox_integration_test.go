//go:build integration

package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rkrysinski/hq/internal/testutil"
)

// hookOutput is what the hook tells Claude when it delivers messages.
type hookOutput struct {
	Decision           string
	Reason             string
	TerminalSequence   string
	HookSpecificOutput struct{ HookEventName, AdditionalContext string }
}

func decodeOutput(t *testing.T, out string) hookOutput {
	t.Helper()
	var o hookOutput
	if out == "" {
		return o
	}
	if !strings.HasSuffix(out, "\n") || strings.Count(out, "\n") != 1 {
		t.Fatalf("not one line of JSON: %q", out)
	}
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatalf("%q: %v", out, err)
	}
	return o
}

// post leaves messages of the user's for id, a millisecond apart so their
// order is sure.
func post(t *testing.T, root, id string, now bool, texts ...string) {
	t.Helper()
	postFrom(t, root, id, false, now, texts...)
}

// postFrom leaves messages for id as post does, the supervisor's when
// supervisor.
func postFrom(t *testing.T, root, id string, supervisor, now bool, texts ...string) {
	t.Helper()
	for _, text := range texts {
		if err := Post(root, id, Message{Text: text, Supervisor: supervisor}, now, time.Now()); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
}

// texts are the texts of messages taken out of an inbox.
func texts(taken []Message) []string {
	var out []string
	for _, m := range taken {
		out = append(out, m.Text)
	}
	return out
}

// inboxLeft lists what is in id's inbox, dot files included.
func inboxLeft(root, id string) []string {
	entries, _ := os.ReadDir(InboxDir(root, id))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func labelled(texts ...string) string {
	for i, t := range texts {
		texts[i] = MessageLabel + t
	}
	return strings.Join(texts, "\n\n")
}

func TestPostTakeAndRemoveKeepTheOrderAndLeaveNothing(t *testing.T) {
	root := t.TempDir()
	const id = "0a1b2c3d"
	if Pending(root, id) != 0 {
		t.Fatal("pending before any message")
	}
	if taken, err := Take(root, id); err != nil || taken != nil {
		t.Fatalf("no inbox: %v %v", taken, err)
	}
	post(t, root, id, false, "first", `second "quoted" \ line`)
	postFrom(t, root, id, true, true, "third\nwith two lines")
	if n := Pending(root, id); n != 3 {
		t.Fatalf("pending %d", n)
	}
	// A file planted in the inbox is taken and dropped, never read through.
	dir := InboxDir(root, id)
	secret := filepath.Join(root, "secret")
	os.WriteFile(secret, []byte("the secret"), 0o600)
	os.Symlink(secret, filepath.Join(dir, "9999999999999999999-link"))
	os.WriteFile(filepath.Join(dir, "9999999999999999999-big"), []byte(strings.Repeat("x", maxFile+1)), 0o644)
	os.WriteFile(filepath.Join(dir, "9999999999999999999-bad"), []byte(`"`), 0o644)
	taken, err := Take(root, id)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(texts(taken), "|"); got != `first|second "quoted" \ line|third`+"\nwith two lines" {
		t.Fatalf("took %q", got)
	}
	// Each says who sent it: the last one the supervisor, through hq mcp.
	if len(taken) != 3 || taken[0].Supervisor || taken[1].Supervisor || !taken[2].Supervisor {
		t.Fatalf("senders: %+v", taken)
	}
	if left := inboxLeft(root, id); len(left) != 0 || Pending(root, id) != 0 {
		t.Fatalf("left %v", left)
	}
	if b, err := os.ReadFile(secret); err != nil || string(b) != "the secret" {
		t.Fatal("the link's target was touched")
	}
	// Remove takes the inbox with what still waits in it, and only that
	// agent's.
	post(t, root, id, false, "never delivered")
	post(t, root, "0a1b2c3e", false, "for another agent")
	if err := Remove(root, id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("inbox left: %v", err)
	}
	if Pending(root, "0a1b2c3e") != 1 {
		t.Fatal("another agent's inbox went too")
	}
}

func TestHookBlocksTheStopWithTheMessagesAndStaysQuiet(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	const id = "0a1b2c3d4e5f"
	stop := func(payload string) hookOutput {
		t.Helper()
		return decodeOutput(t, run(t, root, root, id, hook("stop", "[notify %s]"), fixture(t, payload)))
	}
	fire(t, root, root, id, "prompt", fixture(t, "prompt"))
	post(t, root, id, false, "also run the linter", `and say "done"`)

	// Stop blocked: Claude goes on with the messages, oldest first; the
	// agent stays working and nobody is notified.
	o := stop("stop-to-block")
	if o.Decision != "block" || o.Reason != labelled("also run the linter", `and say "done"`) || o.TerminalSequence != "" {
		t.Fatalf("blocked stop: %+v", o)
	}
	if r, _ := Read(root, id); r.State != Working {
		t.Fatalf("after the blocked stop: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(Dir(root), id+".stop")); !os.IsNotExist(err) {
		t.Fatal("the blocked stop was kept as the last stop")
	}
	if left := inboxLeft(root, id); len(left) != 0 {
		t.Fatalf("inbox left %v", left)
	}
	// A message sent while Claude answered blocks the next stop too, though
	// a stop hook is already active: each block takes its messages, so it
	// cannot loop.
	post(t, root, id, false, "one more")
	if o := stop("stop-after-block"); o.Decision != "block" || o.Reason != labelled("one more") {
		t.Fatalf("second block: %+v", o)
	}
	// Nothing left: the stop is reported and notified once.
	if o := stop("stop-after-block"); o.Decision != "" || o.TerminalSequence != "[notify Done: main]" {
		t.Fatalf("the stop after: %+v", o)
	}
	if r, _ := Read(root, id); r.State != Done || r.Last != "PINEAPPLE" {
		t.Fatalf("done: %+v", r)
	}
}

func TestHookDeliversAfterAToolOnlyWhenAMessageAsksForIt(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	const id = "0a1b2c3d4e5f"
	answer := func(payload []byte) hookOutput {
		t.Helper()
		return decodeOutput(t, run(t, root, root, id, hook("answer", ""), payload))
	}
	fire(t, root, root, id, "prompt", fixture(t, "prompt"))
	post(t, root, id, false, "when you stop")
	if o := answer(fixture(t, "post-tool")); o != (hookOutput{}) || Pending(root, id) != 1 {
		t.Fatalf("delivered without --now: %+v", o)
	}
	post(t, root, id, true, "right now")
	// Not a subagent's tool, nor one that failed: Claude would not see it.
	failed := []byte(strings.Replace(string(fixture(t, "post-tool")), `"PostToolUse"`, `"PostToolUseFailure"`, 1))
	for name, payload := range map[string][]byte{"subagent": fixture(t, "post-tool-subagent"), "failed": failed} {
		if o := answer(payload); o != (hookOutput{}) || Pending(root, id) != 2 {
			t.Fatalf("%s: %+v, pending %d", name, o, Pending(root, id))
		}
	}
	// With it, everything waiting goes, oldest first.
	o := answer(fixture(t, "post-tool"))
	if o.HookSpecificOutput.HookEventName != "PostToolUse" || o.HookSpecificOutput.AdditionalContext != labelled("when you stop", "right now") {
		t.Fatalf("after the tool: %+v", o)
	}
	if Pending(root, id) != 0 {
		t.Fatal("left pending")
	}
	// The turn goes on, working with the age it had.
	if r, _ := Read(root, id); r.State != Working {
		t.Fatalf("%+v", r)
	}
}

func TestHookPassesTheMessagesOnWithAPromptFromAWorktree(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	wt := filepath.Join(filepath.Dir(root), "feat-42")
	testutil.Git(t, root, "worktree", "add", "-q", "-b", "feat-42", wt)
	const id = "0a1b2c3d4e5f"
	post(t, root, id, false, "ride along")
	o := decodeOutput(t, run(t, wt, root, id, hook("prompt", ""), fixture(t, "prompt-pasted")))
	if o.HookSpecificOutput.HookEventName != "UserPromptSubmit" || o.HookSpecificOutput.AdditionalContext != labelled("ride along") {
		t.Fatalf("%+v", o)
	}
	if r, _ := Read(root, id); r.State != Working || r.Branch != "feat-42" {
		t.Fatalf("%+v", r)
	}
	// Nothing waiting, or no inbox at all: the hook prints nothing.
	fire(t, wt, root, id, "prompt", fixture(t, "prompt"))
	os.RemoveAll(InboxDir(root, id))
	for kind, payload := range map[string]string{"prompt": "prompt", "answer": "post-tool", "stop": "stop-to-block"} {
		fire(t, wt, root, id, kind, fixture(t, payload))
	}
}

func TestEachMessageIsDeliveredOnceWhenHqAndTheHookRace(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	const id = "0a1b2c3d4e5f"
	var sent []string
	for i := 0; i < 40; i++ {
		sent = append(sent, fmt.Sprintf("message %02d", i))
	}
	post(t, root, id, true, sent...)
	var (
		mu  sync.Mutex
		got []string
		wg  sync.WaitGroup
	)
	add := func(texts ...string) {
		mu.Lock()
		got = append(got, texts...)
		mu.Unlock()
	}
	for w := 0; w < 3; w++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			taken, err := Take(root, id)
			if err != nil {
				t.Error(err)
			}
			add(texts(taken)...)
		}()
		go func() {
			defer wg.Done()
			o := decodeOutput(t, run(t, root, root, id, hook("stop", ""), fixture(t, "stop-to-block")))
			if o.Reason != "" {
				for _, m := range strings.Split(o.Reason, "\n\n") {
					add(strings.TrimPrefix(m, MessageLabel))
				}
			}
		}()
	}
	wg.Wait()
	sort.Strings(got)
	if strings.Join(got, "|") != strings.Join(sent, "|") {
		t.Fatalf("delivered %d of %d: %v", len(got), len(sent), got)
	}
	if left := inboxLeft(root, id); len(left) != 0 {
		t.Fatalf("left %v", left)
	}
}
