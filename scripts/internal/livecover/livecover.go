// Package livecover reads the live run's steps from the driver's source,
// without building it: the driver compiles only under its build tag, and
// the gates that cite its steps run without one.
//
// A step is what the transcript heads with its name. The driver's call
// logs "=== <name> ..." for each tool call, and a resource read does the
// same, so a step function is one whose first parameter reaches such a
// heading: it logs it, or passes it on as the first argument of another
// step function, alone or after a prefix and ": ", as `mode+": "+label`
// does. A step's name is the string literal a call of a step function
// passes, or the literal after such a prefix: mode+": read the lists"
// names "read the lists". A name built at run time, such as
// "dry run: "+tool.Name, is no name a reader can find in the source, and
// is left out.
package livecover

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"
)

// DriverDir is the live driver's package, from the module root.
const DriverDir = "internal/livecheck"

// DriverTag is the build tag every file of the driver carries.
const DriverTag = "live"

// heading is how the transcript opens a step: its name first.
const heading = "=== %s"

// A Step is one call that names a step with a string literal.
type Step struct {
	Name string
	Pos  token.Pos
}

// Steps is every step the driver's files name, in the order the files
// and their calls come.
func Steps(files []*ast.File) []Step {
	r := newReader(files)
	r.markStepFuncs()
	var out []Step
	for _, decl := range r.decls {
		ast.Inspect(decl.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 || !r.steps[r.callee(decl, call)] {
				return true
			}
			if name, ok := stepName(call.Args[0]); ok {
				out = append(out, Step{Name: name, Pos: call.Args[0].Pos()})
			}
			return true
		})
	}
	return out
}

// stepName is the name a step function's first argument gives: a string
// literal, or a literal after a prefix joined with ": ", as
// mode+": read the lists" is, less the ": ".
func stepName(arg ast.Expr) (string, bool) {
	if name, ok := stringLit(arg); ok {
		return name, true
	}
	sum, ok := arg.(*ast.BinaryExpr)
	if !ok || sum.Op != token.ADD {
		return "", false
	}
	lit, ok := stringLit(sum.Y)
	name, joined := strings.CutPrefix(lit, ": ")
	return name, ok && joined && name != ""
}

// fn is a function the driver declares: a top-level function or method,
// or a function literal assigned to a local name inside one.
type fn struct {
	typ  *ast.FuncType
	body *ast.BlockStmt
	// decl is the top-level function it is in, itself for one.
	decl *ast.FuncDecl
}

// reader holds the driver's functions and which of them are step
// functions.
type reader struct {
	decls []*ast.FuncDecl
	// top is the top-level functions and methods by name. The driver has
	// one receiver type, so a method is found by its name alone.
	top map[string]*fn
	// local is the function literals each top-level function assigns to a
	// name.
	local map[*ast.FuncDecl]map[string]*fn
	// all is every function in top and local.
	all   []*fn
	steps map[*fn]bool
}

func newReader(files []*ast.File) *reader {
	r := &reader{top: map[string]*fn{}, local: map[*ast.FuncDecl]map[string]*fn{}, steps: map[*fn]bool{}}
	for _, file := range files {
		for _, d := range file.Decls {
			decl, ok := d.(*ast.FuncDecl)
			if !ok || decl.Body == nil {
				continue
			}
			top := &fn{typ: decl.Type, body: decl.Body, decl: decl}
			r.decls = append(r.decls, decl)
			r.top[decl.Name.Name] = top
			r.local[decl] = localFuncs(decl)
			r.all = append(r.all, top)
			for _, local := range r.local[decl] {
				r.all = append(r.all, local)
			}
		}
	}
	return r
}

// localFuncs is every `name := func(...) {...}` inside decl, by name.
func localFuncs(decl *ast.FuncDecl) map[string]*fn {
	out := map[string]*fn{}
	ast.Inspect(decl.Body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != len(as.Rhs) {
			return true
		}
		for i, lhs := range as.Lhs {
			id, isIdent := lhs.(*ast.Ident)
			lit, isFunc := as.Rhs[i].(*ast.FuncLit)
			if isIdent && isFunc {
				out[id.Name] = &fn{typ: lit.Type, body: lit.Body, decl: decl}
			}
		}
		return true
	})
	return out
}

// callee is the driver function a call inside decl calls, or nil: a local
// function literal by its name, then a top-level function or a method.
func (r *reader) callee(decl *ast.FuncDecl, call *ast.CallExpr) *fn {
	switch f := call.Fun.(type) {
	case *ast.Ident:
		if local := r.local[decl][f.Name]; local != nil {
			return local
		}
		return r.top[f.Name]
	case *ast.SelectorExpr:
		return r.top[f.Sel.Name]
	}
	return nil
}

// markStepFuncs finds the step functions: those that log their first
// parameter as a transcript heading, and then, until nothing changes,
// those that pass it on to one.
func (r *reader) markStepFuncs() {
	for changed := true; changed; {
		changed = false
		for _, f := range r.all {
			if !r.steps[f] && r.namesAStep(f) {
				r.steps[f] = true
				changed = true
			}
		}
	}
}

// namesAStep reports whether f's own body, not a function literal inside
// it, heads the transcript with its first parameter or passes it to a
// step function.
func (r *reader) namesAStep(f *fn) bool {
	param := firstParam(f.typ)
	if param == "" {
		return false
	}
	found := false
	ast.Inspect(f.body, func(n ast.Node) bool {
		if found {
			return false
		}
		switch v := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.CallExpr:
			found = logsHeading(v, param) || (len(v.Args) > 0 && r.steps[r.callee(f.decl, v)] && passesOn(v.Args[0], param))
		}
		return true
	})
	return found
}

// logsHeading reports whether call is a Logf whose format opens a
// transcript heading with param.
func logsHeading(call *ast.CallExpr, param string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Logf" || len(call.Args) < 2 {
		return false
	}
	format, ok := stringLit(call.Args[0])
	return ok && strings.HasPrefix(format, heading) && isIdent(call.Args[1], param)
}

// passesOn reports whether arg is param, or param after a prefix ending
// in ": ": "word: "+label, or mode+": "+label.
func passesOn(arg ast.Expr, param string) bool {
	if isIdent(arg, param) {
		return true
	}
	sum, ok := arg.(*ast.BinaryExpr)
	if !ok || sum.Op != token.ADD || !isIdent(sum.Y, param) {
		return false
	}
	prefix := sum.X
	if inner, ok := prefix.(*ast.BinaryExpr); ok && inner.Op == token.ADD {
		prefix = inner.Y
	}
	lit, ok := stringLit(prefix)
	return ok && strings.HasSuffix(lit, ": ")
}

// firstParam is the name of a function's first parameter, or empty.
func firstParam(typ *ast.FuncType) string {
	if typ.Params == nil || len(typ.Params.List) == 0 || len(typ.Params.List[0].Names) == 0 {
		return ""
	}
	return typ.Params.List[0].Names[0].Name
}

func isIdent(e ast.Expr, name string) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == name
}

func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(lit.Value)
	return v, err == nil
}
