package service

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mmedum/google-docs-mcp/internal/gapi"
	"github.com/mmedum/google-docs-mcp/internal/gdocs"
	"github.com/mmedum/google-docs-mcp/internal/plan"
)

// compact strips the indentation of a dry run's request dump.
func compact(raw json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	return buf.String()
}

func tableEdit(ops ...EditOp) EditRequest {
	return EditRequest{Document: fixtureID, Mode: "direct", DryRun: true, Ops: ops}
}

func TestTableOpsResolve(t *testing.T) {
	svc, _ := writable(t, false)
	ctx := context.Background()
	cases := []struct {
		name  string
		op    EditOp
		kinds string
		want  []string
	}{
		{"insert rows below", EditOp{Kind: plan.OpInsertRows, Table: &TableOp{Table: "tbl1", Row: 2, Count: 2}}, "insertTableRow insertTableRow", []string{`"rowIndex":1`, `"index":133`, `"tabId":"t.0"`, `"insertBelow":true`}},
		{"insert columns before", EditOp{Kind: plan.OpInsertColumns, Table: &TableOp{Table: "tbl1", Column: 1, Before: true}}, "insertTableColumn", []string{`"columnIndex":0`, `"insertRight":false`}},
		{"delete rows", EditOp{Kind: plan.OpDeleteRows, Table: &TableOp{Table: "tbl1", RowList: []int{2, 2}}}, "deleteTableRow", []string{`"rowIndex":1`}},
		{"delete columns", EditOp{Kind: plan.OpDeleteColumns, Table: &TableOp{Table: "tbl1", ColList: []int{1}}}, "deleteTableColumn", []string{`"columnIndex":0`}},
		{"merge", EditOp{Kind: plan.OpMergeCells, Table: &TableOp{Table: "tbl1", FromCell: "r1c1", ToCell: "tbl1:r1c2"}}, "mergeTableCells", []string{`"rowSpan":1`, `"columnSpan":2`}},
		{"unmerge whole", EditOp{Kind: plan.OpUnmergeCells, Table: &TableOp{Table: "tbl1"}}, "unmergeTableCells", []string{`"rowSpan":2`, `"columnSpan":2`}},
		{"style", EditOp{Kind: plan.OpStyleCells, Table: &TableOp{Table: "tbl1", FromCell: "r2c1", Style: plan.CellStyleSpec{Background: "#ffff00"}}}, "updateTableCellStyle", []string{`"rowIndex":1`, `"columnIndex":0`, `"rowSpan":1`, `"backgroundColor"`}},
		{"pin", EditOp{Kind: plan.OpPinHeaderRows, Table: &TableOp{Table: "tbl1", HeaderRow: 1}}, "pinTableHeaderRows", []string{`"pinnedHeaderRowsCount":1`}},
		{"set cells grid", EditOp{Kind: plan.OpSetCells, Table: &TableOp{Table: "tbl1", Data: [][]string{{"Name", "Score"}, {"Beta", "2"}}}}, "deleteContentRange[155,156) insertText@155 deleteContentRange[148,152) insertText@148 deleteContentRange[141,145) insertText@141", []string{`"text":"Bet"`, `"text":"Scor"`}},
		{"set cells list with markdown", EditOp{Kind: plan.OpSetCells, Table: &TableOp{Table: "tbl1", Cells: []CellContent{{Cell: "r2c2", Content: "**two**\nlines"}}}}, "deleteContentRange[155,156) insertText@155 updateTextStyle[155,164) updateParagraphStyle[155,164) updateTextStyle[155,158)", []string{`"text":"two lines"`}},
		{"set cells clears", EditOp{Kind: plan.OpSetCells, Table: &TableOp{Table: "tbl1", Cells: []CellContent{{Cell: "r2c2", Content: ""}}}}, "deleteContentRange[155,156)", nil},
	}
	for _, tc := range cases {
		res, err := svc.Edit(ctx, tableEdit(tc.op))
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		var reqs []json.RawMessage
		if len(res.Requests) > 0 {
			_ = json.Unmarshal(res.Requests, &reqs)
		}
		if got := kindsOf(t, reqs); got != tc.kinds {
			t.Errorf("%s: requests %s\nwant %s", tc.name, got, tc.kinds)
		}
		for _, w := range tc.want {
			if !strings.Contains(compact(res.Requests), w) {
				t.Errorf("%s: requests lack %s:\n%s", tc.name, w, res.Requests)
			}
		}
		if len(res.Changes) != 1 || res.Changes[0].Seq != 0 {
			t.Errorf("%s: one change expected, got %+v", tc.name, res.Changes)
		}
	}
	// set_cells summary shows the cell count and the minimal diff.
	res, _ := svc.Edit(ctx, tableEdit(cases[8].op))
	if res.Changes[0].Description != "2 cell(s) of tbl1" && res.Changes[0].Description != "4 cell(s) of tbl1" || !res.Changes[0].Minimal {
		t.Fatalf("set_cells summary: %+v", res.Changes[0])
	}
}

func TestTableOpErrors(t *testing.T) {
	svc, _ := writable(t, false)
	ctx := context.Background()
	for _, tc := range []struct {
		op    EditOp
		class string
		msg   string
	}{
		{EditOp{Kind: plan.OpInsertRows}, "invalid", "table arguments"},
		{EditOp{Kind: plan.OpInsertRows, Table: &TableOp{}}, "invalid", "table is empty"},
		{EditOp{Kind: plan.OpInsertRows, Table: &TableOp{Table: "tbl9"}}, "not_found", ""},
		{EditOp{Kind: plan.OpInsertRows, Table: &TableOp{Table: "p5"}}, "invalid", "not a table"},
		{EditOp{Kind: plan.OpInsertRows, Table: &TableOp{Table: "tbl1", Row: 3}}, "invalid", "outside the table"},
		{EditOp{Kind: plan.OpDeleteRows, Table: &TableOp{Table: "tbl1", RowList: []int{1, 2}}}, "invalid", "whole table"},
		{EditOp{Kind: plan.OpDeleteRows, Table: &TableOp{Table: "tbl1", RowList: []int{0}}}, "invalid", "numbered from 1"},
		{EditOp{Kind: plan.OpMergeCells, Table: &TableOp{Table: "tbl1", FromCell: "r1c1"}}, "invalid", "at least two cells"},
		{EditOp{Kind: plan.OpMergeCells, Table: &TableOp{Table: "tbl1", FromCell: "r2c2", ToCell: "r1c1"}}, "invalid", "lies before"},
		{EditOp{Kind: plan.OpMergeCells, Table: &TableOp{Table: "tbl1", ToCell: "r2c2"}}, "invalid", "from_cell is needed"},
		{EditOp{Kind: plan.OpMergeCells, Table: &TableOp{Table: "tbl1", FromCell: "x"}}, "invalid", "cells are named"},
		{EditOp{Kind: plan.OpMergeCells, Table: &TableOp{Table: "tbl1", FromCell: "tbl2:r1c1", ToCell: "r1c2"}}, "invalid", "belongs to"},
		{EditOp{Kind: plan.OpStyleCells, Table: &TableOp{Table: "tbl1"}}, "invalid", "changes nothing"},
		{EditOp{Kind: plan.OpSetCells, Table: &TableOp{Table: "tbl1"}}, "invalid", "needs cells or data"},
		{EditOp{Kind: plan.OpSetCells, Table: &TableOp{Table: "tbl1", Cells: []CellContent{{Cell: "r1c1"}, {Cell: "r1c1"}}}}, "invalid", "set twice"},
		{EditOp{Kind: plan.OpSetCells, Table: &TableOp{Table: "tbl1", Cells: []CellContent{{Cell: "r3c1"}}}}, "not_found", "no cell r3c1"},
		{EditOp{Kind: plan.OpSetCells, Table: &TableOp{Table: "tbl1", Cells: []CellContent{{Cell: "r1c1", Content: "| a | b |\n|---|---|"}}}}, "unsupported", ""},
		{EditOp{Kind: plan.OpInsertTable, Table: &TableOp{Rows: 2, Cols: 2}}, "invalid", "needs a location"},
		{EditOp{Kind: plan.OpInsertTable, Location: &Location{At: "end"}}, "invalid", "rows and columns"},
		{EditOp{Kind: plan.OpInsertTable, Location: &Location{At: "end", Of: &Target{Segment: "footnote"}}, Table: &TableOp{Rows: 1, Cols: 1}}, "unsupported", "footnotes"},
		{EditOp{Kind: plan.OpPinHeaderRows, Table: &TableOp{Table: "tbl1", HeaderRow: 2}}, "invalid", "between 0 and 1"},
	} {
		_, err := svc.Edit(ctx, tableEdit(tc.op))
		if classOf(err) != tc.class || !strings.Contains(messageOf(err), tc.msg) {
			t.Errorf("%+v: got %v, want [%s] …%s…", tc.op.Table, err, tc.class, tc.msg)
		}
	}
	// Cells covered by a merge cannot be written or targeted.
	svc, api := writable(t, false)
	var w gdocs.Document
	if err := json.Unmarshal(api.raw, &w); err != nil {
		t.Fatal(err)
	}
	for _, se := range w.Tabs[0].DocumentTab.Body.Content {
		if se.Table != nil {
			se.Table.TableRows[1].TableCells[0].TableCellStyle = &gdocs.TableCellStyle{ColumnSpan: 2}
		}
	}
	api.raw, _ = json.Marshal(&w)
	if _, err := svc.Edit(ctx, tableEdit(EditOp{Kind: plan.OpSetCells, Table: &TableOp{Table: "tbl1", Cells: []CellContent{{Cell: "r2c2", Content: "x"}}}})); classOf(err) != "invalid" || !strings.Contains(messageOf(err), "merged into r2c1") {
		t.Fatalf("write to merged cell: %v", err)
	}
	if _, err := svc.ResolveTarget(fetched(t, svc), Target{Cell: "tbl1:r2c2"}); classOf(err) != "invalid" {
		t.Fatalf("target merged cell: %v", err)
	}
	// Two grid changes on one table are refused.
	_, err := svc.Edit(ctx, tableEdit(EditOp{Kind: plan.OpInsertRows, Table: &TableOp{Table: "tbl1", Row: 1}}, EditOp{Kind: plan.OpDeleteColumns, Table: &TableOp{Table: "tbl1", ColList: []int{1}}}))
	if classOf(err) != "invalid" || !strings.Contains(messageOf(err), "separate calls") {
		t.Fatalf("two structural: %v", err)
	}
}

func TestInsertTablePositionsAndFills(t *testing.T) {
	svc, api := writable(t, false)
	ctx := context.Background()
	// After a paragraph: before its newline, so the table follows it.
	res, err := svc.Edit(ctx, tableEdit(EditOp{Kind: plan.OpInsertTable, Location: &Location{At: "after", Of: &Target{Handle: "p9"}}, Table: &TableOp{Rows: 2, Cols: 3}}))
	if err != nil || !strings.Contains(compact(res.Requests), `"index":123`) || !strings.Contains(compact(res.Requests), `"rows":2`) {
		t.Fatalf("after p9: %v %s", err, res.Requests)
	}
	// Before a paragraph: the previous paragraph's end.
	res, err = svc.Edit(ctx, tableEdit(EditOp{Kind: plan.OpInsertTable, Location: &Location{At: "before", Of: &Target{Handle: "p10"}}, Table: &TableOp{Rows: 1, Cols: 1}}))
	if err != nil || !strings.Contains(compact(res.Requests), `"index":123`) {
		t.Fatalf("before p10: %v %s", err, res.Requests)
	}
	// End of body: before the final newline.
	res, err = svc.Edit(ctx, tableEdit(EditOp{Kind: plan.OpInsertTable, Location: &Location{At: "end"}, Table: &TableOp{Rows: 1, Cols: 1}}))
	if err != nil || !strings.Contains(compact(res.Requests), `"index":225`) {
		t.Fatalf("end: %v %s", err, res.Requests)
	}
	// Inside text: the exact index.
	res, err = svc.Edit(ctx, tableEdit(EditOp{Kind: plan.OpInsertTable, Location: &Location{At: "after", Of: &Target{Text: "Step", Occurrence: 1}}, Table: &TableOp{Rows: 1, Cols: 1}}))
	if err != nil || !strings.Contains(compact(res.Requests), `"index":119`) {
		t.Fatalf("inline: %v %s", err, res.Requests)
	}

	// Applied with data: the first batch inserts, then the table is found
	// at index+1 and filled through set_cells in a second edit. The fake
	// serves the fixture, so the fill lands in tbl1 at 133 (index 132).
	api.batches = nil
	svc.Invalidate(fixtureID)
	req := EditRequest{Document: fixtureID, Mode: "direct", Ops: []EditOp{{Kind: plan.OpInsertTable, Location: &Location{At: "after", Of: &Target{Handle: "p10"}}, Table: &TableOp{Rows: 2, Cols: 2, Data: [][]string{{"A", "B", "extra"}, {"C", "D"}, {"E", "F"}}}}}}
	res, err = svc.Edit(ctx, req)
	if err != nil || len(api.batches) != 2 {
		t.Fatalf("insert with data: %v batches=%d", err, len(api.batches))
	}
	if !strings.Contains(string(api.batches[0].Requests[0]), `"index":132`) || !strings.Contains(string(api.batches[1].Requests[len(api.batches[1].Requests)-1]), `"text":"A"`) {
		t.Fatalf("batches: %s / %s", api.batches[0].Requests, api.batches[1].Requests)
	}
	joined := strings.Join(res.Warnings, "\n")
	if !strings.Contains(joined, "more rows") || !strings.Contains(joined, "more cells") || !strings.Contains(res.Changes[0].Description, "filled as tbl1") {
		t.Fatalf("fill result: %+v", res)
	}
	// In comment mode the grid is part of the proposal and nothing is filled.
	api.batches = nil
	req.Mode = "comment"
	_, err = svc.Edit(ctx, req)
	if err != nil || len(api.batches) != 0 || len(api.comments) != 1 || !strings.Contains(api.comments[0].Content, "A | B | extra") {
		t.Fatalf("comment mode: %v %+v", err, api.comments)
	}
}

func TestDeleteRowsGuardAndSegments(t *testing.T) {
	svc, api := writable(t, false)
	ctx := context.Background()
	api.comments = []*gapi.DriveComment{{ID: "c1", Content: "x", QuotedFileContent: &gapi.QuotedText{Value: "Alpha"}}}
	_, err := svc.Edit(ctx, EditRequest{Document: fixtureID, Mode: "direct", Ops: []EditOp{{Kind: plan.OpDeleteRows, Table: &TableOp{Table: "tbl1", RowList: []int{2}}}}})
	if classOf(err) != "blocked" || !strings.Contains(messageOf(err), "1 comment (c1)") {
		t.Fatalf("guard: %v", err)
	}
	res, err := svc.Edit(ctx, EditRequest{Document: fixtureID, Mode: "direct", Force: true, Ops: []EditOp{{Kind: plan.OpDeleteRows, Table: &TableOp{Table: "tbl1", RowList: []int{2}}}}})
	if err != nil || len(res.Warnings) != 1 {
		t.Fatalf("forced: %+v %v", res, err)
	}
	// Deleting column 1 also hits the anchor; column 2 does not.
	if _, err := svc.Edit(ctx, tableEdit(EditOp{Kind: plan.OpDeleteColumns, Table: &TableOp{Table: "tbl1", ColList: []int{1}}})); classOf(err) != "blocked" {
		t.Fatalf("column guard: %v", err)
	}
	if _, err := svc.Edit(ctx, tableEdit(EditOp{Kind: plan.OpDeleteColumns, Table: &TableOp{Table: "tbl1", ColList: []int{2}}})); err != nil {
		t.Fatalf("column 2: %v", err)
	}
	// Header removal is guarded by what the header holds and refused when
	// the segment is not of that kind.
	res, err = svc.Edit(ctx, tableEdit(EditOp{Kind: plan.OpDeleteHeader}))
	if err != nil || !strings.Contains(compact(res.Requests), `"headerId":"kix.h1"`) || res.Changes[0].Description != "header1 of tab 1" {
		t.Fatalf("delete header: %v %+v", err, res)
	}
	if _, err := svc.Edit(ctx, tableEdit(EditOp{Kind: plan.OpDeleteFooter})); classOf(err) != "not_found" {
		t.Fatalf("no footer: %v", err)
	}
	if _, err := svc.Edit(ctx, tableEdit(EditOp{Kind: plan.OpDeleteFooter, Target: &Target{Segment: "header"}})); classOf(err) != "invalid" {
		t.Fatalf("wrong kind: %v", err)
	}
	res, err = svc.Edit(ctx, EditRequest{Document: fixtureID, Mode: "comment", DryRun: true, Ops: []EditOp{{Kind: plan.OpDeleteHeader}}})
	if err != nil || len(res.Proposals) != 1 || !strings.Contains(res.Proposals[0].Content, "remove the header") {
		t.Fatalf("header proposal: %v %+v", err, res)
	}
}

func TestInsertObjects(t *testing.T) {
	svc, _ := writable(t, false)
	ctx := context.Background()
	res, err := svc.Edit(ctx, tableEdit(EditOp{Kind: plan.OpInsertObject, Location: &Location{At: "after", Of: &Target{Text: "Step one"}}, Object: &plan.ObjectParams{Kind: "image", URL: "https://example.test/a.png", WidthPt: 120}}))
	if err != nil || !strings.Contains(compact(res.Requests), `"insertInlineImage"`) || !strings.Contains(compact(res.Requests), `"index":123`) {
		t.Fatalf("image: %v %s", err, res.Requests)
	}
	for _, o := range []plan.ObjectParams{
		{Kind: "person", Email: "ann@example.test", Name: "Ann"},
		{Kind: "rich_link", URL: "https://docs.google.com/document/d/x/edit"},
		{Kind: "date", Date: plan.DateSpec{Timestamp: "2026-09-03T00:00:00Z"}},
	} {
		res, err := svc.Edit(ctx, tableEdit(EditOp{Kind: plan.OpInsertObject, Location: &Location{At: "end"}, Object: &o}))
		if err != nil || len(res.Changes) != 1 {
			t.Errorf("%s: %v", o.Kind, err)
		}
	}
	if _, err := svc.Edit(ctx, tableEdit(EditOp{Kind: plan.OpInsertObject, Location: &Location{At: "end", Of: &Target{Segment: "footnote"}}, Object: &plan.ObjectParams{Kind: "image", URL: "https://x/y.png"}})); classOf(err) != "unsupported" {
		t.Fatalf("image in footnote: %v", err)
	}
	if _, err := svc.Edit(ctx, tableEdit(EditOp{Kind: plan.OpInsertObject, Location: &Location{At: "end"}})); classOf(err) != "invalid" {
		t.Fatalf("no object: %v", err)
	}
	res, err = svc.Edit(ctx, EditRequest{Document: fixtureID, Mode: "comment", DryRun: true, Ops: []EditOp{{Kind: plan.OpInsertObject, Location: &Location{At: "end"}, Object: &plan.ObjectParams{Kind: "person", Email: "a@b.test"}}}})
	if err != nil || !strings.Contains(res.Proposals[0].Content, "people chip for a@b.test") {
		t.Fatalf("object proposal: %v %+v", err, res)
	}
}
