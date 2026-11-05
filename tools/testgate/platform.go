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
			// Ignoring case: Windows does, so CMD.EXE runs cmd.exe.
			m[n.name] = regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(n.name) + `\b`)
		}
	}
	return m
}()

// scanPlatform reports every use of a platform name in the Go source file
// rel: a listed name inside a string literal, a listed selector in code
// (a bare identifier, when the file dot-imports the package).
func scanPlatform(rel string, src []byte) ([]platformHit, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	// The import path each package name of this file stands for, and the
	// paths it imports with a dot.
	imported := map[string]string{}
	var dotted []string
	for _, imp := range file.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		local := path[strings.LastIndex(path, "/")+1:]
		if imp.Name != nil {
			local = imp.Name.Name
		}
		if local == "." {
			dotted = append(dotted, path)
		}
		imported[local] = path
	}
	var hits []platformHit
	hit := func(pos token.Pos, name string) {
		hits = append(hits, platformHit{rel, fset.Position(pos).Line, name})
	}
	selector := func(pos token.Pos, path, sel string) {
		for _, n := range platformNames {
			if n.selector && n.name == path+"."+sel {
				hit(pos, n.name)
			}
		}
	}
	selected := map[*ast.Ident]bool{} // the right-hand side of a selector
	ast.Inspect(file, func(node ast.Node) bool {
		switch x := node.(type) {
		case *ast.BasicLit:
			if x.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(x.Value)
			if err != nil {
				s = x.Value
			}
			for _, n := range platformNames {
				if !n.selector && platformPatterns[n.name].MatchString(s) {
					hit(x.Pos(), n.name)
				}
			}
		case *ast.SelectorExpr:
			selected[x.Sel] = true
			if id, ok := x.X.(*ast.Ident); ok {
				if path, ok := imported[id.Name]; ok {
					selector(x.Pos(), path, x.Sel.Name)
				}
			}
		case *ast.Ident:
			if !selected[x] {
				for _, path := range dotted {
					selector(x.Pos(), path, x.Name)
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
