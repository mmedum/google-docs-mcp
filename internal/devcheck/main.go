// Command devcheck is the JSON half of the repository's gates. The
// shell scripts do the git and file plumbing; anything that has to read
// a tool schema happens here, in the language the project is written in.
//
//	go run ./internal/devcheck tool-names schemas.json
//	go run ./internal/devcheck schema-diff old.json new.json
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
)

// schemaDump is the shape of `google-docs-mcp --dump-schemas`.
type schemaDump struct {
	Tools []struct {
		Name        string `json:"name"`
		InputSchema struct {
			Required   []string                   `json:"required"`
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"inputSchema"`
	} `json:"tools"`
}

func main() {
	if len(os.Args) < 2 {
		fail("usage: devcheck tool-names FILE | schema-diff OLD NEW")
	}
	switch os.Args[1] {
	case "tool-names":
		if len(os.Args) != 3 {
			fail("usage: devcheck tool-names FILE")
		}
		d := read(os.Args[2])
		names := make([]string, 0, len(d.Tools))
		for _, t := range d.Tools {
			names = append(names, t.Name)
		}
		slices.Sort(names)
		fmt.Println(strings.Join(names, "\n"))
	case "schema-diff":
		if len(os.Args) != 4 {
			fail("usage: devcheck schema-diff OLD NEW")
		}
		if breaking := diff(read(os.Args[2]), read(os.Args[3])); breaking {
			os.Exit(1)
		}
	default:
		fail("unknown command %q", os.Args[1])
	}
}

// diff reports what changed between two schema dumps and whether any of
// it breaks a caller: a tool or field that disappeared, or a field that
// became required.
func diff(old, new *schemaDump) bool {
	type tool = struct {
		Name        string `json:"name"`
		InputSchema struct {
			Required   []string                   `json:"required"`
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"inputSchema"`
	}
	index := func(d *schemaDump) map[string]tool {
		m := make(map[string]tool, len(d.Tools))
		for _, t := range d.Tools {
			m[t.Name] = t
		}
		return m
	}
	o, n := index(old), index(new)

	var breaking, added []string
	for _, name := range slices.Sorted(mapKeys(o)) {
		nt, ok := n[name]
		if !ok {
			breaking = append(breaking, "tool removed: "+name)
			continue
		}
		for _, f := range nt.InputSchema.Required {
			if !slices.Contains(o[name].InputSchema.Required, f) {
				breaking = append(breaking, fmt.Sprintf("%s: new required field %s", name, f))
			}
		}
		for _, f := range slices.Sorted(mapKeys(o[name].InputSchema.Properties)) {
			if _, ok := nt.InputSchema.Properties[f]; !ok {
				breaking = append(breaking, fmt.Sprintf("%s: field removed %s", name, f))
			}
		}
	}
	for _, name := range slices.Sorted(mapKeys(n)) {
		if _, ok := o[name]; !ok {
			added = append(added, name)
		}
	}
	fmt.Println("added tools:", list(added))
	fmt.Println("breaking changes:", list(breaking))
	return len(breaking) > 0
}

func list(xs []string) string {
	if len(xs) == 0 {
		return "none"
	}
	return "[" + strings.Join(xs, ", ") + "]"
}

func mapKeys[V any](m map[string]V) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

func read(path string) *schemaDump {
	b, err := os.ReadFile(path)
	if err != nil {
		fail("%v", err)
	}
	var d schemaDump
	if err := json.Unmarshal(b, &d); err != nil {
		fail("%s: %v", path, err)
	}
	return &d
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "devcheck: "+format+"\n", args...)
	os.Exit(2)
}
