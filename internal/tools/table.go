package tools

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-docs-mcp/internal/plan"
	"github.com/mmedum/google-docs-mcp/internal/service"
)

// CellInput is new content for one cell.
type CellInput struct {
	Cell    string `json:"cell" jsonschema:"cell name relative to the table, e.g. r2c3"`
	Content string `json:"content" jsonschema:"new cell content as markdown; use content_format text for one paragraph per line"`
}

// TableOpInput is one edit_table operation.
type TableOpInput struct {
	Op            string         `json:"op" jsonschema:"insert_table, set_cells, insert_rows, delete_rows, insert_columns, delete_columns, merge_cells, unmerge_cells, style_cells, pin_header_rows"`
	Table         string         `json:"table,omitempty" jsonschema:"handle of the table (tbl1, tab2/tbl1) from a read with with_handles; every op except insert_table"`
	Location      *LocationInput `json:"location,omitempty" jsonschema:"insert_table: where the table goes (after or before a block, or end of the body)"`
	Rows          int            `json:"rows,omitempty" jsonschema:"insert_table: number of rows"`
	Columns       int            `json:"columns,omitempty" jsonschema:"insert_table: number of columns"`
	Data          [][]string     `json:"data,omitempty" jsonschema:"insert_table or set_cells: a grid of cell contents, row by row, written from start_cell (default r1c1)"`
	StartCell     string         `json:"start_cell,omitempty" jsonschema:"set_cells: the cell where data starts; default r1c1"`
	Cells         []CellInput    `json:"cells,omitempty" jsonschema:"set_cells: individual cells to write"`
	ContentFormat string         `json:"content_format,omitempty" jsonschema:"markdown (default) or text for verbatim cell content"`
	Row           int            `json:"row,omitempty" jsonschema:"insert_rows: the row (1-based) the new rows go next to"`
	Column        int            `json:"column,omitempty" jsonschema:"insert_columns: the column (1-based) the new columns go next to"`
	Count         int            `json:"count,omitempty" jsonschema:"insert_rows, insert_columns: how many (default 1); pin_header_rows: how many rows to pin (0 unpins)"`
	Before        bool           `json:"before,omitempty" jsonschema:"insert_rows, insert_columns: insert above or to the left instead of below or to the right"`
	RowNumbers    []int          `json:"row_numbers,omitempty" jsonschema:"delete_rows: the rows to delete, 1-based"`
	ColumnNumbers []int          `json:"column_numbers,omitempty" jsonschema:"delete_columns: the columns to delete, 1-based"`
	FromCell      string         `json:"from_cell,omitempty" jsonschema:"merge_cells, unmerge_cells, style_cells: top-left cell of the range (r1c1); style_cells defaults to the whole table"`
	ToCell        string         `json:"to_cell,omitempty" jsonschema:"bottom-right cell of the range; default the same cell"`
	Background    string         `json:"background,omitempty" jsonschema:"style_cells: cell background as #rrggbb, or none"`
	Align         string         `json:"align,omitempty" jsonschema:"style_cells: vertical content alignment TOP, MIDDLE or BOTTOM"`
	PaddingPt     *float64       `json:"padding_pt,omitempty" jsonschema:"style_cells: padding on every side in points"`
}

// TableInput is the edit_table call.
type TableInput struct {
	Document       string         `json:"document" jsonschema:"document id or any docs.google.com URL"`
	Ops            []TableOpInput `json:"ops" jsonschema:"operations applied together; at most one op that changes a table's grid (rows, columns, merges) per table per call"`
	Mode           string         `json:"mode,omitempty" jsonschema:"suggest, direct or comment; default from get_document capabilities"`
	DryRun         bool           `json:"dry_run,omitempty"`
	ExpectRevision string         `json:"expect_revision,omitempty"`
	Force          bool           `json:"force,omitempty" jsonschema:"direct mode only: allow deleting rows or columns that hold comments, suggestions, images or footnotes"`
}

func (o TableOpInput) tableOp() *service.TableOp {
	t := &service.TableOp{Table: o.Table, Rows: o.Rows, Cols: o.Columns, Data: o.Data, StartCell: o.StartCell, Row: o.Row, Column: o.Column,
		Count: o.Count, Before: o.Before, RowList: o.RowNumbers, ColList: o.ColumnNumbers, FromCell: o.FromCell, ToCell: o.ToCell,
		Style: plan.CellStyleSpec{Background: o.Background, Align: strings.ToUpper(strings.TrimSpace(o.Align)), PaddingPt: o.PaddingPt}}
	for _, c := range o.Cells {
		t.Cells = append(t.Cells, service.CellContent{Cell: c.Cell, Content: c.Content})
	}
	return t
}

func registerTable(s *mcp.Server, d Deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "edit_table",
		Description: "Create and change tables in a Google Doc. Ops: insert_table (rows, columns, optional data grid, at a " +
			"location), set_cells (write cells by name or as a grid from start_cell; each cell is replaced by minimal " +
			"diff), insert_rows, delete_rows, insert_columns, delete_columns, merge_cells, unmerge_cells, style_cells " +
			"(background, vertical alignment, padding), pin_header_rows. Tables are named by handle (tbl1) from a read " +
			"with with_handles; cells as r2c3. Text inside cells can also be edited with edit_document (target cell) and " +
			"styled with format_document. Same mode, dry_run, expect_revision and force semantics as edit_document; " +
			"deleting rows or columns that hold comments or suggestions is refused in direct mode unless forced.",
		Annotations: writeSafe,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in TableInput) (*mcp.CallToolResult, *service.EditResult, error) {
		ops := make([]service.EditOp, 0, len(in.Ops))
		for i, o := range in.Ops {
			kind := plan.OpKind(strings.ToLower(strings.TrimSpace(o.Op)))
			if !plan.IsTableOp(kind) {
				return nil, nil, fail(service.Errorf("invalid", "op %d: unknown op %q; use %s", i, o.Op, plan.KindList(plan.ToolTable)))
			}
			ops = append(ops, service.EditOp{Kind: kind, Table: o.tableOp(), ContentFormat: o.ContentFormat, Location: o.Location.location()})
		}
		res, err := d.Service.Edit(ctx, service.EditRequest{Document: in.Document, Ops: ops, Mode: in.Mode, DryRun: in.DryRun, ExpectRevision: in.ExpectRevision, Force: in.Force})
		if err != nil {
			return nil, nil, fail(err)
		}
		return text(res.Text), res, nil
	})
}
