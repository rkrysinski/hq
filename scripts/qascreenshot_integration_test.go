//go:build integration

package scripts_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A screenshot is checked by no gate once it is an image, so
// scripts/qa-screenshot.sh checks the pane's text against the pattern list
// before it renders anything (#277).

// screenshotPane is a tmux server of the test's own whose one pane, width
// columns wide, shows text; it is killed when the test ends.
type screenshotPane struct {
	t      *testing.T
	socket string
}

func newScreenshotPane(t *testing.T, width int, text string) *screenshotPane {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	needGo(t)
	p := &screenshotPane{t: t, socket: fmt.Sprintf("qa-shot-%d-%d", os.Getpid(), ids.Add(1))}
	file := filepath.Join(t.TempDir(), "text")
	write(t, file, text, 0o644)
	if out, err := exec.Command("tmux", "-u", "-L", p.socket, "-f", "/dev/null", "new-session", "-d",
		"-x", fmt.Sprint(width), "-y", "6", "-s", "qa", "cat '"+file+"'; printf END; exec sleep 600").CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", p.socket, "kill-server").Run() })
	deadline := time.Now().Add(10 * time.Second)
	for {
		out, _ := exec.Command("tmux", "-u", "-L", p.socket, "capture-pane", "-p", "-t", "qa:").Output()
		if strings.Contains(string(out), "END") {
			return p
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pane did not show its text within 10s:\n%s", out)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// shoot runs scripts/qa-screenshot.sh on the pane with env added to the
// environment (and no pattern list of the machine's own in the variable),
// and a go in front of the real one that writes the text it would render to
// OUT.png instead of running the renderer. It returns the exit code, the
// output and what was written to OUT.png ("" when nothing was).
func (p *screenshotPane) shoot(env ...string) (int, string, string) {
	t := p.t
	t.Helper()
	realGo, _ := exec.LookPath("go")
	stubs := t.TempDir()
	write(t, filepath.Join(stubs, "go"), `#!/bin/sh
case "$2" in
github.com/charmbracelet/freeze@*)
    while [ $# -gt 0 ]; do
        [ "$1" = -o ] && { out=$2; shift; }
        in=$1; shift
    done
    exec cp "$in" "$out" ;;
esac
exec '`+realGo+`' "$@"
`, 0o755)
	out := filepath.Join(t.TempDir(), "shot.png")
	cmd := exec.Command(filepath.Join(repoRoot(t), "scripts", "qa-screenshot.sh"), "-L", p.socket, "qa:", out)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HQ_PRIVATE_PATTERNS=") && !strings.HasPrefix(kv, "PATH=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, append([]string{"PATH=" + stubs + string(os.PathListSeparator) + os.Getenv("PATH")}, env...)...)
	output, err := cmd.CombinedOutput()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	image, err := os.ReadFile(out)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return code, string(output), string(image)
}

func TestQAScreenshotRendersAPaneThatPassesThePrivateCheck(t *testing.T) {
	p := newScreenshotPane(t, 40, "hello world\n")
	code, out, image := p.shoot("HQ_PRIVATE_PATTERNS=acme")
	if code != 0 || !strings.Contains(out, "private: 1 patterns, 0 matching lines") || !strings.Contains(image, "hello world") {
		t.Fatalf("exit %d, rendered %q\n%s", code, image, out)
	}
}

func TestQAScreenshotRendersNothingFromAPaneItCannotClear(t *testing.T) {
	for _, c := range []struct {
		name, list, text string
		width            int
		want             []string // in the output
		notWant          string   // in the output, ignoring case
	}{
		{
			name: "a private detail on a row", list: "acme", width: 40,
			text: "clean\nwe ship for ACME\n",
			want: []string{
				"FAIL: image-rows:2 matches private pattern 1",
				"the pane shows a private detail",
			},
			notWant: "for acme",
		},
		{
			name: "a private detail split by colours", list: "acme", width: 40,
			text: "AC\x1b[1mME\x1b[0m\n",
			want: []string{
				"private: 1 patterns, 2 matching lines",
				"FAIL: image-rows:1 matches private pattern 1",
			},
		},
		{
			// 20 columns: "acme" ends the first row, "-corp" starts the next.
			name: "a private detail split over a wrapped row", list: "acme-corp", width: 20,
			text: "xxxxxxxxxxxxxxxxacme-corp\n",
			want: []string{
				"private: 1 patterns, 1 matching lines",
				"FAIL: pane-lines:1 matches private pattern 1",
				"the pane shows a private detail",
			},
			notWant: "acme-corp",
		},
		{
			name: "no pattern list", list: "# only a comment", width: 40,
			text: "we ship for ACME\n",
			want: []string{
				"no pattern list, so the pane cannot be checked for private details",
				`put the list in "$(git rev-parse --git-common-dir)/info/hq-private-patterns"`,
			},
		},
		{
			name: "a list the gate cannot read", list: "(unclosed", width: 40,
			text: "fine\n",
			want: []string{
				"pattern 1 is not a valid regular expression",
				"the pane could not be checked for private details",
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := newScreenshotPane(t, c.width, c.text)
			code, out, image := p.shoot("HQ_PRIVATE_PATTERNS=" + c.list)
			if code != 1 || image != "" {
				t.Fatalf("exit %d, rendered %q\n%s", code, image, out)
			}
			for _, w := range c.want {
				if !strings.Contains(out, w) {
					t.Errorf("output lacks %q:\n%s", w, out)
				}
			}
			if c.notWant != "" && strings.Contains(strings.ToLower(out), c.notWant) {
				t.Errorf("output repeats %q:\n%s", c.notWant, out)
			}
		})
	}
}
