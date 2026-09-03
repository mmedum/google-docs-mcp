package plan

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Layout ops change how a document is laid out rather than what it says:
// the page itself, one section of it, and the named styles every
// paragraph inherits from.
const (
	OpPageSetup    OpKind = "page"
	OpSectionStyle OpKind = "section"
	OpSectionBreak OpKind = "section_break"
	OpNamedStyle   OpKind = "named_style"
)

// Section types insertSectionBreak accepts. Columns are not a section
// type: they are set with SectionSpec.Columns on the section itself.
const (
	SectionContinuous = "CONTINUOUS"
	SectionNextPage   = "NEXT_PAGE"
)

// NamedStyleTypes are the styles every paragraph inherits from.
var NamedStyleTypes = []string{"NORMAL_TEXT", "TITLE", "SUBTITLE",
	"HEADING_1", "HEADING_2", "HEADING_3", "HEADING_4", "HEADING_5", "HEADING_6"}

// PageMargins are the margin fields a page and a section share, in the
// order the API names them.
type PageMargins struct {
	TopPt    *float64
	BottomPt *float64
	LeftPt   *float64
	RightPt  *float64
}

// body writes the margins that are set. A nil pointer leaves the margin
// alone; zero is a margin like any other, which is why they are pointers.
func (m PageMargins) body(set func(string, any)) {
	for _, f := range []struct {
		name string
		v    *float64
	}{{"marginTop", m.TopPt}, {"marginBottom", m.BottomPt}, {"marginLeft", m.LeftPt}, {"marginRight", m.RightPt}} {
		if f.v != nil {
			set(f.name, pt(*f.v))
		}
	}
}

func (m PageMargins) noneSet() bool {
	return m.TopPt == nil && m.BottomPt == nil && m.LeftPt == nil && m.RightPt == nil
}

func (m PageMargins) check(extra ...*float64) error {
	for _, v := range append([]*float64{m.TopPt, m.BottomPt, m.LeftPt, m.RightPt}, extra...) {
		if v != nil && (*v < 0 || *v > 720) {
			return fmt.Errorf("margins must be between 0 and 720 points")
		}
	}
	return nil
}

// PageSpec is a change to a tab's page setup.
type PageSpec struct {
	WidthPt  float64
	HeightPt float64
	PageMargins
	MarginHeaderPt        *float64
	MarginFooterPt        *float64
	Background            string
	PageNumberStart       *int
	Landscape             *bool
	FirstPageHeaderFooter *bool
	EvenPageHeaderFooter  *bool
}

// IsZero reports whether the spec changes nothing.
func (s PageSpec) IsZero() bool {
	return s.WidthPt == 0 && s.HeightPt == 0 && s.noneSet() &&
		s.MarginHeaderPt == nil && s.MarginFooterPt == nil && s.Background == "" &&
		s.PageNumberStart == nil && s.Landscape == nil &&
		s.FirstPageHeaderFooter == nil && s.EvenPageHeaderFooter == nil
}

// Validate checks the spec on its own, before any document is read.
func (s PageSpec) Validate() error {
	if err := s.check(s.MarginHeaderPt, s.MarginFooterPt); err != nil {
		return err
	}
	if (s.WidthPt > 0) != (s.HeightPt > 0) {
		return fmt.Errorf("page_width_pt and page_height_pt are set together")
	}
	if s.WidthPt < 0 || s.WidthPt > 4000 || s.HeightPt < 0 || s.HeightPt > 4000 {
		return fmt.Errorf("the page must be between 0 and 4000 points on a side")
	}
	if !ValidColor(s.Background) {
		return fmt.Errorf("background must be #rrggbb or none")
	}
	if s.PageNumberStart != nil && *s.PageNumberStart < 0 {
		return fmt.Errorf("page_number_start cannot be negative")
	}
	return nil
}

func (s PageSpec) body() (map[string]any, []string) {
	style := map[string]any{}
	var fields []string
	set := func(name string, v any) {
		style[name] = v
		fields = append(fields, name)
	}
	if s.WidthPt > 0 {
		set("pageSize", map[string]any{"width": pt(s.WidthPt), "height": pt(s.HeightPt)})
	}
	s.PageMargins.body(set)
	if s.MarginHeaderPt != nil {
		set("marginHeader", pt(*s.MarginHeaderPt))
	}
	if s.MarginFooterPt != nil {
		set("marginFooter", pt(*s.MarginFooterPt))
	}
	if c, ok := colorJSON(s.Background); ok {
		// DocumentStyle.background is a Background, whose own color field
		// is the OptionalColor colorJSON builds.
		set("background", map[string]any{"color": c})
	} else if s.Background == "none" {
		fields = append(fields, "background")
	}
	if s.PageNumberStart != nil {
		set("pageNumberStart", *s.PageNumberStart)
	}
	if s.Landscape != nil {
		set("flipPageOrientation", *s.Landscape)
	}
	if s.FirstPageHeaderFooter != nil {
		set("useFirstPageHeaderFooter", *s.FirstPageHeaderFooter)
	}
	if s.EvenPageHeaderFooter != nil {
		set("useEvenPageHeaderFooter", *s.EvenPageHeaderFooter)
	}
	return style, fields
}

// UpdateDocumentStyle sets a tab's page properties.
func UpdateDocumentStyle(s PageSpec, tabID string) json.RawMessage {
	style, fields := s.body()
	req := map[string]any{"documentStyle": style, "fields": strings.Join(fields, ",")}
	if tabID != "" {
		req["tabId"] = tabID
	}
	return raw(map[string]any{"updateDocumentStyle": req})
}

// SectionSpec is a change to one section: its columns, its own margins,
// and where its page numbering starts. It carries no section type:
// SectionStyle.sectionType is output only, so a section is continuous or
// starts a page according to the break that made it.
type SectionSpec struct {
	PageMargins
	Columns               int
	ColumnGapPt           *float64
	ColumnSeparator       string // none or between
	ContentDirection      string // LEFT_TO_RIGHT or RIGHT_TO_LEFT
	PageNumberStart       *int
	Landscape             *bool
	FirstPageHeaderFooter *bool
}

// IsZero reports whether the spec changes nothing.
func (s SectionSpec) IsZero() bool {
	return s.noneSet() && s.Columns == 0 && s.ColumnGapPt == nil &&
		s.ColumnSeparator == "" && s.ContentDirection == "" && s.PageNumberStart == nil &&
		s.Landscape == nil && s.FirstPageHeaderFooter == nil
}

// Validate checks the spec on its own.
func (s SectionSpec) Validate() error {
	if err := s.check(); err != nil {
		return err
	}
	if s.Columns < 0 || s.Columns > 3 {
		return fmt.Errorf("columns must be between 1 and 3")
	}
	if s.ColumnGapPt != nil && (*s.ColumnGapPt < 0 || *s.ColumnGapPt > 720) {
		return fmt.Errorf("column_gap_pt must be between 0 and 720")
	}
	switch s.ColumnSeparator {
	case "", "NONE", "BETWEEN_EACH_COLUMN":
	default:
		return fmt.Errorf("column_separator must be none or between")
	}
	switch s.ContentDirection {
	case "", "LEFT_TO_RIGHT", "RIGHT_TO_LEFT":
	default:
		return fmt.Errorf("content_direction must be left_to_right or right_to_left")
	}
	if s.ColumnGapPt != nil && s.Columns == 0 {
		return fmt.Errorf("column_gap_pt needs columns")
	}
	return nil
}

func (s SectionSpec) body() (map[string]any, []string) {
	style := map[string]any{}
	var fields []string
	set := func(name string, v any) {
		style[name] = v
		fields = append(fields, name)
	}
	s.PageMargins.body(set)
	if s.Columns > 0 {
		// Google spaces equal columns itself when no width is given; the
		// gap is padding after each column but the last.
		cols := make([]map[string]any, 0, s.Columns)
		for range s.Columns {
			col := map[string]any{}
			if s.ColumnGapPt != nil {
				col["paddingEnd"] = pt(*s.ColumnGapPt)
			}
			cols = append(cols, col)
		}
		set("columnProperties", cols)
	}
	if s.ColumnSeparator != "" {
		set("columnSeparatorStyle", s.ColumnSeparator)
	}
	if s.ContentDirection != "" {
		set("contentDirection", s.ContentDirection)
	}
	if s.PageNumberStart != nil {
		set("pageNumberStart", *s.PageNumberStart)
	}
	if s.Landscape != nil {
		set("flipPageOrientation", *s.Landscape)
	}
	if s.FirstPageHeaderFooter != nil {
		set("useFirstPageHeaderFooter", *s.FirstPageHeaderFooter)
	}
	return style, fields
}

// UpdateSectionStyle styles the sections the range touches.
func UpdateSectionStyle(r Rng, s SectionSpec) json.RawMessage {
	style, fields := s.body()
	return raw(map[string]any{"updateSectionStyle": map[string]any{
		"range": r.json(), "sectionStyle": style, "fields": strings.Join(fields, ",")}})
}

// InsertSectionBreak starts a new section at the insertion point.
func InsertSectionBreak(at Loc, sectionType string) json.RawMessage {
	if sectionType == "" {
		sectionType = SectionNextPage
	}
	return raw(map[string]any{"insertSectionBreak": map[string]any{
		"location": at.json(), "sectionType": sectionType}})
}

// NamedStyleSpec redefines one named style for the whole tab.
type NamedStyleSpec struct {
	Style string // NORMAL_TEXT, TITLE, SUBTITLE, HEADING_1..6
	Text  TextStyleSpec
	Para  ParagraphStyleSpec
}

// IsZero reports whether the spec changes nothing.
func (s NamedStyleSpec) IsZero() bool { return s.Text.IsZero() && s.Para.IsZero() }

// Validate checks the spec on its own.
func (s NamedStyleSpec) Validate() error {
	if s.Style == "" {
		return fmt.Errorf("style names the named style to redefine: %s", strings.Join(NamedStyleTypes, ", "))
	}
	for _, t := range NamedStyleTypes {
		if t == s.Style {
			if err := s.Text.Validate(); err != nil {
				return err
			}
			return s.Para.Validate()
		}
	}
	return fmt.Errorf("style must be one of %s", strings.Join(NamedStyleTypes, ", "))
}

// UpdateNamedStyle redefines a named style, which restyles every
// paragraph that carries it and every one written with it afterwards.
func UpdateNamedStyle(s NamedStyleSpec, tabID string) json.RawMessage {
	style := map[string]any{"namedStyleType": s.Style}
	fields := []string{"namedStyleType"}
	// The mask names the parent as well as each leaf: "to update the text
	// style to bold, set fields to include text_style and
	// text_style.bold" (UpdateNamedStyleRequest reference).
	if text, f := s.Text.body(); len(f) > 0 {
		style["textStyle"] = text
		fields = append(fields, "textStyle")
		for _, name := range f {
			fields = append(fields, "textStyle."+name)
		}
	}
	if para, f := s.Para.body(); len(f) > 0 {
		style["paragraphStyle"] = para
		fields = append(fields, "paragraphStyle")
		for _, name := range f {
			fields = append(fields, "paragraphStyle."+name)
		}
	}
	req := map[string]any{"namedStyle": style, "fields": strings.Join(fields, ",")}
	if tabID != "" {
		req["tabId"] = tabID
	}
	return raw(map[string]any{"updateNamedStyle": req})
}

// pt is a length in points, the only unit the API takes.
func pt(v float64) map[string]any { return map[string]any{"magnitude": v, "unit": "PT"} }
