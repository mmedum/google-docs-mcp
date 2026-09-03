// Package gdocs defines the Docs API v1 wire types this server reads.
// They mirror the JSON schema of documents.get for the fields we use,
// which keeps the binary free of the generated client's gRPC and
// telemetry dependency tree. Field names follow Go conventions; JSON
// tags follow the API.
package gdocs

// Document is the documents.get response.
type Document struct {
	DocumentID          string                  `json:"documentId,omitempty"`
	Title               string                  `json:"title,omitempty"`
	RevisionID          string                  `json:"revisionId,omitempty"`
	SuggestionsViewMode string                  `json:"suggestionsViewMode,omitempty"`
	Body                *Body                   `json:"body,omitempty"`
	Headers             map[string]Header       `json:"headers,omitempty"`
	Footers             map[string]Footer       `json:"footers,omitempty"`
	Footnotes           map[string]Footnote     `json:"footnotes,omitempty"`
	Lists               map[string]List         `json:"lists,omitempty"`
	InlineObjects       map[string]InlineObject `json:"inlineObjects,omitempty"`
	Tabs                []*Tab                  `json:"tabs,omitempty"`
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

// StructuralElement is one block of a segment.
type StructuralElement struct {
	StartIndex      int64            `json:"startIndex,omitempty"`
	EndIndex        int64            `json:"endIndex,omitempty"`
	Paragraph       *Paragraph       `json:"paragraph,omitempty"`
	Table           *Table           `json:"table,omitempty"`
	SectionBreak    *SectionBreak    `json:"sectionBreak,omitempty"`
	TableOfContents *TableOfContents `json:"tableOfContents,omitempty"`
}

// Paragraph is a paragraph block.
type Paragraph struct {
	Elements            []*ParagraphElement `json:"elements,omitempty"`
	ParagraphStyle      *ParagraphStyle     `json:"paragraphStyle,omitempty"`
	Bullet              *Bullet             `json:"bullet,omitempty"`
	PositionedObjectIDs []string            `json:"positionedObjectIds,omitempty"`
}

// ParagraphStyle is the subset of paragraph formatting we read.
type ParagraphStyle struct {
	NamedStyleType  string     `json:"namedStyleType,omitempty"`
	HeadingID       string     `json:"headingId,omitempty"`
	Alignment       string     `json:"alignment,omitempty"`
	Direction       string     `json:"direction,omitempty"`
	IndentStart     *Dimension `json:"indentStart,omitempty"`
	IndentFirstLine *Dimension `json:"indentFirstLine,omitempty"`
	LineSpacing     float64    `json:"lineSpacing,omitempty"`
	SpaceAbove      *Dimension `json:"spaceAbove,omitempty"`
	SpaceBelow      *Dimension `json:"spaceBelow,omitempty"`
	KeepWithNext    bool       `json:"keepWithNext,omitempty"`
	PageBreakBefore bool       `json:"pageBreakBefore,omitempty"`
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
	InlineObjectID string     `json:"inlineObjectId,omitempty"`
	TextStyle      *TextStyle `json:"textStyle,omitempty"`
}

// FootnoteReference marks a footnote in the body.
type FootnoteReference struct {
	Suggested
	FootnoteID     string     `json:"footnoteId,omitempty"`
	FootnoteNumber string     `json:"footnoteNumber,omitempty"`
	TextStyle      *TextStyle `json:"textStyle,omitempty"`
}

// Break is a page break, column break or horizontal rule.
type Break struct {
	Suggested
	TextStyle *TextStyle `json:"textStyle,omitempty"`
}

// Person is a people chip.
type Person struct {
	Suggested
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
	DateID                string                 `json:"dateId,omitempty"`
	DateElementProperties *DateElementProperties `json:"dateElementProperties,omitempty"`
	TextStyle             *TextStyle             `json:"textStyle,omitempty"`
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
	Type      string     `json:"type,omitempty"`
	TextStyle *TextStyle `json:"textStyle,omitempty"`
}

// Table is a table block.
type Table struct {
	Suggested
	Rows      int64       `json:"rows,omitempty"`
	Columns   int64       `json:"columns,omitempty"`
	TableRows []*TableRow `json:"tableRows,omitempty"`
}

// TableRow is one row.
type TableRow struct {
	Suggested
	StartIndex int64        `json:"startIndex,omitempty"`
	EndIndex   int64        `json:"endIndex,omitempty"`
	TableCells []*TableCell `json:"tableCells,omitempty"`
}

// TableCell is one cell with nested content.
type TableCell struct {
	Suggested
	StartIndex     int64                `json:"startIndex,omitempty"`
	EndIndex       int64                `json:"endIndex,omitempty"`
	Content        []*StructuralElement `json:"content,omitempty"`
	TableCellStyle *TableCellStyle      `json:"tableCellStyle,omitempty"`
}

// TableCellStyle is the subset of cell formatting we read.
type TableCellStyle struct {
	RowSpan    int64 `json:"rowSpan,omitempty"`
	ColumnSpan int64 `json:"columnSpan,omitempty"`
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
}

// TableOfContents is a generated, read-only block.
type TableOfContents struct {
	Suggested
	Content []*StructuralElement `json:"content,omitempty"`
}

// List describes a list's nesting levels.
type List struct {
	ListProperties *ListProperties `json:"listProperties,omitempty"`
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
}

// InlineObjectProperties wrap the embedded object.
type InlineObjectProperties struct {
	EmbeddedObject *EmbeddedObject `json:"embeddedObject,omitempty"`
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
