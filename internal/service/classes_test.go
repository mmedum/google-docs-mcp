package service_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/mmedum/google-docs-mcp/internal/gapi"
	"github.com/mmedum/google-docs-mcp/internal/service"
)

// errorfClass finds the class literal in a service.Errorf call.
var errorfClass = regexp.MustCompile(`Errorf\("([a-z_]+)"`)

// TestClassVocabularyIsClosed holds the design's oldest claim — that
// errors reach a model as `[class] message` from a fixed vocabulary —
// against the source rather than against a comment.
//
// It was not true when this test was written. The documented vocabulary
// had ten classes; the code emitted fourteen, four of which (unknown,
// stale, unavailable, unsupported) appeared nowhere in the docs, and
// `ambiguous` meant both "your target matches several things" and "the
// write may or may not have landed" — two refusals that ask the caller
// to do opposite things. A vocabulary spread over two packages with a
// comment in each is a vocabulary nobody can check.
func TestClassVocabularyIsClosed(t *testing.T) {
	known := service.Classes()
	if len(known) != len(slices.Compact(slices.Clone(known))) {
		t.Errorf("service.Classes has duplicates: %v", known)
	}

	// The client's half must be a subset: gapi.Class feeds wrapAPI, which
	// puts the result straight in front of the model.
	for _, class := range gapi.Classes() {
		if !slices.Contains(known, class) {
			t.Errorf("gapi.Class can return %q, which service.Classes does not list", class)
		}
	}

	root := moduleRoot(t)
	seen := map[string]bool{}
	files := 0
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files++
		for _, m := range errorfClass.FindAllStringSubmatch(string(data), -1) {
			seen[m[1]] = true
			if !slices.Contains(known, m[1]) {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s: Errorf(%q) is not in service.Classes; add it there with what it asks the reader to do, or use an existing class", rel, m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files < 20 {
		t.Fatalf("only %d source files scanned; the walk is not seeing the packages", files)
	}
	if len(seen) < 5 {
		t.Fatalf("only %d class literals found; the Errorf pattern has probably changed", len(seen))
	}

	// A class nothing can emit is a promise to the reader that no code
	// keeps. gapi.Class's members count as emitted.
	for _, class := range known {
		if !seen[class] && !slices.Contains(gapi.Classes(), class) {
			t.Errorf("service.Classes lists %q, which nothing emits", class)
		}
	}
	t.Logf("%d classes, %d emitted directly, %d files scanned", len(known), len(seen), files)
}

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
