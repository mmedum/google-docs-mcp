//go:build live

package livecheck

import (
	"regexp"
	"testing"
)

var tableHandle = regexp.MustCompile(`\[((?:tab\d+/)?tbl\d+)\]`)

// liveTables covers every grid op, the styling ops, and the rule that a
// grid change puts the ops after it in their own batch.
func liveTables(t *testing.T, d *driver, doc string) {
	d.ok("insert table with data", "edit_table", map[string]any{"document": doc, "mode": "direct", "ops": []any{
		map[string]any{"op": "insert_table", "location": map[string]any{"at": "after", "of": map[string]any{"heading": "Next steps"}},
			"rows": 2, "columns": 3, "data": []any{[]any{"Item", "Owner", "Due"}, []any{"Numbers", "Ann", "Friday"}}},
	}})
	read := d.ok("read table", "read_document", map[string]any{"document": doc, "with_handles": true, "heading": "Next steps"})
	tbl := first(tableHandle, read)
	if tbl == "" {
		t.Fatal("no table handle in the read; the table ops cannot run")
	}

	d.ok("table ops", "edit_table", map[string]any{"document": doc, "mode": "direct", "ops": []any{
		map[string]any{"op": "insert_rows", "table": tbl, "row": 2, "count": 1},
		map[string]any{"op": "set_cells", "table": tbl, "cells": []any{map[string]any{"cell": "r2c3", "content": "**Monday**"}}},
		map[string]any{"op": "style_cells", "table": tbl, "from_cell": "r1c1", "to_cell": "r1c3", "background": "#e8f0fe"},
		map[string]any{"op": "pin_header_rows", "table": tbl, "count": 1},
	}})

	suggestCell := map[string]any{"document": doc, "mode": "suggest", "ops": []any{
		map[string]any{"op": "set_cells", "table": tbl, "cells": []any{map[string]any{"cell": "r3c1", "content": "Suggested cell text"}}}}}
	if d.preview {
		d.ok("suggested cell edit", "edit_table", suggestCell)
	} else {
		d.refused("suggested cell edit without preview", "edit_table", suggestCell, "Developer Preview")
	}

	d.ok("merge cells", "edit_table", map[string]any{"document": doc, "mode": "direct", "ops": []any{
		map[string]any{"op": "merge_cells", "table": tbl, "from_cell": "r3c1", "to_cell": "r3c3"}}})
	d.ok("unmerge cells", "edit_table", map[string]any{"document": doc, "mode": "direct", "ops": []any{
		map[string]any{"op": "unmerge_cells", "table": tbl, "from_cell": "r3c1", "to_cell": "r3c3"}}})
	d.ok("insert column", "edit_table", map[string]any{"document": doc, "mode": "direct", "ops": []any{
		map[string]any{"op": "insert_columns", "table": tbl, "column": 3, "count": 1}}})
	d.ok("set cells in the new column and delete column 2", "edit_table", map[string]any{"document": doc, "mode": "direct", "ops": []any{
		map[string]any{"op": "set_cells", "table": tbl, "cells": []any{
			map[string]any{"cell": "r1c4", "content": "Notes"}, map[string]any{"cell": "r2c4", "content": "none"}}},
		map[string]any{"op": "delete_columns", "table": tbl, "column_numbers": []any{2}},
	}})

	// With preview on, row 3 holds a pending suggestion, so the guard
	// refuses the delete until it is forced. Without preview there is
	// nothing to protect and the delete simply works.
	_, _, refused := d.call("delete row 3", "edit_table", map[string]any{"document": doc, "mode": "direct", "ops": []any{
		map[string]any{"op": "delete_rows", "table": tbl, "row_numbers": []any{3}}}})
	if refused {
		d.ok("delete row 3, forced", "edit_table", map[string]any{"document": doc, "mode": "direct", "force": true, "ops": []any{
			map[string]any{"op": "delete_rows", "table": tbl, "row_numbers": []any{3}}}})
	} else if d.preview {
		t.Log("row 3 held no suggestion, so the guard had nothing to refuse")
	}

	d.ok("three grid changes on one table: a batch each, renumbered between", "edit_table",
		map[string]any{"document": doc, "mode": "direct", "ops": []any{
			map[string]any{"op": "insert_rows", "table": tbl, "row": 1, "count": 1},
			map[string]any{"op": "insert_columns", "table": tbl, "column": 1, "count": 1},
			map[string]any{"op": "delete_columns", "table": tbl, "column_numbers": []any{1}},
		}})

	// A handle is valid for the revision it came from, and the ops above
	// have moved the document on, so re-read before naming the table.
	read = d.ok("read table after the structure ops", "read_document",
		map[string]any{"document": doc, "with_handles": true, "heading": "Next steps"})
	if h := first(tableHandle, read); h != "" {
		tbl = h
	}
	d.ok("column widths, row heights and cell borders", "edit_table", map[string]any{"document": doc, "mode": "direct", "ops": []any{
		map[string]any{"op": "style_cells", "table": tbl, "from_cell": "r1c1", "to_cell": "r2c2",
			"border": "1pt solid #999999", "border_top": "3pt dash #ff0000", "padding_pt": 6, "padding_left_pt": 12},
		map[string]any{"op": "style_columns", "table": tbl, "column_numbers": []any{1}, "width_pt": 140},
		map[string]any{"op": "style_rows", "table": tbl, "row_numbers": []any{1}, "min_height_pt": 24, "prevent_overflow": true},
	}})
}
