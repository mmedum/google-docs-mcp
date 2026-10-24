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
