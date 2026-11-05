package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// platformName is a name only platform code uses (#157): a program or a
// variable of one platform, found in a string literal, or, when selector is
// set, a package-qualified identifier such as runtime.GOOS, found in code
// under whatever name the file imports the package as.
type platformName struct {
	name     string
	selector bool
}

// platformNames are the names that mark code as platform-specific: hq's
// rule is that such code lives in internal/platform, never scattered
// (AGENTS.md, design §3.10). Comments are not checked, and no other word
// is looked for: a message to the user about the system tray passes.
var platformNames = []platformName{
	{name: "cmd.exe"},
	{name: "powershell.exe"},
	{name: "wsl.exe"},
	{name: "wslpath"},
	{name: "sbx.exe"},
	{name: "WSL_DISTRO_NAME"},
	{name: "osascript"},
	{name: "runtime.GOOS", selector: true},
}

// platformExempt are the directories, relative to the root and with every
// directory under them, whose code may use the names; each says why.
var platformExempt = []struct{ dir, why string }{
	{"internal/platform", "the platform adapter: the one place that knows which platform hq runs on (design §3.10)"},
	{"internal/testutil", "the stubs that stand in for the WSL side in tests (sbx.exe, wslpath, cmd.exe) under the names hq looks for"},
	{"tools/testgate", "holds the list of names this gate looks for"},
}

// platformHit is a use of a platform name outside the platform code.
type platformHit struct {
	file string
	line int
	name string
}

func (h platformHit) String() string {
	return fmt.Sprintf("%s:%d uses %s: platform-specific code lives in internal/platform", h.file, h.line, h.name)
}

var platformPatterns = func() map[string]*regexp.Regexp {
	m := map[string]*regexp.Regexp{}
	for _, n := range platformNames {
		if !n.selector {
			m[n.name] = regexp.MustCompile(`\b` + regexp.QuoteMeta(n.name) + `\b`)
		}
	}
	return m
}()

// scanPlatform reports every use of a platform name in a Go source file:
// a listed name inside a string literal, a listed selector in code.
func scanPlatform(name string, src []byte) ([]platformHit, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	// The name each imported package goes by in this file.
	local := map[string]string{}
	for _, imp := range file.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		n := path[strings.LastIndex(path, "/")+1:]
		if imp.Name != nil {
			n = imp.Name.Name
		}
		local[n] = path
	}
	var hits []platformHit
	hit := func(pos token.Pos, n string) {
		hits = append(hits, platformHit{name, fset.Position(pos).Line, n})
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch x := node.(type) {
		case *ast.BasicLit:
			if x.Kind != token.STRING {
				return true
			}
			for _, n := range platformNames {
				if !n.selector && platformPatterns[n.name].MatchString(x.Value) {
					hit(x.Pos(), n.name)
				}
			}
		case *ast.SelectorExpr:
			id, ok := x.X.(*ast.Ident)
			if !ok {
				return true
			}
			path, imported := local[id.Name]
			if !imported {
				return true
			}
			for _, n := range platformNames {
				if n.selector && n.name == path+"."+x.Sel.Name {
					hit(x.Pos(), n.name)
				}
			}
		}
		return true
	})
	return hits, nil
}

// checkPlatform scans the Go files under root that are not tests, leaving
// out the exempt directories, hidden ones (.git, .claude and its worktrees)
// and testdata, and reports every use of a platform name, file paths
// relative to root.
func checkPlatform(root string) ([]platformHit, error) {
	var hits []platformHit
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			if path == root {
				return nil
			}
			if name := d.Name(); strings.HasPrefix(name, ".") || name == "testdata" || platformExemptDir(filepath.ToSlash(rel)) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		h, err := scanPlatform(rel, src)
		if err != nil {
			return err
		}
		hits = append(hits, h...)
		return nil
	})
	return hits, err
}

func platformExemptDir(rel string) bool {
	for _, e := range platformExempt {
		if rel == e.dir {
			return true
		}
	}
	return false
}
