package main

import (
	"io"
	"slices"
	"strings"
	"testing"
)

const releasedDump = `{"tools": [{
	"name": "add_comment",
	"inputSchema": {"required": ["document"], "properties": {"document": {}, "quote": {}}},
	"outputSchema": {"properties": {"id": {}, "anchored": {}}}
}]}`

func TestSchemaDiffBreaksOnALostOutputField(t *testing.T) {
	built := `{"tools": [{
		"name": "add_comment",
		"inputSchema": {"required": ["document"], "properties": {"document": {}, "quote": {}}},
		"outputSchema": {"properties": {"id": {}}}
	}]}`
	var out strings.Builder
	if len(diff(&out, mustParse(t, releasedDump), mustParse(t, built))) == 0 {
		t.Fatal("dropping an output field passed the diff")
	}
	if want := "add_comment: output field removed anchored"; !strings.Contains(out.String(), want) {
		t.Errorf("the report does not name the lost field; want %q in:\n%s", want, out.String())
	}
}

func TestSchemaDiffAllowsANewOutputField(t *testing.T) {
	built := `{"tools": [{
		"name": "add_comment",
		"inputSchema": {"required": ["document"], "properties": {"document": {}, "quote": {}}},
		"outputSchema": {"properties": {"id": {}, "anchored": {}, "handle": {}}}
	}]}`
	if len(diff(io.Discard, mustParse(t, releasedDump), mustParse(t, built))) > 0 {
		t.Error("adding an output field failed the diff")
	}
}

func mustParse(t *testing.T, s string) *schemaDump {
	t.Helper()
	d, err := parseSchemas([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// A break passes only when the unreleased CHANGELOG declares it under
// **Breaking:**, naming its tool, and no release there keeps the tag's
// major version.
func TestUndeclaredBreaks(t *testing.T) {
	breaking := []string{"tool removed: delete_all", "review_suggestion: field removed all",
		"review_suggestion: new required field ids"}
	released := "\n## [2.4.0] - 2026-10-22\n\n- **Breaking:** `delete_all` and `review_suggestion`.\n"
	for _, tc := range []struct {
		name, changelog string
		want            []string
	}{
		{"every one declared", "## [Unreleased]\n\n- **Breaking:** `delete_all` is gone; `review_suggestion` takes `ids`.\n" + released, nil},
		{"one tool not named", "## [Unreleased]\n\n- **Breaking:** `review_suggestion` takes `ids`.\n" + released,
			[]string{"tool removed: delete_all"}},
		{"named outside a Breaking entry", "## [Unreleased]\n\n- `delete_all` and `review_suggestion` changed.\n" + released, breaking},
		{"declared only in a release made", "## [Unreleased]\n" + released, breaking},
		{"a release that raises the major", "## [3.0.0] - 2026-10-24\n\n- **Breaking:** `delete_all`, `review_suggestion`.\n" + released, nil},
		{"a release that does not", "## [2.5.0] - 2026-10-24\n\n- **Breaking:** `delete_all`, `review_suggestion`.\n" + released,
			[]string{"release 2.5.0 breaks the surface without raising the major version from 2.4.0"}},
		{"a prerelease that does not", "## [2.5.0-rc.1] - 2026-10-24\n\n- **Breaking:** `delete_all`, `review_suggestion`.\n" + released,
			[]string{"release 2.5.0-rc.1 breaks the surface without raising the major version from 2.4.0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := undeclaredBreaks(breaking, "# Changelog\n\n"+tc.changelog, "v2.4.0")
			if err != nil || !slices.Equal(got, tc.want) {
				t.Errorf("undeclared = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	// Without the tag's own heading nothing is known to be unreleased.
	if _, err := undeclaredBreaks(breaking, "# Changelog\n\n## [Unreleased]\n\n- **Breaking:** `delete_all`.\n", "v2.4.0"); err == nil ||
		!strings.Contains(err.Error(), "no ## [2.4.0] heading") {
		t.Errorf("no heading for the tag: err = %v", err)
	}
}

// An item removed from any list of the dump, a resource template or a
// prompt, is declared as a tool is: named in backticks in a
// **Breaking:** entry.
func TestUndeclaredBreaksNamesAResourceTemplate(t *testing.T) {
	breaking := []string{"resource template removed: gdocs://{id}/outline", "prompts removed: summarize"}
	released := "\n## [2.4.0] - 2026-10-22\n"
	for _, tc := range []struct {
		name, unreleased string
		want             []string
	}{
		{"both declared", "- **Breaking:** `gdocs://{id}/outline` and `summarize` are gone.\n", nil},
		{"one declared", "- **Breaking:** `gdocs://{id}/outline` is gone.\n", []string{"prompts removed: summarize"}},
		{"neither declared", "- **Breaking:** `delete_all` is gone.\n", breaking},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := undeclaredBreaks(breaking, "# Changelog\n\n## [Unreleased]\n\n"+tc.unreleased+released, "v2.4.0")
			if err != nil || !slices.Equal(got, tc.want) {
				t.Errorf("undeclared = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

// plain is a released surface; the tests below change one thing in it.
const plain = `{"server": "google-docs-mcp", "version": "v1.0.0",
	"tools": [{"name": "add_comment", "annotations": {"destructiveHint": false},
		"inputSchema": {"properties": {"document": {}}}}],
	"resourceTemplates": [{"uriTemplate": "gdocs://{id}", "name": "document"}]}`

var flipped = strings.Replace(plain, `"destructiveHint": false`, `"destructiveHint": true`, 1)

// schema-diff names an annotation that changed, down to the hint.
func TestSchemaDiffListsAChangedAnnotation(t *testing.T) {
	var out strings.Builder
	diff(&out, mustParse(t, plain), mustParse(t, flipped))
	if want := "changed: [tool add_comment (annotations.destructiveHint)]"; !strings.Contains(out.String(), want) {
		t.Errorf("the report does not name the changed hint; want %q in:\n%s", want, out.String())
	}
}

// The same tool written in another key order is no change, and neither
// is a new build stamp.
func TestSchemaDiffIgnoresKeyOrderAndTheStamp(t *testing.T) {
	built := `{"server": "google-docs-mcp", "version": "v1.0.1-dev",
		"resourceTemplates": [{"name": "document", "uriTemplate": "gdocs://{id}"}],
		"tools": [{"inputSchema": {"properties": {"document": {}}}, "annotations": {"destructiveHint": false},
			"name": "add_comment"}]}`
	if c := compareSurfaces(mustParse(t, plain), mustParse(t, built)); !c.empty() {
		t.Errorf("reordered keys or a new version counted as a change: %+v", c)
	}
}

// A tool that breaks is reported once, as the break.
func TestSchemaDiffListsABrokenToolOnce(t *testing.T) {
	broken := strings.Replace(plain, `"document": {}`, `"doc": {}`, 1)
	c := compareSurfaces(mustParse(t, plain), mustParse(t, broken))
	if want := []string{"add_comment: field removed document"}; !slices.Equal(c.breaking, want) {
		t.Errorf("breaking = %v, want %v", c.breaking, want)
	}
	if len(c.changed) != 0 {
		t.Errorf("the broken tool is listed again as changed: %v", c.changed)
	}
}

// A key the dump gains is compared like the rest: an item in a new list
// is added.
func TestSchemaDiffComparesANewKey(t *testing.T) {
	built := strings.Replace(plain, `"resourceTemplates"`, `"prompts": [{"name": "summarize"}], "resourceTemplates"`, 1)
	c := compareSurfaces(mustParse(t, plain), mustParse(t, built))
	if want := []string{"prompts summarize"}; !slices.Equal(c.added, want) {
		t.Errorf("added = %v, want %v", c.added, want)
	}
}

// A list whose items carry no identity is compared as one value: a
// position is no identity, and an item inserted would shift the rest.
func TestSchemaDiffComparesAnUnkeyedListWhole(t *testing.T) {
	released := strings.Replace(plain, `"resourceTemplates"`, `"notes": [{"text": "b"}], "resourceTemplates"`, 1)
	built := strings.Replace(plain, `"resourceTemplates"`, `"notes": [{"text": "a"}, {"text": "b"}], "resourceTemplates"`, 1)
	c := compareSurfaces(mustParse(t, released), mustParse(t, built))
	if len(c.breaking) != 0 || !slices.Equal(c.changed, []string{"notes"}) {
		t.Errorf("breaking = %v, changed = %v; want no break and the list named as changed", c.breaking, c.changed)
	}
}

// schema-ack fails a pull request whose surface changed unless a commit
// it adds carries the footer, and a break needs BREAKING CHANGE:.
func TestSchemaAckNeedsAFooter(t *testing.T) {
	relabeled := strings.Replace(plain, `"name": "document"`, `"name": "doc"`, 1)
	broken := strings.Replace(plain, `"document": {}`, `"doc": {}`, 1)
	withoutTemplate := strings.Replace(plain, `{"uriTemplate": "gdocs://{id}", "name": "document"}`, ``, 1)
	const needsFooter, needsBreaking = "says SCHEMA-CHANGE: or BREAKING CHANGE:", "says BREAKING CHANGE:"
	for _, tc := range []struct {
		name, built, messages, wantErr string
	}{
		{"an unchanged surface needs nothing", plain, "Tidy the README", ""},
		{"a changed annotation needs a footer", flipped, "Mark add_comment destructive", needsFooter},
		{"SCHEMA-CHANGE: acknowledges it", flipped, "Mark add_comment destructive\n\nSCHEMA-CHANGE: destructiveHint true", ""},
		{"an empty footer says nothing", flipped, "Mark it\n\nSCHEMA-CHANGE:", needsFooter},
		{"the next line is not the footer's text", flipped, "Mark it\n\nSCHEMA-CHANGE:\nCo-Authored-By: someone", needsFooter},
		{"a footer must open its line", flipped, "Mark it, see SCHEMA-CHANGE: hints", needsFooter},
		{"a line of prose is not a footer", flipped, "Mark it\n\nThe gate fails unless a commit carries a\nSCHEMA-CHANGE: footer, which this one does not.", needsFooter},
		{"a subject is not a footer", flipped, "SCHEMA-CHANGE: hints", needsFooter},
		{"a footer above the co-author trailer", flipped, "Mark it\n\nSCHEMA-CHANGE: hints\n\nCo-Authored-By: someone <x@example.com>", ""},
		{"a footer beside the co-author trailer", flipped, "Mark it\n\nCo-Authored-By: someone <x@example.com>\nSCHEMA-CHANGE: hints", ""},
		{"a relabeled resource template needs a footer", relabeled, "Rename the resource", needsFooter},
		{"a removed resource template is a break", withoutTemplate, "Drop it\n\nSCHEMA-CHANGE: gone", needsBreaking},
		{"a break needs BREAKING CHANGE:", broken, "Rename the field\n\nSCHEMA-CHANGE: document is doc", needsBreaking},
		{"BREAKING CHANGE: acknowledges a break", broken, "Rename the field\n\nBREAKING CHANGE: document is doc", ""},
		{"BREAKING-CHANGE: is its synonym", broken, "Rename the field\n\nBREAKING-CHANGE: document is doc", ""},
		{"an indented block is not a footer block", flipped, "Mark it\n\nSCHEMA-CHANGE: hints\n\n    go run ./scripts/gates schema-ack", needsFooter},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkAck(io.Discard, compareSurfaces(mustParse(t, plain), mustParse(t, tc.built)), []string{"Tidy the docs", tc.messages}, "base")
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("checkAck = %v, want it to pass", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("checkAck = %v, want an error containing %q", err, tc.wantErr)
			}
		})
	}
}
