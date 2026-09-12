//go:build live

package livecheck

import (
	"strings"
	"testing"
)

// TestStyleOnlySuggestionIsVisible is the regression step for #46.
//
// A suggestion that only changes formatting inserts and deletes nothing.
// The API records it as a suggestedTextStyleChanges entry on the run,
// which this server's wire types had no field for, so every read it
// offers reported the document as holding no suggestion at all:
// list_suggestions said "0 pending suggestion(s)" in the same call where
// the write had just returned a suggestion id. Four reads, one write,
// and each of them has to see it.
func TestStyleOnlySuggestionIsVisible(t *testing.T) {
	d := start(t)
	if !d.preview {
		t.Skip("suggestion mode needs Developer Preview (GDOCS_PREVIEW=true)")
	}
	_, sc := d.okStruct("create", "create_document", map[string]any{
		"title":   "google-docs-mcp style-only suggestion (safe to delete)",
		"content": "# Style-only suggestion\n\nClean sentence one here.\n\nClean sentence two here."})
	doc := str(sc, "id")
	if doc == "" {
		t.Fatal("create returned no document id; nothing else can run")
	}
	t.Cleanup(func() {
		t.Logf("=== scratch document left behind ===\ndelete it when you are done; Drive search title:\"safe to delete\" finds every run's")
	})

	_, made := d.okStruct("suggest a style-only change", "format_document", map[string]any{
		"document": doc, "mode": "suggest", "ops": []any{
			map[string]any{"op": "text_style", "target": map[string]any{"text": "sentence one"}, "bold": true},
		}})
	ids := strs(made, "suggestion_ids")
	if len(ids) == 0 {
		t.Fatal("suggesting a style change returned no suggestion id; the rest of the step cannot check anything")
	}
	id := ids[0]

	listed := d.ok("list_suggestions", "list_suggestions", map[string]any{"document": doc})
	if !strings.Contains(listed, id) {
		t.Errorf("list_suggestions does not name the suggestion the write just made:\n%s", shown(listed, 400))
	}
	if !strings.Contains(listed, "format") || !strings.Contains(listed, "text: bold") {
		t.Errorf("list_suggestions should say what it restyles:\n%s", shown(listed, 400))
	}
	if strings.HasPrefix(strings.TrimSpace(listed), "0 pending") {
		t.Errorf("list_suggestions still counts it as nothing:\n%s", shown(listed, 400))
	}

	crit := d.ok("read include_suggestions", "read_document", map[string]any{
		"document": doc, "include_suggestions": true, "with_handles": true})
	if !strings.Contains(crit, id) {
		t.Errorf("include_suggestions should mark the restyled text with the id:\n%s", shown(crit, 400))
	}
	if !strings.Contains(crit, "{==") {
		t.Errorf("a restyling is a highlight, not an insertion or a deletion:\n%s", shown(crit, 400))
	}

	raw := d.ok("read format raw", "read_document", map[string]any{
		"document": doc, "format": "raw", "max_chars": 60000})
	if !strings.Contains(raw, "suggestedTextStyleChanges") {
		t.Errorf("format raw drops the field the API sent:\n%s", shown(raw, 500))
	}

	// The write half: a direct change to the property the suggestion
	// already sets is accepted by Google and may apply nothing, so the
	// result has to say so rather than report a bare ops_applied.
	same := d.ok("direct change to the suggested property", "format_document", map[string]any{
		"document": doc, "mode": "direct", "dry_run": true, "ops": []any{
			map[string]any{"op": "text_style", "target": map[string]any{"text": "sentence one"}, "bold": true},
		}})
	// The preview under every one of these shows the marker, so the check
	// has to be on the warning itself rather than on the id appearing
	// anywhere in the result.
	if !strings.Contains(same, "already suggests bold") || !strings.Contains(same, id) {
		t.Errorf("a direct change colliding with the suggestion should warn and name it:\n%s", shown(same, 500))
	}
	other := d.ok("direct change to another property", "format_document", map[string]any{
		"document": doc, "mode": "direct", "dry_run": true, "ops": []any{
			map[string]any{"op": "text_style", "target": map[string]any{"text": "sentence one"}, "italic": true},
		}})
	if strings.Contains(other, "already suggests") {
		t.Errorf("a change to a property nothing suggests must not warn:\n%s", shown(other, 500))
	}

	// And a document with no suggestion at all stays quiet, which is the
	// half of the change that no failing read would have revealed.
	quiet := d.ok("read a clean paragraph", "read_document", map[string]any{
		"document": doc, "include_suggestions": true, "heading": "Style-only suggestion"})
	if strings.Contains(quiet, "sentence two here.{==") {
		t.Errorf("the untouched paragraph picked up a marker:\n%s", shown(quiet, 400))
	}
}
