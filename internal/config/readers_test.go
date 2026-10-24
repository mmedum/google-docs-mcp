package config_test

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
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
	if len(vars) < config.VarsFloor {
		t.Fatalf("Vars has %d variables, want at least %d", len(vars), config.VarsFloor)
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

// codeRoots are where the server's code is read from, and codeDrivers
// the drivers whose tests start the server and set its variables: the
// live driver and the evals. Other test files are not read: the
// integration probes' GDOCS_ variables are read by the test, never by
// the server.
var (
	codeRoots   = []string{"../../cmd", "../../internal", "../../scripts"}
	codeDrivers = []string{filepath.Join("..", "..", "internal", "livecheck"), filepath.Join("..", "..", "internal", "evals")}
	configDir   = filepath.Join("..", "..", "internal", "config")
)

// codeFile is one parsed file of the code the scans read.
type codeFile struct {
	f *ast.File
	// inConfig is a file of internal/config, where the names are defined.
	inConfig bool
}

// codeFiles parses every file the scans read: the Go under codeRoots,
// less tests other than codeDrivers'. It fails when a root or a driver
// gives no file, so a scan cannot pass by reading too little.
func codeFiles(t *testing.T) (*token.FileSet, []codeFile) {
	t.Helper()
	fset := token.NewFileSet()
	var parsed []codeFile
	files := map[string]int{}
	for _, root := range codeRoots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
				return err
			}
			if strings.HasSuffix(path, "_test.go") && !slices.Contains(codeDrivers, filepath.Dir(path)) {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			files[root]++
			files[filepath.Dir(path)]++
			parsed = append(parsed, codeFile{f: f, inConfig: filepath.Dir(path) == configDir})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, root := range slices.Concat(codeRoots, codeDrivers) {
		if files[root] == 0 {
			t.Fatalf("the scan parsed no Go file under %s; it read too little", root)
		}
	}
	return fset, parsed
}

// Every GDOCS_ variable the server's code spells is one EnvVars lists,
// so a variable read outside Define cannot escape the bundle and
// staleness gates. So is every one the drivers spell, since a
// misspelling there tests a configuration nobody asked for. A name is
// seen as a string literal, or as a chain of literals and EnvPrefix
// joined by +; one built from any other constant, or through a format
// verb, is not seen. The scan must find a name outside the config
// package, or it read too little.
func TestEveryVariableTheCodeNamesIsInEnvVars(t *testing.T) {
	known := map[string]bool{}
	for _, name := range config.EnvVars() {
		known[name] = true
	}
	found := map[string]string{}
	elsewhere := 0
	fset, files := codeFiles(t)
	for _, file := range files {
		for word, at := range namesIn(file.f) {
			found[word] = fset.Position(at).String()
			if !file.inConfig {
				elsewhere++
			}
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

// Outside internal/config a variable's name is spelled from config's
// constants, EnvPrefix joined to the key or a constant such as
// EnvConfigDir, never typed whole in a literal: a guard's refusal, the
// smoke test's environment and the drivers' included. A struct tag is
// the exception, since Go takes only a literal there. The scan must
// find a name joined from EnvPrefix outside the config package, or it
// read too little.
func TestVariableNamesAreSpelledFromConfig(t *testing.T) {
	joinedElsewhere := 0
	fset, files := codeFiles(t)
	for _, file := range files {
		if file.inConfig {
			continue
		}
		typed, tagged := literalNames(file.f)
		for _, word := range slices.Sorted(maps.Keys(typed)) {
			t.Errorf("%s types %s in a literal; join config.EnvPrefix to the key, or use config's constant",
				fset.Position(typed[word]), word)
		}
		for word := range namesIn(file.f) {
			if _, ok := typed[word]; !ok {
				if _, ok := tagged[word]; !ok {
					joinedElsewhere++
				}
			}
		}
	}
	if joinedElsewhere == 0 {
		t.Error("the scan found no name joined from EnvPrefix outside internal/config; it read too little")
	}
}

// literalNames is every GDOCS_ name f types whole inside one string
// literal, each with where: typed outside a struct tag, and tagged in
// one.
func literalNames(f *ast.File) (typed, tagged map[string]token.Pos) {
	tags := map[*ast.BasicLit]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		if field, ok := n.(*ast.Field); ok && field.Tag != nil {
			tags[field.Tag] = true
		}
		return true
	})
	typed, tagged = map[string]token.Pos{}, map[string]token.Pos{}
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		into := typed
		if tags[lit] {
			into = tagged
		}
		if s, err := strconv.Unquote(lit.Value); err == nil {
			for _, word := range words(s) {
				if variableName.MatchString(word) {
					into[word] = lit.Pos()
				}
			}
		}
		return true
	})
	return typed, tagged
}

// Every reader of a server variable goes through config, Lookup or
// Load, or a reader built on Lookup such as userconfig.BaseDir, so each
// is read trimmed and by the server's own rules. No file the scans read
// calls os.Getenv or os.LookupEnv with a GDOCS_ name, whether a literal,
// a chain from EnvPrefix or one of config's constants for a name. The
// constants are read from config's source, and the scan must see a call
// of either function, or it read too little.
func TestEveryReaderOfAVariableGoesThroughConfig(t *testing.T) {
	constants := nameConstants(t)
	if !constants["EnvConfigDir"] || !constants["EnvRefreshToken"] {
		t.Fatalf("config's constants for a name were read as %v; the derivation read too little", constants)
	}
	calls := 0
	fset, files := codeFiles(t)
	for _, file := range files {
		ast.Inspect(file.f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 1 {
				return true
			}
			fun, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (fun.Sel.Name != "Getenv" && fun.Sel.Name != "LookupEnv") {
				return true
			}
			if pkg, ok := fun.X.(*ast.Ident); !ok || pkg.Name != "os" {
				return true
			}
			calls++
			if namesAVariable(call.Args[0], constants) {
				t.Errorf("%s reads a server variable with os.%s; read it through config.Lookup or config.Load",
					fset.Position(call.Pos()), fun.Sel.Name)
			}
			return true
		})
	}
	if calls == 0 {
		t.Error("the scan saw no call of os.Getenv or os.LookupEnv; it read too little")
	}
}

// namesAVariable reports whether e names a server variable: a chain
// that joins to a GDOCS_ string, or one mentioning a constant config
// spells a name with.
func namesAVariable(e ast.Expr, constants map[string]bool) bool {
	if s, ok := joined(e); ok {
		return strings.HasPrefix(s, config.EnvPrefix)
	}
	named := false
	ast.Inspect(e, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			named = named || constants[n.Sel.Name]
			return false
		case *ast.Ident:
			named = named || constants[n.Name]
		}
		return true
	})
	return named
}

// nameConstants is every constant internal/config declares whose value
// is a chain from EnvPrefix, EnvPrefix included, by name.
func nameConstants(t *testing.T) map[string]bool {
	t.Helper()
	found := map[string]bool{}
	entries, err := os.ReadDir(configDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(configDir, name), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				value := spec.(*ast.ValueSpec)
				for i, ident := range value.Names {
					if i < len(value.Values) {
						if s, ok := joined(value.Values[i]); ok && strings.HasPrefix(s, config.EnvPrefix) {
							found[ident.Name] = true
						}
					}
				}
			}
		}
	}
	return found
}

// variableName is what a variable's name is, config's pattern for a
// word standing alone.
var variableName = regexp.MustCompile(`^` + config.NamePattern + `$`)

// words is s split into runs of capitals, digits and underscores, the
// characters a name is made of.
func words(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return !nameChar(r) })
}

// nameChar reports whether r can be part of a name.
func nameChar(r rune) bool {
	return r == '_' || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9')
}

// namesIn is every GDOCS_ name f spells, each with where it is spelled.
// A run of literals that another operand follows ends in a prefix
// rather than a name when its last character is part of a name, as in
// EnvPrefix + "ENABLE_" + kind, so that last word is not read.
func namesIn(f *ast.File) map[string]token.Pos {
	found := map[string]token.Pos{}
	spell := func(s string, at token.Pos) {
		for _, word := range words(s) {
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
				spell(strings.TrimRightFunc(run, nameChar), at)
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
// operand, which a scan reading only a chain's head would miss. A run
// that another operand follows ends in a prefix when it ends in a name's
// character, as EnvPrefix + "ENABLE_" + kind does, and the rest of it
// is still read. A name is config.NamePattern's, a doubled underscore
// included. A name built from another constant or through a format verb
// is not seen.
func TestTheScanReadsEachChainInOrder(t *testing.T) {
	const src = `package p

var (
	a = "set GDOCS_PLAIN=true"
	b = EnvPrefix + "JOINED"
	c = config.EnvPrefix + "SELECTED"
	d = f() + EnvPrefix + "LATER=" + g() + "GDOCS_" + "LAST"
	e = "x" + (EnvPrefix + "PAREN")
	h = fmt.Sprintf("%sFORMAT", EnvPrefix)
	i = other + "BUILT"
	j = EnvPrefix + "ENABLE_" + kind
	k = "set " + x + EnvPrefix + "NEW"
	l = "GDOCS_KEPT=1, " + EnvPrefix + "CUT_" + kind
	m = "GDOCS__DOUBLED"
)
`
	f, err := parser.ParseFile(token.NewFileSet(), "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"GDOCS_JOINED", "GDOCS_KEPT", "GDOCS_LAST", "GDOCS_LATER", "GDOCS_NEW", "GDOCS_PAREN",
		"GDOCS_PLAIN", "GDOCS_SELECTED", "GDOCS__DOUBLED"}
	if got := slices.Sorted(maps.Keys(namesIn(f))); !slices.Equal(got, want) {
		t.Errorf("namesIn = %v, want %v", got, want)
	}
}

// A name typed whole in a literal is found, in a struct tag apart, and
// one joined from EnvPrefix is not typed.
func TestLiteralNamesFindsANameTypedWhole(t *testing.T) {
	const src = `package p

type s struct {
	F string ` + "`json:\"f\" jsonschema:\"written under GDOCS_TAGGED\"`" + `
}

var (
	a = "set GDOCS_TYPED=true"
	b = EnvPrefix + "JOINED"
	c = "the GDOCS_* settings"
)
`
	f, err := parser.ParseFile(token.NewFileSet(), "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	typed, tagged := literalNames(f)
	if got := slices.Sorted(maps.Keys(typed)); !slices.Equal(got, []string{"GDOCS_TYPED"}) {
		t.Errorf("typed = %v, want [GDOCS_TYPED]", got)
	}
	if got := slices.Sorted(maps.Keys(tagged)); !slices.Equal(got, []string{"GDOCS_TAGGED"}) {
		t.Errorf("tagged = %v, want [GDOCS_TAGGED]", got)
	}
}

// A read is of a server variable when its argument joins to a GDOCS_
// string or mentions one of config's constants for a name.
func TestNamesAVariable(t *testing.T) {
	constants := map[string]bool{"EnvConfigDir": true}
	for src, want := range map[string]bool{
		`"GDOCS_ENABLE_DESTRUCTIVE"`:   true,
		`config.EnvPrefix + "PROFILE"`: true,
		`config.EnvConfigDir`:          true,
		`EnvConfigDir`:                 true,
		`"LIVE_OUT"`:                   false,
		`name`:                         false,
	} {
		e, err := parser.ParseExpr(src)
		if err != nil {
			t.Fatal(err)
		}
		if got := namesAVariable(e, constants); got != want {
			t.Errorf("namesAVariable(%s) = %t, want %t", src, got, want)
		}
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
