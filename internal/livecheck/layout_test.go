//go:build live

package livecheck

import (
	"strings"
	"testing"
)

// liveLayout covers page and section layout, named styles, named ranges
// and the refusals that guard them.
func liveLayout(t *testing.T, d *driver, doc string) {
	d.ok("append a token", "edit_document", map[string]any{"document": doc, "mode": "direct", "ops": []any{
		map[string]any{"op": "append", "content": "Token ALPHA marks the replace_all step."}}})
	d.ok("insert a page break", "edit_document", map[string]any{"document": doc, "mode": "direct", "ops": []any{
		map[string]any{"op": "insert_break", "location": map[string]any{"at": "before", "of": map[string]any{"text": "Token ALPHA marks the replace_all step."}}}}})
	d.ok("insert a footnote", "edit_document", map[string]any{"document": doc, "mode": "direct", "ops": []any{
		map[string]any{"op": "insert_footnote", "content": "Footnote written by the live test.",
			"location": map[string]any{"at": "end", "of": map[string]any{"text": "Token ALPHA marks the replace_all step."}}}}})
	d.ok("read the new footnote", "read_document", map[string]any{"document": doc, "segment": "footnote"})
	d.ok("replace_all across the tab", "edit_document", map[string]any{"document": doc, "mode": "direct", "ops": []any{
		map[string]any{"op": "replace_all", "find": "ALPHA", "replace": "BETA"}}})
	d.ok("find the replaced token", "find_in_document", map[string]any{"document": doc, "query": "Token BETA"})

	d.ok("page setup (A4) and a redefined HEADING_2", "layout_document", map[string]any{"document": doc, "mode": "direct", "ops": []any{
		map[string]any{"op": "page", "page_width_pt": 595, "page_height_pt": 842,
			"margin_top_pt": 56, "margin_bottom_pt": 56, "page_number_start": 1},
		map[string]any{"op": "named_style", "style": "heading_2", "color": "#1a73e8", "space_above_pt": 18},
	}})
	back := d.ok("read the redefined HEADING_2 back", "get_document", map[string]any{"document": doc})
	if !strings.Contains(back, "named style HEADING_2") {
		t.Errorf("get_document should report the named styles in use:\n%s", truncate(back, 600))
	}
	// heading_2 is carried by a paragraph here, so the read below shows the
	// change; a style nothing uses is not reported.
	d.ok("a named style that starts a page", "layout_document", map[string]any{"document": doc, "mode": "direct", "ops": []any{
		map[string]any{"op": "named_style", "style": "heading_2", "page_break_before": true,
			"border_bottom": "2pt solid #1a73e8", "border_padding_pt": 6, "shading": "#f4f6f8"}}})
	d.ok("read page-break-before and the border back", "get_document", map[string]any{"document": doc})

	d.ok("section break", "layout_document", map[string]any{"document": doc, "mode": "direct", "ops": []any{
		map[string]any{"op": "section_break", "section_type": "next_page",
			"location": map[string]any{"at": "before", "of": map[string]any{"text": "Token BETA marks the replace_all step."}}}}})
	d.ok("two columns in the new section", "layout_document", map[string]any{"document": doc, "mode": "direct", "ops": []any{
		map[string]any{"op": "section", "target": map[string]any{"text": "Token BETA marks the replace_all step."},
			"columns": 2, "column_gap_pt": 18, "column_separator": "between"}}})
	d.refused("a section's type is read-only", "layout_document", map[string]any{"document": doc, "mode": "direct", "ops": []any{
		map[string]any{"op": "section", "target": map[string]any{"text": "Token BETA marks the replace_all step."}, "section_type": "continuous"}}},
		"read-only")
	d.refused("page setup in comment mode", "layout_document", map[string]any{"document": doc, "mode": "comment", "ops": []any{
		map[string]any{"op": "page", "landscape": true}}}, "comment")
	pageSuggest := map[string]any{"document": doc, "mode": "suggest", "ops": []any{map[string]any{"op": "page", "landscape": true}}}
	if d.preview {
		d.call("page setup in suggest mode (the API decides whether it can)", "layout_document", pageSuggest)
	} else {
		d.refused("page setup in suggest mode without preview", "layout_document", pageSuggest, "Developer Preview")
	}

	// A named range is the one anchor that outlives an edit, so the test
	// edits through it after other writes have moved the text.
	d.ok("name a range", "edit_document", map[string]any{"document": doc, "mode": "direct", "ops": []any{
		map[string]any{"op": "create_named_range", "name": "live anchor", "target": map[string]any{"text": "Closing line"}}}})
	named := d.ok("get_document lists the named range", "get_document", map[string]any{"document": doc})
	if !strings.Contains(named, "live anchor") {
		t.Errorf("the named range should be reported:\n%s", truncate(named, 600))
	}
	d.ok("edit through the named range", "edit_document", map[string]any{"document": doc, "mode": "direct", "ops": []any{
		map[string]any{"op": "replace", "target": map[string]any{"named_range": "live anchor"},
			"content": "Closing line, edited through its name"}}})
	d.ok("replace the named range's content", "edit_document", map[string]any{"document": doc, "mode": "direct", "ops": []any{
		map[string]any{"op": "replace_named_range", "name": "live anchor", "text": "Closing line"}}})
	d.ok("forget the name, keep the text", "edit_document", map[string]any{"document": doc, "mode": "direct", "ops": []any{
		map[string]any{"op": "delete_named_range", "name": "live anchor"}}})
	d.refused("deleting it again is not a silent no-op", "edit_document", map[string]any{"document": doc, "mode": "direct", "ops": []any{
		map[string]any{"op": "delete_named_range", "name": "live anchor"}}}, "not_found")

	d.ok("export as text", "export_document", map[string]any{"document": doc, "format": "txt", "max_chars": 800})
}
