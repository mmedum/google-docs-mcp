package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
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
// reason, and every modelled field is either published or written off.
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
	// A struct renamed out of the way would quietly stop being checked,
	// so the number of schemas actually compared is part of the rule.
	if matched < 80 {
		problems = append(problems, fmt.Sprintf(
			"only %d published schemas were matched to a struct in %s; either the package shrank or a rename "+
				"has taken types out of this gate's sight (add an alias row if a type was renamed)", matched, wireDir))
	}
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

// fieldProblems is the rule, over three lists rather than three files.
// It returns the problems and how many published schemas it compared.
func fieldProblems(published map[string][]string, verdicts []fieldVerdict, modelled map[string]map[string]bool) ([]string, int) {
	var problems []string

	// Schema-level rows first: an alias says which struct models a schema
	// whose name this package does not use.
	alias := map[string]string{}
	seen := map[string]int{}
	for _, v := range verdicts {
		key := v.Schema + "\t" + v.Property
		if first, dup := seen[key]; dup {
			problems = append(problems, fmt.Sprintf("%s:%d: %s %s already has a verdict on line %d",
				apiFieldVerdict, v.line, v.Schema, v.Property, first))
			continue
		}
		seen[key] = v.line
		if _, ok := published[v.Schema]; !ok {
			problems = append(problems, fmt.Sprintf("%s:%d: %s is not a published schema any more; drop the row",
				apiFieldVerdict, v.line, v.Schema))
			continue
		}
		if strings.TrimSpace(v.Reason) == "" {
			problems = append(problems, fmt.Sprintf("%s:%d: %s %s is %s with no reason given",
				apiFieldVerdict, v.line, v.Schema, v.Property, v.Verdict))
			continue
		}
		switch v.Verdict {
		case "alias":
			if _, ok := modelled[v.Reason]; !ok {
				problems = append(problems, fmt.Sprintf("%s:%d: %s is aliased to %q, which is not a struct in %s",
					apiFieldVerdict, v.line, v.Schema, v.Reason, wireDir))
				continue
			}
			alias[v.Schema] = v.Reason
		case "out", "extra":
			if v.Property == "*" {
				problems = append(problems, fmt.Sprintf("%s:%d: %s is %s for every property at once; %s is per-property",
					apiFieldVerdict, v.line, v.Schema, v.Verdict, v.Verdict))
			}
		default:
			problems = append(problems, fmt.Sprintf("%s:%d: verdict %q is none of out, extra or alias",
				apiFieldVerdict, v.line, v.Verdict))
		}
	}

	written := func(schema, prop, kind string) bool {
		for _, v := range verdicts {
			if v.Schema == schema && v.Property == prop && v.Verdict == kind {
				return true
			}
		}
		return false
	}

	matched := 0
	for _, schema := range slices.Sorted(mapKeys(published)) {
		name := schema
		if a, ok := alias[schema]; ok {
			name = a
		}
		tags, ok := modelled[name]
		if !ok {
			// A schema this package does not model at all is a different
			// question, and api-coverage is where capability lives.
			continue
		}
		matched++
		for _, prop := range published[schema] {
			if tags[prop] || written(schema, prop, "out") {
				continue
			}
			problems = append(problems, fmt.Sprintf(
				"%s: %s.%s is published and %s does not model it; add the field, or a row saying why not",
				apiFieldVerdict, schema, prop, structRef(name, schema)))
		}
		// And the other direction: a field we carry that Google does not
		// publish is either a preview field or a mistake, and the file has
		// to say which.
		pub := map[string]bool{}
		for _, p := range published[schema] {
			pub[p] = true
		}
		for _, tag := range slices.Sorted(mapKeys(tags)) {
			if pub[tag] || written(schema, tag, "extra") {
				continue
			}
			problems = append(problems, fmt.Sprintf(
				"%s: %s.%s is modelled and %s does not publish it; add a row saying why it is there",
				apiFieldVerdict, name, tag, schemaRef(schema, name)))
		}
	}
	return problems, matched
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

// fetchSchemas reads the type half of a discovery document, where
// fetchDiscovery reads the method half.
func fetchSchemas(url string) ([]fieldSchema, error) {
	ctx, cancel := contextWithTimeout(30 * time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discovery returned %s", res.Status)
	}
	var doc struct {
		Schemas map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"schemas"`
	}
	if err := json.NewDecoder(res.Body).Decode(&doc); err != nil {
		return nil, err
	}
	out := make([]fieldSchema, 0, len(doc.Schemas))
	for name, s := range doc.Schemas {
		out = append(out, fieldSchema{Name: name, Properties: slices.Sorted(mapKeys(s.Properties))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
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

// writeFieldSnapshot refetches the schemas and rewrites the snapshot,
// reporting what moved. Called by api-diff, which is the one command
// here that touches the network.
func writeFieldSnapshot(w io.Writer, root string) error {
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
		schemas, err := fetchSchemas(d.url)
		if err != nil {
			return fmt.Errorf("%s %s schemas: %w", d.api, d.version, err)
		}
		if len(schemas) == 0 {
			return fmt.Errorf("%s %s published no schemas; refusing to write that", d.api, d.version)
		}
		fresh.APIs = append(fresh.APIs, fieldAPI{API: d.api, Version: d.version, Discovery: d.url, Schemas: schemas})
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
