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
	}{
		{"a matched set", publishedFields(), ok, modelledFields(), ""},
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
			name:      "a verdict that is none of the three",
			published: publishedFields(),
			verdicts:  []fieldVerdict{{Schema: "TextRun", Property: "content", Verdict: "probably", Reason: "who knows", line: 1}},
			modelled:  modelledFields(),
			want:      "is none of out, extra or alias",
		},
		{
			name:      "writing off every property of a schema at once",
			published: publishedFields(),
			verdicts:  []fieldVerdict{{Schema: "TextRun", Property: "*", Verdict: "out", Reason: "all of it", line: 1}},
			modelled:  modelledFields(),
			want:      "is out for every property at once",
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
		})
	}
}

// A schema that stops being matched stops being checked, silently, which
// is the failure mode a gate over a name match has. The count is part of
// the rule for that reason, and this is the test of the count.
func TestFieldsGateNoticesWhenItStopsReading(t *testing.T) {
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
	out[schema][tag] = true
	return out
}
