//go:build live

package livecheck

import (
	"regexp"
	"strings"
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
	d.ok("suggested cell edit", "edit_table", suggestCell)

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

	// Row 3 holds a pending suggestion, so the guard refuses the delete
	// until it is forced.
	_, _, refused := d.call("delete row 3", "edit_table", map[string]any{"document": doc, "mode": "direct", "ops": []any{
		map[string]any{"op": "delete_rows", "table": tbl, "row_numbers": []any{3}}}})
	if refused {
		d.person.answer("accept")
		d.ok("delete row 3, forced", "edit_table", map[string]any{"document": doc, "mode": "direct", "force": true, "ops": []any{
			map[string]any{"op": "delete_rows", "table": tbl, "row_numbers": []any{3}}}})
		d.wasAsked("delete row 3, forced", "edit_table: run a forced edit on")
	} else {
		t.Error("row 3 held the suggested cell edit, yet the guard did not refuse its deletion")
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

// bareHandle is an empty paragraph in a read with handles.
var bareHandle = regexp.MustCompile(`(?m)^\[(?:tab\d+/)?p\d+\]$`)

// liveContentEmbeds puts tables and images in edit_document content. The
// text lands first, later batches place each table and image in the
// empty paragraph left for it, and what a table leaves is deleted
// (spike E). Run before liveTabs, which reorders the tabs.
func liveContentEmbeds(t *testing.T, d *driver, doc string) {
	const logo = "https://www.gstatic.com/images/branding/product/1x/docs_2020q4_48dp.png"
	afterBackground := map[string]any{"at": "after", "of": map[string]any{"heading": "Background", "include_heading": true}}
	insert := func(mode, content string) map[string]any {
		return map[string]any{"document": doc, "mode": mode, "ops": []any{
			map[string]any{"op": "insert", "location": afterBackground, "content": content}}}
	}
	// A cell reading "# Item" stays as written: cells are plain text. A
	// <br> in a cell is a second paragraph there, as a read shows it.
	content := "Embeds lead.\n\n| # Item | Owner |\n|---|---|\n| One<br>two | Ann |\n\n![docs logo](" + logo + ")\n\nEmbeds tail."

	before := first(revisionOf, d.ok("revision before the dry run", "get_document", map[string]any{"document": doc}))
	dry := insert("direct", content)
	dry["dry_run"] = true
	if text := d.ok("dry run: content with a table and an image", "edit_document", dry); !strings.Contains(text, "later batches place 1 table and 1 image") {
		t.Errorf("the dry run does not name the rounds:\n%s", shown(text, 400))
	}
	if after := first(revisionOf, d.ok("revision after the dry run", "get_document", map[string]any{"document": doc})); after != before {
		t.Errorf("a dry run moved the revision from %s to %s", shown(before, 60), shown(after, 60))
	}

	placed := func(label, mode, content, want string) {
		text := d.ok(label, "edit_document", insert(mode, content))
		if !strings.Contains(text, want) || strings.Contains(text, "not placed") || strings.Contains(text, "not removed") {
			t.Errorf("%s: want %q and no failed round:\n%s", label, want, shown(text, 600))
		}
	}
	placed("content with a table and an image", "direct", content, "then placed 1 table and 1 image")
	read := d.ok("read the placed table and image", "read_document", map[string]any{"document": doc, "with_handles": true, "heading": "Background"})
	lead, tail := strings.Index(read, "Embeds lead."), strings.Index(read, "Embeds tail.")
	if lead < 0 || tail < lead {
		t.Fatalf("the content's text is not in the section:\n%s", shown(read, 900))
	}
	region := read[lead:tail]
	for _, want := range []string{"table 2×2", "| # Item | Owner |", "| One<br>two | Ann |", "!["} {
		if !strings.Contains(region, want) {
			t.Errorf("the region lacks %q:\n%s", want, shown(region, 600))
		}
	}
	if blank := bareHandle.FindString(region); blank != "" {
		t.Errorf("an empty paragraph %s is left between the text and what was placed:\n%s", shown(blank, 40), shown(region, 600))
	}

	// What Google does with a suggested deletion of a suggested empty
	// paragraph is not in the spike; the transcript shows it, and the
	// suggestions are rejected so later steps see the document as it was.
	suggested := strings.ReplaceAll(content, "Item", "Proposed")
	text, sc := d.okStruct("content with a table and an image, suggested", "edit_document", insert("suggest", suggested))
	if strings.Contains(text, "not placed") || strings.Contains(text, "not removed") {
		t.Errorf("a suggested round failed:\n%s", shown(text, 600))
	}
	if strings.Contains(text, "nothing is removed until accepted") {
		t.Errorf("a later batch warned about the call's own suggestions:\n%s", shown(text, 600))
	}
	if text := d.ok("read the suggested table and image", "read_document",
		map[string]any{"document": doc, "with_handles": true, "include_suggestions": true, "heading": "Background"}); !strings.Contains(text, "{++# Proposed++}") {
		t.Errorf("the suggested table is not in the read:\n%s", shown(text, 900))
	}
	if ids := strs(sc, "suggestion_ids"); len(ids) > 0 {
		args := map[string]any{"document": doc, "action": "reject", "ids": []any{}}
		for _, id := range ids {
			args["ids"] = append(args["ids"].([]any), id)
		}
		d.ok("reject the suggested table and image", "review_suggestion", args)
		if text := d.ok("read after rejecting them", "read_document",
			map[string]any{"document": doc, "with_handles": true, "include_suggestions": true, "heading": "Background"}); strings.Contains(text, "Proposed") {
			t.Errorf("the rejected table is still in the read:\n%s", shown(text, 900))
		}
	} else {
		t.Error("the suggested content returned no suggestion ids")
	}

	// An image Google cannot fetch is reported, and the text stays.
	text = d.ok("content with an image Google cannot fetch", "edit_document",
		insert("direct", "Unfetchable image below.\n\n![missing](https://www.gstatic.com/images/branding/product/1x/no_such_image_48dp.png)"))
	if !strings.Contains(text, "the image on line 3 of the content was not placed") || !strings.Contains(text, "still there") {
		t.Errorf("the failed image is not reported:\n%s", shown(text, 600))
	}

	// Last in the body, the table keeps the paragraph Docs needs after it.
	text = d.ok("content ending the body with a table", "edit_document", map[string]any{"document": doc, "mode": "direct", "ops": []any{
		map[string]any{"op": "append", "content": "| End | table |\n|---|---|\n| x | y |"}}})
	if !strings.Contains(text, "then placed 1 table") || strings.Contains(text, "not removed") {
		t.Errorf("the table at the end: %s", shown(text, 600))
	}

	// The body now ends in the empty paragraph that table keeps. A lone
	// table appended takes it as its slot, so nothing is inserted first,
	// and goes in at a paragraph's start right after a table.
	text = d.ok("a lone table into the empty last paragraph", "edit_document", map[string]any{"document": doc, "mode": "direct", "ops": []any{
		map[string]any{"op": "append", "content": "| Second | end |\n|---|---|\n| z | w |"}}})
	if !strings.Contains(text, "then placed 1 table") || strings.Contains(text, "not placed") || strings.Contains(text, "not removed") {
		t.Errorf("the lone table: %s", shown(text, 600))
	}

	// Right before a table, the slot stays: Docs needs a paragraph
	// between two tables.
	read = d.ok("read for a table to go before", "read_document", map[string]any{"document": doc, "with_handles": true})
	if tbl := first(tableHandle, read); tbl != "" {
		text = d.ok("content with a table before a table", "edit_document", map[string]any{"document": doc, "mode": "direct", "ops": []any{
			map[string]any{"op": "insert", "location": map[string]any{"at": "before", "of": map[string]any{"handle": tbl}},
				"content": "| Before | it |\n|---|---|\n| p | q |"}}})
		if !strings.Contains(text, "then placed 1 table") || strings.Contains(text, "not placed") || strings.Contains(text, "not removed") {
			t.Errorf("the table before a table: %s", shown(text, 600))
		}
	} else {
		t.Error("no table handle in the read; the table before a table did not run")
	}
}
