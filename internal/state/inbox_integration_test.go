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

func TestOldestIsWhenTheFirstMessageWaitingWasSent(t *testing.T) {
	root := t.TempDir()
	const id = "0a1b2c3d"
	if _, ok := Oldest(root, id); ok {
		t.Fatal("a time with no inbox")
	}
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	for _, sent := range []time.Time{at.Add(time.Second), at, at.Add(time.Minute)} {
		if err := Post(root, id, Message{Text: "hi"}, false, sent); err != nil {
			t.Fatal(err)
		}
	}
	if got, ok := Oldest(root, id); !ok || !got.Equal(at) {
		t.Fatalf("oldest %v %v", got, ok)
	}
	if _, err := Take(root, id); err != nil {
		t.Fatal(err)
	}
	if _, ok := Oldest(root, id); ok {
		t.Fatal("a time once taken")
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
	if r, _ := reported(root, id); r.State != Working {
		t.Fatalf("after the blocked stop: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(Dir(root), id+endedSuffix)); !os.IsNotExist(err) {
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
	if r, _ := reported(root, id); r.State != Done || r.Last != "PINEAPPLE" {
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
	if r, _ := reported(root, id); r.State != Working {
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
	if r, _ := reported(root, id); r.State != Working || r.Branch != "feat-42" {
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

func TestHeldMessagesWaitForHqAloneInTheOrderSent(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	const id = "0a1b2c3d4e5f"
	fire(t, root, root, id, "prompt", fixture(t, "prompt"))
	post(t, root, id, false, "first")
	postFrom(t, root, id, true, false, "/probe second")
	post(t, root, id, false, "third")
	taken, err := Take(root, id)
	if err != nil || len(taken) != 3 {
		t.Fatalf("took %+v %v", taken, err)
	}
	// hq types the first in and holds the others for the agent's next
	// prompt (#173).
	if err := Hold(root, id, taken[1:]); err != nil {
		t.Fatal(err)
	}
	if n, h := Pending(root, id), Held(root, id); n != 2 || h != 2 {
		t.Fatalf("pending %d, held %d, want the 2 held", n, h)
	}
	if got, ok := Oldest(root, id); !ok || !got.Equal(taken[1].Sent) {
		t.Fatalf("oldest %v %v, want %v", got, ok, taken[1].Sent)
	}
	// The hooks leave held messages alone and deliver only the others.
	post(t, root, id, false, "sent meanwhile")
	if o := decodeOutput(t, run(t, root, root, id, hook("stop", "[notify %s]"), fixture(t, "stop-to-block"))); o.Decision != "block" || o.Reason != labelled("sent meanwhile") {
		t.Fatalf("stop: %+v", o)
	}
	post(t, root, id, false, "fourth")
	taken, err = Take(root, id)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(texts(taken), "|"); got != "/probe second|third|fourth" {
		t.Fatalf("took %q", got)
	}
	if !taken[0].Supervisor || taken[1].Supervisor {
		t.Fatalf("senders: %+v", taken)
	}
	if Pending(root, id) != 0 {
		t.Fatal("left after the take")
	}
	// A message without its time is held as sent now, after the others.
	if err := Hold(root, id, []Message{{Text: "no time"}}); err != nil {
		t.Fatal(err)
	}
	if got, ok := Oldest(root, id); !ok || time.Since(got) > time.Minute {
		t.Fatalf("held without a time: %v %v", got, ok)
	}
	if err := Hold(root, "not an id", taken); err == nil || Held(root, "not an id") != 0 {
		t.Fatal("held for a bad id")
	}
	// A hold that fails holds none of them.
	held := filepath.Join(InboxDir(root, id), "held")
	os.RemoveAll(held)
	os.WriteFile(held, nil, 0o644)
	if err := Hold(root, id, taken); err == nil || Held(root, id) != 0 {
		t.Fatal("held where the held directory cannot be")
	}
}

// Taken, held and put back, a message keeps the name it was left under, so
// every delivery of it names it as hq send left it (#195): the delivery
// event of hq typing it in, and of the hook.
func TestAMessageKeepsItsNameWhenHeldOrPutBack(t *testing.T) {
	root := testutil.GitRepo(t, "app")
	const id = "0a1b2c3d4e5f"
	post(t, root, id, true, "first")
	post(t, root, id, false, "/probe second")
	names := messages(InboxDir(root, id))
	taken, err := Take(root, id)
	if err != nil || len(taken) != 2 || taken[0].Name != names[0] || taken[1].Name != names[1] {
		t.Fatalf("took %+v %v, names %v", taken, err, names)
	}
	if err := Hold(root, id, taken[1:]); err != nil {
		t.Fatal(err)
	}
	if got := messages(filepath.Join(InboxDir(root, id), "held")); len(got) != 1 || got[0] != names[1] {
		t.Fatalf("held %v", got)
	}
	// A held message of that name is there already: none is held twice.
	if err := Hold(root, id, taken[1:]); err == nil || !strings.Contains(err.Error(), "held already") {
		t.Fatalf("held twice: %v", err)
	}
	// The first put back after a failed typing, the held one taken again.
	if err := Post(root, id, taken[0], false, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := messages(InboxDir(root, id)); len(got) != 1 || got[0] != names[0] || !strings.HasSuffix(got[0], ".now") {
		t.Fatalf("put back %v, want %s", got, names[0])
	}
	if err := Post(root, id, taken[0], false, time.Now()); err == nil {
		t.Fatal("put back over a message of its name")
	}
	again, err := Take(root, id)
	if err != nil || len(again) != 2 || again[0].Name != names[0] || again[1].Name != names[1] || again[0].Text != "first" {
		t.Fatalf("took again %+v %v", again, err)
	}
}
