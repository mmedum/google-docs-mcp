// Package gdocs defines the Docs API v1 wire types this server reads.
// They mirror the JSON schema of documents.get for the fields we use,
// which keeps the binary free of the generated client's gRPC and
// telemetry dependency tree. Field names follow Go conventions; JSON
// tags follow the API.
package gdocs

import "encoding/json"

// Document is the documents.get response.
type Document struct {
	DocumentID          string                      `json:"documentId,omitempty"`
	Title               string                      `json:"title,omitempty"`
	RevisionID          string                      `json:"revisionId,omitempty"`
	SuggestionsViewMode string                      `json:"suggestionsViewMode,omitempty"`
	Body                *Body                       `json:"body,omitempty"`
	Headers             map[string]Header           `json:"headers,omitempty"`
	Footers             map[string]Footer           `json:"footers,omitempty"`
	Footnotes           map[string]Footnote         `json:"footnotes,omitempty"`
	Lists               map[string]List             `json:"lists,omitempty"`
	InlineObjects       map[string]InlineObject     `json:"inlineObjects,omitempty"`
	PositionedObjects   map[string]PositionedObject `json:"positionedObjects,omitempty"`
	NamedRanges         map[string]NamedRanges      `json:"namedRanges,omitempty"`
	DocumentStyle       *DocumentStyle              `json:"documentStyle,omitempty"`
	NamedStyles         *NamedStyles                `json:"namedStyles,omitempty"`
	Tabs                []*Tab                      `json:"tabs,omitempty"`
	// Pending suggestions to the document-wide styles.
	SuggestedDocumentStyleChanges map[string]SuggestedDocumentStyle `json:"suggestedDocumentStyleChanges,omitempty"`
	SuggestedNamedStylesChanges   map[string]SuggestedNamedStyles   `json:"suggestedNamedStylesChanges,omitempty"`
	// Developer Preview fields, populated when commentsViewMode asks for them.
	Comments    []CommentThread    `json:"comments,omitempty"`
	Suggestions []SuggestionThread `json:"suggestions,omitempty"`
}

// Post is a comment or suggestion post (Developer Preview).
type Post struct {
	PostID           string     `json:"postId,omitempty"`
	Content          string     `json:"content,omitempty"`
	Author           PostAuthor `json:"author,omitempty"`
	CreateTime       string     `json:"createTime,omitempty"`
	UpdateTime       string     `json:"updateTime,omitempty"`
	CommentAction    string     `json:"commentAction,omitempty"`
	SuggestionAction string     `json:"suggestionAction,omitempty"`
}

// PostAuthor identifies who wrote a post.
type PostAuthor struct {
	DisplayName string `json:"displayName,omitempty"`
	Me          bool   `json:"me,omitempty"`
	User        string `json:"user,omitempty"`
}

// CommentThread is the shape observed live: no range, but the quoted text.
type CommentThread struct {
	CommentID      string `json:"commentId,omitempty"`
	AnchorID       string `json:"anchorId,omitempty"`
	HeadPost       Post   `json:"headPost,omitempty"`
	Replies        []Post `json:"replies,omitempty"`
	Status         string `json:"status,omitempty"`
	PlainTextQuote string `json:"plainTextQuote,omitempty"`
}

// SuggestionThread is the shape observed live: id, head post, status, summary.
type SuggestionThread struct {
	SuggestionID string `json:"suggestionId,omitempty"`
	HeadPost     Post   `json:"headPost,omitempty"`
	Replies      []Post `json:"replies,omitempty"`
	Status       string `json:"status,omitempty"`
	SummaryText  string `json:"summaryText,omitempty"`
}

// Tab is one tab with its content and children.
type Tab struct {
	TabProperties *TabProperties `json:"tabProperties,omitempty"`
	DocumentTab   *DocumentTab   `json:"documentTab,omitempty"`
	ChildTabs     []*Tab         `json:"childTabs,omitempty"`
}

// WalkTabs visits tabs in document order, parents before children,
// stopping when fn returns false.
func WalkTabs(tabs []*Tab, fn func(*Tab) bool) bool {
	for _, t := range tabs {
		if !fn(t) || !WalkTabs(t.ChildTabs, fn) {
			return false
		}
	}
	return true
}

// DocumentTabs flattens a response's tabs into their content, parents
// before children, skipping the tabs that carry none. Every caller that
// wants a tab's id-keyed collections wants this rather than WalkTabs.
func DocumentTabs(d *Document) []*DocumentTab {
	if d == nil {
		return nil
	}
	if len(d.Tabs) == 0 {
		return []*DocumentTab{LegacyTab(d)}
	}
	var out []*DocumentTab
	WalkTabs(d.Tabs, func(t *Tab) bool {
		if t.DocumentTab != nil {
			out = append(out, t.DocumentTab)
		}
		return true
	})
	return out
}

// LegacyTab is a response without tabs content read as the single tab it
// describes: the Docs API carries the same collections on the document
// itself when the caller did not ask for tabs.
//
// One list, because there were two and they disagreed. Parse built this
// shape for its own use and left out the two suggested-style maps, while
// the suggestion reader built it again and left out everything else — so
// a collection Google adds to a tab had to be remembered in two places,
// and forgetting one is exactly the "the read says no suggestion exists"
// bug that sent anyone looking here in the first place.
func LegacyTab(d *Document) *DocumentTab {
	if d == nil {
		return nil
	}
	return &DocumentTab{
		Body: d.Body, Headers: d.Headers, Footers: d.Footers, Footnotes: d.Footnotes,
		Lists: d.Lists, InlineObjects: d.InlineObjects, PositionedObjects: d.PositionedObjects,
		NamedRanges: d.NamedRanges, DocumentStyle: d.DocumentStyle, NamedStyles: d.NamedStyles,
		SuggestedDocumentStyleChanges: d.SuggestedDocumentStyleChanges,
		SuggestedNamedStylesChanges:   d.SuggestedNamedStylesChanges,
	}
}

// TabProperties identify and place a tab.
type TabProperties struct {
	TabID        string `json:"tabId,omitempty"`
	Title        string `json:"title,omitempty"`
	Index        int64  `json:"index,omitempty"`
	ParentTabID  string `json:"parentTabId,omitempty"`
	NestingLevel int64  `json:"nestingLevel,omitempty"`
	IconEmoji    string `json:"iconEmoji,omitempty"`
}

// DocumentTab is a tab's content.
type DocumentTab struct {
	Body          *Body                   `json:"body,omitempty"`
	Headers       map[string]Header       `json:"headers,omitempty"`
	Footers       map[string]Footer       `json:"footers,omitempty"`
	Footnotes     map[string]Footnote     `json:"footnotes,omitempty"`
	Lists         map[string]List         `json:"lists,omitempty"`
	InlineObjects map[string]InlineObject `json:"inlineObjects,omitempty"`
	// PositionedObjects are floating images, keyed by object id; unlike
	// an inline object they sit on a paragraph rather than in its text.
	PositionedObjects map[string]PositionedObject `json:"positionedObjects,omitempty"`
	// NamedRanges are the tab's named ranges, keyed by name; one name can
	// cover several ranges.
	NamedRanges   map[string]NamedRanges `json:"namedRanges,omitempty"`
	DocumentStyle *DocumentStyle         `json:"documentStyle,omitempty"`
	// NamedStyles are the definitions every paragraph inherits from; a
	// paragraph's namedStyleType names the one it uses.
	NamedStyles *NamedStyles `json:"namedStyles,omitempty"`
	// CommentAnchors map anchor ids to ranges (Developer Preview, with
	// commentsViewMode).
	CommentAnchors map[string]CommentAnchor `json:"commentAnchors,omitempty"`
	// Pending suggestions to the document-wide styles.
	SuggestedDocumentStyleChanges map[string]SuggestedDocumentStyle `json:"suggestedDocumentStyleChanges,omitempty"`
	SuggestedNamedStylesChanges   map[string]SuggestedNamedStyles   `json:"suggestedNamedStylesChanges,omitempty"`
}

// UnmarshalJSON decodes the element and keeps the bytes it came from.
// The alias is what stops it recursing into itself.
func (s *StructuralElement) UnmarshalJSON(data []byte) error {
	type plain StructuralElement
	if err := json.Unmarshal(data, (*plain)(s)); err != nil {
		return err
	}
	// Copied, not aliased: json.Unmarshal makes no promise about the
	// lifetime of what it hands an UnmarshalJSON method.
	s.raw = append(json.RawMessage(nil), data...)
	return nil
}

// RawJSON is the element exactly as the API sent it, or nil for an
// element that came from somewhere other than a response — a fixture
// built in Go, or one this server constructed.
func (s *StructuralElement) RawJSON() json.RawMessage {
	if s == nil {
		return nil
	}
	return s.raw
}

// CommentAnchor is where a comment thread is pinned (Developer Preview).
type CommentAnchor struct {
	AnchorID string   `json:"anchorId,omitempty"`
	Ranges   []*Range `json:"ranges,omitempty"`
}

// PositionedObject is a floating image anchored to a paragraph.
type PositionedObject struct {
	ObjectID                   string                      `json:"objectId,omitempty"`
	PositionedObjectProperties *PositionedObjectProperties `json:"positionedObjectProperties,omitempty"`

	SuggestedInsertionID                       string                                         `json:"suggestedInsertionId,omitempty"`
	SuggestedDeletionIDs                       []string                                       `json:"suggestedDeletionIds,omitempty"`
	SuggestedPositionedObjectPropertiesChanges map[string]SuggestedPositionedObjectProperties `json:"suggestedPositionedObjectPropertiesChanges,omitempty"`
}

// PositionedObjectProperties wrap the embedded object and its placement.
type PositionedObjectProperties struct {
	EmbeddedObject *EmbeddedObject `json:"embeddedObject,omitempty"`
}

// Embedded returns the object, or nil when the properties are absent.
func (p *PositionedObjectProperties) Embedded() *EmbeddedObject {
	if p == nil {
		return nil
	}
	return p.EmbeddedObject
}

// NamedRanges is every range sharing one name.
type NamedRanges struct {
	Name        string       `json:"name,omitempty"`
	NamedRanges []NamedRange `json:"namedRanges,omitempty"`
}

// NamedRange is one named span, which may cover several ranges.
type NamedRange struct {
	NamedRangeID string   `json:"namedRangeId,omitempty"`
	Name         string   `json:"name,omitempty"`
	Ranges       []*Range `json:"ranges,omitempty"`
}

// NamedStyles holds a tab's named style definitions.
type NamedStyles struct {
	Styles []*NamedStyle `json:"styles,omitempty"`
}

// NamedStyle is one named style definition: the text and paragraph
// formatting every paragraph carrying that style inherits.
type NamedStyle struct {
	NamedStyleType string          `json:"namedStyleType,omitempty"`
	TextStyle      *TextStyle      `json:"textStyle,omitempty"`
	ParagraphStyle *ParagraphStyle `json:"paragraphStyle,omitempty"`
}

// DocumentStyle is a tab's page setup.
type DocumentStyle struct {
	Background                *Background `json:"background,omitempty"`
	PageSize                  *Size       `json:"pageSize,omitempty"`
	MarginTop                 *Dimension  `json:"marginTop,omitempty"`
	MarginBottom              *Dimension  `json:"marginBottom,omitempty"`
	MarginLeft                *Dimension  `json:"marginLeft,omitempty"`
	MarginRight               *Dimension  `json:"marginRight,omitempty"`
	MarginHeader              *Dimension  `json:"marginHeader,omitempty"`
	MarginFooter              *Dimension  `json:"marginFooter,omitempty"`
	PageNumberStart           int64       `json:"pageNumberStart,omitempty"`
	FlipPageOrientation       bool        `json:"flipPageOrientation,omitempty"`
	UseFirstPageHeaderFooter  bool        `json:"useFirstPageHeaderFooter,omitempty"`
	UseEvenPageHeaderFooter   bool        `json:"useEvenPageHeaderFooter,omitempty"`
	UseCustomHeaderFooterMgns bool        `json:"useCustomHeaderFooterMargins,omitempty"`
}

// Background is a solid page colour.
type Background struct {
	Color *OptionalColor `json:"color,omitempty"`
}

// Range is a half-open UTF-16 range in one segment of one tab.
type Range struct {
	SegmentID  string `json:"segmentId,omitempty"`
	StartIndex int64  `json:"startIndex,omitempty"`
	EndIndex   int64  `json:"endIndex,omitempty"`
	TabID      string `json:"tabId,omitempty"`
}

// Body is the main content segment.
type Body struct {
	Content []*StructuralElement `json:"content,omitempty"`
}

// Header is a header segment.
type Header struct {
	HeaderID string               `json:"headerId,omitempty"`
	Content  []*StructuralElement `json:"content,omitempty"`
}

// Footer is a footer segment.
type Footer struct {
	FooterID string               `json:"footerId,omitempty"`
	Content  []*StructuralElement `json:"content,omitempty"`
}

// Footnote is a footnote segment.
type Footnote struct {
	FootnoteID string               `json:"footnoteId,omitempty"`
	Content    []*StructuralElement `json:"content,omitempty"`
}

// Suggested carries the suggestion ids most elements can have.
type Suggested struct {
	SuggestedInsertionIDs []string `json:"suggestedInsertionIds,omitempty"`
	SuggestedDeletionIDs  []string `json:"suggestedDeletionIds,omitempty"`
}

// SuggestedStyle carries the suggested text-style changes every inline
// element can have, keyed by suggestion id. It is separate from
// Suggested because the block-level elements that embed Suggested — a
// table, a section break, an equation — have insertion and deletion ids
// but no text style of their own to change.
type SuggestedStyle struct {
	SuggestedTextStyleChanges map[string]SuggestedTextStyle `json:"suggestedTextStyleChanges,omitempty"`
}

// StructuralElement is one block of a segment.
//
// It keeps the bytes it was decoded from, because read_document's
// format: raw promises "the Docs API JSON" and a round trip through
// these types can only ever return the fields these types model. That
// was not a theoretical gap: it is how #46 came to be filed as a silent
// write failure, when the write had worked and the raw read was dropping
// the field that proved it. Nine types still model fewer fields than the
// API publishes; with the bytes kept, format: raw stops caring.
type StructuralElement struct {
	StartIndex      int64            `json:"startIndex,omitempty"`
	EndIndex        int64            `json:"endIndex,omitempty"`
	Paragraph       *Paragraph       `json:"paragraph,omitempty"`
	Table           *Table           `json:"table,omitempty"`
	SectionBreak    *SectionBreak    `json:"sectionBreak,omitempty"`
	TableOfContents *TableOfContents `json:"tableOfContents,omitempty"`

	// raw is the element as it arrived; see the type comment.
	raw json.RawMessage
}

// Paragraph is a paragraph block.
type Paragraph struct {
	Elements            []*ParagraphElement `json:"elements,omitempty"`
	ParagraphStyle      *ParagraphStyle     `json:"paragraphStyle,omitempty"`
	Bullet              *Bullet             `json:"bullet,omitempty"`
	PositionedObjectIDs []string            `json:"positionedObjectIds,omitempty"`
	// The pending suggestions on the paragraph itself, keyed by
	// suggestion id: its style, its list membership, and the floating
	// objects a suggestion would anchor to it.
	SuggestedParagraphStyleChanges map[string]SuggestedParagraphStyle `json:"suggestedParagraphStyleChanges,omitempty"`
	SuggestedBulletChanges         map[string]SuggestedBullet         `json:"suggestedBulletChanges,omitempty"`
	SuggestedPositionedObjectIDs   map[string]ObjectReferences        `json:"suggestedPositionedObjectIds,omitempty"`
}

// ParagraphStyle is paragraph formatting. Every field the API accepts on
// a write is here; tabStops and headingId are read-only per the
// discovery document, so they are read and never sent.
type ParagraphStyle struct {
	NamedStyleType      string           `json:"namedStyleType,omitempty"`
	HeadingID           string           `json:"headingId,omitempty"`
	Alignment           string           `json:"alignment,omitempty"`
	Direction           string           `json:"direction,omitempty"`
	SpacingMode         string           `json:"spacingMode,omitempty"`
	IndentStart         *Dimension       `json:"indentStart,omitempty"`
	IndentEnd           *Dimension       `json:"indentEnd,omitempty"`
	IndentFirstLine     *Dimension       `json:"indentFirstLine,omitempty"`
	LineSpacing         float64          `json:"lineSpacing,omitempty"`
	SpaceAbove          *Dimension       `json:"spaceAbove,omitempty"`
	SpaceBelow          *Dimension       `json:"spaceBelow,omitempty"`
	KeepWithNext        bool             `json:"keepWithNext,omitempty"`
	KeepLinesTogether   bool             `json:"keepLinesTogether,omitempty"`
	AvoidWidowAndOrphan bool             `json:"avoidWidowAndOrphan,omitempty"`
	PageBreakBefore     bool             `json:"pageBreakBefore,omitempty"`
	Shading             *Shading         `json:"shading,omitempty"`
	BorderTop           *ParagraphBorder `json:"borderTop,omitempty"`
	BorderBottom        *ParagraphBorder `json:"borderBottom,omitempty"`
	BorderLeft          *ParagraphBorder `json:"borderLeft,omitempty"`
	BorderRight         *ParagraphBorder `json:"borderRight,omitempty"`
	BorderBetween       *ParagraphBorder `json:"borderBetween,omitempty"`
	TabStops            []*TabStop       `json:"tabStops,omitempty"`
}

// ParagraphBorder is one edge of a paragraph's box.
type ParagraphBorder struct {
	Color     *OptionalColor `json:"color,omitempty"`
	Width     *Dimension     `json:"width,omitempty"`
	Padding   *Dimension     `json:"padding,omitempty"`
	DashStyle string         `json:"dashStyle,omitempty"`
}

// Shading is a paragraph's background.
type Shading struct {
	BackgroundColor *OptionalColor `json:"backgroundColor,omitempty"`
}

// TabStop is one tab stop. The API reports these and refuses to set
// them: "This property is read-only" (discovery document).
type TabStop struct {
	Offset    *Dimension `json:"offset,omitempty"`
	Alignment string     `json:"alignment,omitempty"`
}

// Dimension is a magnitude with a unit (PT).
type Dimension struct {
	Magnitude float64 `json:"magnitude,omitempty"`
	Unit      string  `json:"unit,omitempty"`
}

// Bullet is list membership.
type Bullet struct {
	ListID       string     `json:"listId,omitempty"`
	NestingLevel int64      `json:"nestingLevel,omitempty"`
	TextStyle    *TextStyle `json:"textStyle,omitempty"`
}

// ParagraphElement is one inline element; exactly one kind is set.
type ParagraphElement struct {
	StartIndex          int64                `json:"startIndex,omitempty"`
	EndIndex            int64                `json:"endIndex,omitempty"`
	TextRun             *TextRun             `json:"textRun,omitempty"`
	InlineObjectElement *InlineObjectElement `json:"inlineObjectElement,omitempty"`
	FootnoteReference   *FootnoteReference   `json:"footnoteReference,omitempty"`
	PageBreak           *Break               `json:"pageBreak,omitempty"`
	ColumnBreak         *Break               `json:"columnBreak,omitempty"`
	HorizontalRule      *Break               `json:"horizontalRule,omitempty"`
	Person              *Person              `json:"person,omitempty"`
	RichLink            *RichLink            `json:"richLink,omitempty"`
	DateElement         *DateElement         `json:"dateElement,omitempty"`
	Equation            *Suggested           `json:"equation,omitempty"`
	AutoText            *AutoText            `json:"autoText,omitempty"`
}

// TextRun is styled text.
type TextRun struct {
	Suggested
	SuggestedStyle
	Content   string     `json:"content,omitempty"`
	TextStyle *TextStyle `json:"textStyle,omitempty"`
}

// TextStyle is character formatting.
type TextStyle struct {
	Bold               bool                `json:"bold,omitempty"`
	Italic             bool                `json:"italic,omitempty"`
	Underline          bool                `json:"underline,omitempty"`
	Strikethrough      bool                `json:"strikethrough,omitempty"`
	SmallCaps          bool                `json:"smallCaps,omitempty"`
	BaselineOffset     string              `json:"baselineOffset,omitempty"`
	FontSize           *Dimension          `json:"fontSize,omitempty"`
	WeightedFontFamily *WeightedFontFamily `json:"weightedFontFamily,omitempty"`
	ForegroundColor    *OptionalColor      `json:"foregroundColor,omitempty"`
	BackgroundColor    *OptionalColor      `json:"backgroundColor,omitempty"`
	Link               *Link               `json:"link,omitempty"`
}

// WeightedFontFamily is a font name and weight.
type WeightedFontFamily struct {
	FontFamily string `json:"fontFamily,omitempty"`
	Weight     int64  `json:"weight,omitempty"`
}

// OptionalColor wraps a colour that may be unset.
type OptionalColor struct {
	Color *Color `json:"color,omitempty"`
}

// Color is an RGB colour.
type Color struct {
	RgbColor *RgbColor `json:"rgbColor,omitempty"`
}

// RgbColor has components in [0, 1].
type RgbColor struct {
	Red   float64 `json:"red,omitempty"`
	Green float64 `json:"green,omitempty"`
	Blue  float64 `json:"blue,omitempty"`
}

// Link is a hyperlink target.
type Link struct {
	URL        string       `json:"url,omitempty"`
	HeadingID  string       `json:"headingId,omitempty"`
	BookmarkID string       `json:"bookmarkId,omitempty"`
	TabID      string       `json:"tabId,omitempty"`
	Heading    *HeadingLink `json:"heading,omitempty"`
}

// HeadingLink targets a heading in a tab.
type HeadingLink struct {
	ID    string `json:"id,omitempty"`
	TabID string `json:"tabId,omitempty"`
}

// InlineObjectElement references an inline object.
type InlineObjectElement struct {
	Suggested
	SuggestedStyle
	InlineObjectID string     `json:"inlineObjectId,omitempty"`
	TextStyle      *TextStyle `json:"textStyle,omitempty"`
}

// FootnoteReference marks a footnote in the body.
type FootnoteReference struct {
	Suggested
	SuggestedStyle
	FootnoteID     string     `json:"footnoteId,omitempty"`
	FootnoteNumber string     `json:"footnoteNumber,omitempty"`
	TextStyle      *TextStyle `json:"textStyle,omitempty"`
}

// Break is a page break, column break or horizontal rule.
type Break struct {
	Suggested
	SuggestedStyle
	TextStyle *TextStyle `json:"textStyle,omitempty"`
}

// Person is a people chip.
type Person struct {
	Suggested
	SuggestedStyle
	PersonID         string            `json:"personId,omitempty"`
	PersonProperties *PersonProperties `json:"personProperties,omitempty"`
	TextStyle        *TextStyle        `json:"textStyle,omitempty"`
}

// PersonProperties name the person.
type PersonProperties struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

// RichLink is a smart chip linking to a Drive file or URL.
type RichLink struct {
	Suggested
	SuggestedStyle
	RichLinkID         string              `json:"richLinkId,omitempty"`
	RichLinkProperties *RichLinkProperties `json:"richLinkProperties,omitempty"`
	TextStyle          *TextStyle          `json:"textStyle,omitempty"`
}

// RichLinkProperties describe the link target.
type RichLinkProperties struct {
	Title    string `json:"title,omitempty"`
	URI      string `json:"uri,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// DateElement is a date chip.
type DateElement struct {
	Suggested
	SuggestedStyle
	DateID                string                 `json:"dateId,omitempty"`
	DateElementProperties *DateElementProperties `json:"dateElementProperties,omitempty"`
	TextStyle             *TextStyle             `json:"textStyle,omitempty"`

	SuggestedDateElementPropertiesChanges map[string]SuggestedDateElementProperties `json:"suggestedDateElementPropertiesChanges,omitempty"`
}

// DateElementProperties describe the date chip.
type DateElementProperties struct {
	DisplayText string `json:"displayText,omitempty"`
	Timestamp   string `json:"timestamp,omitempty"`
	DateFormat  string `json:"dateFormat,omitempty"`
	TimeFormat  string `json:"timeFormat,omitempty"`
	Locale      string `json:"locale,omitempty"`
	TimeZoneID  string `json:"timeZoneId,omitempty"`
}

// AutoText is generated text such as a page number.
type AutoText struct {
	Suggested
	SuggestedStyle
	Type      string     `json:"type,omitempty"`
	TextStyle *TextStyle `json:"textStyle,omitempty"`
}

// Table is a table block.
type Table struct {
	Suggested
	Rows      int64       `json:"rows,omitempty"`
	Columns   int64       `json:"columns,omitempty"`
	TableRows []*TableRow `json:"tableRows,omitempty"`
	// TableStyle holds the column widths edit_table's style_columns op
	// sets through updateTableColumnProperties; this is where a read
	// finds them again.
	TableStyle *TableStyle `json:"tableStyle,omitempty"`
}

// TableStyle is a table's column properties.
type TableStyle struct {
	TableColumnProperties []TableColumnProperties `json:"tableColumnProperties,omitempty"`
}

// TableColumnProperties is one column's width and how it was set.
type TableColumnProperties struct {
	WidthType string     `json:"widthType,omitempty"`
	Width     *Dimension `json:"width,omitempty"`
}

// TableRow is one row.
type TableRow struct {
	Suggested
	StartIndex int64        `json:"startIndex,omitempty"`
	EndIndex   int64        `json:"endIndex,omitempty"`
	TableCells []*TableCell `json:"tableCells,omitempty"`
	// TableRowStyle carries the pinned-header flag pin_header_rows sets
	// and the minimum height style_rows sets. The suggestion map beside
	// it arrived first, which left this type able to report a suggested
	// change to a row height it could not report the height of.
	TableRowStyle *TableRowStyle `json:"tableRowStyle,omitempty"`

	SuggestedTableRowStyleChanges map[string]SuggestedTableRowStyle `json:"suggestedTableRowStyleChanges,omitempty"`
}

// TableCell is one cell with nested content.
type TableCell struct {
	Suggested
	StartIndex     int64                `json:"startIndex,omitempty"`
	EndIndex       int64                `json:"endIndex,omitempty"`
	Content        []*StructuralElement `json:"content,omitempty"`
	TableCellStyle *TableCellStyle      `json:"tableCellStyle,omitempty"`

	SuggestedTableCellStyleChanges map[string]SuggestedTableCellStyle `json:"suggestedTableCellStyleChanges,omitempty"`
}

// TableCellStyle is the subset of cell formatting we read.
type TableCellStyle struct {
	// RowSpan and ColumnSpan are read-only (discovery document); a merge
	// is what changes them.
	RowSpan          int64            `json:"rowSpan,omitempty"`
	ColumnSpan       int64            `json:"columnSpan,omitempty"`
	BackgroundColor  *OptionalColor   `json:"backgroundColor,omitempty"`
	ContentAlignment string           `json:"contentAlignment,omitempty"`
	PaddingTop       *Dimension       `json:"paddingTop,omitempty"`
	PaddingBottom    *Dimension       `json:"paddingBottom,omitempty"`
	PaddingLeft      *Dimension       `json:"paddingLeft,omitempty"`
	PaddingRight     *Dimension       `json:"paddingRight,omitempty"`
	BorderTop        *TableCellBorder `json:"borderTop,omitempty"`
	BorderBottom     *TableCellBorder `json:"borderBottom,omitempty"`
	BorderLeft       *TableCellBorder `json:"borderLeft,omitempty"`
	BorderRight      *TableCellBorder `json:"borderRight,omitempty"`
}

// TableCellBorder is one edge of a cell. Unlike a paragraph border it
// has no padding: the cell's own padding covers that.
type TableCellBorder struct {
	Color     *OptionalColor `json:"color,omitempty"`
	Width     *Dimension     `json:"width,omitempty"`
	DashStyle string         `json:"dashStyle,omitempty"`
}

// SectionBreak starts a section.
type SectionBreak struct {
	Suggested
	SectionStyle *SectionStyle `json:"sectionStyle,omitempty"`
}

// SectionStyle is the subset of section formatting we read.
type SectionStyle struct {
	SectionType       string `json:"sectionType,omitempty"`
	DefaultHeaderID   string `json:"defaultHeaderId,omitempty"`
	DefaultFooterID   string `json:"defaultFooterId,omitempty"`
	FirstPageHeaderID string `json:"firstPageHeaderId,omitempty"`
	FirstPageFooterID string `json:"firstPageFooterId,omitempty"`
	EvenPageHeaderID  string `json:"evenPageHeaderId,omitempty"`
	EvenPageFooterID  string `json:"evenPageFooterId,omitempty"`
	// Everything layout_document's section_style op writes. It wrote all
	// of it and could read none of it back until the api-fields gate
	// asked why, which is the asymmetry that gate exists to find: a
	// person could set a section's margins and no read would show them.
	ColumnProperties         []SectionColumnProperties `json:"columnProperties,omitempty"`
	ColumnSeparatorStyle     string                    `json:"columnSeparatorStyle,omitempty"`
	ContentDirection         string                    `json:"contentDirection,omitempty"`
	MarginTop                *Dimension                `json:"marginTop,omitempty"`
	MarginBottom             *Dimension                `json:"marginBottom,omitempty"`
	MarginLeft               *Dimension                `json:"marginLeft,omitempty"`
	MarginRight              *Dimension                `json:"marginRight,omitempty"`
	MarginHeader             *Dimension                `json:"marginHeader,omitempty"`
	MarginFooter             *Dimension                `json:"marginFooter,omitempty"`
	PageNumberStart          int64                     `json:"pageNumberStart,omitempty"`
	FlipPageOrientation      bool                      `json:"flipPageOrientation,omitempty"`
	UseFirstPageHeaderFooter bool                      `json:"useFirstPageHeaderFooter,omitempty"`
}

// SectionColumnProperties is one column of a multi-column section.
type SectionColumnProperties struct {
	Width   *Dimension `json:"width,omitempty"`
	Padding *Dimension `json:"paddingEnd,omitempty"`
}

// TableOfContents is a generated, read-only block.
type TableOfContents struct {
	Suggested
	Content []*StructuralElement `json:"content,omitempty"`
}

// List describes a list's nesting levels.
type List struct {
	ListProperties *ListProperties `json:"listProperties,omitempty"`
	// A list, an inline object and a positioned object carry one
	// suggestedInsertionId rather than a list of them: the thing either
	// came in with a suggestion or it did not.
	SuggestedInsertionID           string                             `json:"suggestedInsertionId,omitempty"`
	SuggestedDeletionIDs           []string                           `json:"suggestedDeletionIds,omitempty"`
	SuggestedListPropertiesChanges map[string]SuggestedListProperties `json:"suggestedListPropertiesChanges,omitempty"`
}

// ListProperties hold the nesting levels.
type ListProperties struct {
	NestingLevels []*NestingLevel `json:"nestingLevels,omitempty"`
}

// NestingLevel is one list level's glyph.
type NestingLevel struct {
	GlyphType   string `json:"glyphType,omitempty"`
	GlyphSymbol string `json:"glyphSymbol,omitempty"`
	GlyphFormat string `json:"glyphFormat,omitempty"`
	StartNumber int64  `json:"startNumber,omitempty"`
}

// InlineObject is an embedded image, drawing or chart.
type InlineObject struct {
	ObjectID               string                  `json:"objectId,omitempty"`
	InlineObjectProperties *InlineObjectProperties `json:"inlineObjectProperties,omitempty"`

	SuggestedInsertionID                   string                                     `json:"suggestedInsertionId,omitempty"`
	SuggestedDeletionIDs                   []string                                   `json:"suggestedDeletionIds,omitempty"`
	SuggestedInlineObjectPropertiesChanges map[string]SuggestedInlineObjectProperties `json:"suggestedInlineObjectPropertiesChanges,omitempty"`
}

// InlineObjectProperties wrap the embedded object.
type InlineObjectProperties struct {
	EmbeddedObject *EmbeddedObject `json:"embeddedObject,omitempty"`
}

// Embedded returns the object, or nil when the properties are absent.
func (p *InlineObjectProperties) Embedded() *EmbeddedObject {
	if p == nil {
		return nil
	}
	return p.EmbeddedObject
}

// EmbeddedObject describes the object.
type EmbeddedObject struct {
	Title                     string                  `json:"title,omitempty"`
	Description               string                  `json:"description,omitempty"`
	ImageProperties           *ImageProperties        `json:"imageProperties,omitempty"`
	EmbeddedDrawingProperties *struct{}               `json:"embeddedDrawingProperties,omitempty"`
	LinkedContentReference    *LinkedContentReference `json:"linkedContentReference,omitempty"`
	Size                      *Size                   `json:"size,omitempty"`
}

// ImageProperties carry image URIs.
type ImageProperties struct {
	ContentURI string `json:"contentUri,omitempty"`
	SourceURI  string `json:"sourceUri,omitempty"`
}

// LinkedContentReference marks a linked chart.
type LinkedContentReference struct {
	SheetsChartReference *struct {
		SpreadsheetID string `json:"spreadsheetId,omitempty"`
		ChartID       int64  `json:"chartId,omitempty"`
	} `json:"sheetsChartReference,omitempty"`
}

// Size is width and height.
type Size struct {
	Width  *Dimension `json:"width,omitempty"`
	Height *Dimension `json:"height,omitempty"`
}
