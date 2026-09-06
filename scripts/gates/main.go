// Command gates runs this repository's own checks.
//
// They were shell scripts until two went wrong in ways bash made easy: a
// hand-written package list that fell behind in silence, and a staleness
// rule that failed on the release it was written to guard. Being Go, they
// run wherever CI runs, they are held to gofmt, vet and lint like
// everything else, and they have tests of their own. GoReleaser builds
// only ./cmd/..., so none of this ships.
//
//	go run ./scripts/gates coverage cov.out 80
//	go run ./scripts/gates staleness ./google-docs-mcp
//	go run ./scripts/gates parity
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// command is one thing this program can do.
type command struct {
	run  func(io.Writer, []string) error
	args string
	doc  string
	// gate marks a command that must run in BOTH `make check` and the CI
	// workflow. The parity check reads this field, which is the whole
	// reason the registry exists: the two lists it compares used to be a
	// Makefile and a workflow with nothing tying them together.
	gate bool
}

// commands is the one list of what this program does — the usage text,
// the dispatch and the parity check all read it, so they cannot drift
// from each other.
var commands map[string]command

// init populates the registry rather than a var literal, because parity
// reads it and Go will not let a map initialiser refer to a function
// that refers back to the map.
func init() {
	commands = map[string]command{
		"tool-names": {
			run: toolNames, args: "FILE",
			doc: "print the tool names in a schema dump",
		},
		"schema-diff": {
			run: schemaDiff, args: "[BIN]", gate: true,
			doc: "diff the binary's tool schemas against the last tag",
		},
		"coverage": {
			run: coverage, args: "PROFILE MIN", gate: true,
			doc: "enforce the per-package statement floor",
		},
		"staleness": {
			run: stalenessCmd, args: "[BIN]", gate: true,
			doc: "documentation must match the code",
		},
		"smoke": {
			run: smoke, args: "[BIN]", gate: true,
			doc: "drive the binary over stdio, twice",
		},
		"leaks": {
			run: leaks, args: "", gate: true,
			doc: "nothing identifying may be in the repository",
		},
		"pins": {
			run: pins, args: "", gate: true,
			doc: "every action and every tool it installs is one exact version",
		},
		"classes": {
			run: classes, args: "", gate: true,
			doc: "the error classes the code emits are the ones it documents",
		},
		"parity": {
			run: parity, args: "", gate: true,
			doc: "`make check` and CI run the same gates",
		},
	}
}

func main() {
	if len(os.Args) < 2 {
		fail(usage())
	}
	c, ok := commands[os.Args[1]]
	if !ok {
		fail("unknown command %q\n\n%s", os.Args[1], usage())
	}
	if err := c.run(os.Stdout, os.Args[2:]); err != nil {
		fail("%v", err)
	}
}

func usage() string {
	var b strings.Builder
	b.WriteString("usage: gates COMMAND [ARGS]\n\n")
	for _, name := range slices.Sorted(mapKeys(commands)) {
		c := commands[name]
		mark := " "
		if c.gate {
			mark = "*"
		}
		fmt.Fprintf(&b, "  %s %-12s %-14s %s\n", mark, name, c.args, c.doc)
	}
	b.WriteString("\n  * runs in both `make check` and CI; the parity gate checks that.\n")
	return b.String()
}

func toolNames(w io.Writer, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: gates tool-names FILE")
	}
	d, err := read(args[0])
	if err != nil {
		return err
	}
	names := make([]string, 0, len(d.Tools))
	for _, t := range d.Tools {
		names = append(names, t.Name)
	}
	slices.Sort(names)
	_, err = fmt.Fprintln(w, strings.Join(names, "\n"))
	return err
}

func coverage(w io.Writer, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: gates coverage PROFILE MIN")
	}
	min, err := strconv.ParseFloat(args[1], 64)
	if err != nil {
		return fmt.Errorf("coverage: %w", err)
	}
	if err := coverageFloor(args[0], min); err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, "coverage floor ok")
	return err
}

func stalenessCmd(w io.Writer, args []string) error {
	bin := binOr(args)
	if err := staleness(bin); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w, "staleness check ok")
	return err
}

func binOr(args []string) string {
	if len(args) == 1 {
		return args[0]
	}
	return "./google-docs-mcp"
}

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

// diff reports what changed between two schema dumps and whether any of
// it breaks a caller: a tool or field that disappeared, or a field that
// became required.
func diff(w io.Writer, old, new *schemaDump) bool {
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
	_, _ = fmt.Fprintln(w, "added tools:", list(added))
	_, _ = fmt.Fprintln(w, "breaking changes:", list(breaking))
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

func read(path string) (*schemaDump, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	d, err := parseSchemas(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return d, nil
}

func parseSchemas(b []byte) (*schemaDump, error) {
	var d schemaDump
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	if len(d.Tools) == 0 {
		return nil, fmt.Errorf("no tools in the dump; this is not a schema document")
	}
	return &d, nil
}

// trimLine drops the trailing newline a command leaves behind.
func trimLine(s string) string { return strings.TrimSpace(s) }

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "gates: "+format+"\n", args...)
	os.Exit(2)
}

// moduleRoot walks up from the working directory to the directory
// holding go.mod. Every gate that reads the repository needs it, and it
// existed four times as a test helper before this file did.
func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod above %s", dir)
		}
		dir = parent
	}
}
