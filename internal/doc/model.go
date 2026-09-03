// Package doc holds the parsed document model. It turns the Docs API's
// index-addressed JSON into a tree of tabs, segments, blocks and runs
// that carries the original UTF-16 indices, assigns handles the model
// can name, and derives sections from headings. Nothing here talks to
// the network.
package doc

import (
	"sort"
	"strconv"
	"strings"

	"github.com/mmedum/google-docs-mcp/internal/gdocs"
)

// Document is a parsed documents.get response.
type Document struct {
	ID                  string
	Title               string
	RevisionID          string
	SuggestionsViewMode string
	// Tabs in document order, parents before children.
	Tabs []*Tab

	byHandle map[string]*Block // every block by handle, filled by Parse
	byCell   map[string]*Cell  // every table cell by handle, filled by Parse
}

// Tab is one document tab. Documents without tabs get a single synthetic
// tab so callers have one code path.
type Tab struct {
	ID       string
	Title    string
	Number   int // 1-based position in Document.Tabs
	Index    int64
	ParentID string
	Nesting  int

	Body      *Segment
	Headers   []*Segment
	Footers   []*Segment
	Footnotes []*Segment

	Lists         map[string]*ListInfo
	InlineObjects map[string]*InlineObjectInfo
	// PositionedObjects are floating images, keyed by object id. They sit
	// on a paragraph rather than in its text, so no range covers them and
	// only delete_object removes one.
	PositionedObjects map[string]*InlineObjectInfo
	// NamedRanges are the tab's named ranges by name, in name order. A
	// name can cover several ranges.
	NamedRanges []*NamedRange
	// Page is the tab's page setup.
	Page *PageSetup
	// NamedStyles are the tab's named style definitions, in
	// NamedStyleOrder. Every paragraph inherits from the one its
	// NamedStyle names, and layout_document named_style redefines them.
	NamedStyles []*NamedStyleDef
}

// NamedStyleOrder lists the named styles in the order a read reports
// them: the document's body style first, then its headings top down.
var NamedStyleOrder = []string{"NORMAL_TEXT", "TITLE", "SUBTITLE",
	"HEADING_1", "HEADING_2", "HEADING_3", "HEADING_4", "HEADING_5", "HEADING_6"}

// ParagraphStyle is block-level formatting, in the units the API uses:
// line spacing is a percentage of single (100 = single) and lengths are
// points. A named style defines one, and a paragraph shows it unless an
// edit overrode it on the paragraph itself, so both carry this type.
type ParagraphStyle struct {
	Alignment         string
	LineSpacing       float64
	SpaceAbovePt      float64
	SpaceBelowPt      float64
	IndentStartPt     float64
	IndentFirstLinePt float64
	KeepWithNext      bool
	// PageBreakBefore is read only: it is worth knowing that every
	// HEADING_1 starts a page, but no write op sets it.
	PageBreakBefore bool
}

// NamedStyleDef is what one named style means in a tab: the formatting
// every paragraph carrying it inherits, and so the formatting a
// paragraph shows unless it overrides it.
type NamedStyleDef struct {
	Type string // NORMAL_TEXT, TITLE, SUBTITLE, HEADING_1..6
	Text TextStyle
	ParagraphStyle
}

// NamedRange is a span the document remembers by name. Unlike a handle
// it survives edits, because Google moves it with the text it covers.
type NamedRange struct {
	ID      string
	Name    string
	Segment string
	Start   int64
	End     int64
}

// PageSetup is a tab's page size, margins and header/footer choices.
type PageSetup struct {
	WidthPt         float64
	HeightPt        float64
	MarginTopPt     float64
	MarginBottomPt  float64
	MarginLeftPt    float64
	MarginRightPt   float64
	MarginHeaderPt  float64
	MarginFooterPt  float64
	PageNumberStart int64
	Landscape       bool
	FirstPageHF     bool
	EvenPageHF      bool
	Background      string
}

// Segments returns body, headers, footers and footnotes in that order.
func (t *Tab) Segments() []*Segment {
	out := make([]*Segment, 0, 1+len(t.Headers)+len(t.Footers)+len(t.Footnotes))
	out = append(out, t.Body)
	out = append(out, t.Headers...)
	out = append(out, t.Footers...)
	out = append(out, t.Footnotes...)
	return out
}

// NamedStyleUse counts the tab's paragraphs by the named style they
// carry, so a read can report the definitions actually in use rather
// than all nine. Every segment counts, not just the body: redefining a
// style changes the whole tab, headers and footnotes with it. A
// paragraph that names no style carries NORMAL_TEXT, which is what
// leaving the field out means.
func (t *Tab) NamedStyleUse() map[string]int {
	use := make(map[string]int, len(NamedStyleOrder))
	for _, seg := range t.Segments() {
		for _, b := range seg.AllBlocks() {
			if b.Paragraph == nil {
				continue
			}
			name := b.Paragraph.NamedStyle
			if name == "" {
				name = "NORMAL_TEXT"
			}
			use[name]++
		}
	}
	return use
}

// Prefix is the handle prefix for this tab ("" for the first tab).
func (t *Tab) Prefix() string {
	if t.Number <= 1 {
		return ""
	}
	return "tab" + strconv.Itoa(t.Number) + "/"
}

// ListInfo describes a list's nesting levels.
type ListInfo struct {
	ID     string
	Levels []ListLevel
}

// ListLevel is one nesting level of a list.
type ListLevel struct {
	Ordered   bool
	Glyph     string
	GlyphType string
}

// InlineObjectInfo describes an inline image, drawing or chart.
type InlineObjectInfo struct {
	ID          string
	Kind        string // image, drawing, chart, object
	Title       string
	Description string
	SourceURI   string
	ContentURI  string
	WidthPt     float64
	HeightPt    float64
}

// SegmentKind distinguishes index spaces.
type SegmentKind string

// Segment kinds.
const (
	SegmentBody     SegmentKind = "body"
	SegmentHeader   SegmentKind = "header"
	SegmentFooter   SegmentKind = "footer"
	SegmentFootnote SegmentKind = "footnote"
)

// Segment is one index space: the body, a header, a footer or a footnote.
type Segment struct {
	Kind           SegmentKind
	ID             string // segmentId for the API; "" for the body
	Number         int    // 1-based within its kind and tab
	FootnoteNumber string // visible number for footnotes, when known
	Prefix         string // handle prefix, e.g. "tab2/header1/"
	Tab            *Tab
	Blocks         []*Block

	all []*Block // Blocks flattened, filled by Parse
}

// Label names the segment for people: body, header1, footnote3.
func (s *Segment) Label() string {
	switch s.Kind {
	case SegmentBody:
		return "body"
	case SegmentFootnote:
		if s.FootnoteNumber != "" {
			return "footnote" + s.FootnoteNumber
		}
		return "footnote" + strconv.Itoa(s.Number)
	default:
		return string(s.Kind) + strconv.Itoa(s.Number)
	}
}

// AllBlocks lists the segment's blocks in document order, descending
// into table cells and tables of contents. Parsed segments share one
// slice across calls; callers must not modify it.
func (s *Segment) AllBlocks() []*Block {
	if s.all != nil {
		return s.all
	}
	return Flatten(s.Blocks)
}

// BlockAt finds the innermost paragraph covering an index of the
// segment, the enclosing top-level block when no paragraph does, or nil
// when the index lies outside the segment.
func (s *Segment) BlockAt(index int64) *Block { return blockAt(s.Blocks, index) }

func blockAt(blocks []*Block, index int64) *Block {
	i := sort.Search(len(blocks), func(i int) bool { return blocks[i].End > index })
	if i == len(blocks) || blocks[i].Start > index {
		return nil
	}
	b := blocks[i]
	var inner []*Block
	switch {
	case b.Table != nil:
		for _, row := range b.Table.Cells {
			for _, c := range row {
				if c.Start <= index && index < c.End {
					inner = c.Blocks
				}
			}
		}
	case b.TOC != nil:
		inner = b.TOC.Blocks
	}
	if p := blockAt(inner, index); p != nil && p.Paragraph != nil {
		return p
	}
	return b
}

// Flatten lists blocks in document order, descending into table cells
// and tables of contents.
func Flatten(blocks []*Block) []*Block {
	var out []*Block
	var walk func(bs []*Block)
	walk = func(bs []*Block) {
		for _, b := range bs {
			out = append(out, b)
			switch {
			case b.Table != nil:
				for _, row := range b.Table.Cells {
					for _, c := range row {
						walk(c.Blocks)
					}
				}
			case b.TOC != nil:
				walk(b.TOC.Blocks)
			}
		}
	}
	walk(blocks)
	return out
}

// ContentStart is the first index content occupies: after the leading
// section break of a body, 0 elsewhere.
func (s *Segment) ContentStart() int64 {
	for _, b := range s.Blocks {
		if b.Kind != KindSectionBreak {
			return b.Start
		}
	}
	return 0
}

// End is one past the segment's final newline.
func (s *Segment) End() int64 {
	if n := len(s.Blocks); n > 0 {
		return s.Blocks[n-1].End
	}
	return 0
}

// ContentBlocks lists the top-level blocks that carry content (no
// section breaks).
func (s *Segment) ContentBlocks() []*Block {
	out := make([]*Block, 0, len(s.Blocks))
	for _, b := range s.Blocks {
		if b.Kind != KindSectionBreak {
			out = append(out, b)
		}
	}
	return out
}

// IsBlankParagraph reports whether the block is a paragraph holding
// nothing but whitespace: empty, or holding only spaces, as a footnote
// Google has just created does. Content written there fills it, which
// removes that whitespace, so a paragraph holding an image, a footnote
// reference, a chip, an equation or a break is never blank however
// little text it shows.
func (b *Block) IsBlankParagraph() bool {
	if b.Paragraph == nil {
		return false
	}
	for _, r := range b.Paragraph.Runs {
		if r.Kind != RunText {
			return false
		}
	}
	return strings.TrimSpace(b.Paragraph.Text(ViewInline)) == ""
}

// BlockKind is the structural element type.
type BlockKind string

// Block kinds.
const (
	KindParagraph    BlockKind = "paragraph"
	KindTable        BlockKind = "table"
	KindSectionBreak BlockKind = "section_break"
	KindTOC          BlockKind = "toc"
)

// Block is one structural element with its UTF-16 range.
type Block struct {
	Kind    BlockKind
	Handle  string
	Start   int64
	End     int64
	Segment *Segment
	Cell    *Cell // enclosing table cell, nil at top level

	Paragraph    *Paragraph
	Table        *Table
	TOC          *TOC
	SectionBreak *SectionBreakInfo

	Inserted []string // suggestion ids that insert this block
	Deleted  []string // suggestion ids that delete this block

	// Wire is the structural element the block was parsed from, for the
	// raw read format.
	Wire *gdocs.StructuralElement
}

// IsHeading reports whether the block is a heading paragraph.
func (b *Block) IsHeading() bool {
	return b.Paragraph != nil && b.Paragraph.Level > 0
}

// Paragraph is a paragraph's style, bullet and runs.
type Paragraph struct {
	NamedStyle string
	HeadingID  string
	Level      int // 1..6 for HEADING_n, 0 otherwise
	IsTitle    bool
	IsSubtitle bool
	// ParagraphStyle is what this paragraph carries itself; where it
	// says nothing, the tab's definition of NamedStyle applies.
	ParagraphStyle
	Bullet              *BulletInfo
	Runs                []*Run
	PositionedObjectIDs []string
}

// BulletInfo is list membership.
type BulletInfo struct {
	ListID  string
	Nesting int
	Ordered bool
	Glyph   string
	// Number is the item's 1-based position among the preceding siblings
	// of the same list and level, restarting after any interruption.
	Number int
}

// RunKind is the paragraph element type.
type RunKind string

// Run kinds.
const (
	RunText           RunKind = "text"
	RunInlineObject   RunKind = "inline_object"
	RunFootnoteRef    RunKind = "footnote_ref"
	RunPageBreak      RunKind = "page_break"
	RunColumnBreak    RunKind = "column_break"
	RunHorizontalRule RunKind = "horizontal_rule"
	RunPerson         RunKind = "person"
	RunRichLink       RunKind = "rich_link"
	RunDate           RunKind = "date"
	RunEquation       RunKind = "equation"
	RunAutoText       RunKind = "auto_text"
)

// Run is one paragraph element.
type Run struct {
	Kind  RunKind
	Start int64
	End   int64
	Text  string // text content; display text for chips
	Style TextStyle

	ObjectID       string
	FootnoteID     string
	FootnoteNumber string
	PersonName     string
	PersonEmail    string
	LinkTitle      string
	LinkURI        string
	AutoTextType   string

	Inserted []string
	Deleted  []string
}

// IsSuggestedInsertion reports whether the run exists only as a suggestion.
func (r *Run) IsSuggestedInsertion() bool { return len(r.Inserted) > 0 }

// IsSuggestedDeletion reports whether a suggestion removes the run.
func (r *Run) IsSuggestedDeletion() bool { return len(r.Deleted) > 0 }

// TextStyle is the subset of character formatting we render and compare.
// All fields are comparable so styles can be tested with ==.
type TextStyle struct {
	Bold          bool
	Italic        bool
	Underline     bool
	Strikethrough bool
	SmallCaps     bool
	Baseline      string // SUPERSCRIPT, SUBSCRIPT or ""
	FontFamily    string
	FontSizePt    float64
	Foreground    string // #rrggbb or ""
	Background    string
	LinkURL       string
	LinkHeadingID string
	LinkBookmark  string
	LinkTabID     string
}

// Monospace reports whether the font is a code font.
func (s TextStyle) Monospace() bool {
	switch s.FontFamily {
	case "Courier New", "Courier", "Consolas", "Roboto Mono", "Source Code Pro", "Fira Code", "JetBrains Mono", "Menlo", "Monaco", "Ubuntu Mono", "Inconsolata":
		return true
	}
	return false
}

// HasLink reports whether the style carries any link.
func (s TextStyle) HasLink() bool {
	return s.LinkURL != "" || s.LinkHeadingID != "" || s.LinkBookmark != ""
}

// Table is a grid of cells.
type Table struct {
	Handle string
	Rows   int
	Cols   int
	Cells  [][]*Cell
}

// Cell is one table cell with its nested blocks. A cell covered by
// another cell's span stays in the grid (the API keeps it, empty) and
// points at the head cell through MergedInto.
type Cell struct {
	Table      *Table
	Row        int // 1-based
	Col        int // 1-based
	Handle     string
	Start      int64
	End        int64
	RowSpan    int
	ColSpan    int
	Blocks     []*Block
	MergedInto *Cell
}

// TOC is a table of contents; read-only in the API.
type TOC struct {
	Blocks []*Block
}

// SectionBreakInfo is the part of a section break we surface.
type SectionBreakInfo struct {
	Type            string
	DefaultHeaderID string
	DefaultFooterID string
}

// AllBlocks lists every block of every tab and segment in document order.
func (d *Document) AllBlocks() []*Block {
	var out []*Block
	for _, t := range d.Tabs {
		for _, s := range t.Segments() {
			out = append(out, s.AllBlocks()...)
		}
	}
	return out
}

// Tab finds a tab by id, then by title (case-insensitive), then by
// "tabN" or a bare number. The empty reference is the first tab.
func (d *Document) Tab(ref string) (*Tab, bool) {
	if len(d.Tabs) == 0 {
		return nil, false
	}
	if ref == "" {
		return d.Tabs[0], true
	}
	for _, t := range d.Tabs {
		if t.ID == ref {
			return t, true
		}
	}
	for _, t := range d.Tabs {
		if equalFold(t.Title, ref) {
			return t, true
		}
	}
	if n, ok := parseTabNumber(ref); ok && n >= 1 && n <= len(d.Tabs) {
		return d.Tabs[n-1], true
	}
	return nil, false
}

// FindHandle looks a handle up across every tab and segment.
func (d *Document) FindHandle(handle string) (*Block, bool) {
	if d.byHandle != nil {
		b, ok := d.byHandle[handle]
		return b, ok
	}
	for _, b := range d.AllBlocks() {
		if b.Handle == handle {
			return b, true
		}
	}
	return nil, false
}

// FindCell looks a cell handle (tbl1:r2c3) up across the document.
func (d *Document) FindCell(handle string) (*Cell, bool) {
	if d.byCell != nil {
		c, ok := d.byCell[handle]
		return c, ok
	}
	for _, b := range d.AllBlocks() {
		if b.Table == nil {
			continue
		}
		for _, row := range b.Table.Cells {
			for _, c := range row {
				if c.Handle == handle {
					return c, true
				}
			}
		}
	}
	return nil, false
}

// Covered reports whether another cell's span hides this one.
func (c *Cell) Covered() bool { return c.MergedInto != nil }

// ContentEnd is the index before the cell's final newline, the last
// position text can occupy.
func (c *Cell) ContentEnd() int64 {
	if n := len(c.Blocks); n > 0 {
		return c.Blocks[n-1].End - 1
	}
	return c.End - 1
}
