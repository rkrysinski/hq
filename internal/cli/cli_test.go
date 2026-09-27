package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rkrysinski/hq/internal/version"
)

func runCLI(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = mainWith(args, Env{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errOut}, newFakes().deps())
	return code, out.String(), errOut.String()
}

func TestVersionPrintsBuildVersion(t *testing.T) {
	code, out, _ := runCLI("--version")
	if code != ExitOK || out != "hq dev\n" {
		t.Fatalf("got %d %q, want 0 \"hq dev\\n\"", code, out)
	}
}

func TestVersionPrintsReleaseTag(t *testing.T) {
	old := version.Version
	version.Version = "v1.2.3"
	defer func() { version.Version = old }()
	if _, out, _ := runCLI("--version"); out != "hq v1.2.3\n" {
		t.Fatalf("got %q", out)
	}
}

func TestHelpListsEveryCommand(t *testing.T) {
	code, out, _ := runCLI("help")
	if code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"hq dash", "hq new NAME [DIR] [PROMPT]", "hq ls [--json]", "hq go NAME", "hq kill NAME [-y]", "hq stop [-y]", "hq sandbox rm|restart REPO", "hq update", "hq help", "hq --version"} {
		if !strings.Contains(out, want) {
			t.Errorf("help lacks %q", want)
		}
	}
}

func TestUnknownCommandIsUsageError(t *testing.T) {
	code, out, errOut := runCLI("frobnicate")
	if code != ExitUsage {
		t.Fatalf("exit %d, want %d", code, ExitUsage)
	}
	if out != "" || errOut != "hq: unknown command 'frobnicate' (see hq help)\n" {
		t.Fatalf("stdout %q stderr %q", out, errOut)
	}
}

func TestErrorsCarryTheirExitCode(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
	}{
		{usageErr("u"), ExitUsage},
		{notFoundErr("n"), ExitNotFound},
		{envErr("e"), ExitEnvironment},
	} {
		if got := tc.err.(*Error).Code; got != tc.code {
			t.Errorf("%v: code %d, want %d", tc.err, got, tc.code)
		}
	}
}
