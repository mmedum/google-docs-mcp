package plan

import (
	"errors"
	"strings"
	"testing"
)

func tableOp(kind OpKind, t TableParams) Op {
	s := seg()
	return Op{Seq: 0, Kind: kind, Seg: s, TableAt: &Loc{Index: 133, TabID: "t"}, Table: t, Description: "tbl1"}
}

func TestPlanTableOps(t *testing.T) {
	size := TableParams{Rows: 2, Cols: 2}
	cases := []struct {
		name  string
		op    Op
		kinds string
		wantN int
	}{
		{"insert rows", tableOp(OpInsertRows, TableParams{Rows: 2, Cols: 2, Row: 1, Count: 2}), "insertTableRow insertTableRow", 2},
		{"insert columns before", tableOp(OpInsertColumns, TableParams{Rows: 2, Cols: 2, Col: 0, Count: 1, Before: true}), "insertTableColumn", 1},
		{"delete rows descending", tableOp(OpDeleteRows, TableParams{Rows: 3, Cols: 2, Indices: []int{0, 2}}), "deleteTableRow deleteTableRow", 2},
		{"delete columns", tableOp(OpDeleteColumns, TableParams{Rows: 2, Cols: 3, Indices: []int{1}}), "deleteTableColumn", 1},
		{"merge", tableOp(OpMergeCells, TableParams{Rows: 2, Cols: 2, Row: 0, Col: 0, RowSpan: 1, ColSpan: 2}), "mergeTableCells", 1},
		{"unmerge", tableOp(OpUnmergeCells, TableParams{Rows: 2, Cols: 2, Row: 0, Col: 0, RowSpan: 2, ColSpan: 1}), "unmergeTableCells", 1},
		{"style", tableOp(OpStyleCells, TableParams{Rows: 2, Cols: 2, Row: 0, Col: 0, RowSpan: 1, ColSpan: 2, Cell: CellStyleSpec{Background: "#eeeeee"}}), "updateTableCellStyle", 1},
		{"pin", tableOp(OpPinHeaderRows, TableParams{Rows: 2, Cols: 2, HeaderRows: 1}), "pinTableHeaderRows", 1},
		{"insert table", Op{Kind: OpInsertTable, Seg: seg(), Insert: &Loc{Index: 80, TabID: "t"}, Table: size, Description: "after p5"}, "insertTable", 1},
	}
	for _, tc := range cases {
		res, err := Plan([]Op{tc.op}, Options{Mode: ModeDirect})
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		var kinds []string
		for _, r := range res.Requests {
			kinds = append(kinds, Kind(r))
		}
		if strings.Join(kinds, " ") != tc.kinds || res.Summary[0].Requests != tc.wantN {
			t.Errorf("%s: requests %v summary %+v", tc.name, kinds, res.Summary)
		}
		// Every table op has a comment-mode wording.
		cres, err := Plan([]Op{withAnchor(tc.op)}, Options{Mode: ModeComment})
		if err != nil || len(cres.Proposals) != 1 || !strings.HasPrefix(cres.Proposals[0].Content, "Proposed") {
			t.Errorf("%s: proposal %+v %v", tc.name, cres, err)
		}
	}
	// Deleting rows two rows must run highest first.
	res, _ := Plan([]Op{tableOp(OpDeleteRows, TableParams{Rows: 3, Cols: 2, Indices: []int{0, 2}})}, Options{})
	if !strings.Contains(string(res.Requests[0]), `"rowIndex":2`) || !strings.Contains(string(res.Requests[1]), `"rowIndex":0`) {
		t.Fatalf("delete order: %s %s", res.Requests[0], res.Requests[1])
	}
}

func withAnchor(op Op) Op {
	op.CommentAnchor = &Rng{Start: 134, End: 140, TabID: "t"}
	return op
}

func TestTableValidationAndOrdering(t *testing.T) {
	bad := []Op{
		tableOp(OpInsertRows, TableParams{Rows: 2, Cols: 2, Row: 2, Count: 1}),
		tableOp(OpInsertRows, TableParams{Rows: 2, Cols: 2, Row: 0, Count: 0}),
		tableOp(OpDeleteRows, TableParams{Rows: 2, Cols: 2, Indices: []int{0, 1}}),
		tableOp(OpDeleteColumns, TableParams{Rows: 2, Cols: 2}),
		tableOp(OpMergeCells, TableParams{Rows: 2, Cols: 2, Row: 0, Col: 0, RowSpan: 1, ColSpan: 1}),
		tableOp(OpMergeCells, TableParams{Rows: 2, Cols: 2, Row: 1, Col: 0, RowSpan: 2, ColSpan: 1}),
		tableOp(OpStyleCells, TableParams{Rows: 2, Cols: 2, Row: 0, Col: 0, RowSpan: 1, ColSpan: 1}),
		tableOp(OpStyleCells, TableParams{Rows: 2, Cols: 2, Row: 0, Col: 0, RowSpan: 1, ColSpan: 1, Cell: CellStyleSpec{Align: "LEFT"}}),
		tableOp(OpPinHeaderRows, TableParams{Rows: 2, Cols: 2, HeaderRows: 2}),
		{Kind: OpInsertTable, Seg: seg(), Insert: &Loc{Index: 80}, Table: TableParams{Rows: 0, Cols: 2}},
		{Kind: OpInsertTable, Seg: seg(), Table: TableParams{Rows: 2, Cols: 2}},
		{Kind: OpInsertRows, Seg: seg(), Table: TableParams{Rows: 2, Cols: 2, Count: 1}},
	}
	for i, op := range bad {
		if _, err := Plan([]Op{op}, Options{}); err == nil {
			t.Errorf("case %d (%s) should fail validation", i, op.Kind)
		}
	}
	// Two grid changes on one table are refused; a grid change plus a
	// style on the same table is fine; a delete of the table block overlaps.
	a := tableOp(OpInsertRows, TableParams{Rows: 2, Cols: 2, Row: 0, Count: 1})
	b := tableOp(OpDeleteColumns, TableParams{Rows: 2, Cols: 2, Indices: []int{0}})
	b.Seq = 1
	if _, err := Plan([]Op{a, b}, Options{}); err == nil || !strings.Contains(err.Error(), "cannot go in one batch") {
		t.Fatalf("two structural ops: %v", err)
	}
	c := tableOp(OpStyleCells, TableParams{Rows: 2, Cols: 2, RowSpan: 1, ColSpan: 1, Cell: CellStyleSpec{Align: "TOP"}})
	c.Seq = 1
	if _, err := Plan([]Op{a, c}, Options{}); err != nil {
		t.Fatalf("structural plus style: %v", err)
	}
	del := Op{Seq: 2, Kind: OpDelete, Seg: seg(), Target: &Rng{Start: 133, End: 158, TabID: "t"}, TargetIsBlock: true, Description: "tbl1"}
	if _, err := Plan([]Op{a, del}, Options{}); err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("delete over table op: %v", err)
	}
	// Content after the table is edited before the grid changes; an edit
	// inside a cell too.
	after := Op{Seq: 3, Kind: OpDelete, Seg: seg(), Target: &Rng{Start: 160, End: 170, TabID: "t"}, Description: "p13"}
	inCell := Op{Seq: 4, Kind: OpDelete, Seg: seg(), Target: &Rng{Start: 140, End: 142, TabID: "t"}, Description: "cell"}
	res, err := Plan([]Op{a, after, inCell}, Options{})
	if err != nil || Kind(res.Requests[0]) != "deleteContentRange" || Kind(res.Requests[1]) != "deleteContentRange" || Kind(res.Requests[2]) != "insertTableRow" {
		t.Fatalf("ordering: %v %v", err, res)
	}
	// The guard applies to row deletion with anchors.
	g := tableOp(OpDeleteRows, TableParams{Rows: 3, Cols: 2, Indices: []int{1}})
	g.Anchors = []Anchor{{Kind: "comment", ID: "c1", Start: 140, End: 145}}
	if _, err := Plan([]Op{g}, Options{Mode: ModeDirect}); !errors.Is(err, ErrBlocked) {
		t.Fatalf("guard on delete_rows: %v", err)
	}
}

func TestObjectAndSegmentOps(t *testing.T) {
	at := &Loc{Index: 5, TabID: "t"}
	good := []Op{
		{Kind: OpInsertObject, Seg: seg(), Insert: at, Object: ObjectParams{Kind: "image", URL: "https://example.test/a.png", WidthPt: 100}, Description: "after p1"},
		{Kind: OpInsertObject, Seg: seg(), Insert: at, Object: ObjectParams{Kind: "person", Email: "a@example.test"}},
		{Kind: OpInsertObject, Seg: seg(), Insert: at, Object: ObjectParams{Kind: "rich_link", URL: "https://docs.google.com/x"}},
		{Kind: OpInsertObject, Seg: seg(), Insert: at, Object: ObjectParams{Kind: "date", Date: DateSpec{Timestamp: "2026-09-03T00:00:00Z"}}},
		{Kind: OpDeleteHeader, Seg: seg(), SegmentRef: "h1", CommentAnchor: &Rng{Start: 1, End: 4}},
		{Kind: OpDeleteFooter, Seg: seg(), SegmentRef: "f1", CommentAnchor: &Rng{Start: 1, End: 4}},
	}
	want := []string{"insertInlineImage", "insertPerson", "insertRichLink", "insertDate", "deleteHeader", "deleteFooter"}
	for i, op := range good {
		op.CommentAnchor = &Rng{Start: 1, End: 4}
		res, err := Plan([]Op{op}, Options{})
		if err != nil || Kind(res.Requests[0]) != want[i] {
			t.Errorf("%s: %v %v", want[i], err, res)
		}
		if cres, err := Plan([]Op{op}, Options{Mode: ModeComment}); err != nil || !strings.HasPrefix(cres.Proposals[0].Content, "Proposed") {
			t.Errorf("%s proposal: %v", want[i], err)
		}
	}
	bad := []Op{
		{Kind: OpInsertObject, Seg: seg(), Insert: at, Object: ObjectParams{Kind: "image", URL: "ftp://x"}},
		{Kind: OpInsertObject, Seg: seg(), Insert: at, Object: ObjectParams{Kind: "image", URL: "https://x", WidthPt: -1}},
		{Kind: OpInsertObject, Seg: seg(), Insert: at, Object: ObjectParams{Kind: "person", Email: "nobody"}},
		{Kind: OpInsertObject, Seg: seg(), Insert: at, Object: ObjectParams{Kind: "rich_link", URL: "http://x"}},
		{Kind: OpInsertObject, Seg: seg(), Insert: at, Object: ObjectParams{Kind: "date"}},
		{Kind: OpInsertObject, Seg: seg(), Insert: at, Object: ObjectParams{Kind: "date", Date: DateSpec{Timestamp: "2026-09-03T00:00:00Z", DateFormat: "DATE_FORMAT_ISO8601"}}},
		{Kind: OpInsertObject, Seg: seg(), Insert: at, Object: ObjectParams{Kind: "chart"}},
		{Kind: OpInsertObject, Seg: seg(), Object: ObjectParams{Kind: "image", URL: "https://x"}},
		{Kind: OpDeleteHeader, Seg: seg()},
	}
	for i, op := range bad {
		if _, err := Plan([]Op{op}, Options{}); err == nil {
			t.Errorf("bad case %d should fail", i)
		}
	}
}
