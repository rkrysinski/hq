package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// The one place a test may call a method named Sees (#285): the helper of
// the state package's tests that first requires ScreenMayTell for the same
// fold at the same moment, so that no test passes a capture the look never
// meets (docs/hq/testing.md, the rule of #232).
const (
	seesHelperFile = "internal/state/seen_test.go"
	seesHelperFunc = "seesWhenItMayTell"
)

// seesHit is a use of a method named Sees outside the helper, or, with line
// 0 or why set, the helper missing or not asking ScreenMayTell.
type seesHit struct {
	file string
	line int
	why  string // "" for a use of Sees
}

func (h seesHit) String() string {
	if h.why != "" {
		return fmt.Sprintf("%s:%d %s: the gate exempts it as the one place a test calls Sees, so it must be there and first require ScreenMayTell",
			h.file, h.line, h.why)
	}
	return fmt.Sprintf("%s:%d uses Sees outside %s: get what a look sees through %s (%s), which first requires ScreenMayTell at that moment",
		h.file, h.line, seesHelperFunc, seesHelperFunc, seesHelperFile)
}

// seesHelper is what scanSees found of the helper in its file.
type seesHelper struct {
	found, asks bool
	line        int
}

// scanSees reports every use of a method named Sees in the Go source file
// rel, a call or the method taken as a value, whatever the receiver, outside
// the helper when rel is the helper's file, and what it found of the helper
// there. A function or a variable merely named Sees, or a function of an
// imported package, is no such use.
func scanSees(rel string, src []byte) ([]seesHit, seesHelper, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, src, 0)
	if err != nil {
		return nil, seesHelper{}, err
	}
	imports := map[string]bool{}
	for _, imp := range file.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		name := path.Base(p)
		if imp.Name != nil {
			name = imp.Name.Name
		}
		imports[name] = true
	}
	var hits []seesHit
	var helper seesHelper
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && rel == seesHelperFile && fn.Recv == nil && fn.Name.Name == seesHelperFunc {
			helper = seesHelper{found: true, line: fset.Position(fn.Pos()).Line, asks: asksScreenMayTell(fn)}
			continue
		}
		ast.Inspect(decl, func(node ast.Node) bool {
			x, ok := node.(*ast.SelectorExpr)
			if !ok || x.Sel.Name != "Sees" {
				return true
			}
			if id, ok := x.X.(*ast.Ident); ok && id.Obj == nil && imports[id.Name] {
				return true // a function of an imported package
			}
			hits = append(hits, seesHit{file: rel, line: fset.Position(x.Sel.Pos()).Line})
			return true
		})
	}
	return hits, helper, nil
}

// asksScreenMayTell reports whether fn calls a method named ScreenMayTell.
func asksScreenMayTell(fn *ast.FuncDecl) bool {
	asks := false
	ast.Inspect(fn, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "ScreenMayTell" {
				asks = true
			}
		}
		return !asks
	})
	return asks
}

// checkSees reads every *_test.go under root, whatever its build tag,
// leaving out hidden directories and testdata, and reports every use of a
// method named Sees outside the helper, and the helper when it is not in
// its file or does not ask ScreenMayTell. It returns the number of test
// files read too.
func checkSees(root string) ([]seesHit, int, error) {
	var hits []seesHit
	var helper seesHelper
	checked := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); p != root && (strings.HasPrefix(name, ".") || name == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		h, found, err := scanSees(filepath.ToSlash(rel), src)
		if err != nil {
			return err
		}
		checked++
		hits = append(hits, h...)
		if found.found {
			helper = found
		}
		return nil
	})
	if err != nil {
		return nil, checked, err
	}
	switch {
	case !helper.found:
		hits = append(hits, seesHit{file: seesHelperFile, why: "has no function " + seesHelperFunc})
	case !helper.asks:
		hits = append(hits, seesHit{file: seesHelperFile, line: helper.line, why: seesHelperFunc + " does not call ScreenMayTell"})
	}
	return hits, checked, nil
}
