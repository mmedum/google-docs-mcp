// Package doc holds the parsed document model. It turns the Docs API's
// index-addressed JSON into a tree of tabs, segments, blocks and runs
// that carries the original UTF-16 indices, assigns handles the model
// can name, and derives sections from headings. Nothing here talks to
// the network.
package doc

// Document is a parsed documents.get response.
type Document struct {
	ID                  string
	Title               string
	RevisionID          string
	SuggestionsViewMode string
	// Tabs in document order, parents before children.
	Tabs []*Tab
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

// Prefix is the handle prefix for this tab ("" for the first tab).
func (t *Tab) Prefix() string {
	if t.Number <= 1 {
		return ""
	}
	return "tab" + itoa(t.Number) + "/"
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
		return "footnote" + itoa(s.Number)
	default:
		return string(s.Kind) + itoa(s.Number)
	}
}

// AllBlocks flattens the segment in document order, descending into
// table cells and tables of contents.
func (s *Segment) AllBlocks() []*Block {
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
	walk(s.Blocks)
	return out
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
	Ordinal int
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
}

// IsHeading reports whether the block is a heading paragraph.
func (b *Block) IsHeading() bool {
	return b.Paragraph != nil && b.Paragraph.Level > 0
}

// Paragraph is a paragraph's style, bullet and runs.
type Paragraph struct {
	NamedStyle          string
	HeadingID           string
	Level               int // 1..6 for HEADING_n, 0 otherwise
	IsTitle             bool
	IsSubtitle          bool
	Alignment           string
	IndentStartPt       float64
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
	Handle   string
	Rows     int
	Cols     int
	Cells    [][]*Cell
	Inserted []string
	Deleted  []string
}

// Cell is one table cell with its nested blocks.
type Cell struct {
	Table   *Table
	Row     int // 1-based
	Col     int // 1-based
	Handle  string
	Start   int64
	End     int64
	RowSpan int
	ColSpan int
	Blocks  []*Block
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
	for _, t := range d.Tabs {
		for _, s := range t.Segments() {
			for _, b := range s.AllBlocks() {
				if b.Handle == handle {
					return b, true
				}
			}
		}
	}
	return nil, false
}

// FindCell looks a cell handle (tbl1:r2c3) up across the document.
func (d *Document) FindCell(handle string) (*Cell, bool) {
	for _, t := range d.Tabs {
		for _, s := range t.Segments() {
			for _, b := range s.AllBlocks() {
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
		}
	}
	return nil, false
}
