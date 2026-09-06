package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/mmedum/google-docs-mcp/internal/gapi"
	"github.com/mmedum/google-docs-mcp/internal/service"
)

// errorfClass finds the class literal in a service.Errorf call.
var errorfClass = regexp.MustCompile(`Errorf\("([a-z_]+)"`)

// classes holds the design's oldest claim — that errors reach a model as
// `[class] message` from a fixed vocabulary — against the source rather
// than against a comment.
//
// It was not true when the check was written. The documented vocabulary
// had ten classes; the code emitted fourteen, four of which appeared
// nowhere in the documentation, and `ambiguous` meant both "your target
// matches several things" and "the write may or may not have landed" —
// two refusals asking the caller to do opposite things. A vocabulary
// spread over two packages with a comment in each is a vocabulary nobody
// can check.
//
// This is the one gate that imports the server's own packages, because
// the vocabulary is a value the server exports rather than a string in a
// document. That is also why it reads `internal/` rather than living
// there.
func classes(w io.Writer, _ []string) error {
	var problems []string
	known := service.Classes()
	if len(known) != len(slices.Compact(slices.Clone(known))) {
		problems = append(problems, fmt.Sprintf("service.Classes has duplicates: %v", known))
	}

	// The client's half must be a subset: gapi.Class feeds wrapAPI, which
	// puts the result straight in front of the model.
	for _, class := range gapi.Classes() {
		if !slices.Contains(known, class) {
			problems = append(problems, fmt.Sprintf(
				"gapi.Class can return %q, which service.Classes does not list", class))
		}
	}

	root, err := moduleRoot()
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	files := 0
	err = filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d os.DirEntry, err error) error {
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
				problems = append(problems, fmt.Sprintf(
					"%s: Errorf(%q) is not in service.Classes; add it there with what it asks the "+
						"reader to do, or use an existing class", rel, m[1]))
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if files < 20 {
		return fmt.Errorf("only %d source files scanned; the walk is not seeing the packages", files)
	}
	if len(seen) < 5 {
		return fmt.Errorf("only %d class literals found; the Errorf pattern has probably changed", len(seen))
	}

	// A class nothing can emit is a promise to the reader that no code
	// keeps. gapi.Class's members count as emitted.
	for _, class := range known {
		if !seen[class] && !slices.Contains(gapi.Classes(), class) {
			problems = append(problems, fmt.Sprintf("service.Classes lists %q, which nothing emits", class))
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	_, err = fmt.Fprintf(w, "classes ok: %d in the vocabulary, %d emitted directly, %d files scanned\n",
		len(known), len(seen), files)
	return err
}
