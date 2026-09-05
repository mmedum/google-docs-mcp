//go:build live

package livecheck

import (
	"regexp"
	"strings"
	"testing"
)

const scratchContent = `# Live test

This document was created by google-docs-mcp. It is safe to delete.

## Background

Revenue grew a lot in Q3. The team shipped **three** features.

- First point
- Second point
    - Nested point

## Next steps

1. Review the numbers
2. Send the summary

Closing line with a [link](https://example.com).`

// TestLive walks the whole tool surface against one scratch document, in
// an order where each step leaves the state the next one needs.
func TestLive(t *testing.T) {
	d := start(t)
	if !d.preview {
		t.Log("=== preview off: suggest-mode steps are expected to be refused with [unavailable]; comments go through Drive ===")
	}

	// ---- create and read ------------------------------------------------
	_, sc := d.okStruct("create", "create_document", map[string]any{
		"title": "google-docs-mcp live test (safe to delete)", "content": scratchContent})
	doc := str(sc, "id")
	if doc == "" {
		t.Fatal("create returned no document id; nothing else can run")
	}
	t.Cleanup(func() {
		t.Logf("=== scratch document left behind ===\nhttps://docs.google.com/document/d/%s/edit\ndelete it when you are done; Drive search title:\"safe to delete\" finds every run's", doc)
	})

	d.ok("read without handles", "read_document", map[string]any{"document": doc, "with_handles": false})
	d.ok("read after create", "read_document", map[string]any{"document": doc, "with_handles": true})
	d.ok("outline", "get_outline", map[string]any{"document": doc})
	d.ok("search by title", "search_documents", map[string]any{"title": "google-docs-mcp live test", "limit": 3})

	// ---- edits in each mode ---------------------------------------------
	d.ok("direct edit", "edit_document", map[string]any{"document": doc, "mode": "direct", "ops": []any{
		map[string]any{"op": "replace", "target": map[string]any{"text": "Revenue grew a lot in Q3."}, "content": "Revenue grew substantially in Q3."},
		map[string]any{"op": "insert", "location": map[string]any{"at": "after", "of": map[string]any{"heading": "Background", "include_heading": true}}, "content": "Inserted after the Background section.\n\n- with a bullet"},
		map[string]any{"op": "append", "content": "Appended paragraph at the very end."},
	}})

	suggestArgs := map[string]any{"document": doc, "mode": "suggest", "ops": []any{
		map[string]any{"op": "replace", "target": map[string]any{"text": "three"}, "content": "four"},
		map[string]any{"op": "delete", "target": map[string]any{"text": "Second point"}},
	}}
	var suggestions []string
	if d.preview {
		_, sc := d.okStruct("suggest edit", "edit_document", suggestArgs)
		suggestions = strs(sc, "suggestion_ids")
	} else {
		d.refused("suggest edit without preview", "edit_document", suggestArgs, "Developer Preview")
	}

	d.ok("comment mode", "edit_document", map[string]any{"document": doc, "mode": "comment", "ops": []any{
		map[string]any{"op": "replace", "target": map[string]any{"text": "Send the summary"}, "content": "Send the summary to the board"},
	}})

	// ---- formatting, including the whole paragraph-style surface ---------
	d.ok("format", "format_document", map[string]any{"document": doc, "mode": "direct", "ops": []any{
		map[string]any{"op": "text_style", "target": map[string]any{"text": "Closing line"}, "bold": true, "color": "#1a73e8"},
		map[string]any{"op": "paragraph_style", "target": map[string]any{"text": "Appended paragraph at the very end."},
			"alignment": "CENTER", "border": "1pt solid #cccccc", "border_padding_pt": 4, "shading": "#eeeeee",
			"indent_end_pt": 18, "keep_lines_together": true, "avoid_widow_and_orphan": true},
	}})
	// with_styles annotates only what a run sets itself: Google reports no
	// inherited formatting, so the blue "Closing line" is annotated and the
	// text around it is not.
	styled := d.ok("read with styles", "read_document", map[string]any{"document": doc, "with_styles": true})
	if !strings.Contains(styled, "color: #1a73e8") {
		t.Errorf("with_styles should annotate the colour a run sets itself:\n%s", shown(styled, 400))
	}

	d.ok("find", "find_in_document", map[string]any{"document": doc, "query": "point"})
	d.ok("list suggestions", "list_suggestions", map[string]any{"document": doc})
	if len(suggestions) > 0 {
		d.ok("reject the last suggestion", "review_suggestion", map[string]any{
			"document": doc, "action": "reject", "ids": []any{suggestions[len(suggestions)-1]}})
	}

	// ---- the overwrite guard --------------------------------------------
	// The comment-mode edit above left a comment on this passage, so a
	// direct delete of it must be refused rather than silently destroying
	// the anchor.
	d.refused("dry run delete over a commented range", "edit_document", map[string]any{
		"document": doc, "mode": "direct", "dry_run": true,
		"ops": []any{map[string]any{"op": "delete", "target": map[string]any{"text": "Send the summary"}}}}, "")
	d.refused("guard: direct delete over a commented range", "edit_document", map[string]any{
		"document": doc, "mode": "direct",
		"ops": []any{map[string]any{"op": "delete", "target": map[string]any{"text": "Send the summary"}}}}, "")

	d.ok("export md", "export_document", map[string]any{"document": doc, "format": "md", "max_chars": 1500})
	d.ok("read with suggestions", "read_document", map[string]any{"document": doc, "with_handles": true, "include_suggestions": true})
	if d.preview && len(suggestions) > 1 {
		d.ok("accept the first suggestion", "review_suggestion", map[string]any{
			"document": doc, "action": "accept", "ids": []any{suggestions[0]}})
	}

	liveComments(t, d, doc)
	liveHistory(t, d, doc)
	liveTables(t, d, doc)

	// After the tables exist, so every tool that takes dry_run has
	// something real to plan. A dry run must send nothing, and only the
	// document's own revision can show that — a fake accepts the batch
	// either way.
	handleRead := d.ok("read for a table handle", "read_document", map[string]any{"document": doc, "with_handles": true})
	liveDryRuns(t, d, doc, first(tableHandle, handleRead))

	liveObjects(t, d, doc)
	liveTabs(t, d, doc)
	liveLayout(t, d, doc)
	liveResources(t, d, doc)
	liveRegistration(t, d)

	d.ok("final get_document", "get_document", map[string]any{"document": doc})
}

var listedID = regexp.MustCompile(`(?m)^- (\S+)`)
