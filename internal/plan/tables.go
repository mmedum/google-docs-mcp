package plan

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Table operations (edit_table). set_cells is expanded by the service
// into replace ops on cells and never reaches the planner.
const (
	OpInsertTable    OpKind = "insert_table"
	OpSetCells       OpKind = "set_cells"
	OpInsertRows     OpKind = "insert_rows"
	OpDeleteRows     OpKind = "delete_rows"
	OpInsertColumns  OpKind = "insert_columns"
	OpDeleteColumns  OpKind = "delete_columns"
	OpMergeCells     OpKind = "merge_cells"
	OpUnmergeCells   OpKind = "unmerge_cells"
	OpStyleCells     OpKind = "style_cells"
	OpPinHeaderRows  OpKind = "pin_header_rows"
	OpDeleteHeader   OpKind = "delete_header"
	OpDeleteFooter   OpKind = "delete_footer"
	OpInsertObject   OpKind = "insert_object"
	opTableStructure        = "structure" // internal grouping label
)

// TableParams are the resolved arguments of a table op. Row and column
// numbers are zero-based here; the service converts from the 1-based
// numbers people use.
type TableParams struct {
	Rows, Cols int // insert_table size; table size for other ops
	Row, Col   int // reference cell
	Count      int
	Before     bool  // insert above / to the left
	Indices    []int // rows or columns to delete
	RowSpan    int
	ColSpan    int
	Cell       CellStyleSpec
	HeaderRows int
	// Data is the grid an insert_table fills after the table exists.
	Data [][]string
}

// ObjectParams describe an insert_object op.
type ObjectParams struct {
	Kind     string // image, person, rich_link, date
	URL      string
	WidthPt  float64
	HeightPt float64
	Name     string
	Email    string
	Title    string
	Date     DateSpec
}

// IsTableOp reports whether the kind belongs to edit_table.
func IsTableOp(k OpKind) bool {
	switch k {
	case OpInsertTable, OpSetCells, OpInsertRows, OpDeleteRows, OpInsertColumns, OpDeleteColumns, OpMergeCells, OpUnmergeCells, OpStyleCells, OpPinHeaderRows:
		return true
	}
	return false
}

// isStructural reports whether the op changes a table's grid, which
// renumbers rows or columns; at most one such op per table per batch.
func isStructural(k OpKind) bool {
	switch k {
	case OpInsertRows, OpDeleteRows, OpInsertColumns, OpDeleteColumns, OpMergeCells, OpUnmergeCells:
		return true
	}
	return false
}

func validateTableOp(op *Op) error {
	if op.Kind == OpInsertTable {
		if op.Insert == nil {
			return fmt.Errorf("op %d (%s): no insertion point", op.Seq, op.Kind)
		}
		if op.Table.Rows < 1 || op.Table.Cols < 1 || op.Table.Rows > 200 || op.Table.Cols > 20 {
			return fmt.Errorf("op %d: insert_table needs rows 1-200 and columns 1-20", op.Seq)
		}
		return nil
	}
	if op.TableAt == nil {
		return fmt.Errorf("op %d (%s): no table", op.Seq, op.Kind)
	}
	switch op.Kind {
	case OpInsertRows, OpInsertColumns, OpDeleteRows, OpDeleteColumns:
		return validateGridOp(op)
	case OpMergeCells, OpUnmergeCells, OpStyleCells:
		return validateCellRangeOp(op)
	case OpPinHeaderRows:
		if op.Table.HeaderRows < 0 || op.Table.HeaderRows >= op.Table.Rows {
			return fmt.Errorf("op %d: header rows must be between 0 and %d", op.Seq, op.Table.Rows-1)
		}
		return nil
	}
	return fmt.Errorf("op %d: unknown table op %q", op.Seq, op.Kind)
}

// inTable checks a zero-based row or column number against the table.
func (op *Op) inTable(what string, v, n int) error {
	if v < 0 || v >= n {
		return fmt.Errorf("op %d: %s %d is outside the table (%d rows × %d columns)", op.Seq, what, v+1, op.Table.Rows, op.Table.Cols)
	}
	return nil
}

func validateGridOp(op *Op) error {
	t := op.Table
	switch op.Kind {
	case OpInsertRows, OpInsertColumns:
		if t.Count < 1 || t.Count > 100 {
			return fmt.Errorf("op %d: count must be 1-100", op.Seq)
		}
		if op.Kind == OpInsertRows {
			return op.inTable("row", t.Row, t.Rows)
		}
		return op.inTable("column", t.Col, t.Cols)
	}
	if len(t.Indices) == 0 {
		return fmt.Errorf("op %d: nothing to delete; list the rows or columns", op.Seq)
	}
	what, n := "row", t.Rows
	if op.Kind == OpDeleteColumns {
		what, n = "column", t.Cols
	}
	if len(t.Indices) >= n {
		return fmt.Errorf("op %d: deleting every %s removes the whole table; delete the table block instead", op.Seq, what)
	}
	for _, i := range t.Indices {
		if err := op.inTable(what, i, n); err != nil {
			return err
		}
	}
	return nil
}

func validateCellRangeOp(op *Op) error {
	t := op.Table
	if err := op.inTable("row", t.Row, t.Rows); err != nil {
		return err
	}
	if err := op.inTable("column", t.Col, t.Cols); err != nil {
		return err
	}
	if t.RowSpan < 1 || t.ColSpan < 1 || t.Row+t.RowSpan > t.Rows || t.Col+t.ColSpan > t.Cols {
		return fmt.Errorf("op %d: the cell range runs past the table's %d rows × %d columns", op.Seq, t.Rows, t.Cols)
	}
	if op.Kind == OpStyleCells {
		if t.Cell.IsZero() {
			return fmt.Errorf("op %d: style_cells changes nothing; set background, align or padding_pt", op.Seq)
		}
		if err := t.Cell.Validate(); err != nil {
			return fmt.Errorf("op %d: %w", op.Seq, err)
		}
		return nil
	}
	if t.RowSpan == 1 && t.ColSpan == 1 {
		return fmt.Errorf("op %d: %s needs a range of at least two cells", op.Seq, op.Kind)
	}
	return nil
}

// tableRequests compiles one table op.
func tableRequests(op *Op) ([]json.RawMessage, error) {
	t := op.Table
	if op.Kind == OpInsertTable {
		return []json.RawMessage{InsertTable(t.Rows, t.Cols, *op.Insert)}, nil
	}
	cell := func(r, c int) CellLoc { return CellLoc{Table: *op.TableAt, Row: r, Col: c} }
	var reqs []json.RawMessage
	switch op.Kind {
	case OpInsertRows:
		for range t.Count {
			reqs = append(reqs, InsertTableRow(cell(t.Row, t.Col), !t.Before))
		}
	case OpInsertColumns:
		for range t.Count {
			reqs = append(reqs, InsertTableColumn(cell(t.Row, t.Col), !t.Before))
		}
	case OpDeleteRows, OpDeleteColumns:
		// Highest first so earlier numbers stay valid.
		idx := append([]int(nil), t.Indices...)
		sort.Sort(sort.Reverse(sort.IntSlice(idx)))
		for _, i := range idx {
			if op.Kind == OpDeleteRows {
				reqs = append(reqs, DeleteTableRow(cell(i, 0)))
			} else {
				reqs = append(reqs, DeleteTableColumn(cell(0, i)))
			}
		}
	case OpMergeCells:
		reqs = append(reqs, MergeTableCells(cell(t.Row, t.Col), t.RowSpan, t.ColSpan))
	case OpUnmergeCells:
		reqs = append(reqs, UnmergeTableCells(cell(t.Row, t.Col), t.RowSpan, t.ColSpan))
	case OpStyleCells:
		reqs = append(reqs, UpdateTableCellStyle(cell(t.Row, t.Col), t.RowSpan, t.ColSpan, t.Cell))
	case OpPinHeaderRows:
		reqs = append(reqs, PinTableHeaderRows(*op.TableAt, t.HeaderRows))
	default:
		return nil, fmt.Errorf("op %d: unknown table op %q", op.Seq, op.Kind)
	}
	return reqs, nil
}

// tableProposal words a table op as a comment-mode proposal.
func tableProposal(op *Op) string {
	t := op.Table
	plural := func(n int, s string) string {
		if n == 1 {
			return fmt.Sprintf("%d %s", n, s)
		}
		return fmt.Sprintf("%d %ss", n, s)
	}
	nums := func(idx []int) string {
		parts := make([]string, 0, len(idx))
		for _, i := range idx {
			parts = append(parts, fmt.Sprint(i+1))
		}
		return strings.Join(parts, ", ")
	}
	switch op.Kind {
	case OpInsertTable:
		s := fmt.Sprintf("Proposed: insert a %d×%d table at %s.", t.Rows, t.Cols, op.Description)
		if len(t.Data) > 0 {
			s += "\n\n" + gridText(t.Data)
		}
		return s
	case OpInsertRows:
		side := "below"
		if t.Before {
			side = "above"
		}
		return fmt.Sprintf("Proposed: insert %s %s row %d of %s.", plural(t.Count, "row"), side, t.Row+1, op.Description)
	case OpInsertColumns:
		side := "right of"
		if t.Before {
			side = "left of"
		}
		return fmt.Sprintf("Proposed: insert %s %s column %d of %s.", plural(t.Count, "column"), side, t.Col+1, op.Description)
	case OpDeleteRows:
		return fmt.Sprintf("Proposed: delete row(s) %s of %s.", nums(t.Indices), op.Description)
	case OpDeleteColumns:
		return fmt.Sprintf("Proposed: delete column(s) %s of %s.", nums(t.Indices), op.Description)
	case OpMergeCells:
		return fmt.Sprintf("Proposed: merge the %d×%d cells from r%dc%d of %s.", t.RowSpan, t.ColSpan, t.Row+1, t.Col+1, op.Description)
	case OpUnmergeCells:
		return fmt.Sprintf("Proposed: unmerge the %d×%d cells from r%dc%d of %s.", t.RowSpan, t.ColSpan, t.Row+1, t.Col+1, op.Description)
	case OpStyleCells:
		return fmt.Sprintf("Proposed cell formatting for %d×%d cells from r%dc%d of %s: %s.", t.RowSpan, t.ColSpan, t.Row+1, t.Col+1, op.Description, describeCell(t.Cell))
	case OpPinHeaderRows:
		return fmt.Sprintf("Proposed: pin %s of %s as repeating header rows.", plural(t.HeaderRows, "row"), op.Description)
	}
	return "Proposed table change to " + op.Description + "."
}

func describeCell(s CellStyleSpec) string {
	var parts []string
	if s.Background != "" {
		parts = append(parts, "background "+s.Background)
	}
	if s.Align != "" {
		parts = append(parts, "aligned "+strings.ToLower(s.Align))
	}
	if s.PaddingPt != nil {
		parts = append(parts, fmt.Sprintf("padding %gpt", *s.PaddingPt))
	}
	return strings.Join(parts, ", ")
}

func gridText(data [][]string) string {
	rows := make([]string, 0, len(data))
	for _, r := range data {
		rows = append(rows, strings.Join(r, " | "))
	}
	return strings.Join(rows, "\n")
}

// objectRequests compiles an insert_object op.
func objectRequests(op *Op) ([]json.RawMessage, error) {
	o := op.Object
	at := *op.Insert
	switch o.Kind {
	case "image":
		return []json.RawMessage{InsertInlineImage(o.URL, at, o.WidthPt, o.HeightPt)}, nil
	case "person":
		return []json.RawMessage{InsertPerson(o.Name, o.Email, at)}, nil
	case "rich_link":
		return []json.RawMessage{InsertRichLink(o.URL, o.Title, at)}, nil
	case "date":
		return []json.RawMessage{InsertDate(o.Date, at)}, nil
	}
	return nil, fmt.Errorf("op %d: unknown object kind %q", op.Seq, o.Kind)
}

func validateObjectOp(op *Op) error {
	if op.Insert == nil {
		return fmt.Errorf("op %d (%s): no insertion point", op.Seq, op.Kind)
	}
	o := op.Object
	switch o.Kind {
	case "image":
		if !strings.HasPrefix(o.URL, "https://") && !strings.HasPrefix(o.URL, "http://") {
			return fmt.Errorf("op %d: image url must be a public http(s) URL", op.Seq)
		}
		if len(o.URL) > 2000 {
			return fmt.Errorf("op %d: image url is longer than 2 kB", op.Seq)
		}
		if o.WidthPt < 0 || o.HeightPt < 0 || o.WidthPt > 2000 || o.HeightPt > 2000 {
			return fmt.Errorf("op %d: width_pt and height_pt must be between 0 and 2000", op.Seq)
		}
	case "person":
		if !strings.Contains(o.Email, "@") {
			return fmt.Errorf("op %d: a person chip needs an email address", op.Seq)
		}
	case "rich_link":
		if !strings.HasPrefix(o.URL, "https://") {
			return fmt.Errorf("op %d: a rich link needs an https URL of a Google resource", op.Seq)
		}
	case "date":
		if o.Date.Timestamp == "" {
			return fmt.Errorf("op %d: a date chip needs a date (RFC 3339 or YYYY-MM-DD)", op.Seq)
		}
	default:
		return fmt.Errorf("op %d: kind must be image, person, rich_link or date", op.Seq)
	}
	return nil
}

func objectProposal(op *Op) string {
	o := op.Object
	switch o.Kind {
	case "image":
		return "Proposed: insert the image " + o.URL + " at " + op.Description + "."
	case "person":
		return "Proposed: insert a people chip for " + o.Email + " at " + op.Description + "."
	case "rich_link":
		return "Proposed: insert a link chip to " + o.URL + " at " + op.Description + "."
	}
	return "Proposed: insert a date chip (" + o.Date.Timestamp + ") at " + op.Description + "."
}
