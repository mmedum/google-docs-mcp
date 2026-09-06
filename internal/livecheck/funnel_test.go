package livecheck

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The redactor only protects what goes through it. Both driver packages
// write a transcript people paste, and in both of them the guarantee has
// so far rested on everyone having remembered to use the helper —
// exactly the kind of rule this repository keeps turning into a test
// after it fails. It failed twice: a dry-run step formatted two raw
// revision ids into t.Errorf, and the eval driver logged a real document
// URL, neither of them through `shown`.
//
// Two rules: no direct print, and no logging of text that came back
// from a tool without passing it through `shown`.
func TestDriversDoNotPrint(t *testing.T) {
	root := moduleRoot(t)
	// Both drivers, and the whole of each: the taint closure is over a
	// package's call graph, so the files have to be parsed together.
	dirs := []string{
		filepath.Join(root, "internal", "livecheck"),
		filepath.Join(root, "internal", "evals"),
	}

	parsed := 0
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		fset := token.NewFileSet()
		var files []*ast.File
		lines := map[string][]string{}
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".go" {
				continue
			}
			path := filepath.Join(dir, e.Name())
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			// ParseFile ignores build tags, which is the point: the files
			// that matter are the ones behind `live` and `evals`.
			file, err := parser.ParseFile(fset, path, src, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			files = append(files, file)
			lines[path] = strings.Split(string(src), "\n")
			parsed++
		}
		for _, where := range unredactedIn(fset, files, lines) {
			t.Errorf("%s: %s", filepath.Base(dir), where)
		}
	}

	// Zero files parsed and zero findings are the same output.
	if parsed < 8 {
		t.Fatalf("only %d driver files parsed; the check is not reading them", parsed)
	}
	t.Logf("%d driver files checked", parsed)
}

// unredactedIn reports two things: a direct print, and tool text logged
// without passing through the redactor.
//
// The second rule follows the value rather than the format verb. A first
// draft refused any bare name under a `%s` and produced fifty-four
// findings — twenty `err`, ten `label` — of which two were real; a gate
// that noisy is a gate somebody turns off.
//
// The sources of tool text are derived, not listed. A second draft named
// four of them, and missed `okStruct` and every one of the eval
// harness's six, which made the rule inert over the package this same
// check claims to cover. So the root is the SDK call itself — a function
// whose body reaches `CallTool` — and anything that calls a source, or
// passes a tainted value into a call, is tainted in turn. One fact about
// the SDK instead of a list of our own function names.
//
// A tracked value that genuinely needs no redacting carries
// `// redact:allow` on its line, the `<gate>:allow` spelling the leak
// scanners already use.
func unredactedIn(fset *token.FileSet, files []*ast.File, lines map[string][]string) []string {
	logger := map[string]bool{"Logf": true, "Errorf": true, "Fatalf": true, "Skipf": true}

	// Every function in these packages, and whether it can hand back
	// text that came from the server.
	decls := map[string]*ast.FuncDecl{}
	for _, file := range files {
		for _, d := range file.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Body != nil {
				decls[fn.Name.Name] = fn
			}
		}
	}
	source := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for name, fn := range decls {
			if source[name] {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if callee(call) == "CallTool" || source[callee(call)] {
					source[name] = true
					changed = true
					return false
				}
				return true
			})
		}
	}

	var out []string
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			raw := taintedNames(fn, source)
			ast.Inspect(fn, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pos := fset.Position(call.Pos())
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "fmt" {
					switch sel.Sel.Name {
					case "Print", "Printf", "Println":
						out = append(out, fmt.Sprintf("%s calls fmt.%s at %s", fn.Name.Name, sel.Sel.Name, pos))
					}
					return true
				}
				if !logger[sel.Sel.Name] {
					return true
				}
				for _, arg := range call.Args {
					id, ok := arg.(*ast.Ident)
					if !ok || !raw[id.Name] || allowed(lines, fset, arg) {
						continue
					}
					out = append(out, fmt.Sprintf("%s logs %s, which came from a tool, without the redactor at %s",
						fn.Name.Name, id.Name, fset.Position(arg.Pos())))
				}
				return true
			})
		}
	}
	return out
}

// taintedNames are the names in fn holding text that came from the
// server: assigned from a source, or from a call given something already
// tainted, which is how a helper that slices one id out of a result
// carries the taint with it.
func taintedNames(fn *ast.FuncDecl, source map[string]bool) map[string]bool {
	raw := map[string]bool{}
	for changed := true; changed; {
		changed = false
		ast.Inspect(fn, func(n ast.Node) bool {
			as, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for _, rhs := range as.Rhs {
				call, ok := rhs.(*ast.CallExpr)
				if !ok {
					continue
				}
				tainted := source[callee(call)]
				for _, arg := range call.Args {
					if id, ok := arg.(*ast.Ident); ok && raw[id.Name] {
						tainted = true
					}
				}
				if !tainted {
					continue
				}
				for _, lhs := range as.Lhs {
					if id, ok := lhs.(*ast.Ident); ok && id.Name != "_" && !raw[id.Name] {
						raw[id.Name] = true
						changed = true
					}
				}
			}
			return true
		})
	}
	return raw
}

func callee(call *ast.CallExpr) string {
	switch f := call.Fun.(type) {
	case *ast.SelectorExpr:
		return f.Sel.Name
	case *ast.Ident:
		return f.Name
	}
	return ""
}

func allowed(lines map[string][]string, fset *token.FileSet, arg ast.Expr) bool {
	pos := fset.Position(arg.Pos())
	src := lines[pos.Filename]
	return pos.Line-1 < len(src) && strings.Contains(src[pos.Line-1], "redact:allow")
}

// Redacting by position only works while the redactor knows every
// position, and the positions live in other packages.
//
// The field names are read out of the wire types rather than typed here.
// The first version typed them and got one wrong on the day it was
// written — it said `Email`, the Drive type says `EmailAddress`, so the
// four reads inside `userLabel` (the one function that renders an
// address into a transcript, and the function the count beside this one
// exists to guard) were invisible to it. A list mirroring a type is a
// list that drifts from the type.
//
// Be clear about what the second number is: a tripwire, not a census. It
// counts every read of a person field, including the assignments that
// merely carry one from a wire type to a model, so a refactor moves it
// too. That is the price of counting syntax rather than semantics. The
// question it forces — "does the redactor still cover this?" — is the
// one nobody asks unprompted.
func TestEveryPlaceAPersonIsWrittenIsKnown(t *testing.T) {
	root := moduleRoot(t)
	const (
		wantUserLabel = 5  // service: ops.go x2, history.go, search.go x2
		wantPerson    = 32 // every read of a person field in the three packages
	)

	dirs := []string{"internal/service", "internal/render", "internal/plan"}
	personField := personFields(t, root, dirs)
	if len(personField) < 4 {
		t.Fatalf("only %d person fields derived; the derivation is not reading the types", len(personField))
	}
	// The two the derivation exists for. `EmailAddress` is what the Drive
	// type declares and what the first, typed version of this list got
	// wrong — it said `Email`, so the four reads inside `userLabel` were
	// invisible to the check written to guard them. `Author` is the model
	// side. If either stops being found, the derivation broke, and the
	// count below would go quietly green.
	for _, want := range []string{"EmailAddress", "Author"} {
		if !personField[want] {
			t.Errorf("%q is not among the derived person fields; the derivation is looking in the wrong place", want)
		}
	}

	var gotUserLabel, gotPerson, scanned int
	for _, dir := range dirs {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, filepath.Join(root, dir, e.Name()), nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", e.Name(), err)
			}
			scanned++
			ast.Inspect(file, func(n ast.Node) bool {
				switch v := n.(type) {
				case *ast.SelectorExpr:
					if personField[v.Sel.Name] {
						gotPerson++
					}
				case *ast.CallExpr:
					if id, ok := v.Fun.(*ast.Ident); ok && id.Name == "userLabel" {
						gotUserLabel++
					}
				}
				return true
			})
		}
	}

	if scanned < 10 {
		t.Fatalf("only %d renderer files scanned; the check is not reading them", scanned)
	}
	if gotUserLabel != wantUserLabel {
		t.Errorf("userLabel is called %d times, expected %d", gotUserLabel, wantUserLabel)
	}
	if gotPerson != wantPerson {
		t.Errorf("%d reads of a person field in these packages, expected %d. Something changed "+
			"around a name or an address: if it reaches output, check internal/redact covers the "+
			"position, then update this count and say where it went", gotPerson, wantPerson)
	}
}

// personFields are the field names that carry a person, derived in two
// steps rather than typed.
//
// First the wire types: any struct in internal/gapi or internal/gdocs
// whose name ends in User or Author. That gives DisplayName and
// EmailAddress — the two the first, typed version got wrong, since it
// guessed `Email`.
//
// Then the model types the renderers actually read: a struct field in
// those packages whose name ends in Author. Both halves come from type
// declarations, which is the point — a closure over assignment was tried
// instead and propagated through names like `User` until it counted
// fifteen hundred reads, so it is the declarations that are followed,
// not the data flow.
// wireTypePackages hold the types that come off the wire carrying a
// person: one per line, because the two together on one line score as
// a high-entropy secret.
var wireTypePackages = []string{
	"internal/gapi",
	"internal/gdocs",
}

func personFields(t *testing.T, root string, dirs []string) map[string]bool {
	t.Helper()
	fields := map[string]bool{}
	for _, dir := range wireTypePackages {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, filepath.Join(root, dir, e.Name()), nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", e.Name(), err)
			}
			ast.Inspect(file, func(n ast.Node) bool {
				ts, ok := n.(*ast.TypeSpec)
				if !ok || (!strings.HasSuffix(ts.Name.Name, "User") && !strings.HasSuffix(ts.Name.Name, "Author")) {
					return true
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					return true
				}
				for _, f := range st.Fields.List {
					if id, ok := f.Type.(*ast.Ident); !ok || id.Name != "string" {
						continue // Me is a bool, and a person is text
					}
					for _, name := range f.Names {
						fields[name.Name] = true
					}
				}
				return true
			})
		}
	}

	// The model side: whatever the renderers call the person they carry.
	for _, dir := range dirs {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, filepath.Join(root, dir, e.Name()), nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", e.Name(), err)
			}
			ast.Inspect(file, func(n ast.Node) bool {
				st, ok := n.(*ast.StructType)
				if !ok {
					return true
				}
				for _, f := range st.Fields.List {
					for _, name := range f.Names {
						if strings.HasSuffix(name.Name, "Author") {
							fields[name.Name] = true
						}
					}
				}
				return true
			})
		}
	}

	return fields
}

// moduleRoot walks up to the directory holding go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's working directory")
		}
		dir = parent
	}
}
