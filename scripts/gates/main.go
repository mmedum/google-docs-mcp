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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
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
	// pr marks a command only a pull request can run, since it measures
	// from the merge-base. Parity holds it to a `run:` step in ci.yml and
	// keeps it out of `make check`.
	pr bool
}

// commands is the one list of what this program does — the usage text,
// the dispatch and the parity check all read it, so they cannot drift
// from each other.
var commands map[string]command

// init populates the registry rather than a var literal, because parity
// reads it and Go will not let a map initializer refer to a function
// that refers back to the map.
func init() {
	commands = map[string]command{
		"tool-names": {
			run: toolNames, args: "FILE",
			doc: "print the tool names in a schema dump",
		},
		"release-notes": {
			run: releaseNotes, args: "VERSION [CHANGELOG]",
			doc: "one version's CHANGELOG section, which is the release note",
		},
		"schema-ack": {
			run: schemaAck, args: "BASE HEAD", pr: true,
			doc: "a tool-surface change since the merge-base carries a SCHEMA-CHANGE: or BREAKING CHANGE: footer",
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
		"transcript": {
			run: transcript, args: "", gate: true,
			doc: "the live driver and the evals put nothing in their transcript unredacted",
		},
		"parity": {
			run: parity, args: "", gate: true,
			doc: "`make check` and CI run the same gates",
		},
		"api-coverage": {
			run: apiCoverage, args: "", gate: true,
			doc: "every published API method has a verdict, and every call has a row",
		},
		"api-fields": {
			run: apiFields, args: "", gate: true,
			doc: "every published field of a type we model is modeled or written off",
		},
		"mcpb": {
			run: mcpbCmd, gate: true,
			doc: "the bundle manifest describes the bundle the packer stages",
		},
		"mcpb-pack": {
			run: mcpbPackCmd, args: "DIST VERSION OUT",
			doc: "pack the .mcpb from a built dist tree; runs at release time",
		},
		"registry-publish": {
			run: registryPublishCmd, args: "VERSION CHECKSUMS",
			doc: "the MCP registry entry, with the bundle's hash from the published checksums",
		},
		"api-diff": {
			run: apiDiff, args: "",
			doc: "refetch the discovery documents and rewrite the snapshot (network)",
		},
		"schema-refetch": {
			run: schemaRefetchCmd, args: "",
			doc: "check the vendored schemas against what their sources serve (network)",
		},
	}
}

func main() {
	if len(os.Args) < 2 {
		fail("%s", usage())
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
	Tools []dumpedTool `json:"tools"`
	// whole is every key of the dump but the build stamp, parsed. It is
	// what a change is measured on, so a key the dump gains later is
	// compared without this file knowing its name.
	whole map[string]any
}

// stampKeys are the dump's build stamp. They change with every build and
// are not part of the surface; what an SDK bump changes on the wire
// shows up inside the tools.
var stampKeys = []string{"server", "version", "sdk"}

// dumpedTool is what the other gates read of a tool: its name. The
// comparison reads the whole tool from schemaDump.whole.
type dumpedTool struct {
	Name string `json:"name"`
}

// diff reports what changed between two schema dumps and returns each
// change that breaks a caller.
func diff(w io.Writer, old, new *schemaDump) []string {
	c := compareSurfaces(old, new)
	c.write(w)
	return c.breaking
}

// surfaceChanges is what differs between two dumps. breaking is what
// breaks a caller: a tool, resource template, argument or output field
// that disappeared, or an argument that became required. changed is
// every other difference, an annotation or a description included.
type surfaceChanges struct {
	breaking, added, changed []string
}

func (c surfaceChanges) empty() bool {
	return len(c.breaking) == 0 && len(c.added) == 0 && len(c.changed) == 0
}

func (c surfaceChanges) write(w io.Writer) {
	_, _ = fmt.Fprintln(w, "added:", list(c.added))
	_, _ = fmt.Fprintln(w, "breaking changes:", list(c.breaking))
	_, _ = fmt.Fprintln(w, "changed:", list(c.changed))
}

// compareSurfaces compares every top-level key of two dumps. A list is
// compared item by item, keyed by identity (itemIndex): an item gone
// breaks a caller, a new one is added, and any other difference names
// the parts that moved. A tool still there breaks a caller when it loses
// an argument or output field or gains a required one (toolBreaks).
func compareSurfaces(old, new *schemaDump) surfaceChanges {
	var c surfaceChanges
	for _, key := range sortedUnion(old.whole, new.whole) {
		ol, oList := asList(old.whole[key])
		nl, nList := asList(new.whole[key])
		oi, oKeyed := itemIndex(ol)
		ni, nKeyed := itemIndex(nl)
		if !oList || !nList || !oKeyed || !nKeyed {
			if !reflect.DeepEqual(old.whole[key], new.whole[key]) {
				c.changed = append(c.changed, key)
			}
			continue
		}
		label := itemLabel(key)
		for _, id := range slices.Sorted(mapKeys(oi)) {
			nv, ok := ni[id]
			if !ok {
				c.breaking = append(c.breaking, label+" removed: "+id)
				continue
			}
			if key == "tools" {
				if breaks := toolBreaks(id, oi[id], nv); len(breaks) > 0 {
					c.breaking = append(c.breaking, breaks...)
					continue
				}
			}
			if parts := changedParts(oi[id], nv); len(parts) > 0 {
				c.changed = append(c.changed, label+" "+id+" ("+strings.Join(parts, ", ")+")")
			}
		}
		for _, id := range slices.Sorted(mapKeys(ni)) {
			if _, ok := oi[id]; !ok {
				c.added = append(c.added, label+" "+id)
			}
		}
	}
	return c
}

// toolBreaks is what breaks a caller of a tool that is still there: an
// argument or output field gone, or an argument that became required.
func toolBreaks(name string, old, new map[string]any) []string {
	var breaks []string
	for _, f := range stringList(new, "inputSchema", "required") {
		if !slices.Contains(stringList(old, "inputSchema", "required"), f) {
			breaks = append(breaks, fmt.Sprintf("%s: new required field %s", name, f))
		}
	}
	for _, part := range []struct{ schema, what string }{{"inputSchema", "field"}, {"outputSchema", "output field"}} {
		was, now := object(old, part.schema, "properties"), object(new, part.schema, "properties")
		for _, f := range slices.Sorted(mapKeys(was)) {
			if _, ok := now[f]; !ok {
				breaks = append(breaks, fmt.Sprintf("%s: %s removed %s", name, part.what, f))
			}
		}
	}
	return breaks
}

// object follows keys down a dumped object, nil where one is missing.
func object(m map[string]any, keys ...string) map[string]any {
	for _, k := range keys {
		m, _ = m[k].(map[string]any)
	}
	return m
}

// stringList is the list of strings at the end of keys.
func stringList(m map[string]any, keys ...string) []string {
	list, _ := object(m, keys[:len(keys)-1]...)[keys[len(keys)-1]].([]any)
	var out []string
	for _, v := range list {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// asList reads a dumped value as a list. A key one side lacks is an
// empty list, so a list the dump gains reads as items added.
func asList(v any) ([]any, bool) {
	if v == nil {
		return nil, true
	}
	l, ok := v.([]any)
	return l, ok
}

// itemLabel is how a report names an item of a top-level list.
func itemLabel(key string) string {
	switch key {
	case "tools":
		return "tool"
	case "resourceTemplates":
		return "resource template"
	}
	return key
}

// itemIndex keys a dumped list by each item's identity: a resource
// template's uriTemplate, a resource's uri, a tool's name. A template's
// name is only its label. It reports false when an item has none, and
// the list is then compared as one value, since a position is no
// identity: an item inserted would shift every one after it.
func itemIndex(items []any) (map[string]map[string]any, bool) {
	m := make(map[string]map[string]any, len(items))
	for _, item := range items {
		obj, _ := item.(map[string]any)
		id := ""
		for _, k := range []string{"uriTemplate", "uri", "name"} {
			if v, ok := obj[k].(string); ok {
				id = v
				break
			}
		}
		if id == "" {
			return nil, false
		}
		m[id] = obj
	}
	return m, true
}

// changedParts names what differs between two dumped objects, one level
// deep, so a flipped hint reads as annotations.destructiveHint.
func changedParts(o, n map[string]any) []string {
	var parts []string
	for _, k := range sortedUnion(o, n) {
		if reflect.DeepEqual(o[k], n[k]) {
			continue
		}
		om, oMap := o[k].(map[string]any)
		nm, nMap := n[k].(map[string]any)
		if !oMap || !nMap {
			parts = append(parts, k)
			continue
		}
		for _, sub := range sortedUnion(om, nm) {
			if !reflect.DeepEqual(om[sub], nm[sub]) {
				parts = append(parts, k+"."+sub)
			}
		}
	}
	return parts
}

// sortedUnion is every key of a and b, sorted.
func sortedUnion[V any](a, b map[string]V) []string {
	keys := slices.Collect(mapKeys(a))
	for k := range b {
		if _, ok := a[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	return keys
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
	if err := json.Unmarshal(b, &d.whole); err != nil {
		return nil, err
	}
	for _, k := range stampKeys {
		delete(d.whole, k)
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

// contextWithTimeout keeps the one network call in this program bounded.
func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}
