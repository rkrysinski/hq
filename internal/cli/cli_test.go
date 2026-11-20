package cli

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"regexp"
	"slices"
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
	for _, want := range []string{"hq dash", "hq new NAME [DIR] [PROMPT]", "hq new also takes --setup NAME", "hq sandbox also takes --setup NAME", "hq mcp [install] [claude-desktop|claude-code|sbx] | load|unload REPO", "hq mcp load and unload take REPO and those flags", "hq ls [--json]", "hq go NAME", "hq send NAME TEXT [--now] [--wait [--timeout DURATION]] [--json]", "hq send --wait takes --timeout DURATION too, as hq wait does, counted from the start of the call.", "Exit codes: 0 success, 1 usage or refused, 2 not found, 3 environment, 4 stalled (hq send --wait).", "--wait returns when the turn that took it ends", "hq answer NAME --dialog ID ANSWER...|--cancel [--json]", "hq kill NAME [-y]", "hq stop [-y]", "hq sandbox rm|restart REPO|SANDBOX", "hq update", "hq help", "hq --version", "hq -V", "hq --help", "hq -h", "--yes", "--setup=NAME", "(--setup NAME, --all-setups)", "NAME: letters, digits, '-', '_' and '.', at least one letter or digit, at most 32 characters"} {
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

func TestHelpFlagsPrintTheHelp(t *testing.T) {
	_, help, _ := runCLI("help")
	for _, flag := range []string{"--help", "-h"} {
		if code, out, _ := runCLI(flag); code != ExitOK || out != help {
			t.Errorf("hq %s: exit %d, output %q, want hq help's", flag, code, out)
		}
	}
	if code, out, _ := runCLI("-V"); code != ExitOK || out != "hq dev\n" {
		t.Errorf("hq -V: exit %d, output %q, want hq --version's", code, out)
	}
}

// exitConstants are this package's Exit* constants with their values,
// read from its source, not from a list.
func exitConstants(t *testing.T) map[string]int64 {
	t.Helper()
	isExit := regexp.MustCompile(`^Exit[A-Z]`).MatchString
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	// The const blocks that hold an Exit* constant, checked on their own,
	// so iota and constants of the same block still count.
	consts := &ast.File{Name: ast.NewIdent("cli")}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			g, ok := d.(*ast.GenDecl)
			if ok && g.Tok == token.CONST && slices.ContainsFunc(g.Specs, func(s ast.Spec) bool {
				return slices.ContainsFunc(s.(*ast.ValueSpec).Names, func(n *ast.Ident) bool { return isExit(n.Name) })
			}) {
				consts.Decls = append(consts.Decls, g)
			}
		}
	}
	pkg, err := (&types.Config{}).Check("cli", fset, []*ast.File{consts}, nil)
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]int64{}
	for _, name := range pkg.Scope().Names() {
		if isExit(name) {
			codes[name], _ = constant.Int64Val(pkg.Scope().Lookup(name).(*types.Const).Val())
		}
	}
	return codes
}

func TestHelpNamesEveryExitCode(t *testing.T) {
	codes := exitConstants(t)
	if len(codes) == 0 {
		t.Fatal("found no Exit* constant")
	}
	for name, code := range codes {
		if !slices.ContainsFunc(exitCodes, func(e exitCode) bool { return int64(e.code) == code }) {
			t.Errorf("%s (%d) has no entry in exitCodes, so hq help does not name it", name, code)
		}
	}
	_, help, _ := runCLI("help")
	if !strings.Contains(help, "\n"+exitCodesLine()) {
		t.Errorf("hq help lacks the line of exit codes %q", exitCodesLine())
	}
}

// namesWord reports whether text names word as a word of its own: a flag, or
// hq and a command.
func namesWord(text, word string) bool {
	return regexp.MustCompile(`(^|[^\w-])` + regexp.QuoteMeta(word) + `($|[^\w-])`).MatchString(text)
}

func TestNamesMatchesWholeWordsOnly(t *testing.T) {
	for _, tc := range []struct {
		text, word string
		want       bool
	}{
		{"hq kill NAME [-y]", "-y", true},
		{"--yes", "-y", false},
		{"--setup=NAME", "--setup", true},
		{"--all-setups", "--setup", false},
		{"(also: hq -h)", "-h", true},
		{"--help", "-h", false},
		{"hq new also", "hq new", true},
		{"hq newer", "hq new", false},
	} {
		if got := namesWord(tc.text, tc.word); got != tc.want {
			t.Errorf("names(%q, %q) = %v, want %v", tc.text, tc.word, got, tc.want)
		}
	}
}

func TestHelpNamesEveryDeclaredFlag(t *testing.T) {
	_, help, _ := runCLI("help")
	lines := strings.Split(help, "\n")
	for _, c := range append(commands(), versionCommand()) {
		// The command's usage line, and every sentence that names it.
		about := slices.DeleteFunc(slices.Clone(lines), func(l string) bool { return !namesWord(l, "hq "+c.name) })
		for _, flag := range c.flags {
			if !slices.ContainsFunc(about, func(l string) bool { return namesWord(l, flag) }) {
				t.Errorf("hq %s takes %s, but hq help names it neither in its usage line nor in a sentence that names hq %s", c.name, flag, c.name)
			}
		}
	}
}

func TestEveryFlagIsDeclared(t *testing.T) {
	want := map[string][]string{
		"new":       {"--setup"},
		"ls":        {"--json"},
		"read":      {"--offset", "--since", "--json"},
		"explain":   {"--all", "--json"},
		"wait":      {"--since", "--timeout", "--json"},
		"send":      {"--now", "--wait", "--timeout", "--json"},
		"answer":    {"--dialog", "--cancel", "--json"},
		"kill":      {"-y", "--yes"},
		"stop":      {"-y", "--yes"},
		"sandbox":   {"--setup", "--all-setups", "-y", "--yes"},
		"mcp":       {"--setup", "--all-setups"},
		"help":      {"--help", "-h"},
		"--version": {"--version", "-V"},
	}
	for _, c := range append(commands(), versionCommand()) {
		got, w := slices.Sorted(slices.Values(c.flags)), slices.Sorted(slices.Values(want[c.name]))
		if !slices.Equal(got, w) {
			t.Errorf("hq %s declares %q, want %q", c.name, got, w)
		}
	}
}

func TestParsingAnUndeclaredFlagPanics(t *testing.T) {
	defer func() {
		if r := fmt.Sprint(recover()); !strings.Contains(r, "hq kill") || !strings.Contains(r, "--json") {
			t.Errorf("recovered %q, want a panic naming hq kill and --json", r)
		}
	}()
	flagsOf("kill").is("NAME", "-y", "--json")
}

func TestFlagSetIs(t *testing.T) {
	kill := flagsOf("kill")
	for _, tc := range []struct {
		arg  string
		want bool
	}{{"-y", true}, {"--yes", true}, {"NAME", false}, {"-yes", false}} {
		if got := kill.is(tc.arg, "-y", "--yes"); got != tc.want {
			t.Errorf("is(%q) = %v, want %v", tc.arg, got, tc.want)
		}
	}
	if flagsOf("nope").names != nil {
		t.Error("an unknown command declares flags")
	}
}
