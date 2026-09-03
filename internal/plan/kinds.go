package plan

import "strings"

// Tool names the MCP tool an op kind belongs to.
type Tool string

// Tools.
const (
	ToolEdit   Tool = "edit_document"
	ToolFormat Tool = "format_document"
	ToolTable  Tool = "edit_table"
	ToolObject Tool = "insert_object"
	ToolLayout Tool = "layout_document"
)

// Shape says what an op must carry to be planned.
type Shape int

// Shapes.
const (
	ShapeNone    Shape = iota // nothing positional: replace_all, create_header, create_footer
	ShapeTarget               // Target: replace, delete, formatting
	ShapeInsert               // Insert: insert, append, breaks, footnotes, objects, insert_table
	ShapeTable                // TableAt: grid and cell ops
	ShapeSegment              // SegmentRef: delete_header, delete_footer
	ShapeTab                  // nothing but the tab: page setup, named styles, objects by id
)

// Group is an op's place in the compile order.
type Group int

// Groups, in the order compile emits them.
const (
	GroupContent Group = iota // index-ordered content ops, highest index first
	GroupFormat               // formatting first: it shifts nothing
	GroupBullets              // lists after the content that would shift them
	GroupGlobal               // replace_all last
	GroupSegment              // header and footer creation and deletion: no range to overlap
)

// KindInfo is everything the planner, the service and the tools need
// to know about an op kind apart from its own arithmetic.
type KindInfo struct {
	Tool    Tool
	Shape   Shape
	Content bool // carries a markdown fragment
	// Deletes says the op removes existing content, which the overwrite
	// guard inspects.
	Deletes bool
	Group   Group
	// Structural says the op changes a table's grid, which renumbers rows
	// or columns; at most one per table per batch.
	Structural bool
	// Followup says the op may create something (a segment, a table)
	// whose content lands in a second batch once it exists.
	Followup bool
	// SuggestRefused names the request kind the API refuses in SUGGEST
	// mode, when the op compiles to one.
	SuggestRefused string
	// Reply names the batchUpdate reply an op that creates a segment
	// comes back in; the reply carries the new segment's id.
	Reply string
	// Noun is what the op works on, for messages: "header", "footer",
	// "footnote", "table".
	Noun string
}

type kindEntry struct {
	kind OpKind
	info KindInfo
}

// kindTable is the registry, in the order tools list the kinds.
var kindTable = []kindEntry{
	{OpInsert, KindInfo{Tool: ToolEdit, Shape: ShapeInsert, Content: true}},
	{OpAppend, KindInfo{Tool: ToolEdit, Shape: ShapeInsert, Content: true}},
	{OpReplace, KindInfo{Tool: ToolEdit, Shape: ShapeTarget, Content: true, Deletes: true}},
	{OpDelete, KindInfo{Tool: ToolEdit, Shape: ShapeTarget, Deletes: true}},
	{OpReplaceAll, KindInfo{Tool: ToolEdit, Shape: ShapeNone, Deletes: true, Group: GroupGlobal}},
	{OpPageBreak, KindInfo{Tool: ToolEdit, Shape: ShapeInsert}},
	{OpFootnote, KindInfo{Tool: ToolEdit, Shape: ShapeInsert, Content: true, Followup: true, Reply: "createFootnote", Noun: "footnote"}},
	{OpCreateHeader, KindInfo{Tool: ToolEdit, Shape: ShapeNone, Content: true, Group: GroupSegment, Followup: true, Reply: "createHeader", Noun: "header"}},
	{OpCreateFooter, KindInfo{Tool: ToolEdit, Shape: ShapeNone, Content: true, Group: GroupSegment, Followup: true, Reply: "createFooter", Noun: "footer"}},
	{OpDeleteHeader, KindInfo{Tool: ToolEdit, Shape: ShapeSegment, Deletes: true, Group: GroupSegment, SuggestRefused: "deleteHeader", Noun: "header"}},
	{OpDeleteFooter, KindInfo{Tool: ToolEdit, Shape: ShapeSegment, Deletes: true, Group: GroupSegment, SuggestRefused: "deleteFooter", Noun: "footer"}},

	{OpTextStyle, KindInfo{Tool: ToolFormat, Shape: ShapeTarget, Group: GroupFormat}},
	{OpParagraphStyle, KindInfo{Tool: ToolFormat, Shape: ShapeTarget, Group: GroupFormat}},
	{OpBullets, KindInfo{Tool: ToolFormat, Shape: ShapeTarget, Group: GroupBullets}},
	{OpClearFormatting, KindInfo{Tool: ToolFormat, Shape: ShapeTarget, Group: GroupFormat}},

	{OpInsertTable, KindInfo{Tool: ToolTable, Shape: ShapeInsert, Followup: true, Noun: "table"}},
	// set_cells is expanded by the service into replace ops on cells and
	// never reaches the planner; it deletes what those replaces delete.
	{OpSetCells, KindInfo{Tool: ToolTable, Shape: ShapeTable, Deletes: true}},
	{OpInsertRows, KindInfo{Tool: ToolTable, Shape: ShapeTable, Structural: true}},
	{OpDeleteRows, KindInfo{Tool: ToolTable, Shape: ShapeTable, Deletes: true, Structural: true}},
	{OpInsertColumns, KindInfo{Tool: ToolTable, Shape: ShapeTable, Structural: true}},
	{OpDeleteColumns, KindInfo{Tool: ToolTable, Shape: ShapeTable, Deletes: true, Structural: true}},
	{OpMergeCells, KindInfo{Tool: ToolTable, Shape: ShapeTable, Deletes: true, Structural: true}},
	{OpUnmergeCells, KindInfo{Tool: ToolTable, Shape: ShapeTable, Structural: true}},
	{OpStyleCells, KindInfo{Tool: ToolTable, Shape: ShapeTable}},
	{OpPinHeaderRows, KindInfo{Tool: ToolTable, Shape: ShapeTable}},

	{OpStyleColumns, KindInfo{Tool: ToolTable, Shape: ShapeTable, SuggestRefused: "updateTableColumnProperties"}},
	{OpStyleRows, KindInfo{Tool: ToolTable, Shape: ShapeTable}},

	{OpInsertObject, KindInfo{Tool: ToolObject, Shape: ShapeInsert}},
	{OpReplaceImage, KindInfo{Tool: ToolObject, Shape: ShapeTab, Noun: "image"}},
	// A floating object is deleted by id and an inline one with its
	// range, which is why the shape is the tab and the service fills in
	// the range when it finds the object in the text.
	{OpDeleteObject, KindInfo{Tool: ToolObject, Shape: ShapeTab, Deletes: true, Noun: "object"}},

	{OpPageSetup, KindInfo{Tool: ToolLayout, Shape: ShapeTab}},
	{OpSectionStyle, KindInfo{Tool: ToolLayout, Shape: ShapeTarget, Group: GroupFormat}},
	{OpSectionBreak, KindInfo{Tool: ToolLayout, Shape: ShapeInsert}},
	{OpNamedStyle, KindInfo{Tool: ToolLayout, Shape: ShapeTab}},

	{OpCreateNamedRange, KindInfo{Tool: ToolEdit, Shape: ShapeTarget, SuggestRefused: "createNamedRange", Noun: "named range"}},
	{OpDeleteNamedRange, KindInfo{Tool: ToolEdit, Shape: ShapeTab, SuggestRefused: "deleteNamedRange", Noun: "named range"}},
	{OpReplaceNamedRange, KindInfo{Tool: ToolEdit, Shape: ShapeTab, Deletes: true, Noun: "named range"}},
}

var kindInfos = func() map[OpKind]KindInfo {
	m := make(map[OpKind]KindInfo, len(kindTable))
	for _, e := range kindTable {
		m[e.kind] = e.info
	}
	return m
}()

// Info describes an op kind; ok is false for a kind the registry does
// not know.
func Info(k OpKind) (info KindInfo, ok bool) {
	info, ok = kindInfos[k]
	return info, ok
}

// Noun is what the kind works on, for messages.
func Noun(k OpKind) string { return kindInfos[k].Noun }

// Has reports whether the kind belongs to this tool.
func (t Tool) Has(k OpKind) bool { return kindInfos[k].Tool == t }

// SegmentReplies lists the kinds whose reply names a new segment, with
// the reply key, in registry order.
func SegmentReplies() []struct {
	Kind  OpKind
	Reply string
} {
	var out []struct {
		Kind  OpKind
		Reply string
	}
	for _, e := range kindTable {
		if e.info.Reply != "" {
			out = append(out, struct {
				Kind  OpKind
				Reply string
			}{e.kind, e.info.Reply})
		}
	}
	return out
}

// KindsOf lists a tool's op kinds in registry order.
func KindsOf(t Tool) []OpKind {
	var out []OpKind
	for _, e := range kindTable {
		if e.info.Tool == t {
			out = append(out, e.kind)
		}
	}
	return out
}

// KindList words a tool's op kinds for an error message: "a, b or c".
func KindList(t Tool) string {
	ks := KindsOf(t)
	names := make([]string, len(ks))
	for i, k := range ks {
		names[i] = string(k)
	}
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

// Deletes reports whether the kind removes existing content, which is
// what the overwrite guard protects.
func Deletes(k OpKind) bool { return kindInfos[k].Deletes }

// Structural reports whether the op changes a table's grid, which
// renumbers the rows or columns that later ops name.
func Structural(k OpKind) bool { return kindInfos[k].Structural }

// NeedsFollowup reports whether the op creates something whose content
// is inserted in a second batch: a header, footer or footnote with
// content, or a table with a data grid.
func (op *Op) NeedsFollowup() bool {
	if !kindInfos[op.Kind].Followup {
		return false
	}
	if op.Kind == OpInsertTable {
		return len(op.Table.Data) > 0
	}
	return op.Fragment != nil && len(op.Fragment.Blocks) > 0
}
