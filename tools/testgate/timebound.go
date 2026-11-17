package main

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// timeboundLimit is the shortest upper bound a test may put on real elapsed
// time (#232): a tighter one fails at random on a busy runner.
const timeboundLimit = time.Second

// timeboundHit is an upper bound under timeboundLimit on real elapsed time
// in a test.
type timeboundHit struct {
	file  string
	line  int
	bound string
}

func (h timeboundHit) String() string {
	return fmt.Sprintf("%s:%d bounds real elapsed time by %s: a test's upper bound on real time is %s or more, or the test follows the order of events instead of a clock",
		h.file, h.line, h.bound, timeboundLimit)
}

// timeUnits are the time package's constant durations, in nanoseconds.
var timeUnits = map[string]int64{
	"Nanosecond":  int64(time.Nanosecond),
	"Microsecond": int64(time.Microsecond),
	"Millisecond": int64(time.Millisecond),
	"Second":      int64(time.Second),
	"Minute":      int64(time.Minute),
	"Hour":        int64(time.Hour),
}

// scanTimebound reports every upper bound under timeboundLimit on real
// elapsed time in the Go source file rel: a comparison that holds when
// time.Since(x) or time.Now().Sub(x), directly or through a variable it was
// assigned to, is greater than (>, >=, or < and <= written the other way
// round) a constant duration under the limit. A fake clock's Sub, a lower
// bound, timers and timeouts are not such a bound.
func scanTimebound(rel string, src []byte) ([]timeboundHit, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, src, 0)
	if err != nil {
		return nil, err
	}
	pkg := ""
	for _, imp := range file.Imports {
		if path, _ := strconv.Unquote(imp.Path.Value); path == "time" {
			pkg = "time"
			if imp.Name != nil {
				pkg = imp.Name.Name
			}
		}
	}
	if pkg == "" {
		return nil, nil
	}
	s := timeScan{pkg: pkg, elapsed: map[*ast.Object]bool{}}
	// The variables that hold the elapsed time, also through one another.
	for grew := true; grew; {
		grew = false
		mark := func(lhs ast.Expr, rhs ast.Expr) {
			if id, ok := lhs.(*ast.Ident); ok && id.Obj != nil && !s.elapsed[id.Obj] && s.isElapsed(rhs) {
				s.elapsed[id.Obj] = true
				grew = true
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch x := node.(type) {
			case *ast.AssignStmt:
				if len(x.Lhs) == len(x.Rhs) {
					for i := range x.Lhs {
						mark(x.Lhs[i], x.Rhs[i])
					}
				}
			case *ast.ValueSpec:
				if len(x.Names) == len(x.Values) {
					for i := range x.Names {
						mark(x.Names[i], x.Values[i])
					}
				}
			}
			return true
		})
	}
	var hits []timeboundHit
	ast.Inspect(file, func(node ast.Node) bool {
		x, ok := node.(*ast.BinaryExpr)
		if !ok {
			return true
		}
		var elapsed, bound ast.Expr
		switch x.Op {
		case token.GTR, token.GEQ:
			elapsed, bound = x.X, x.Y
		case token.LSS, token.LEQ:
			elapsed, bound = x.Y, x.X
		default:
			return true
		}
		if !s.isElapsed(elapsed) {
			return true
		}
		if d, ok := s.duration(bound, 0); ok && d < timeboundLimit {
			hits = append(hits, timeboundHit{rel, fset.Position(x.Pos()).Line, d.String()})
		}
		return true
	})
	return hits, nil
}

// timeScan holds what scanTimebound knows of one file: the name it imports
// the time package under and the variables that hold real elapsed time.
type timeScan struct {
	pkg     string
	elapsed map[*ast.Object]bool
}

// isTime reports whether e is the selector time.<sel> of the time package.
func (s timeScan) isTime(e ast.Expr, sel string) bool {
	x, ok := e.(*ast.SelectorExpr)
	if !ok || x.Sel.Name != sel {
		return false
	}
	id, ok := x.X.(*ast.Ident)
	return ok && id.Name == s.pkg && id.Obj == nil // a package name resolves to no object
}

// isElapsed reports whether e is real elapsed time: time.Since(x),
// time.Now().Sub(x) or a variable assigned one of them.
func (s timeScan) isElapsed(e ast.Expr) bool {
	switch x := ast.Unparen(e).(type) {
	case *ast.Ident:
		return x.Obj != nil && s.elapsed[x.Obj]
	case *ast.CallExpr:
		if len(x.Args) != 1 {
			return false
		}
		if s.isTime(x.Fun, "Since") {
			return true
		}
		sub, ok := x.Fun.(*ast.SelectorExpr)
		if !ok || sub.Sel.Name != "Sub" {
			return false
		}
		now, ok := sub.X.(*ast.CallExpr)
		return ok && len(now.Args) == 0 && s.isTime(now.Fun, "Now")
	}
	return false
}

// duration evaluates e as a constant duration: literals, the time
// package's units, constants declared in the file, conversions to
// time.Duration and arithmetic on them. depth stops a constant that refers
// to itself.
func (s timeScan) duration(e ast.Expr, depth int) (time.Duration, bool) {
	v, ok := s.constant(e, depth)
	if !ok {
		return 0, false
	}
	n, exact := constant.Int64Val(constant.ToInt(v))
	return time.Duration(n), exact
}

func (s timeScan) constant(e ast.Expr, depth int) (constant.Value, bool) {
	if depth > 16 {
		return nil, false
	}
	switch x := ast.Unparen(e).(type) {
	case *ast.BasicLit:
		if x.Kind == token.INT || x.Kind == token.FLOAT {
			return constant.MakeFromLiteral(x.Value, x.Kind, 0), true
		}
	case *ast.SelectorExpr:
		for unit, n := range timeUnits {
			if s.isTime(x, unit) {
				return constant.MakeInt64(n), true
			}
		}
	case *ast.Ident:
		if x.Obj == nil || x.Obj.Kind != ast.Con {
			return nil, false
		}
		spec, ok := x.Obj.Decl.(*ast.ValueSpec)
		if !ok {
			return nil, false
		}
		for i, name := range spec.Names {
			if name.Obj == x.Obj && i < len(spec.Values) {
				return s.constant(spec.Values[i], depth+1)
			}
		}
	case *ast.CallExpr:
		if len(x.Args) == 1 && s.isTime(x.Fun, "Duration") {
			return s.constant(x.Args[0], depth+1)
		}
	case *ast.UnaryExpr:
		if v, ok := s.constant(x.X, depth+1); ok && (x.Op == token.SUB || x.Op == token.ADD) {
			return constant.UnaryOp(x.Op, v, 0), true
		}
	case *ast.BinaryExpr:
		a, ok := s.constant(x.X, depth+1)
		if !ok {
			return nil, false
		}
		b, ok := s.constant(x.Y, depth+1)
		if !ok {
			return nil, false
		}
		switch x.Op {
		case token.ADD, token.SUB, token.MUL:
			return constant.BinaryOp(a, x.Op, b), true
		case token.QUO:
			if constant.Sign(b) == 0 {
				return nil, false
			}
			op := token.QUO
			if a.Kind() == constant.Int && b.Kind() == constant.Int {
				op = token.QUO_ASSIGN // integer division, as Go's on durations
			}
			return constant.BinaryOp(a, op, b), true
		}
	}
	return nil, false
}

// checkTimebound scans every test file under root, whatever its build tag,
// leaving out hidden directories (.git, .claude and its worktrees) and
// testdata, and reports every upper bound under timeboundLimit on real
// elapsed time, file paths relative to root, and how many files it read.
func checkTimebound(root string) ([]timeboundHit, int, error) {
	var hits []timeboundHit
	checked := 0
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
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		h, err := scanTimebound(filepath.ToSlash(rel), src)
		if err != nil {
			return err
		}
		checked++
		hits = append(hits, h...)
		return nil
	})
	return hits, checked, err
}
