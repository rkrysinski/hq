package cli

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
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

// sourceFiles are this package's files but its tests, parsed into fset.
func sourceFiles(t *testing.T, fset *token.FileSet) []*ast.File {
	t.Helper()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	return files
}

// exitConstants are this package's Exit* constants with their values,
// read from its source, not from a list.
func exitConstants(t *testing.T) map[string]int64 {
	t.Helper()
	isExit := regexp.MustCompile(`^Exit[A-Z]`).MatchString
	fset := token.NewFileSet()
	// The const blocks that hold an Exit* constant, checked on their own,
	// so iota and constants of the same block still count.
	consts := &ast.File{Name: ast.NewIdent("cli")}
	for _, f := range sourceFiles(t, fset) {
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
	byCode := map[int64]string{}
	for _, name := range slices.Sorted(maps.Keys(codes)) {
		code := codes[name]
		if other, ok := byCode[code]; ok {
			t.Errorf("%s and %s are both %d, so hq help cannot name each", other, name, code)
		}
		byCode[code] = name
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

func TestNamesWordMatchesWholeWordsOnly(t *testing.T) {
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
			t.Errorf("namesWord(%q, %q) = %v, want %v", tc.text, tc.word, got, tc.want)
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

// TestFlagsAreRecognisedOnlyThroughTheirDeclaration fails for a comparison
// with a declared flag's spelling (a == "--json", case "--json":) anywhere
// in the package but through flagSet.is, which checks the declaration.
func TestFlagsAreRecognisedOnlyThroughTheirDeclaration(t *testing.T) {
	declared := map[string]bool{}
	for _, c := range append(commands(), versionCommand()) {
		for _, flag := range c.flags {
			declared[strconv.Quote(flag)] = true
		}
	}
	fset := token.NewFileSet()
	for _, f := range sourceFiles(t, fset) {
		ast.Inspect(f, func(n ast.Node) bool {
			var compared []ast.Expr
			switch n := n.(type) {
			case *ast.BinaryExpr:
				if n.Op == token.EQL || n.Op == token.NEQ {
					compared = []ast.Expr{n.X, n.Y}
				}
			case *ast.CaseClause:
				compared = n.List
			}
			for _, e := range compared {
				if lit, ok := e.(*ast.BasicLit); ok && declared[lit.Value] {
					t.Errorf("%s: compares with %s, not through flagSet.is", fset.Position(lit.Pos()), lit.Value)
				}
			}
			return true
		})
	}
}

// composeRefusal are the functions of this package that compose the
// refusal of a name no agent has themselves, by file and name, each with
// its reason: the commands that read the agents from their journals with
// no look (spec #336, O1). Every other command takes the refusal from the
// look (agent.Seen.Named).
var composeRefusal = map[string]string{
	"go.go runGo":             "hq go reads the agents from their journals, with no look",
	"code.go openCode":        "hq code reads the agents from their journals, with no look",
	"kill.go runKill":         "hq kill reads the agents from their journals, with no look",
	"dialog.go runKillDialog": "the dashboard's Kill window reads the agents from their journals, with no look",
	"dash.go dockAs":          "docking the row pointed at reads the agents from their journals, with no look",
}

// composedRefusals are the places in files where the text of the refusal
// of a name no agent has ("no agent '") stands outside the functions of
// composeRefusal, each as "FILE:LINE in FUNCTION" (no function for one
// outside every function).
func composedRefusals(fset *token.FileSet, files []*ast.File) []string {
	var found []string
	for _, f := range files {
		file := filepath.Base(fset.Position(f.Pos()).Filename)
		for _, decl := range f.Decls {
			fn := ""
			if d, ok := decl.(*ast.FuncDecl); ok {
				fn = d.Name.Name
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING || !strings.Contains(lit.Value, "no agent '") {
					return true
				}
				if _, exempt := composeRefusal[file+" "+fn]; !exempt {
					found = append(found, fmt.Sprintf("%s:%d in %s", file, fset.Position(lit.Pos()).Line, fn))
				}
				return true
			})
		}
	}
	return found
}

// No command but those that take no look composes the refusal of a name
// no agent has: every other one takes it from the look (agent.Seen.Named),
// so the rule has one place and a command that composes it again, such as
// hq ls NAME..., fails the build (#336, story 18). The check sees a refusal
// composed in a command the look serves, and one outside every function.
func TestOnlyTheCommandsWithNoLookComposeTheRefusalOfAMissingName(t *testing.T) {
	fset := token.NewFileSet()
	if found := composedRefusals(fset, sourceFiles(t, fset)); len(found) != 0 {
		t.Errorf("the refusal of a missing name composed at %v: take it from the look (agent.Seen.Named), or add the function to composeRefusal with its reason", found)
	}
	var files []*ast.File
	for name, src := range map[string]string{
		"ls.go":   "package cli\n\nfunc runLs(name string) error {\n\treturn notFoundErr(\"no agent '%s' (see hq ls)\", name)\n}\n",
		"send.go": "package cli\n\nconst refusal = `no agent '`\n",
		"go.go":   "package cli\n\nfunc runGo(name string) error {\n\treturn notFoundErr(\"no agent '%s' (see hq ls)\", name)\n}\n",
	} {
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	found := composedRefusals(fset, files)
	slices.Sort(found)
	if want := []string{"ls.go:4 in runLs", "send.go:3 in "}; !slices.Equal(found, want) {
		t.Fatalf("found %q, want %q", found, want)
	}
}
