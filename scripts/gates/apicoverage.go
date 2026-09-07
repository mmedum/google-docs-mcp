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

// The two files this gate reads, split by who writes them. Nobody edits
// the snapshot by hand and nothing but a person writes the verdicts:
// mixing the two is how a sibling ended up with machine-owned columns in
// a hand-kept file, checked only by a target CI never runs.
const (
	apiMethodsFile  = "testdata/api-methods.json"
	apiCoverageFile = "testdata/api-coverage.tsv"
)

// discovery is where each API publishes its own method list.
var discovery = []struct{ api, version, url string }{
	{"docs", "v1", "https://docs.googleapis.com/$discovery/rest?version=v1"},
	{"drive", "v3", "https://www.googleapis.com/discovery/v1/apis/drive/v3/rest"},
}

type apiSnapshot struct {
	Fetched string     `json:"fetched"`
	APIs    []apiEntry `json:"apis"`
}

type apiEntry struct {
	API       string      `json:"api"`
	Version   string      `json:"version"`
	Discovery string      `json:"discovery"`
	Methods   []apiMethod `json:"methods"`
}

// apiMethod is a published method as the discovery document gives it.
// The verb and the path are here rather than in the hand-written file
// because Google owns them: a method that keeps its name and changes its
// path is a break, and a break nobody would see in a list of names.
type apiMethod struct {
	Method string `json:"method"`
	Verb   string `json:"verb"`
	Path   string `json:"path"`
}

func (m apiMethod) String() string { return m.Verb + " " + m.Path }

// verdict is one line of the hand-written file.
type verdict struct {
	API, Method, Verdict, Reason string
	line                         int
}

// apiCoverage holds the published method list, the verdicts and the
// client to each other, offline.
//
// It reads the snapshot rather than the network on purpose. A gate that
// fails when Google is slow is one people learn to rerun until it
// passes, and completeness that lives only in a manual target is a claim
// CI never checks — which is the same as not having the gate, with a
// sentence in the documentation saying otherwise.
func apiCoverage(w io.Writer, _ []string) error {
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	snap, err := readSnapshot(filepath.Join(root, apiMethodsFile))
	if err != nil {
		return err
	}
	published := map[string]apiMethod{}
	for _, a := range snap.APIs {
		for _, m := range a.Methods {
			published[a.API+"\t"+m.Method] = m
		}
	}
	if len(snap.APIs) < 2 || len(published) < 20 {
		return fmt.Errorf("%s lists %d APIs and %d methods; that is not a reading of the discovery documents",
			apiMethodsFile, len(snap.APIs), len(published))
	}

	verdicts, err := readVerdicts(filepath.Join(root, apiCoverageFile))
	if err != nil {
		return err
	}
	calls, err := clientCalls(filepath.Join(root, "internal", "gapi"))
	if err != nil {
		return err
	}
	if len(calls) < 10 {
		return fmt.Errorf("found %d client calls in internal/gapi; the derivation is not reading the client", len(calls))
	}

	problems := coverageProblems(published, verdicts, calls)
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	_, err = fmt.Fprintf(w, "api coverage ok: %d methods published, %d used by %d client calls, snapshot fetched %s\n",
		len(published), countUsed(verdicts), len(calls), snap.Fetched)
	return err
}

// countUsed is the number of methods this server actually calls.
func countUsed(verdicts []verdict) int {
	n := 0
	for _, v := range verdicts {
		if v.Verdict == "used" {
			n++
		}
	}
	return n
}

// coverageProblems is the rule, over three lists rather than three files.
// The reading is above it: a fix that lives in the reader is one any
// caller can walk past, which a sibling proved by watching its own tests
// bypass exactly such a fix.
func coverageProblems(published map[string]apiMethod, verdicts []verdict, calls []string) []string {
	var problems []string
	seen := map[string]int{}
	used := map[string]string{}
	for _, v := range verdicts {
		key := v.API + "\t" + v.Method
		if _, ok := published[key]; !ok {
			problems = append(problems, fmt.Sprintf("%s:%d: %s %s has a verdict and is not a published method any more",
				apiCoverageFile, v.line, v.API, v.Method))
			continue
		}
		if first, dup := seen[key]; dup {
			problems = append(problems, fmt.Sprintf("%s:%d: %s %s already has a verdict on line %d",
				apiCoverageFile, v.line, v.API, v.Method, first))
			continue
		}
		seen[key] = v.line
		switch v.Verdict {
		case "used":
			if !slices.Contains(calls, v.Reason) {
				problems = append(problems, fmt.Sprintf(
					"%s:%d: %s %s says it is used by %q, which is not a call in internal/gapi",
					apiCoverageFile, v.line, v.API, v.Method, v.Reason))
				continue
			}
			used[v.Reason] = v.API + " " + v.Method
		case "out":
			if strings.TrimSpace(v.Reason) == "" {
				problems = append(problems, fmt.Sprintf("%s:%d: %s %s is out with no reason given",
					apiCoverageFile, v.line, v.API, v.Method))
			}
		default:
			problems = append(problems, fmt.Sprintf("%s:%d: verdict %q is neither used nor out",
				apiCoverageFile, v.line, v.Verdict))
		}
	}

	// A capability Google adds must fail the build, which is the whole
	// point of keeping the snapshot in the repository.
	for key, m := range published {
		if _, ok := seen[key]; !ok {
			api, method, _ := strings.Cut(key, "\t")
			problems = append(problems, fmt.Sprintf("%s: %s %s (%s) is published and has no verdict; "+
				"add a row saying whether this server uses it or why it does not", apiCoverageFile, api, method, m))
		}
	}
	// And a call this server makes must be accounted for in the other
	// direction, or the coverage claim is about a client nobody read.
	for _, call := range calls {
		if _, ok := used[call]; !ok {
			problems = append(problems, fmt.Sprintf("%s: internal/gapi.%s calls an API and no row names it as used",
				apiCoverageFile, call))
		}
	}

	return problems
}

// apiDiff refetches the discovery documents and rewrites the snapshot,
// reporting what moved. It is deliberately not a gate: it needs the
// network, and a check that fails when Google is slow is one people
// learn to rerun rather than read.
func apiDiff(w io.Writer, _ []string) error {
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	path := filepath.Join(root, apiMethodsFile)
	old, err := readSnapshot(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if old == nil {
		old = &apiSnapshot{}
	}

	fresh := &apiSnapshot{Fetched: time.Now().UTC().Format("2006-01-02")}
	for _, d := range discovery {
		methods, err := fetchDiscovery(d.url)
		if err != nil {
			return fmt.Errorf("%s %s: %w", d.api, d.version, err)
		}
		if len(methods) == 0 {
			return fmt.Errorf("%s %s published no methods; refusing to write that", d.api, d.version)
		}
		fresh.APIs = append(fresh.APIs, apiEntry{API: d.api, Version: d.version, Discovery: d.url, Methods: methods})
	}

	index := func(s *apiSnapshot) map[string]apiMethod {
		m := map[string]apiMethod{}
		for _, a := range s.APIs {
			for _, x := range a.Methods {
				m[a.API+" "+x.Method] = x
			}
		}
		return m
	}
	before, after := index(old), index(fresh)
	var lines []string
	for _, k := range slices.Sorted(mapKeys(after)) {
		b, had := before[k]
		switch {
		case !had:
			lines = append(lines, "NEW     "+k+"  "+after[k].String())
		case b.String() != after[k].String():
			lines = append(lines, "CHANGED "+k+"  "+b.String()+" -> "+after[k].String())
		}
	}
	for _, k := range slices.Sorted(mapKeys(before)) {
		if _, still := after[k]; !still {
			lines = append(lines, "GONE    "+k+"  "+before[k].String())
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
		_, err = fmt.Fprintf(w, "api diff: nothing moved; %s rewritten with today's date\n", apiMethodsFile)
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n\n%s rewritten. Every NEW method needs a verdict in %s before `make check` passes.\n",
		strings.Join(lines, "\n"), apiMethodsFile, apiCoverageFile)
	return err
}

func fetchDiscovery(url string) ([]apiMethod, error) {
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
		Resources map[string]json.RawMessage `json:"resources"`
	}
	if err := json.NewDecoder(res.Body).Decode(&doc); err != nil {
		return nil, err
	}
	var methods []apiMethod
	if err := walkResources(doc.Resources, &methods); err != nil {
		return nil, err
	}
	sort.Slice(methods, func(i, j int) bool { return methods[i].Method < methods[j].Method })
	return methods, nil
}

// walkResources collects every method of every resource, at any depth:
// Drive nests replies under comments, and a reader that only looks one
// level down reports coverage of an API it has not seen.
func walkResources(resources map[string]json.RawMessage, out *[]apiMethod) error {
	for _, raw := range resources {
		var r struct {
			Methods map[string]struct {
				ID         string `json:"id"`
				HTTPMethod string `json:"httpMethod"`
				Path       string `json:"path"`
			} `json:"methods"`
			Resources map[string]json.RawMessage `json:"resources"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			return err
		}
		for _, m := range r.Methods {
			// The id is "drive.files.list"; the API's own name is not
			// part of what this file is about.
			name := m.ID
			if _, rest, ok := strings.Cut(name, "."); ok {
				name = rest
			}
			*out = append(*out, apiMethod{Method: name, Verb: m.HTTPMethod, Path: m.Path})
		}
		if err := walkResources(r.Resources, out); err != nil {
			return err
		}
	}
	return nil
}

func readSnapshot(path string) (*apiSnapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s apiSnapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &s, nil
}

func readVerdicts(path string) ([]verdict, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []verdict
	for i, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			return nil, fmt.Errorf("%s:%d: expected api, method, verdict and reason separated by tabs", path, i+1)
		}
		v := verdict{API: parts[0], Method: parts[1], Verdict: parts[2], line: i + 1}
		if len(parts) > 3 {
			v.Reason = strings.TrimSpace(parts[3])
		}
		out = append(out, v)
	}
	return out, nil
}

// clientCalls is every exported method on *Client that takes a context
// first and returns an error last, which is what an API call looks like
// here.
//
// A sibling derives the same list by reflection, which is shorter and
// sees promoted methods — but that is safe there only because its client
// package has a test refusing any method that is neither a call by this
// rule nor a named reporter. internal/gapi has no such test, so the rule
// stays syntactic: a helper wrongly counted as a call would report
// coverage this server does not have, which is worse than no gate.
func clientCalls(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	var names []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			return nil, err
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || !fn.Name.IsExported() || !receiverIsClient(fn) {
				continue
			}
			if takesContextFirst(fn) && returnsErrorLast(fn) {
				names = append(names, fn.Name.Name)
			}
		}
	}
	sort.Strings(names)
	return names, nil
}

func receiverIsClient(fn *ast.FuncDecl) bool {
	if len(fn.Recv.List) != 1 {
		return false
	}
	star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	id, ok := star.X.(*ast.Ident)
	return ok && id.Name == "Client"
}

func takesContextFirst(fn *ast.FuncDecl) bool {
	if fn.Type.Params == nil || len(fn.Type.Params.List) == 0 {
		return false
	}
	sel, ok := fn.Type.Params.List[0].Type.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "context" && sel.Sel.Name == "Context"
}

func returnsErrorLast(fn *ast.FuncDecl) bool {
	if fn.Type.Results == nil || len(fn.Type.Results.List) == 0 {
		return false
	}
	last := fn.Type.Results.List[len(fn.Type.Results.List)-1]
	id, ok := last.Type.(*ast.Ident)
	return ok && id.Name == "error"
}
