package main

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The gate itself, over this repository's own snapshot and verdicts.
func TestAPIFieldsGate(t *testing.T) {
	if err := apiFields(io.Discard, nil); err != nil {
		t.Errorf("api fields gate: %v", err)
	}
}

func publishedFields() map[string][]string {
	return map[string][]string{
		"TextRun":      {"content", "suggestedInsertionIds", "textStyle"},
		"SectionStyle": {"marginTop", "sectionType"},
		"PageBreak":    {"suggestedInsertionIds", "textStyle"},
	}
}

func modelledFields() map[string]map[string]bool {
	return map[string]map[string]bool{
		"TextRun":      {"content": true, "suggestedInsertionIds": true, "textStyle": true},
		"SectionStyle": {"marginTop": true, "sectionType": true},
		"Break":        {"suggestedInsertionIds": true, "textStyle": true},
	}
}

func TestFieldProblems(t *testing.T) {
	ok := []fieldVerdict{
		{Schema: "PageBreak", Property: "*", Verdict: "alias", Reason: "Break", line: 1},
	}

	cases := []struct {
		name      string
		published map[string][]string
		verdicts  []fieldVerdict
		modelled  map[string]map[string]bool
		want      string
		// andAlso is the second thing the same input must report, for the
		// cases where a bad row must not also silence a real problem.
		andAlso string
	}{
		{name: "a matched set", published: publishedFields(), verdicts: ok, modelled: modelledFields(), want: ""},
		{
			name:      "Google published a field nobody models or writes off",
			published: withProp(publishedFields(), "TextRun", "somethingNew"),
			verdicts:  ok,
			modelled:  modelledFields(),
			want:      "TextRun.somethingNew is published and TextRun does not model it",
		},
		{
			name:      "a published field written off with a reason is fine",
			published: withProp(publishedFields(), "TextRun", "somethingNew"),
			verdicts: append(slices.Clone(ok),
				fieldVerdict{Schema: "TextRun", Property: "somethingNew", Verdict: "out", Reason: "no tool reads it", line: 2}),
			modelled: modelledFields(),
			want:     "",
		},
		{
			name:      "written off with no reason at all",
			published: withProp(publishedFields(), "TextRun", "somethingNew"),
			verdicts: append(slices.Clone(ok),
				fieldVerdict{Schema: "TextRun", Property: "somethingNew", Verdict: "out", line: 2}),
			modelled: modelledFields(),
			want:     "is out with no reason given",
			// And the rejected row must not excuse the field it names.
			// It used to: every row was searched, valid or not, so a row
			// the gate had just complained about still counted as a
			// verdict and the missing field went unreported.
			andAlso: "TextRun.somethingNew is published and TextRun does not model it",
		},
		{
			name:      "a field we model that Google does not publish",
			published: publishedFields(),
			verdicts:  ok,
			modelled:  withTag(modelledFields(), "TextRun", "inventedByUs"),
			want:      "TextRun.inventedByUs is modelled and TextRun does not publish it",
		},
		{
			name:      "an unpublished field declared as a preview extra",
			published: publishedFields(),
			verdicts: append(slices.Clone(ok),
				fieldVerdict{Schema: "TextRun", Property: "inventedByUs", Verdict: "extra", Reason: "Developer Preview", line: 2}),
			modelled: withTag(modelledFields(), "TextRun", "inventedByUs"),
			want:     "",
		},
		{
			name:      "an alias naming a struct that does not exist",
			published: publishedFields(),
			verdicts:  []fieldVerdict{{Schema: "PageBreak", Property: "*", Verdict: "alias", Reason: "Fictional", line: 1}},
			modelled:  modelledFields(),
			want:      `aliased to "Fictional", which is not a struct`,
		},
		{
			name:      "a row for a schema Google has withdrawn",
			published: publishedFields(),
			verdicts: append(slices.Clone(ok),
				fieldVerdict{Schema: "Telepathy", Property: "x", Verdict: "out", Reason: "invented", line: 2}),
			modelled: modelledFields(),
			want:     "is not a published schema any more",
		},
		{
			name:      "the same field judged twice",
			published: publishedFields(),
			verdicts: append(slices.Clone(ok),
				fieldVerdict{Schema: "PageBreak", Property: "*", Verdict: "alias", Reason: "Break", line: 9}),
			modelled: modelledFields(),
			want:     "already has a verdict on line",
		},
		{
			name:      "a verdict that is none of the four",
			published: publishedFields(),
			verdicts:  []fieldVerdict{{Schema: "TextRun", Property: "content", Verdict: "probably", Reason: "who knows", line: 1}},
			modelled:  modelledFields(),
			want:      "is none of out, extra, alias or local",
		},
		{
			name:      "writing off every property of a schema at once",
			published: publishedFields(),
			verdicts:  []fieldVerdict{{Schema: "TextRun", Property: "*", Verdict: "out", Reason: "all of it", line: 1}},
			modelled:  modelledFields(),
			want:      "is out for every property at once",
		},
		{
			name:      "aliasing one property rather than the schema",
			published: publishedFields(),
			verdicts:  []fieldVerdict{{Schema: "PageBreak", Property: "textStyle", Verdict: "alias", Reason: "Break", line: 1}},
			modelled:  modelledFields(),
			want:      "is aliased as a whole schema; the property column must be *",
		},

		// The third direction: the structs, not the schemas. Before it, a
		// struct whose name no schema shares was simply not compared.
		{
			name:      "a struct that matches no schema and says nothing",
			published: publishedFields(),
			verdicts:  ok,
			modelled:  withTag(modelledFields(), "Invented", "x"),
			want:      "Invented is a struct in internal/gdocs and no schema of that name is published",
		},
		{
			name:      "a struct written off as local",
			published: publishedFields(),
			verdicts: append(slices.Clone(ok),
				fieldVerdict{Schema: "Invented", Property: "*", Verdict: "local", Reason: "Developer Preview", line: 2}),
			modelled: withTag(modelledFields(), "Invented", "x"),
			want:     "",
		},
		{
			name:      "a struct carrying no wire field at all is not a wire type",
			published: publishedFields(),
			verdicts:  ok,
			modelled:  withStruct(modelledFields(), "Helper"),
			want:      "",
		},
		{
			name:      "a local row for a schema Google does publish",
			published: publishedFields(),
			verdicts: append(slices.Clone(ok),
				fieldVerdict{Schema: "TextRun", Property: "*", Verdict: "local", Reason: "not really", line: 2}),
			modelled: modelledFields(),
			want:     "is a published schema, so it is not local",
		},
		{
			name:      "a local row naming no struct",
			published: publishedFields(),
			verdicts: append(slices.Clone(ok),
				fieldVerdict{Schema: "Ghost", Property: "*", Verdict: "local", Reason: "Developer Preview", line: 2}),
			modelled: modelledFields(),
			want:     "is written off as local and is not a struct in",
		},
		{
			name:      "a local row about one property",
			published: publishedFields(),
			verdicts: append(slices.Clone(ok),
				fieldVerdict{Schema: "Invented", Property: "x", Verdict: "local", Reason: "Developer Preview", line: 2}),
			modelled: withTag(modelledFields(), "Invented", "x"),
			want:     "is local as a whole type; the property column must be *",
		},

		// A row can outlive the thing it describes, the way an api-coverage
		// row can. All four of these are green without the staleness pass.
		{
			name:      "an out row for a property Google has withdrawn",
			published: publishedFields(),
			verdicts: append(slices.Clone(ok),
				fieldVerdict{Schema: "TextRun", Property: "withdrawn", Verdict: "out", Reason: "no tool reads it", line: 2}),
			modelled: modelledFields(),
			want:     "TextRun.withdrawn is written off and Google does not publish it any more",
		},
		{
			name:      "an out row for a field the types have since grown",
			published: publishedFields(),
			verdicts: append(slices.Clone(ok),
				fieldVerdict{Schema: "TextRun", Property: "content", Verdict: "out", Reason: "no tool reads it", line: 2}),
			modelled: modelledFields(),
			want:     "TextRun.content is written off and TextRun models it now",
		},
		{
			name:      "an extra row for a field Google now publishes",
			published: publishedFields(),
			verdicts: append(slices.Clone(ok),
				fieldVerdict{Schema: "TextRun", Property: "content", Verdict: "extra", Reason: "Developer Preview", line: 2}),
			modelled: modelledFields(),
			want:     "TextRun.content is declared an extra and Google publishes it now",
		},
		{
			name:      "an extra row for a field nothing carries any more",
			published: publishedFields(),
			verdicts: append(slices.Clone(ok),
				fieldVerdict{Schema: "TextRun", Property: "dropped", Verdict: "extra", Reason: "Developer Preview", line: 2}),
			modelled: modelledFields(),
			want:     "TextRun.dropped is declared an extra and no struct carries it any more",
		},
		{
			name:      "a property verdict on a schema no struct models",
			published: withProp(publishedFields(), "Unmodelled", "x"),
			verdicts: append(slices.Clone(ok),
				fieldVerdict{Schema: "Unmodelled", Property: "x", Verdict: "out", Reason: "no tool reads it", line: 2}),
			modelled: modelledFields(),
			want:     "Unmodelled is published and no struct models it",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			problems, _ := fieldProblems(tc.published, tc.verdicts, tc.modelled)
			joined := strings.Join(problems, "\n")
			switch {
			case tc.want == "" && len(problems) > 0:
				t.Errorf("wanted no problem, got:\n%s", joined)
			case tc.want != "" && !strings.Contains(joined, tc.want):
				t.Errorf("wanted a problem containing %q, got:\n%s", tc.want, joined)
			}
			if tc.andAlso != "" && !strings.Contains(joined, tc.andAlso) {
				t.Errorf("wanted a problem containing %q as well, got:\n%s", tc.andAlso, joined)
			}
		})
	}
}

// An alias is what brings a schema whose name this package does not use
// into the comparison at all, so the count of schemas compared moves with
// it. The count is reported rather than enforced — a floor of 80 against
// a real 104 let two dozen types leave the comparison quietly, and the
// third direction in fieldProblems catches the rename itself instead.
func TestAnAliasBringsASchemaIntoTheComparison(t *testing.T) {
	_, matched := fieldProblems(publishedFields(), []fieldVerdict{}, modelledFields())
	if matched != 2 {
		t.Fatalf("two of the three schemas match a struct by name; matched = %d", matched)
	}
	// With the alias row, the third is reached too.
	_, matched = fieldProblems(publishedFields(),
		[]fieldVerdict{{Schema: "PageBreak", Property: "*", Verdict: "alias", Reason: "Break", line: 1}},
		modelledFields())
	if matched != 3 {
		t.Fatalf("the alias should bring PageBreak into sight; matched = %d", matched)
	}
}

// The reader's own rules: a line that is not four columns is a file
// nobody can trust, and the line number in the message has to be the line
// number in the file, counted over the comments and blanks it skips.
func TestReadFieldVerdicts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "api-fields.tsv")
	write := func(t *testing.T, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write(t, "# a comment\n\nTextRun\tcontent\tout\tno tool reads it\n")
	got, err := readFieldVerdicts(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want one row, got %+v", got)
	}
	// Third line of the file, not first row read: a message pointing at
	// line 1 sends a person to the comment.
	if got[0].line != 3 {
		t.Errorf("line = %d, want 3 — the comment and the blank line count", got[0].line)
	}
	if got[0].Reason != "no tool reads it" {
		t.Errorf("reason = %q", got[0].Reason)
	}

	write(t, "TextRun\tcontent\n")
	if _, err := readFieldVerdicts(path); err == nil {
		t.Error("a row with two columns is not a verdict; want an error")
	} else if !strings.Contains(err.Error(), ":1:") {
		t.Errorf("the error should name the line: %v", err)
	}
}

// wireStructs is the half of the gate that reads Go rather than JSON, and
// the promotion of an embedded struct's tags is the part with a rule in
// it: a field promoted from Suggested is on the wire exactly as if it had
// been declared.
func TestWireStructsResolvesEmbedding(t *testing.T) {
	dir := t.TempDir()
	src := `package gdocs

type Suggested struct {
	SuggestedInsertionIDs []string ` + "`" + `json:"suggestedInsertionIds,omitempty"` + "`" + `
}

type SuggestedStyle struct {
	SuggestedTextStyleChanges map[string]int ` + "`" + `json:"suggestedTextStyleChanges,omitempty"` + "`" + `
}

type TextRun struct {
	Suggested
	SuggestedStyle
	Content string ` + "`" + `json:"content,omitempty"` + "`" + `
	Skipped string ` + "`" + `json:"-"` + "`" + `
	NoTag   string
}
`
	if err := os.WriteFile(filepath.Join(dir, "types.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	// A test file in the same directory must not be read.
	if err := os.WriteFile(filepath.Join(dir, "types_test.go"), []byte("package gdocs\n\ntype Fake struct{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := wireStructs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["Fake"]; ok {
		t.Error("a _test.go file was read")
	}
	tags := got["TextRun"]
	for _, want := range []string{"content", "suggestedInsertionIds", "suggestedTextStyleChanges"} {
		if !tags[want] {
			t.Errorf("TextRun should carry %q, got %v", want, tags)
		}
	}
	for _, unwanted := range []string{"-", "Skipped", "NoTag", ""} {
		if tags[unwanted] {
			t.Errorf("TextRun should not carry %q, got %v", unwanted, tags)
		}
	}
	if len(tags) != 3 {
		t.Errorf("want exactly three wire fields, got %v", tags)
	}
}

// withStruct adds a struct with no JSON tag on any field, which is a Go
// type that never reaches the wire and so is nothing this gate judges.
func withStruct(m map[string]map[string]bool, name string) map[string]map[string]bool {
	out := withTag(m, name, "x")
	out[name] = map[string]bool{}
	return out
}

// withProp and withTag are the table's way of adding one thing to a
// copy, so no case can disturb another.
func withProp(m map[string][]string, schema, prop string) map[string][]string {
	out := map[string][]string{}
	for k, v := range m {
		out[k] = slices.Clone(v)
	}
	out[schema] = append(out[schema], prop)
	return out
}

func withTag(m map[string]map[string]bool, schema, tag string) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for k, v := range m {
		copied := map[string]bool{}
		for t := range v {
			copied[t] = true
		}
		out[k] = copied
	}
	if out[schema] == nil {
		out[schema] = map[string]bool{}
	}
	out[schema][tag] = true
	return out
}
