package main

import (
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
)

type counts struct {
	unit, integration, e2e int
	misplaced              []string // e2e tests outside e2e/
}

func (c counts) unitShare() float64 {
	if c.unit+c.integration == 0 {
		return 1
	}
	return float64(c.unit) / float64(c.unit+c.integration)
}

func (c counts) problems(minShare float64) []string {
	var p []string
	for _, f := range c.misplaced {
		p = append(p, f+": end-to-end tests live in e2e/")
	}
	if c.e2e > 0 && (c.e2e >= c.integration || c.e2e >= c.unit) {
		p = append(p, fmt.Sprintf("%d end-to-end tests: there must be fewer than integration (%d) and unit (%d) tests", c.e2e, c.integration, c.unit))
	}
	if c.unitShare() < minShare {
		p = append(p, fmt.Sprintf("unit share %.0f%% is below %.0f%%", 100*c.unitShare(), 100*minShare))
	}
	return p
}

// level returns "unit", "integration" or "e2e" from a file's build constraint.
func level(file *ast.File) string {
	for _, group := range file.Comments {
		if group.Pos() > file.Package {
			break
		}
		for _, cm := range group.List {
			if !constraint.IsGoBuild(cm.Text) {
				continue
			}
			expr, err := constraint.Parse(cm.Text)
			if err != nil {
				continue
			}
			s := expr.String()
			switch {
			case strings.Contains(s, "e2e"):
				return "e2e"
			case strings.Contains(s, "integration"):
				return "integration"
			}
		}
	}
	return "unit"
}

func isTest(fn *ast.FuncDecl) bool {
	if fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") || fn.Name.Name == "TestMain" {
		return false
	}
	params := fn.Type.Params.List
	if len(params) != 1 {
		return false
	}
	star, ok := params[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "T"
}

func countTests(root string) (counts, error) {
	var c counts
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); path != root && (strings.HasPrefix(name, ".") || name == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		n := 0
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && isTest(fn) {
				n++
			}
		}
		rel, _ := filepath.Rel(root, path)
		switch level(file) {
		case "e2e":
			c.e2e += n
			if n > 0 && !strings.HasPrefix(filepath.ToSlash(rel), "e2e/") {
				c.misplaced = append(c.misplaced, rel)
			}
		case "integration":
			c.integration += n
		default:
			c.unit += n
		}
		return nil
	})
	return c, err
}
