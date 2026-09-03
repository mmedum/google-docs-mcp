package service

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mmedum/google-docs-mcp/internal/doc"
	"github.com/mmedum/google-docs-mcp/internal/markdown"
	"github.com/mmedum/google-docs-mcp/internal/plan"
)

// TableOp carries the table-specific arguments of an edit_table op, in
// the 1-based numbering people use.
type TableOp struct {
	Table     string // table handle: tbl1, tab2/tbl1
	Rows      int    // insert_table
	Cols      int    // insert_table
	Data      [][]string
	StartCell string        // set_cells: where Data starts; default r1c1
	Cells     []CellContent // set_cells: explicit cells
	Row       int           // insert_rows: reference row
	Column    int           // insert_columns: reference column
	Count     int
	Before    bool
	RowList   []int // delete_rows
	ColList   []int // delete_columns
	FromCell  string
	ToCell    string
	Style     plan.CellStyleSpec
	HeaderRow int // pin_header_rows
}

// CellContent is new content for one cell.
type CellContent struct {
	Cell    string
	Content string
}

var cellRef = regexp.MustCompile(`^(?:([A-Za-z0-9/]+):)?r(\d+)c(\d+)$`)

// parseCell reads r2c3 or tbl1:r2c3 into 1-based row and column, and
// checks any table prefix against the expected handle.
func parseCell(ref, table string) (int, int, error) {
	m := cellRef.FindStringSubmatch(strings.TrimSpace(ref))
	if m == nil {
		return 0, 0, Errorf("invalid", "cell %q; cells are named like r2c3 (or %s:r2c3)", ref, table)
	}
	if m[1] != "" && m[1] != table {
		return 0, 0, Errorf("invalid", "cell %s belongs to %s, not %s", ref, m[1], table)
	}
	r, _ := strconv.Atoi(m[2])
	c, _ := strconv.Atoi(m[3])
	if r < 1 || c < 1 {
		return 0, 0, Errorf("invalid", "cell %q: rows and columns start at 1", ref)
	}
	return r, c, nil
}

// resolveTable finds a top-level table by handle, checked against the
// handle memory like any other block.
func (s *Service) resolveTable(f *Fetched, handle string) (*doc.Block, error) {
	handle = strings.TrimSpace(handle)
	if handle == "" {
		return nil, Errorf("invalid", "table is empty; pass the table's handle (tbl1) from a read with with_handles")
	}
	b, ok := f.Doc.FindHandle(handle)
	if !ok {
		return nil, Errorf("not_found", "no block %s at this revision; re-read with with_handles", handle)
	}
	if b.Table == nil {
		return nil, Errorf("invalid", "%s is a %s, not a table", handle, b.Kind)
	}
	if b.Cell != nil {
		return nil, Errorf("unsupported", "%s is nested inside a table cell; nested tables cannot be edited", handle)
	}
	if _, err := s.checkedIndex(f, b.Segment, handle); err != nil {
		return nil, err
	}
	return b, nil
}

// tableCell returns the cell at 1-based row and column.
func tableCell(b *doc.Block, row, col int) (*doc.Cell, error) {
	t := b.Table
	if row < 1 || row > len(t.Cells) || col < 1 || col > len(t.Cells[row-1]) {
		return nil, Errorf("not_found", "%s has no cell r%dc%d (it is %d×%d)", b.Handle, row, col, t.Rows, t.Cols)
	}
	c := t.Cells[row-1][col-1]
	if c.MergedInto != nil {
		return nil, Errorf("invalid", "cell r%dc%d is merged into r%dc%d; write that cell instead", row, col, c.MergedInto.Row, c.MergedInto.Col)
	}
	return c, nil
}

// resolveTableOp fills a planner op for every edit_table kind except
// insert_table (an insertion) and set_cells (expanded into replaces).
func (s *Service) resolveTableOp(f *Fetched, op EditOp, p *plan.Op, out *resolvedOps) error {
	if op.Table == nil {
		return Errorf("invalid", "%s needs table arguments", op.Kind)
	}
	to := op.Table
	b, err := s.resolveTable(f, to.Table)
	if err != nil {
		return err
	}
	tab, seg := b.Segment.Tab, b.Segment
	p.Seg = SegmentBounds(tab, seg)
	p.TableAt = &plan.Loc{Index: b.Start, SegmentID: seg.ID, TabID: tab.ID}
	p.Table = plan.TableParams{Rows: b.Table.Rows, Cols: b.Table.Cols}
	p.Description = fmt.Sprintf("%s (%d×%d)", b.Handle, b.Table.Rows, b.Table.Cols)
	p.CommentAnchor = tableAnchor(tab, seg, b)
	out.note(tab.ID, seg.ID, b.Start)
	tp := &p.Table
	switch op.Kind {
	case plan.OpInsertRows:
		tp.Row, tp.Count, tp.Before = max(to.Row, 1)-1, max(to.Count, 1), to.Before
	case plan.OpInsertColumns:
		tp.Col, tp.Count, tp.Before = max(to.Column, 1)-1, max(to.Count, 1), to.Before
	case plan.OpDeleteRows, plan.OpDeleteColumns:
		list := to.RowList
		if op.Kind == plan.OpDeleteColumns {
			list = to.ColList
		}
		seen := map[int]bool{}
		for _, n := range list {
			if n < 1 {
				return Errorf("invalid", "rows and columns are numbered from 1")
			}
			if !seen[n] {
				seen[n] = true
				tp.Indices = append(tp.Indices, n-1)
			}
		}
		p.Anchors = tableLineAnchors(b, op.Kind == plan.OpDeleteRows, tp.Indices, out.threads)
	case plan.OpMergeCells, plan.OpUnmergeCells, plan.OpStyleCells:
		if err := cellRange(b, to, tp); err != nil {
			return err
		}
		tp.Cell = to.Style
	case plan.OpPinHeaderRows:
		tp.HeaderRows = to.HeaderRow
	default:
		return Errorf("invalid", "unknown table op %q", op.Kind)
	}
	return nil
}

// cellRange turns from_cell/to_cell (default: the whole table) into a
// zero-based origin and spans.
func cellRange(b *doc.Block, to *TableOp, tp *plan.TableParams) error {
	if to.FromCell == "" && to.ToCell == "" {
		tp.Row, tp.Col, tp.RowSpan, tp.ColSpan = 0, 0, b.Table.Rows, b.Table.Cols
		return nil
	}
	if to.FromCell == "" {
		return Errorf("invalid", "from_cell is needed with to_cell")
	}
	r1, c1, err := parseCell(to.FromCell, b.Handle)
	if err != nil {
		return err
	}
	r2, c2 := r1, c1
	if to.ToCell != "" {
		if r2, c2, err = parseCell(to.ToCell, b.Handle); err != nil {
			return err
		}
	}
	if r2 < r1 || c2 < c1 {
		return Errorf("invalid", "to_cell %s lies before from_cell %s", to.ToCell, to.FromCell)
	}
	tp.Row, tp.Col, tp.RowSpan, tp.ColSpan = r1-1, c1-1, r2-r1+1, c2-c1+1
	return nil
}

// tableAnchor is where a comment-mode proposal about a table attaches:
// the first paragraph of its first cell.
func tableAnchor(tab *doc.Tab, seg *doc.Segment, b *doc.Block) *plan.Rng {
	for _, row := range b.Table.Cells {
		for _, c := range row {
			for _, nb := range c.Blocks {
				if nb.Paragraph != nil {
					return blockRng(tab, seg, nb)
				}
			}
		}
	}
	return blockRng(tab, seg, b)
}

// tableLineAnchors lists anchored content inside the rows or columns a
// deletion removes.
func tableLineAnchors(b *doc.Block, rows bool, indices []int, threads []CommentThread) []plan.Anchor {
	var out []plan.Anchor
	seen := map[string]bool{}
	tab, seg := b.Segment.Tab, b.Segment
	for _, i := range indices {
		for ri, row := range b.Table.Cells {
			for ci, c := range row {
				if (rows && ri != i) || (!rows && ci != i) {
					continue
				}
				for _, a := range anchorsIn(tab, seg, c.Start, c.End, threads) {
					key := a.Kind + ":" + a.ID
					if !seen[key] {
						seen[key] = true
						out = append(out, a)
					}
				}
			}
		}
	}
	return out
}

// expandSetCells turns a set_cells op into one replace per cell, all
// sharing the op's sequence number so the result reports them together.
func (s *Service) expandSetCells(f *Fetched, seq int, op EditOp, out *resolvedOps) ([]plan.Op, error) {
	if op.Table == nil {
		return nil, Errorf("invalid", "set_cells needs table arguments")
	}
	to := op.Table
	b, err := s.resolveTable(f, to.Table)
	if err != nil {
		return nil, err
	}
	cells, err := cellContents(b, to)
	if err != nil {
		return nil, err
	}
	tab, seg := b.Segment.Tab, b.Segment
	desc := fmt.Sprintf("%d cell(s) of %s", len(cells), b.Handle)
	ops := make([]plan.Op, 0, len(cells))
	for _, cc := range cells {
		r, c, err := parseCell(cc.Cell, b.Handle)
		if err != nil {
			return nil, err
		}
		cell, err := tableCell(b, r, c)
		if err != nil {
			return nil, err
		}
		if len(cell.Blocks) == 0 {
			return nil, Errorf("invalid", "cell %s has no content blocks", cell.Handle)
		}
		frag, err := parseContent(cc.Content, op.ContentFormat)
		if err != nil {
			return nil, Errorf("unsupported", "cell %s: %v", cell.Handle, err)
		}
		if len(frag.Blocks) == 0 {
			frag = parseEmpty()
		}
		p := plan.Op{Seq: seq, Kind: plan.OpReplace, Seg: SegmentBounds(tab, seg), Description: desc, Fragment: frag,
			Target: &plan.Rng{Start: cell.Blocks[0].Start, End: cell.ContentEnd(), SegmentID: seg.ID, TabID: tab.ID}, TargetText: cell.Text(doc.ViewInline)}
		p.Anchors = anchorsIn(tab, seg, p.Target.Start, p.Target.End, out.threads)
		p.CommentAnchor = &plan.Rng{Start: cell.Blocks[0].Start, End: cell.Blocks[0].End, SegmentID: seg.ID, TabID: tab.ID}
		ops = append(ops, p)
	}
	out.note(tab.ID, seg.ID, b.Start)
	return ops, nil
}

// cellContents lists the cells a set_cells op writes: the explicit list,
// plus a grid laid out from start_cell.
func cellContents(b *doc.Block, to *TableOp) ([]CellContent, error) {
	cells := append([]CellContent(nil), to.Cells...)
	if len(to.Data) > 0 {
		r0, c0 := 1, 1
		if to.StartCell != "" {
			var err error
			if r0, c0, err = parseCell(to.StartCell, b.Handle); err != nil {
				return nil, err
			}
		}
		for ri, row := range to.Data {
			for ci, text := range row {
				cells = append(cells, CellContent{Cell: fmt.Sprintf("r%dc%d", r0+ri, c0+ci), Content: text})
			}
		}
	}
	if len(cells) == 0 {
		return nil, Errorf("invalid", "set_cells needs cells or data")
	}
	seen := map[string]bool{}
	for _, c := range cells {
		if seen[c.Cell] {
			return nil, Errorf("invalid", "cell %s is set twice", c.Cell)
		}
		seen[c.Cell] = true
	}
	return cells, nil
}

// resolveInsertTable positions a new table. The API inserts a newline at
// the index and starts the table right after it, so a block boundary is
// turned into "before this block's newline": the table follows the block
// and the block's newline becomes the paragraph after the table.
func (s *Service) resolveInsertTable(f *Fetched, op EditOp, p *plan.Op, out *resolvedOps) error {
	if op.Table == nil {
		return Errorf("invalid", "insert_table needs rows and columns")
	}
	if op.Location == nil {
		return Errorf("invalid", "insert_table needs a location")
	}
	ip, err := s.resolveLocation(f, *op.Location)
	if err != nil {
		return err
	}
	tab, ok := f.Doc.Tab(ip.seg.TabID)
	if !ok {
		return Errorf("unexpected", "tab %s vanished", ip.seg.TabID)
	}
	seg := segmentByID(tab, ip.seg.ID)
	if seg != nil && seg.Kind == doc.SegmentFootnote {
		return Errorf("unsupported", "tables cannot be inserted in footnotes")
	}
	index := ip.index
	if !ip.inline && !ip.atEnd && seg != nil {
		for _, b := range seg.Blocks {
			if b.End == index && b.Paragraph != nil {
				index = b.End - 1
				break
			}
		}
	}
	p.Seg = ip.seg
	p.Insert = &plan.Loc{Index: index, SegmentID: ip.seg.ID, TabID: ip.seg.TabID}
	p.Table = plan.TableParams{Rows: op.Table.Rows, Cols: op.Table.Cols, Data: op.Table.Data}
	p.Description = ip.description
	p.CommentAnchor = ip.anchor
	out.note(ip.seg.TabID, ip.seg.ID, index)
	return nil
}

// parseEmpty is the fragment that clears a cell: one empty paragraph.
func parseEmpty() *markdown.Fragment { return markdown.Plain("") }

func segmentByID(tab *doc.Tab, id string) *doc.Segment {
	for _, sg := range tab.Segments() {
		if sg.ID == id {
			return sg
		}
	}
	return nil
}

// fillNewTables writes the data of every insert_table op into the table
// it created, one edit per table, after the first batch landed. The
// tables are found by the index the insertion named.
func (s *Service) fillNewTables(ctx context.Context, req EditRequest, ro *resolvedOps, result *EditResult) {
	type fill struct {
		seq   int
		seg   plan.Segment
		index int64
		data  [][]string
		rows  int
		cols  int
	}
	var fills []fill
	for _, p := range ro.ops {
		if p.Kind == plan.OpInsertTable && len(p.Table.Data) > 0 {
			fills = append(fills, fill{p.Seq, p.Seg, p.Insert.Index, p.Table.Data, p.Table.Rows, p.Table.Cols})
		}
	}
	if len(fills) == 0 {
		return
	}
	f, err := s.FetchFresh(ctx, req.Document)
	if err != nil {
		result.Warnings = append(result.Warnings, "the table was created but re-reading the document failed, so it was not filled: "+err.Error())
		return
	}
	// Handles of the new tables, before any fill shifts indices.
	handles := make([]string, len(fills))
	for i, fl := range fills {
		tab, ok := f.Doc.Tab(fl.seg.TabID)
		if !ok {
			continue
		}
		if seg := segmentByID(tab, fl.seg.ID); seg != nil {
			for _, b := range seg.Blocks {
				if b.Table != nil && b.Start == fl.index+1 {
					handles[i] = b.Handle
				}
			}
		}
	}
	s.Remember(f)
	for i, fl := range fills {
		if handles[i] == "" {
			result.Warnings = append(result.Warnings, fmt.Sprintf("op %d: the table was created but could not be found again to fill it; use set_cells", fl.seq))
			continue
		}
		data := fl.data
		if len(data) > fl.rows {
			data = data[:fl.rows]
			result.Warnings = append(result.Warnings, fmt.Sprintf("op %d: data has more rows than the table; extra rows were dropped", fl.seq))
		}
		data = append([][]string(nil), data...) // never trim the caller's rows in place
		for ri := range data {
			if len(data[ri]) > fl.cols {
				data[ri] = data[ri][:fl.cols]
				result.Warnings = append(result.Warnings, fmt.Sprintf("op %d: row %d has more cells than the table; extras were dropped", fl.seq, ri+1))
			}
		}
		sub := EditRequest{Document: req.Document, Mode: req.Mode, Ops: []EditOp{{Kind: plan.OpSetCells, Table: &TableOp{Table: handles[i], Data: data}}}}
		res, err := s.Edit(ctx, sub)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("op %d: the empty table %s exists but filling it failed: %v", fl.seq, handles[i], err))
			continue
		}
		result.RevisionID = res.RevisionID
		result.SuggestionIDs = append(result.SuggestionIDs, res.SuggestionIDs...)
		result.Warnings = append(result.Warnings, res.Warnings...)
		for j := range result.Changes {
			if result.Changes[j].Seq == fl.seq {
				result.Changes[j].Description += fmt.Sprintf(", filled as %s", handles[i])
			}
		}
	}
}
