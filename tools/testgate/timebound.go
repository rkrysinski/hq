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
// time.Since(x) or time.Now().Sub(x), directly or through a variable that
// is only ever assigned one of them, is greater than (>, >=, or < and <=
// written the other way round) a constant duration under the limit. A fake
// clock's Sub, a lower bound, timers and timeouts are not such a bound.
// consts are the constants the file's package declares in its other files.
func scanTimebound(rel string, src []byte, consts map[string]pkgConst) ([]timeboundHit, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, src, 0)
	if err != nil {
		return nil, err
	}
	s := newTimeScan(file, consts)
	if s.pkg == "" {
		return nil, nil
	}
	s.markElapsed(file)
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
// the time package under, the variables that hold real elapsed time and the
// constants of its package declared in other files.
type timeScan struct {
	pkg     string
	elapsed map[*ast.Object]bool
	consts  map[string]pkgConst
}

// pkgConst is a constant a package declares, with the file it stands in.
type pkgConst struct {
	value ast.Expr
	file  timeScan
}

func newTimeScan(file *ast.File, consts map[string]pkgConst) timeScan {
	s := timeScan{elapsed: map[*ast.Object]bool{}, consts: consts}
	for _, imp := range file.Imports {
		if path, _ := strconv.Unquote(imp.Path.Value); path == "time" {
			s.pkg = "time"
			if imp.Name != nil {
				s.pkg = imp.Name.Name
			}
		}
	}
	return s
}

// markElapsed finds the variables of file that hold real elapsed time: every
// assignment to them assigns time.Since(x), time.Now().Sub(x) or another
// such variable. A variable that is also given anything else is not one.
func (s timeScan) markElapsed(file *ast.File) {
	for range 16 { // a chain of variables longer than this is not followed
		since, other := map[*ast.Object]bool{}, map[*ast.Object]bool{}
		assign := func(lhs ast.Expr, rhs ast.Expr) {
			id, ok := lhs.(*ast.Ident)
			if !ok || id.Obj == nil {
				return
			}
			if rhs != nil && s.isElapsed(rhs) {
				since[id.Obj] = true
			} else {
				other[id.Obj] = true
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch x := node.(type) {
			case *ast.AssignStmt:
				for i, lhs := range x.Lhs {
					var rhs ast.Expr
					if (x.Tok == token.DEFINE || x.Tok == token.ASSIGN) && len(x.Lhs) == len(x.Rhs) {
						rhs = x.Rhs[i]
					}
					assign(lhs, rhs)
				}
			case *ast.ValueSpec:
				if len(x.Values) > 0 {
					for i, name := range x.Names {
						var rhs ast.Expr
						if len(x.Names) == len(x.Values) {
							rhs = x.Values[i]
						}
						assign(name, rhs)
					}
				}
			case *ast.IncDecStmt:
				assign(x.X, nil)
			case *ast.RangeStmt:
				assign(x.Key, nil)
				assign(x.Value, nil)
			}
			return true
		})
		grew := false
		for obj := range since {
			if !other[obj] && !s.elapsed[obj] {
				s.elapsed[obj] = true
				grew = true
			}
		}
		if !grew {
			return
		}
	}
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
// package's units, constants of the package, conversions to
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
		if x.Obj == nil { // declared in another file of the package, or not a constant
			if c, ok := s.consts[x.Name]; ok {
				return c.file.constant(c.value, depth+1)
			}
			return nil, false
		}
		if x.Obj.Kind != ast.Con {
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
	var consts map[string]map[string]pkgConst // of the directory being read, by package
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); path != root && (strings.HasPrefix(name, ".") || name == "testdata") {
				return filepath.SkipDir
			}
			consts, err = packageConsts(path)
			return err
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		h, err := scanTimebound(filepath.ToSlash(rel), src, consts[packageName(src)])
		if err != nil {
			return err
		}
		checked++
		hits = append(hits, h...)
		return nil
	})
	return hits, checked, err
}

// packageConsts reads the package-level constants that the Go files of dir
// declare, by package name, each with the file it stands in.
func packageConsts(dir string) (map[string]map[string]pkgConst, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	consts := map[string]map[string]pkgConst{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, src, 0)
		if err != nil {
			return nil, err
		}
		pkg := file.Name.Name
		if consts[pkg] == nil {
			consts[pkg] = map[string]pkgConst{}
		}
		s := newTimeScan(file, consts[pkg])
		for _, decl := range file.Decls {
			g, ok := decl.(*ast.GenDecl)
			if !ok || g.Tok != token.CONST {
				continue
			}
			for _, spec := range g.Specs {
				v := spec.(*ast.ValueSpec)
				for i, name := range v.Names {
					if len(v.Names) == len(v.Values) {
						consts[pkg][name.Name] = pkgConst{v.Values[i], s}
					}
				}
			}
		}
	}
	return consts, nil
}

// packageName is the package clause of a Go source file, or "" when it has
// none that parses.
func packageName(src []byte) string {
	file, err := parser.ParseFile(token.NewFileSet(), "", src, parser.PackageClauseOnly)
	if err != nil {
		return ""
	}
	return file.Name.Name
}
