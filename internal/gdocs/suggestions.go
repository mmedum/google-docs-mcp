package gdocs

// Suggestion types: the change a pending suggestion would make, as
// documents.get reports it in the SUGGESTIONS_INLINE view.
//
// Every one of these comes as a map keyed by suggestion id, because
// several people can suggest a change to the same run. The pair in each
// value is the trap: the style is the style as it WOULD BE once the
// suggestion is accepted, and it says nothing about what changed, so
// only the matching state tells you which fields the suggestion
// actually sets. A suggestion that turns on bold arrives with italic,
// underline, a font size and a colour all filled in and every one of
// them inherited — reading the style without the state reports a
// suggestion to change nine properties where a person asked for one.
//
// The fields are the discovery document's, in full: a suggestion this
// server cannot render is still a suggestion it must not report as
// absent, which is what a partial list did until #46.

// BackgroundSuggestionState marks which fields of a suggested
// background the suggestion sets.
type BackgroundSuggestionState struct {
	BackgroundColorSuggested bool `json:"backgroundColorSuggested,omitempty"`
}

// BulletSuggestionState marks which fields of a suggested
// bullet the suggestion sets.
type BulletSuggestionState struct {
	ListIDSuggested          bool                      `json:"listIdSuggested,omitempty"`
	NestingLevelSuggested    bool                      `json:"nestingLevelSuggested,omitempty"`
	TextStyleSuggestionState *TextStyleSuggestionState `json:"textStyleSuggestionState,omitempty"`
}

// CropPropertiesSuggestionState marks which fields of a suggested
// crop properties the suggestion sets.
type CropPropertiesSuggestionState struct {
	AngleSuggested        bool `json:"angleSuggested,omitempty"`
	OffsetBottomSuggested bool `json:"offsetBottomSuggested,omitempty"`
	OffsetLeftSuggested   bool `json:"offsetLeftSuggested,omitempty"`
	OffsetRightSuggested  bool `json:"offsetRightSuggested,omitempty"`
	OffsetTopSuggested    bool `json:"offsetTopSuggested,omitempty"`
}

// DateElementPropertiesSuggestionState marks which fields of a suggested
// date element properties the suggestion sets.
type DateElementPropertiesSuggestionState struct {
	DateFormatSuggested bool `json:"dateFormatSuggested,omitempty"`
	LocaleSuggested     bool `json:"localeSuggested,omitempty"`
	TimeFormatSuggested bool `json:"timeFormatSuggested,omitempty"`
	TimeZoneIDSuggested bool `json:"timeZoneIdSuggested,omitempty"`
	TimestampSuggested  bool `json:"timestampSuggested,omitempty"`
}

// DocumentStyleSuggestionState marks which fields of a suggested
// document style the suggestion sets.
type DocumentStyleSuggestionState struct {
	BackgroundSuggestionState             *BackgroundSuggestionState `json:"backgroundSuggestionState,omitempty"`
	DefaultFooterIDSuggested              bool                       `json:"defaultFooterIdSuggested,omitempty"`
	DefaultHeaderIDSuggested              bool                       `json:"defaultHeaderIdSuggested,omitempty"`
	EvenPageFooterIDSuggested             bool                       `json:"evenPageFooterIdSuggested,omitempty"`
	EvenPageHeaderIDSuggested             bool                       `json:"evenPageHeaderIdSuggested,omitempty"`
	FirstPageFooterIDSuggested            bool                       `json:"firstPageFooterIdSuggested,omitempty"`
	FirstPageHeaderIDSuggested            bool                       `json:"firstPageHeaderIdSuggested,omitempty"`
	FlipPageOrientationSuggested          bool                       `json:"flipPageOrientationSuggested,omitempty"`
	MarginBottomSuggested                 bool                       `json:"marginBottomSuggested,omitempty"`
	MarginFooterSuggested                 bool                       `json:"marginFooterSuggested,omitempty"`
	MarginHeaderSuggested                 bool                       `json:"marginHeaderSuggested,omitempty"`
	MarginLeftSuggested                   bool                       `json:"marginLeftSuggested,omitempty"`
	MarginRightSuggested                  bool                       `json:"marginRightSuggested,omitempty"`
	MarginTopSuggested                    bool                       `json:"marginTopSuggested,omitempty"`
	PageNumberStartSuggested              bool                       `json:"pageNumberStartSuggested,omitempty"`
	PageSizeSuggestionState               *SizeSuggestionState       `json:"pageSizeSuggestionState,omitempty"`
	UseCustomHeaderFooterMarginsSuggested bool                       `json:"useCustomHeaderFooterMarginsSuggested,omitempty"`
	UseEvenPageHeaderFooterSuggested      bool                       `json:"useEvenPageHeaderFooterSuggested,omitempty"`
	UseFirstPageHeaderFooterSuggested     bool                       `json:"useFirstPageHeaderFooterSuggested,omitempty"`
}

// EmbeddedDrawingPropertiesSuggestionState marks which fields of a suggested
// embedded drawing properties the suggestion sets.
type EmbeddedDrawingPropertiesSuggestionState struct{}

// EmbeddedObjectBorderSuggestionState marks which fields of a suggested
// embedded object border the suggestion sets.
type EmbeddedObjectBorderSuggestionState struct {
	ColorSuggested         bool `json:"colorSuggested,omitempty"`
	DashStyleSuggested     bool `json:"dashStyleSuggested,omitempty"`
	PropertyStateSuggested bool `json:"propertyStateSuggested,omitempty"`
	WidthSuggested         bool `json:"widthSuggested,omitempty"`
}

// EmbeddedObjectSuggestionState marks which fields of a suggested
// embedded object the suggestion sets.
type EmbeddedObjectSuggestionState struct {
	DescriptionSuggested                     bool                                      `json:"descriptionSuggested,omitempty"`
	EmbeddedDrawingPropertiesSuggestionState *EmbeddedDrawingPropertiesSuggestionState `json:"embeddedDrawingPropertiesSuggestionState,omitempty"`
	EmbeddedObjectBorderSuggestionState      *EmbeddedObjectBorderSuggestionState      `json:"embeddedObjectBorderSuggestionState,omitempty"`
	ImagePropertiesSuggestionState           *ImagePropertiesSuggestionState           `json:"imagePropertiesSuggestionState,omitempty"`
	LinkedContentReferenceSuggestionState    *LinkedContentReferenceSuggestionState    `json:"linkedContentReferenceSuggestionState,omitempty"`
	MarginBottomSuggested                    bool                                      `json:"marginBottomSuggested,omitempty"`
	MarginLeftSuggested                      bool                                      `json:"marginLeftSuggested,omitempty"`
	MarginRightSuggested                     bool                                      `json:"marginRightSuggested,omitempty"`
	MarginTopSuggested                       bool                                      `json:"marginTopSuggested,omitempty"`
	SizeSuggestionState                      *SizeSuggestionState                      `json:"sizeSuggestionState,omitempty"`
	TitleSuggested                           bool                                      `json:"titleSuggested,omitempty"`
}

// ImagePropertiesSuggestionState marks which fields of a suggested
// image properties the suggestion sets.
type ImagePropertiesSuggestionState struct {
	AngleSuggested                bool                           `json:"angleSuggested,omitempty"`
	BrightnessSuggested           bool                           `json:"brightnessSuggested,omitempty"`
	ContentURISuggested           bool                           `json:"contentUriSuggested,omitempty"`
	ContrastSuggested             bool                           `json:"contrastSuggested,omitempty"`
	CropPropertiesSuggestionState *CropPropertiesSuggestionState `json:"cropPropertiesSuggestionState,omitempty"`
	SourceURISuggested            bool                           `json:"sourceUriSuggested,omitempty"`
	TransparencySuggested         bool                           `json:"transparencySuggested,omitempty"`
}

// InlineObjectPropertiesSuggestionState marks which fields of a suggested
// inline object properties the suggestion sets.
type InlineObjectPropertiesSuggestionState struct {
	EmbeddedObjectSuggestionState *EmbeddedObjectSuggestionState `json:"embeddedObjectSuggestionState,omitempty"`
}

// LinkedContentReferenceSuggestionState marks which fields of a suggested
// linked content reference the suggestion sets.
type LinkedContentReferenceSuggestionState struct {
	SheetsChartReferenceSuggestionState *SheetsChartReferenceSuggestionState `json:"sheetsChartReferenceSuggestionState,omitempty"`
}

// ListPropertiesSuggestionState marks which fields of a suggested
// list properties the suggestion sets.
type ListPropertiesSuggestionState struct {
	NestingLevelsSuggestionStates []NestingLevelSuggestionState `json:"nestingLevelsSuggestionStates,omitempty"`
}

// NamedStyleSuggestionState marks which fields of a suggested
// named style the suggestion sets.
type NamedStyleSuggestionState struct {
	NamedStyleType                string                         `json:"namedStyleType,omitempty"`
	ParagraphStyleSuggestionState *ParagraphStyleSuggestionState `json:"paragraphStyleSuggestionState,omitempty"`
	TextStyleSuggestionState      *TextStyleSuggestionState      `json:"textStyleSuggestionState,omitempty"`
}

// NamedStylesSuggestionState marks which fields of a suggested
// named styles the suggestion sets.
type NamedStylesSuggestionState struct {
	StylesSuggestionStates []NamedStyleSuggestionState `json:"stylesSuggestionStates,omitempty"`
}

// NestingLevelSuggestionState marks which fields of a suggested
// nesting level the suggestion sets.
type NestingLevelSuggestionState struct {
	BulletAlignmentSuggested bool                      `json:"bulletAlignmentSuggested,omitempty"`
	GlyphFormatSuggested     bool                      `json:"glyphFormatSuggested,omitempty"`
	GlyphSymbolSuggested     bool                      `json:"glyphSymbolSuggested,omitempty"`
	GlyphTypeSuggested       bool                      `json:"glyphTypeSuggested,omitempty"`
	IndentFirstLineSuggested bool                      `json:"indentFirstLineSuggested,omitempty"`
	IndentStartSuggested     bool                      `json:"indentStartSuggested,omitempty"`
	StartNumberSuggested     bool                      `json:"startNumberSuggested,omitempty"`
	TextStyleSuggestionState *TextStyleSuggestionState `json:"textStyleSuggestionState,omitempty"`
}

// ObjectReferences is a collection of object IDs.
type ObjectReferences struct {
	ObjectIDs []string `json:"objectIds,omitempty"`
}

// ParagraphStyleSuggestionState marks which fields of a suggested
// paragraph style the suggestion sets.
type ParagraphStyleSuggestionState struct {
	AlignmentSuggested           bool                    `json:"alignmentSuggested,omitempty"`
	AvoidWidowAndOrphanSuggested bool                    `json:"avoidWidowAndOrphanSuggested,omitempty"`
	BorderBetweenSuggested       bool                    `json:"borderBetweenSuggested,omitempty"`
	BorderBottomSuggested        bool                    `json:"borderBottomSuggested,omitempty"`
	BorderLeftSuggested          bool                    `json:"borderLeftSuggested,omitempty"`
	BorderRightSuggested         bool                    `json:"borderRightSuggested,omitempty"`
	BorderTopSuggested           bool                    `json:"borderTopSuggested,omitempty"`
	DirectionSuggested           bool                    `json:"directionSuggested,omitempty"`
	HeadingIDSuggested           bool                    `json:"headingIdSuggested,omitempty"`
	IndentEndSuggested           bool                    `json:"indentEndSuggested,omitempty"`
	IndentFirstLineSuggested     bool                    `json:"indentFirstLineSuggested,omitempty"`
	IndentStartSuggested         bool                    `json:"indentStartSuggested,omitempty"`
	KeepLinesTogetherSuggested   bool                    `json:"keepLinesTogetherSuggested,omitempty"`
	KeepWithNextSuggested        bool                    `json:"keepWithNextSuggested,omitempty"`
	LineSpacingSuggested         bool                    `json:"lineSpacingSuggested,omitempty"`
	NamedStyleTypeSuggested      bool                    `json:"namedStyleTypeSuggested,omitempty"`
	PageBreakBeforeSuggested     bool                    `json:"pageBreakBeforeSuggested,omitempty"`
	ShadingSuggestionState       *ShadingSuggestionState `json:"shadingSuggestionState,omitempty"`
	SpaceAboveSuggested          bool                    `json:"spaceAboveSuggested,omitempty"`
	SpaceBelowSuggested          bool                    `json:"spaceBelowSuggested,omitempty"`
	SpacingModeSuggested         bool                    `json:"spacingModeSuggested,omitempty"`
}

// PositionedObjectPositioningSuggestionState marks which fields of a suggested
// positioned object positioning the suggestion sets.
type PositionedObjectPositioningSuggestionState struct {
	LayoutSuggested     bool `json:"layoutSuggested,omitempty"`
	LeftOffsetSuggested bool `json:"leftOffsetSuggested,omitempty"`
	TopOffsetSuggested  bool `json:"topOffsetSuggested,omitempty"`
}

// PositionedObjectPropertiesSuggestionState marks which fields of a suggested
// positioned object properties the suggestion sets.
type PositionedObjectPropertiesSuggestionState struct {
	EmbeddedObjectSuggestionState *EmbeddedObjectSuggestionState              `json:"embeddedObjectSuggestionState,omitempty"`
	PositioningSuggestionState    *PositionedObjectPositioningSuggestionState `json:"positioningSuggestionState,omitempty"`
}

// ShadingSuggestionState marks which fields of a suggested
// shading the suggestion sets.
type ShadingSuggestionState struct {
	BackgroundColorSuggested bool `json:"backgroundColorSuggested,omitempty"`
}

// SheetsChartReferenceSuggestionState marks which fields of a suggested
// sheets chart reference the suggestion sets.
type SheetsChartReferenceSuggestionState struct {
	ChartIDSuggested       bool `json:"chartIdSuggested,omitempty"`
	SpreadsheetIDSuggested bool `json:"spreadsheetIdSuggested,omitempty"`
}

// SizeSuggestionState marks which fields of a suggested
// size the suggestion sets.
type SizeSuggestionState struct {
	HeightSuggested bool `json:"heightSuggested,omitempty"`
	WidthSuggested  bool `json:"widthSuggested,omitempty"`
}

// SuggestedBullet is one suggestion's change to a bullet.
type SuggestedBullet struct {
	Bullet                *Bullet                `json:"bullet,omitempty"`
	BulletSuggestionState *BulletSuggestionState `json:"bulletSuggestionState,omitempty"`
}

// SuggestedDateElementProperties is one suggestion's change to a date element properties.
type SuggestedDateElementProperties struct {
	DateElementProperties                *DateElementProperties                `json:"dateElementProperties,omitempty"`
	DateElementPropertiesSuggestionState *DateElementPropertiesSuggestionState `json:"dateElementPropertiesSuggestionState,omitempty"`
}

// SuggestedDocumentStyle is one suggestion's change to a document style.
type SuggestedDocumentStyle struct {
	DocumentStyle                *DocumentStyle                `json:"documentStyle,omitempty"`
	DocumentStyleSuggestionState *DocumentStyleSuggestionState `json:"documentStyleSuggestionState,omitempty"`
}

// SuggestedInlineObjectProperties is one suggestion's change to a inline object properties.
type SuggestedInlineObjectProperties struct {
	InlineObjectProperties                *InlineObjectProperties                `json:"inlineObjectProperties,omitempty"`
	InlineObjectPropertiesSuggestionState *InlineObjectPropertiesSuggestionState `json:"inlineObjectPropertiesSuggestionState,omitempty"`
}

// SuggestedListProperties is one suggestion's change to a list properties.
type SuggestedListProperties struct {
	ListProperties                *ListProperties                `json:"listProperties,omitempty"`
	ListPropertiesSuggestionState *ListPropertiesSuggestionState `json:"listPropertiesSuggestionState,omitempty"`
}

// SuggestedNamedStyles is one suggestion's change to a named styles.
type SuggestedNamedStyles struct {
	NamedStyles                *NamedStyles                `json:"namedStyles,omitempty"`
	NamedStylesSuggestionState *NamedStylesSuggestionState `json:"namedStylesSuggestionState,omitempty"`
}

// SuggestedParagraphStyle is one suggestion's change to a paragraph style.
type SuggestedParagraphStyle struct {
	ParagraphStyle                *ParagraphStyle                `json:"paragraphStyle,omitempty"`
	ParagraphStyleSuggestionState *ParagraphStyleSuggestionState `json:"paragraphStyleSuggestionState,omitempty"`
}

// SuggestedPositionedObjectProperties is one suggestion's change to a positioned object properties.
type SuggestedPositionedObjectProperties struct {
	PositionedObjectProperties                *PositionedObjectProperties                `json:"positionedObjectProperties,omitempty"`
	PositionedObjectPropertiesSuggestionState *PositionedObjectPropertiesSuggestionState `json:"positionedObjectPropertiesSuggestionState,omitempty"`
}

// SuggestedTableCellStyle is one suggestion's change to a table cell style.
type SuggestedTableCellStyle struct {
	TableCellStyle                *TableCellStyle                `json:"tableCellStyle,omitempty"`
	TableCellStyleSuggestionState *TableCellStyleSuggestionState `json:"tableCellStyleSuggestionState,omitempty"`
}

// SuggestedTableRowStyle is one suggestion's change to a table row style.
type SuggestedTableRowStyle struct {
	TableRowStyle                *TableRowStyle                `json:"tableRowStyle,omitempty"`
	TableRowStyleSuggestionState *TableRowStyleSuggestionState `json:"tableRowStyleSuggestionState,omitempty"`
}

// SuggestedTextStyle is one suggestion's change to a text style.
type SuggestedTextStyle struct {
	TextStyle                *TextStyle                `json:"textStyle,omitempty"`
	TextStyleSuggestionState *TextStyleSuggestionState `json:"textStyleSuggestionState,omitempty"`
}

// TableCellStyleSuggestionState marks which fields of a suggested
// table cell style the suggestion sets.
type TableCellStyleSuggestionState struct {
	BackgroundColorSuggested  bool `json:"backgroundColorSuggested,omitempty"`
	BorderBottomSuggested     bool `json:"borderBottomSuggested,omitempty"`
	BorderLeftSuggested       bool `json:"borderLeftSuggested,omitempty"`
	BorderRightSuggested      bool `json:"borderRightSuggested,omitempty"`
	BorderTopSuggested        bool `json:"borderTopSuggested,omitempty"`
	ColumnSpanSuggested       bool `json:"columnSpanSuggested,omitempty"`
	ContentAlignmentSuggested bool `json:"contentAlignmentSuggested,omitempty"`
	PaddingBottomSuggested    bool `json:"paddingBottomSuggested,omitempty"`
	PaddingLeftSuggested      bool `json:"paddingLeftSuggested,omitempty"`
	PaddingRightSuggested     bool `json:"paddingRightSuggested,omitempty"`
	PaddingTopSuggested       bool `json:"paddingTopSuggested,omitempty"`
	RowSpanSuggested          bool `json:"rowSpanSuggested,omitempty"`
}

// TableRowStyle is styles that apply to a table row.
type TableRowStyle struct {
	MinRowHeight    *Dimension `json:"minRowHeight,omitempty"`
	PreventOverflow bool       `json:"preventOverflow,omitempty"`
	TableHeader     bool       `json:"tableHeader,omitempty"`
}

// TableRowStyleSuggestionState marks which fields of a suggested
// table row style the suggestion sets.
type TableRowStyleSuggestionState struct {
	MinRowHeightSuggested bool `json:"minRowHeightSuggested,omitempty"`
}

// TextStyleSuggestionState marks which fields of a suggested
// text style the suggestion sets.
type TextStyleSuggestionState struct {
	BackgroundColorSuggested    bool `json:"backgroundColorSuggested,omitempty"`
	BaselineOffsetSuggested     bool `json:"baselineOffsetSuggested,omitempty"`
	BoldSuggested               bool `json:"boldSuggested,omitempty"`
	FontSizeSuggested           bool `json:"fontSizeSuggested,omitempty"`
	ForegroundColorSuggested    bool `json:"foregroundColorSuggested,omitempty"`
	ItalicSuggested             bool `json:"italicSuggested,omitempty"`
	LinkSuggested               bool `json:"linkSuggested,omitempty"`
	SmallCapsSuggested          bool `json:"smallCapsSuggested,omitempty"`
	StrikethroughSuggested      bool `json:"strikethroughSuggested,omitempty"`
	UnderlineSuggested          bool `json:"underlineSuggested,omitempty"`
	WeightedFontFamilySuggested bool `json:"weightedFontFamilySuggested,omitempty"`
}
