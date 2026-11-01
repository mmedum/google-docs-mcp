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
	c := compareSurfaces(mustParse(t, releasedDump), mustParse(t, built))
	c.write(&out)
	if len(c.breaking) == 0 {
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
	if len(compareSurfaces(mustParse(t, releasedDump), mustParse(t, built)).breaking) > 0 {
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
	breaking := []change{{"delete_all", "tool removed: delete_all"}, {"review_suggestion", "review_suggestion: field removed all"},
		{"review_suggestion", "review_suggestion: new required field ids"}}
	released := "\n## [2.4.0] - 2026-10-22\n\n- **Breaking:** `delete_all` and `review_suggestion`.\n"
	for _, tc := range []struct {
		name, changelog string
		want            []string
	}{
		{"every one declared", "## [Unreleased]\n\n- **Breaking:** `delete_all` is gone; `review_suggestion` takes `ids`.\n" + released, nil},
		{"one tool not named", "## [Unreleased]\n\n- **Breaking:** `review_suggestion` takes `ids`.\n" + released,
			[]string{"tool removed: delete_all"}},
		{"named outside a Breaking entry", "## [Unreleased]\n\n- `delete_all` and `review_suggestion` changed.\n" + released, texts(breaking)},
		{"declared only in a release made", "## [Unreleased]\n" + released, texts(breaking)},
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
	breaking := []change{{"gdocs://{id}/outline", "resource template removed: gdocs://{id}/outline"}, {"summarize", "prompts removed: summarize"}}
	released := "\n## [2.4.0] - 2026-10-22\n"
	for _, tc := range []struct {
		name, unreleased string
		want             []string
	}{
		{"both declared", "- **Breaking:** `gdocs://{id}/outline` and `summarize` are gone.\n", nil},
		{"one declared", "- **Breaking:** `gdocs://{id}/outline` is gone.\n", []string{"prompts removed: summarize"}},
		{"neither declared", "- **Breaking:** `delete_all` is gone.\n", texts(breaking)},
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
	compareSurfaces(mustParse(t, plain), mustParse(t, flipped)).write(&out)
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

// A tool that breaks is reported as the break, with its other changes,
// such as a flipped hint, but not its schemas again.
func TestSchemaDiffListsABrokenToolsOtherChanges(t *testing.T) {
	broken := strings.Replace(flipped, `"document": {}`, `"doc": {}`, 1)
	c := compareSurfaces(mustParse(t, plain), mustParse(t, broken))
	if want := []string{"add_comment: field removed document"}; !slices.Equal(texts(c.breaking), want) {
		t.Errorf("breaking = %v, want %v", texts(c.breaking), want)
	}
	if want := []string{"tool add_comment (annotations.destructiveHint)"}; !slices.Equal(texts(c.changed), want) {
		t.Errorf("changed = %v, want %v", texts(c.changed), want)
	}
}

// A key the dump gains is compared like the rest: an item in a new list
// is added.
func TestSchemaDiffComparesANewKey(t *testing.T) {
	built := strings.Replace(plain, `"resourceTemplates"`, `"prompts": [{"name": "summarize"}], "resourceTemplates"`, 1)
	c := compareSurfaces(mustParse(t, plain), mustParse(t, built))
	if want := []string{"prompts summarize"}; !slices.Equal(texts(c.added), want) {
		t.Errorf("added = %v, want %v", texts(c.added), want)
	}
}

// A list whose items carry no identity is compared as one value: a
// position is no identity, and an item inserted would shift the rest.
func TestSchemaDiffComparesAnUnkeyedListWhole(t *testing.T) {
	released := strings.Replace(plain, `"resourceTemplates"`, `"notes": [{"text": "b"}], "resourceTemplates"`, 1)
	built := strings.Replace(plain, `"resourceTemplates"`, `"notes": [{"text": "a"}, {"text": "b"}], "resourceTemplates"`, 1)
	c := compareSurfaces(mustParse(t, released), mustParse(t, built))
	if len(c.breaking) != 0 || !slices.Equal(texts(c.changed), []string{"notes"}) {
		t.Errorf("breaking = %v, changed = %v; want no break and the list named as changed", c.breaking, c.changed)
	}
}

// schema-ack fails a pull request unless its commits name every changed
// item in a footer, and every break of a release in a BREAKING CHANGE:
// footer.
func TestSchemaAckNeedsEveryItemNamed(t *testing.T) {
	relabeled := strings.Replace(plain, `"name": "document"`, `"name": "doc"`, 1)
	broken := strings.Replace(plain, `"document": {}`, `"doc": {}`, 1)
	withoutTemplate := strings.Replace(plain, `{"uriTemplate": "gdocs://{id}", "name": "document"}`, ``, 1)
	reworded := strings.Replace(plain, `"version": "v1.0.0",`, `"version": "v1.0.0", "instructions": "new",`, 1)
	const (
		unnamed      = "no footer names add_comment"
		unnamedBreak = "no BREAKING CHANGE: footer names add_comment"
	)
	for _, tc := range []struct {
		name, built, message, wantErr string
	}{
		{"an unchanged surface needs nothing", plain, "Tidy the README", ""},
		{"a change needs a footer", flipped, "Mark add_comment destructive", unnamed},
		{"a footer naming the tool acknowledges it", flipped, "Mark it\n\nSCHEMA-CHANGE: add_comment: destructiveHint true", ""},
		{"a footer naming another tool does not", flipped, "Mark it\n\nSCHEMA-CHANGE: export_document: destructiveHint true", unnamed},
		{"a name is a whole token", flipped, "Mark it\n\nSCHEMA-CHANGE: add_comments: destructiveHint true", unnamed},
		{"a continuation line can name it", flipped, "Mark them\n\nSCHEMA-CHANGE: export_document,\n  add_comment: destructiveHint true", ""},
		{"an empty footer says nothing", flipped, "Mark it\n\nSCHEMA-CHANGE:", unnamed},
		{"the next line is not the footer's text", flipped, "Mark it\n\nSCHEMA-CHANGE:\nCo-Authored-By: add_comment", unnamed},
		{"a footer must open its line", flipped, "Mark it, see SCHEMA-CHANGE: add_comment", unnamed},
		{"a line of prose is not a footer", flipped, "Mark it\n\nThe gate wants a\nSCHEMA-CHANGE: add_comment footer, which this is not.", unnamed},
		{"a subject is not a footer", flipped, "SCHEMA-CHANGE: add_comment", unnamed},
		{"an indented block is not a footer block", flipped, "Mark it\n\nSCHEMA-CHANGE: add_comment: hints\n\n    go run ./scripts/gates schema-ack", unnamed},
		{"a footer above the co-author trailer", flipped, "Mark it\n\nSCHEMA-CHANGE: add_comment: hints\n\nCo-Authored-By: someone <x@example.com>", ""},
		{"a footer beside the co-author trailer", flipped, "Mark it\n\nCo-Authored-By: someone <x@example.com>\nSCHEMA-CHANGE: add_comment: hints", ""},
		{"a resource template by its URI", relabeled, "Rename it\n\nSCHEMA-CHANGE: gdocs://{id}: renamed", ""},
		{"a resource template unnamed", relabeled, "Rename it\n\nSCHEMA-CHANGE: add_comment: renamed", "no footer names gdocs://{id}"},
		{"the instructions by their key", reworded, "Reword them\n\nSCHEMA-CHANGE: instructions: reworded", ""},
		{"a break named only as a change", broken, "Rename the field\n\nSCHEMA-CHANGE: add_comment: document is doc", unnamedBreak},
		{"BREAKING CHANGE: acknowledges a break", broken, "Rename the field\n\nBREAKING CHANGE: add_comment: document is doc", ""},
		{"BREAKING-CHANGE: is its synonym", broken, "Rename the field\n\nBREAKING-CHANGE: add_comment: document is doc", ""},
		{"a removed template needs BREAKING CHANGE:", withoutTemplate, "Drop it\n\nSCHEMA-CHANGE: gdocs://{id}: gone", "no BREAKING CHANGE: footer names gdocs://{id}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := compareSurfaces(mustParse(t, plain), mustParse(t, tc.built))
			err := checkAck(io.Discard, c, []string{"Tidy the docs", tc.message}, "base")
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("checkAck = %v, want it to pass", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("checkAck = %v, want an error containing %q", err, tc.wantErr)
			}
		})
	}
}

// A name in backticks is taken whole, so a URI with a comma in it can be
// named; split on its comma, it names nothing.
func TestAFooterCanNameAURIWithACommaInBackticks(t *testing.T) {
	const tmpl = "gdocs://{id}{?tab,mode}"
	was := strings.Replace(plain, `"gdocs://{id}"`, `"`+tmpl+`"`, 1)
	now := strings.Replace(was, `"name": "document"`, `"name": "doc"`, 1)
	c := compareSurfaces(mustParse(t, was), mustParse(t, now))
	if err := checkAck(io.Discard, c, []string{"Rename\n\nSCHEMA-CHANGE: `" + tmpl + "`: renamed"}, "base"); err != nil {
		t.Errorf("checkAck = %v, want the backticked URI to name the template", err)
	}
	if err := checkAck(io.Discard, c, []string{"Rename\n\nSCHEMA-CHANGE: " + tmpl + ": renamed"}, "base"); err == nil {
		t.Error("checkAck passed with the URI split on its comma, which names nothing")
	}
}

// A break of the released surface needs BREAKING CHANGE:; removing a tool
// added since the release breaks no caller, so it is a plain change.
func TestABreakIsABreakOfARelease(t *testing.T) {
	release := mustParse(t, plain)
	base := mustParse(t, strings.Replace(plain, `"tools": [`, `"tools": [{"name": "draft_tool"}, `, 1))
	head := mustParse(t, strings.Replace(plain, `"document": {}`, `"doc": {}`, 1))
	c := releaseBreaksOnly(compareSurfaces(base, head), compareSurfaces(release, head).breaking)
	if want := []change{{"add_comment", "add_comment: field removed document"}}; !slices.Equal(c.breaking, want) {
		t.Errorf("breaking = %v, want %v", c.breaking, want)
	}
	if want := []change{{"draft_tool", "tool removed: draft_tool"}}; !slices.Equal(c.changed, want) {
		t.Errorf("changed = %v, want %v", c.changed, want)
	}
	footer := "Drop and rename\n\nSCHEMA-CHANGE: draft_tool: never released\nBREAKING CHANGE: add_comment: document is doc"
	if err := checkAck(io.Discard, c, []string{footer}, "base"); err != nil {
		t.Errorf("checkAck = %v, want both acknowledged", err)
	}
}
