package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// The two files this gate reads, split the same way api-coverage splits
// its pair: nobody edits the snapshot and nothing generates the verdicts.
const (
	apiFieldsFile   = "testdata/api-fields.json"
	apiFieldVerdict = "testdata/api-fields.tsv"
)

// wireDir is the package this gate holds to the discovery document.
//
// Docs only, and deliberately. The Drive types are inline anonymous
// structs in internal/gapi/drive.go, so the rule below — match a struct
// name to a schema name — cannot see them; claiming to cover them would
// be the kind of half-met gate this repository keeps deleting.
const wireDir = "internal/gdocs"

type fieldSnapshot struct {
	Fetched string     `json:"fetched"`
	APIs    []fieldAPI `json:"apis"`
}

type fieldAPI struct {
	API       string        `json:"api"`
	Version   string        `json:"version"`
	Discovery string        `json:"discovery"`
	Schemas   []fieldSchema `json:"schemas"`
}

// fieldSchema is one published type and the properties it publishes.
type fieldSchema struct {
	Name       string   `json:"name"`
	Properties []string `json:"properties"`
}

// fieldVerdict is one line of the hand-written file. Property is "*" for
// a row about the whole schema.
type fieldVerdict struct {
	Schema, Property, Verdict, Reason string
	line                              int
}

// apiFields holds the wire types to the discovery document: for every
// struct in internal/gdocs that shares a name with a published schema,
// every published property is either modelled or written off with a
// reason, and every modelled field is either published or written off —
// and every struct that shares its name with no schema at all says so in
// a row, so that the set being compared is itself part of the rule.
//
// This gate exists because the types drifted and nothing said so. Issue
// #46 was a suggestion the API recorded and this server could not see,
// because internal/gdocs had no field for it; the spike that found out
// why also counted 39 other published fields the types silently dropped,
// SectionStyle alone missing all four margins. The evidence log had a row
// saying the types had drifted and the repository had nothing that would
// say it again. "A rule a comment cannot hold is a rule that needs a
// test" — so here it is as a test.
func apiFields(w io.Writer, _ []string) error {
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	snap, err := readFieldSnapshot(filepath.Join(root, apiFieldsFile))
	if err != nil {
		return err
	}
	published := map[string][]string{}
	for _, a := range snap.APIs {
		for _, s := range a.Schemas {
			published[s.Name] = s.Properties
		}
	}
	if len(published) < 100 {
		return fmt.Errorf("%s lists %d schemas; that is not a reading of the discovery document",
			apiFieldsFile, len(published))
	}

	verdicts, err := readFieldVerdicts(filepath.Join(root, apiFieldVerdict))
	if err != nil {
		return err
	}
	modelled, err := wireStructs(filepath.Join(root, wireDir))
	if err != nil {
		return err
	}
	if len(modelled) < 80 {
		return fmt.Errorf("found %d structs in %s; the derivation is not reading the package", len(modelled), wireDir)
	}

	problems, matched := fieldProblems(published, verdicts, modelled)
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	_, err = fmt.Fprintf(w, "api fields ok: %d schemas published, %d matched to %s, %d fields written off, snapshot fetched %s\n",
		len(published), matched, wireDir, countFieldVerdicts(verdicts, "out"), snap.Fetched)
	return err
}

func countFieldVerdicts(vs []fieldVerdict, kind string) int {
	n := 0
	for _, v := range vs {
		if v.Verdict == kind {
			n++
		}
	}
	return n
}

// fieldDecisions is the hand-written file once it has been read and
// judged: the rows that survived, indexed the way the three directions
// below ask about them. Rejected rows are left out on purpose — keeping
// one would let an invalid `out` excuse a missing field, and the gate
// would report the row's own fault and nothing else.
type fieldDecisions struct {
	alias map[string]string // published schema -> the struct modelling it
	// A struct some row accounts for: either an alias row names it as
	// what models a schema, or a local row says it models none. The two
	// are one set because the third direction asks one question of it.
	accountedFor map[string]bool
	accepted     map[string]bool // schema \t property \t verdict
}

func (d *fieldDecisions) written(schema, prop, kind string) bool {
	return d.accepted[schema+"\t"+prop+"\t"+kind]
}

// structFor is the struct that models a schema: its own name unless an
// alias row says otherwise.
func (d *fieldDecisions) structFor(schema string) string {
	if a, ok := d.alias[schema]; ok {
		return a
	}
	return schema
}

// fieldProblems is the rule, over three lists rather than three files.
// It returns the problems and how many published schemas it compared.
//
// Three directions, not two. The first two are per-property, over the
// schemas that are matched to a struct. The third is over the structs
// themselves: a wire type whose name no schema shares is either an alias
// target or a local type written off with a reason. Without that
// direction a renamed type simply stops being compared, and the only
// thing standing between that and a green gate is a count.
func fieldProblems(published map[string][]string, verdicts []fieldVerdict, modelled map[string]map[string]bool) ([]string, int) {
	d, problems := readFieldDecisions(published, modelled, verdicts)
	problems = append(problems, staleFieldRows(published, modelled, verdicts, d)...)
	compared, matched := compareFields(published, modelled, d)
	problems = append(problems, compared...)
	problems = append(problems, unmatchedStructs(published, modelled, d)...)
	return problems, matched
}

// readFieldDecisions judges each row on its own terms. It runs before
// anything else because the property rows are read against the struct an
// alias names.
func readFieldDecisions(published map[string][]string, modelled map[string]map[string]bool, verdicts []fieldVerdict) (*fieldDecisions, []string) {
	d := &fieldDecisions{
		alias: map[string]string{}, accountedFor: map[string]bool{}, accepted: map[string]bool{},
	}
	var problems []string
	problem := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}
	// withdrawn is the complaint every verdict but local shares.
	withdrawn := func(v fieldVerdict) {
		problem("%s:%d: %s is not a published schema any more; drop the row", apiFieldVerdict, v.line, v.Schema)
	}
	seen := map[string]int{}
	for _, v := range verdicts {
		key := v.Schema + "\t" + v.Property
		if first, dup := seen[key]; dup {
			problem("%s:%d: %s %s already has a verdict on line %d", apiFieldVerdict, v.line, v.Schema, v.Property, first)
			continue
		}
		seen[key] = v.line
		if strings.TrimSpace(v.Reason) == "" {
			problem("%s:%d: %s %s is %s with no reason given", apiFieldVerdict, v.line, v.Schema, v.Property, v.Verdict)
			continue
		}
		// local is the one verdict whose first column names a struct
		// rather than a schema, so it is also the one that is fine with a
		// name the discovery document does not carry.
		_, isPublished := published[v.Schema]
		switch v.Verdict {
		case "alias":
			switch _, isStruct := modelled[v.Reason]; {
			case !isPublished:
				withdrawn(v)
			case v.Property != "*":
				problem("%s:%d: %s is aliased as a whole schema; the property column must be *",
					apiFieldVerdict, v.line, v.Schema)
			case !isStruct:
				problem("%s:%d: %s is aliased to %q, which is not a struct in %s",
					apiFieldVerdict, v.line, v.Schema, v.Reason, wireDir)
			default:
				d.alias[v.Schema], d.accountedFor[v.Reason] = v.Reason, true
			}
		case "local":
			switch _, isStruct := modelled[v.Schema]; {
			case isPublished:
				problem("%s:%d: %s is a published schema, so it is not local; drop the row",
					apiFieldVerdict, v.line, v.Schema)
			case v.Property != "*":
				problem("%s:%d: %s is local as a whole type; the property column must be *",
					apiFieldVerdict, v.line, v.Schema)
			case !isStruct:
				problem("%s:%d: %s is written off as local and is not a struct in %s; drop the row",
					apiFieldVerdict, v.line, v.Schema, wireDir)
			default:
				d.accountedFor[v.Schema] = true
			}
		case "out", "extra":
			switch {
			case !isPublished:
				withdrawn(v)
			case v.Property == "*":
				problem("%s:%d: %s is %s for every property at once; %s is per-property",
					apiFieldVerdict, v.line, v.Schema, v.Verdict, v.Verdict)
			default:
				d.accepted[key+"\t"+v.Verdict] = true
			}
		default:
			problem("%s:%d: verdict %q is none of out, extra, alias or local", apiFieldVerdict, v.line, v.Verdict)
		}
	}
	return d, problems
}

// staleFieldRows catches a row that has outlived the thing it describes.
// api-coverage checks that for methods; without the same check here an
// `out` row for a property Google has withdrawn, or one the types have
// since grown, sits inert for good and still counts towards "N fields
// left out on purpose".
func staleFieldRows(published map[string][]string, modelled map[string]map[string]bool, verdicts []fieldVerdict, d *fieldDecisions) []string {
	var problems []string
	problem := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}
	for _, v := range verdicts {
		if !d.written(v.Schema, v.Property, v.Verdict) {
			continue
		}
		name := d.structFor(v.Schema)
		tags, isModelled := modelled[name]
		if !isModelled {
			problem("%s:%d: %s is published and no struct models it, so a verdict on one of its properties decides nothing; drop the row",
				apiFieldVerdict, v.line, v.Schema)
			continue
		}
		isPublished := slices.Contains(published[v.Schema], v.Property)
		switch {
		case v.Verdict == "out" && !isPublished:
			problem("%s:%d: %s.%s is written off and Google does not publish it any more; drop the row",
				apiFieldVerdict, v.line, v.Schema, v.Property)
		case v.Verdict == "out" && tags[v.Property]:
			problem("%s:%d: %s.%s is written off and %s models it now; drop the row",
				apiFieldVerdict, v.line, v.Schema, v.Property, structRef(name, v.Schema))
		case v.Verdict == "extra" && !tags[v.Property]:
			problem("%s:%d: %s.%s is declared an extra and no struct carries it any more; drop the row",
				apiFieldVerdict, v.line, v.Schema, v.Property)
		case v.Verdict == "extra" && isPublished:
			problem("%s:%d: %s.%s is declared an extra and Google publishes it now; drop the row",
				apiFieldVerdict, v.line, v.Schema, v.Property)
		}
	}
	return problems
}

// compareFields is the first two directions, per property, over the
// schemas that are matched to a struct. It also returns how many that
// was, which the gate reports.
func compareFields(published map[string][]string, modelled map[string]map[string]bool, d *fieldDecisions) ([]string, int) {
	var problems []string
	matched := 0
	for _, schema := range slices.Sorted(mapKeys(published)) {
		name := d.structFor(schema)
		tags, ok := modelled[name]
		if !ok {
			// A schema this package does not model at all is a different
			// question, and api-coverage is where capability lives.
			continue
		}
		matched++
		pub := map[string]bool{}
		for _, prop := range published[schema] {
			pub[prop] = true
			if tags[prop] || d.written(schema, prop, "out") {
				continue
			}
			problems = append(problems, fmt.Sprintf(
				"%s: %s.%s is published and %s does not model it; add the field, or a row saying why not",
				apiFieldVerdict, schema, prop, structRef(name, schema)))
		}
		// And the other direction: a field we carry that Google does not
		// publish is either a preview field or a mistake, and the file has
		// to say which.
		for _, tag := range slices.Sorted(mapKeys(tags)) {
			if pub[tag] || d.written(schema, tag, "extra") {
				continue
			}
			problems = append(problems, fmt.Sprintf(
				"%s: %s.%s is modelled and %s does not publish it; add a row saying why it is there",
				apiFieldVerdict, name, tag, schemaRef(schema, name)))
		}
	}
	return problems, matched
}

// unmatchedStructs is the third direction, over the structs rather than
// the schemas. A type renamed out of the way matches no schema, so before
// this the gate simply stopped comparing it and said ok — which is the
// whole failure mode a name match has.
func unmatchedStructs(published map[string][]string, modelled map[string]map[string]bool, d *fieldDecisions) []string {
	var problems []string
	for _, name := range slices.Sorted(mapKeys(modelled)) {
		if len(modelled[name]) == 0 {
			// Not a wire type: no field of it is ever on the wire.
			continue
		}
		if _, ok := published[name]; ok || d.accountedFor[name] {
			continue
		}
		problems = append(problems, fmt.Sprintf(
			"%s: %s is a struct in %s and no schema of that name is published; alias the schema it models to it, or add a local row saying it models none",
			apiFieldVerdict, name, wireDir))
	}
	return problems
}

// structRef names the struct, saying so only when it is not just the
// schema's own name: "SectionStyle" reads better than
// "SectionStyle.marginTop … SectionStyle.marginTop".
func structRef(name, schema string) string {
	if name == schema {
		return name
	}
	return name + " (modelling " + schema + ")"
}

// schemaRef is structRef the other way round.
func schemaRef(schema, name string) string {
	if name == schema {
		return schema
	}
	return schema + " (modelled by " + name + ")"
}

// wireStructs is every struct in the wire package by name, with the JSON
// tags it carries. Embedded structs are resolved, because a field
// promoted from Suggested is on the wire exactly as if it were declared.
func wireStructs(dir string) (map[string]map[string]bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	direct := map[string]map[string]bool{}
	embeds := map[string][]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			return nil, err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return true
			}
			tags := map[string]bool{}
			for _, f := range st.Fields.List {
				if len(f.Names) == 0 {
					// An embedded struct: gdocs embeds by plain name only.
					if id, ok := f.Type.(*ast.Ident); ok {
						embeds[ts.Name.Name] = append(embeds[ts.Name.Name], id.Name)
					}
					continue
				}
				if tag := jsonTag(f); tag != "" {
					tags[tag] = true
				}
			}
			direct[ts.Name.Name] = tags
			return true
		})
	}
	// Flatten the embeddings. Depth is two in this package and a visited
	// set keeps a future cycle from hanging the build.
	out := make(map[string]map[string]bool, len(direct))
	for name := range direct {
		tags := map[string]bool{}
		var add func(string, map[string]bool)
		add = func(n string, seen map[string]bool) {
			if seen[n] {
				return
			}
			seen[n] = true
			for t := range direct[n] {
				tags[t] = true
			}
			for _, e := range embeds[n] {
				add(e, seen)
			}
		}
		add(name, map[string]bool{})
		out[name] = tags
	}
	return out, nil
}

// jsonTag is the field's name on the wire, or "" for a field the API
// never sees.
func jsonTag(f *ast.Field) string {
	if f.Tag == nil {
		return ""
	}
	raw := strings.Trim(f.Tag.Value, "`")
	i := strings.Index(raw, `json:"`)
	if i < 0 {
		return ""
	}
	rest := raw[i+len(`json:"`):]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		return ""
	}
	name, _, _ := strings.Cut(rest[:j], ",")
	if name == "-" {
		return ""
	}
	return name
}

func readFieldSnapshot(path string) (*fieldSnapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s fieldSnapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &s, nil
}

func readFieldVerdicts(path string) ([]fieldVerdict, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []fieldVerdict
	for i, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			return nil, fmt.Errorf("%s:%d: expected schema, property, verdict and reason separated by tabs", path, i+1)
		}
		v := fieldVerdict{Schema: parts[0], Property: parts[1], Verdict: parts[2], line: i + 1}
		if len(parts) > 3 {
			v.Reason = strings.TrimSpace(parts[3])
		}
		out = append(out, v)
	}
	return out, nil
}

// writeFieldSnapshot rewrites the snapshot from the discovery documents
// api-diff has already fetched, reporting what moved. Called by api-diff,
// which is the one command here that touches the network — and which
// hands the documents over rather than letting this refetch them, so that
// the two snapshots it writes describe one reading of the API.
func writeFieldSnapshot(w io.Writer, root string, docsDoc *discoveryDoc) error {
	path := filepath.Join(root, apiFieldsFile)
	old, err := readFieldSnapshot(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if old == nil {
		old = &fieldSnapshot{}
	}
	fresh := &fieldSnapshot{Fetched: time.Now().UTC().Format("2006-01-02")}
	for _, d := range discovery {
		if d.api != "docs" {
			// See wireDir: only the Docs types are structs this gate can read.
			continue
		}
		if docsDoc == nil || len(docsDoc.schemas) == 0 {
			return fmt.Errorf("%s %s published no schemas; refusing to write that", d.api, d.version)
		}
		fresh.APIs = append(fresh.APIs, fieldAPI{API: d.api, Version: d.version, Discovery: d.url, Schemas: docsDoc.schemas})
	}

	index := func(s *fieldSnapshot) map[string]bool {
		m := map[string]bool{}
		for _, a := range s.APIs {
			for _, sc := range a.Schemas {
				for _, p := range sc.Properties {
					m[sc.Name+"."+p] = true
				}
			}
		}
		return m
	}
	before, after := index(old), index(fresh)
	var lines []string
	for _, k := range slices.Sorted(mapKeys(after)) {
		if !before[k] {
			lines = append(lines, "NEW FIELD  "+k)
		}
	}
	for _, k := range slices.Sorted(mapKeys(before)) {
		if !after[k] {
			lines = append(lines, "GONE FIELD "+k)
		}
	}
	data, err := json.MarshalIndent(fresh, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return err
	}
	if len(lines) == 0 {
		_, err = fmt.Fprintf(w, "api diff: no field moved; %s rewritten with today's date\n", apiFieldsFile)
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n\n%s rewritten. Every NEW FIELD on a schema this server models needs the field or a row in %s.\n",
		strings.Join(lines, "\n"), apiFieldsFile, apiFieldVerdict)
	return err
}

// discoverySchema is as much of a discovery document's schema as this
// gate reads: the property names, and the shape of any property that
// carries its own properties rather than a $ref.
type discoverySchema struct {
	Type       string                     `json:"type"`
	Properties map[string]discoverySchema `json:"properties"`
	Items      *discoverySchema           `json:"items"`
}

// flattenSchema is one published schema and every object defined inline
// inside it, each as a schema of its own named Parent.property.
//
// Docs v1 has none today — every nested type is a $ref to a named schema,
// checked against the live document. Drive v3 is the one of the seven
// documents these four servers read that does declare them, and there
// reading only the top level collapsed 166 sub-properties into 21 names
// and left the types modelling them matching no schema at all. The
// descent is here too because the omission is invisible until an API
// starts doing it, which is exactly how it bit the first time.
//
// Named rather than nested so that everything downstream — the alias
// rows, the per-property verdicts, both directions of the comparison —
// works on them unchanged.
func flattenSchema(name string, props map[string]discoverySchema) []fieldSchema {
	// A schema with no properties is still a published schema, so it is
	// recorded rather than skipped.
	out := []fieldSchema{{Name: name, Properties: slices.Sorted(mapKeys(props))}}
	for _, prop := range slices.Sorted(mapKeys(props)) {
		p := props[prop]
		// An array of inline objects is its element's shape; a $ref has
		// no properties here and is reached as its own schema.
		if p.Type == "array" && p.Items != nil {
			p = *p.Items
		}
		if p.Type == "object" && len(p.Properties) > 0 {
			out = append(out, flattenSchema(name+"."+prop, p.Properties)...)
		}
	}
	return out
}
