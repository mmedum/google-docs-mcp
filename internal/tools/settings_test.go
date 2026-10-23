package tools

import (
	"encoding/json"
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mmedum/google-docs-mcp/v3/internal/config"
)

// The model never widens its own reach: only the operator changes what
// this server may do, outside the conversation (§12a). These two tests
// hold that no tool can write the server's settings.

// No tool takes one of the server's settings as an input, nested ones
// included: a call that could name its own export directory, write mode
// default or destructive switch would move a limit the operator set.
// The settings are read from the flags config.Define registers.
func TestNoToolTakesAServerSetting(t *testing.T) {
	fs := flag.NewFlagSet("settings", flag.ContinueOnError)
	config.Define(fs, func(string) string { return "" })
	settings := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) { settings[strings.ReplaceAll(f.Name, "-", "_")] = true })
	if len(settings) < 9 {
		t.Fatalf("read %d settings; the check is not looking at the configuration", len(settings))
	}

	tools := listedUnder(t, FullSurface(config.Config{}))
	if len(tools) < 20 {
		t.Fatalf("read %d tools; the check is not looking at the surface", len(tools))
	}
	for _, tool := range tools {
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema any
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		for _, input := range inputNames(schema) {
			if settings[input] {
				t.Errorf("%s takes %s, which is the operator's setting", tool.Name, input)
			}
		}
	}
}

// inputNames is every property name in a JSON schema, at any depth.
func inputNames(schema any) []string {
	var out []string
	switch v := schema.(type) {
	case map[string]any:
		if props, ok := v["properties"].(map[string]any); ok {
			for name := range props {
				out = append(out, name)
			}
		}
		for _, child := range v {
			out = append(out, inputNames(child)...)
		}
	case []any:
		for _, child := range v {
			out = append(out, inputNames(child)...)
		}
	}
	return out
}

// No code a tool reaches can write a setting. The settings are the
// process environment and the profile login writes, with the token
// beside it; so the packages behind the tools neither import the ones
// that write the profile, the token or the sign-in, nor set an
// environment variable. The package list is followed from this package's
// own imports, not typed.
func TestNoToolCanWriteTheServersSettings(t *testing.T) {
	root, module := moduleOf(t)
	forbidden := []string{module + "/internal/userconfig", module + "/internal/credentials", module + "/internal/auth"}
	envWrites := []string{"Setenv", "Unsetenv", "Clearenv"}

	seen := map[string]bool{}
	queue := []string{module + "/internal/tools"}
	files := 0
	for len(queue) > 0 {
		pkg := queue[0]
		queue = queue[1:]
		if seen[pkg] {
			continue
		}
		seen[pkg] = true
		dir := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(pkg, module+"/")))
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, e.Name()), nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", e.Name(), err)
			}
			files++
			for _, imp := range file.Imports {
				path, _ := strconv.Unquote(imp.Path.Value)
				if slices.Contains(forbidden, path) {
					t.Errorf("%s/%s imports %s, which writes the server's own settings", pkg, e.Name(), path)
				}
				if strings.HasPrefix(path, module+"/") {
					queue = append(queue, path)
				}
			}
			ast.Inspect(file, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok {
					if id, ok := sel.X.(*ast.Ident); ok && id.Name == "os" && slices.Contains(envWrites, sel.Sel.Name) {
						t.Errorf("%s/%s calls os.%s", pkg, e.Name(), sel.Sel.Name)
					}
				}
				return true
			})
		}
	}
	if len(seen) < 8 || files < 60 {
		t.Fatalf("followed %d packages and %d files from internal/tools; the check is not reading the code", len(seen), files)
	}
}

// moduleOf is the directory holding go.mod and the module it names.
func moduleOf(t *testing.T) (root, module string) {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if data, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil {
			for line := range strings.Lines(string(data)) {
				if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
					return dir, strings.TrimSpace(rest)
				}
			}
			t.Fatal("go.mod names no module")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test")
		}
		dir = parent
	}
}
