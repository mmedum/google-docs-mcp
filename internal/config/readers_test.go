package config_test

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mmedum/google-docs-mcp/v3/internal/config"
	"github.com/mmedum/google-docs-mcp/v3/internal/credentials"
	"github.com/mmedum/google-docs-mcp/v3/internal/userconfig"
)

// The variables and their readers, from outside the package: the
// readers of the two variables read outside Define import config, so
// a test that drives them cannot live inside it.

// A variable of spaces alone is unset, as an empty one is, and the spaces
// around a value are dropped, so a form field cleared to a space starts
// the server with the default. That holds for every variable the server
// reads: Define's through Load, and each one read outside Define through
// its reader. One read outside Define with no reader here fails, so a
// variable added there cannot miss the rule.
func TestASettingOfSpacesIsUnset(t *testing.T) {
	outside := map[string]func(t *testing.T, value string) string{
		config.EnvConfigDir: func(t *testing.T, value string) string {
			t.Setenv(config.EnvConfigDir, value)
			dir, err := userconfig.BaseDir()
			return fmt.Sprintf("%q %v", dir, err)
		},
		config.EnvRefreshToken: func(_ *testing.T, value string) string {
			s := credentials.Store{Profile: "p", Env: func(string) string { return value }}
			tok, src, err := s.Resolve()
			return fmt.Sprintf("%q %q %v", tok, src, err)
		},
	}
	defined := map[string]bool{}
	config.Define(flag.NewFlagSet("test", flag.ContinueOnError), func(name string) string {
		defined[name] = true
		return ""
	})
	vars := config.Vars()
	if len(vars) < 13 {
		t.Fatalf("Vars has %d variables, want at least 13", len(vars))
	}
	for _, v := range vars {
		read := func(_ *testing.T, value string) string {
			c, err := config.Load(nil, func(name string) string {
				if name == v.Name {
					return value
				}
				return ""
			})
			return fmt.Sprintf("%+v %v", c, err)
		}
		if !defined[v.Name] {
			var ok bool
			if read, ok = outside[v.Name]; !ok {
				t.Errorf("%s is read outside Define, and this test has no reader for it", v.Name)
				continue
			}
			delete(outside, v.Name)
		}
		for _, pair := range [][2]string{{"  ", ""}, {" x ", "x"}} {
			if got, want := read(t, pair[0]), read(t, pair[1]); got != want {
				t.Errorf("%s=%q: %s\nwant %s, as %s=%q", v.Name, pair[0], got, want, v.Name, pair[1])
			}
		}
	}
	for name := range outside {
		t.Errorf("this test has a reader for %s, which Vars does not list as read outside Define", name)
	}
}

// Every GDOCS_ variable the server's code spells is one EnvVars lists,
// so a variable read outside Define cannot escape the bundle and
// staleness gates. So is every one the drivers spell: the smoke test in
// scripts/, and the live driver and the evals, whose tests start the
// server, set its variables, and a misspelling there tests a
// configuration nobody asked for. Other test files are not read: the
// integration probes' GDOCS_ variables are read by the test, never by
// the server. A name is seen as a string literal, or as a chain of
// literals and EnvPrefix joined by +; one built from any other
// constant, or through a format verb, is not seen. The scan must read
// every root and both drivers and find a name outside the config
// package, or it read too little.
func TestEveryVariableTheCodeNamesIsInEnvVars(t *testing.T) {
	known := map[string]bool{}
	for _, name := range config.EnvVars() {
		known[name] = true
	}
	found := map[string]string{}
	elsewhere := 0
	fset := token.NewFileSet()
	roots := []string{"../../cmd", "../../internal", "../../scripts"}
	drivers := []string{filepath.Join("..", "..", "internal", "livecheck"), filepath.Join("..", "..", "internal", "evals")}
	files := map[string]int{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
				return err
			}
			if strings.HasSuffix(path, "_test.go") && !slices.Contains(drivers, filepath.Dir(path)) {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			files[root]++
			files[filepath.Dir(path)]++
			outside := filepath.Dir(path) != filepath.Join("..", "..", "internal", "config")
			for word, at := range namesIn(f) {
				found[word] = fset.Position(at).String()
				if outside {
					elsewhere++
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, root := range slices.Concat(roots, drivers) {
		if files[root] == 0 {
			t.Errorf("the scan parsed no Go file under %s; it read too little", root)
		}
	}
	if elsewhere == 0 {
		t.Error("the scan found no GDOCS_ name outside internal/config; it read too little")
	}
	for _, v := range slices.Sorted(maps.Keys(found)) {
		if !known[v] {
			t.Errorf("%s names %s, which EnvVars does not list", found[v], v)
		}
	}
}

// variableName is a name the server's variables are spelled as.
var variableName = regexp.MustCompile(`^GDOCS_[A-Z0-9][A-Z0-9_]*$`)

// namesIn is every GDOCS_ name f spells, each with where it is spelled.
func namesIn(f *ast.File) map[string]token.Pos {
	found := map[string]token.Pos{}
	spell := func(s string, at token.Pos) {
		for _, word := range strings.FieldsFunc(s, func(r rune) bool {
			return r != '_' && (r < 'A' || r > 'Z') && (r < '0' || r > '9')
		}) {
			if variableName.MatchString(word) {
				found[word] = at
			}
		}
	}
	var visit func(n ast.Node) bool
	visit = func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.BinaryExpr:
			if n.Op != token.ADD {
				return true
			}
			// A + chain is read in order: each run of literals and
			// EnvPrefix is one string, and any other operand ends the
			// run and is read on its own.
			run, at := "", n.Pos()
			for _, op := range operands(n) {
				if s, ok := joined(op); ok {
					if run == "" {
						at = op.Pos()
					}
					run += s
					continue
				}
				spell(run, at)
				run = ""
				ast.Inspect(op, visit)
			}
			spell(run, at)
			return false
		case *ast.BasicLit:
			if s, err := strconv.Unquote(n.Value); n.Kind == token.STRING && err == nil {
				spell(s, n.Pos())
			}
		}
		return true
	}
	ast.Inspect(f, visit)
	return found
}

// The scan reads a literal, EnvPrefix joined to literals by +, through
// a selector and parentheses, and a run that starts after another
// operand, which a scan reading only a chain's head would miss. A name
// built from another constant or through a format verb is not seen.
func TestTheScanReadsEachChainInOrder(t *testing.T) {
	const src = `package p

var (
	a = "set GDOCS_PLAIN=true"
	b = EnvPrefix + "JOINED"
	c = config.EnvPrefix + "SELECTED"
	d = f() + EnvPrefix + "LATER" + g() + "GDOCS_" + "LAST"
	e = "x" + (EnvPrefix + "PAREN")
	h = fmt.Sprintf("%sFORMAT", EnvPrefix)
	i = other + "BUILT"
)
`
	f, err := parser.ParseFile(token.NewFileSet(), "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"GDOCS_JOINED", "GDOCS_LAST", "GDOCS_LATER", "GDOCS_PAREN", "GDOCS_PLAIN", "GDOCS_SELECTED"}
	if got := slices.Sorted(maps.Keys(namesIn(f))); !slices.Equal(got, want) {
		t.Errorf("namesIn = %v, want %v", got, want)
	}
}

// operands is a + chain's operands in order, through parentheses.
func operands(e ast.Expr) []ast.Expr {
	switch e := e.(type) {
	case *ast.BinaryExpr:
		if e.Op == token.ADD {
			return append(operands(e.X), operands(e.Y)...)
		}
	case *ast.ParenExpr:
		if b, ok := e.X.(*ast.BinaryExpr); ok && b.Op == token.ADD {
			return operands(b)
		}
	}
	return []ast.Expr{e}
}

// joined is a + chain of string literals and EnvPrefix as one string.
func joined(e ast.Expr) (string, bool) {
	switch e := e.(type) {
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		x, ok := joined(e.X)
		if !ok {
			return "", false
		}
		y, ok := joined(e.Y)
		return x + y, ok
	case *ast.ParenExpr:
		return joined(e.X)
	case *ast.BasicLit:
		s, err := strconv.Unquote(e.Value)
		return s, e.Kind == token.STRING && err == nil
	case *ast.Ident:
		return config.EnvPrefix, e.Name == "EnvPrefix"
	case *ast.SelectorExpr:
		return config.EnvPrefix, e.Sel.Name == "EnvPrefix"
	}
	return "", false
}
