// Package plan compiles high-level edit operations into Docs API
// batchUpdate requests. It owns every piece of index arithmetic on the
// write path: fragment layout, minimal diffs, request ordering, and the
// overwrite guard. It never talks to the network.
package plan

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Loc is an insertion point.
type Loc struct {
	Index     int64
	SegmentID string
	TabID     string
}

// Rng is a half-open UTF-16 range in one segment.
type Rng struct {
	Start     int64
	End       int64
	SegmentID string
	TabID     string
}

func (l Loc) json() map[string]any {
	m := map[string]any{"index": l.Index}
	if l.SegmentID != "" {
		m["segmentId"] = l.SegmentID
	}
	if l.TabID != "" {
		m["tabId"] = l.TabID
	}
	return m
}

func (r Rng) json() map[string]any {
	m := map[string]any{"startIndex": r.Start, "endIndex": r.End}
	if r.SegmentID != "" {
		m["segmentId"] = r.SegmentID
	}
	if r.TabID != "" {
		m["tabId"] = r.TabID
	}
	return m
}

func raw(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic("plan: marshal request: " + err.Error())
	}
	return b
}

// InsertText inserts text at a location.
func InsertText(text string, at Loc) json.RawMessage {
	return raw(map[string]any{"insertText": map[string]any{"text": text, "location": at.json()}})
}

// DeleteRange deletes a range.
func DeleteRange(r Rng) json.RawMessage {
	return raw(map[string]any{"deleteContentRange": map[string]any{"range": r.json()}})
}

// TextStyleSpec is a set of character-formatting changes. Nil pointers
// and empty strings mean "leave as is"; the string "none" clears a font,
// colour or link.
type TextStyleSpec struct {
	Bold          *bool
	Italic        *bool
	Underline     *bool
	Strikethrough *bool
	SmallCaps     *bool
	Font          string
	SizePt        float64
	Foreground    string
	Background    string
	Link          string
	Baseline      string // SUPERSCRIPT, SUBSCRIPT, NONE
}

// IsZero reports whether the spec changes nothing.
func (s TextStyleSpec) IsZero() bool { return s == TextStyleSpec{} }

var baselines = map[string]bool{"SUPERSCRIPT": true, "SUBSCRIPT": true, "NONE": true}

// Validate checks enum and colour values.
func (s TextStyleSpec) Validate() error {
	if !ValidColor(s.Foreground) || !ValidColor(s.Background) {
		return fmt.Errorf("colours must be #rrggbb or none")
	}
	if s.Baseline != "" && !baselines[s.Baseline] {
		return fmt.Errorf("baseline must be SUPERSCRIPT, SUBSCRIPT or NONE")
	}
	if s.SizePt < 0 || s.SizePt > 400 {
		return fmt.Errorf("size_pt must be between 0 and 400")
	}
	return nil
}

func (s TextStyleSpec) body() (map[string]any, []string) {
	style := map[string]any{}
	var fields []string
	set := func(name string, v any) {
		style[name] = v
		fields = append(fields, name)
	}
	if s.Bold != nil {
		set("bold", *s.Bold)
	}
	if s.Italic != nil {
		set("italic", *s.Italic)
	}
	if s.Underline != nil {
		set("underline", *s.Underline)
	}
	if s.Strikethrough != nil {
		set("strikethrough", *s.Strikethrough)
	}
	if s.SmallCaps != nil {
		set("smallCaps", *s.SmallCaps)
	}
	switch s.Font {
	case "":
	case "none":
		fields = append(fields, "weightedFontFamily")
	default:
		set("weightedFontFamily", map[string]any{"fontFamily": s.Font})
	}
	if s.SizePt > 0 {
		set("fontSize", map[string]any{"magnitude": s.SizePt, "unit": "PT"})
	}
	if c, ok := colorJSON(s.Foreground); ok {
		set("foregroundColor", c)
	} else if s.Foreground == "none" {
		fields = append(fields, "foregroundColor")
	}
	if c, ok := colorJSON(s.Background); ok {
		set("backgroundColor", c)
	} else if s.Background == "none" {
		fields = append(fields, "backgroundColor")
	}
	switch s.Link {
	case "":
	case "none":
		fields = append(fields, "link")
	default:
		set("link", map[string]any{"url": s.Link})
	}
	if s.Baseline != "" {
		set("baselineOffset", s.Baseline)
	}
	return style, fields
}

// colorJSON turns #rrggbb into the API colour object.
func colorJSON(hex string) (map[string]any, bool) {
	hex = strings.TrimPrefix(strings.ToLower(hex), "#")
	if len(hex) != 6 {
		return nil, false
	}
	var r, g, b int
	if _, err := fmt.Sscanf(hex, "%02x%02x%02x", &r, &g, &b); err != nil {
		return nil, false
	}
	rgb := map[string]any{}
	if r > 0 {
		rgb["red"] = float64(r) / 255
	}
	if g > 0 {
		rgb["green"] = float64(g) / 255
	}
	if b > 0 {
		rgb["blue"] = float64(b) / 255
	}
	return map[string]any{"color": map[string]any{"rgbColor": rgb}}, true
}

// ValidColor reports whether s is "#rrggbb" or "none".
func ValidColor(s string) bool {
	if s == "" || s == "none" {
		return true
	}
	_, ok := colorJSON(s)
	return ok
}

// UpdateTextStyle applies a spec to a range.
func UpdateTextStyle(r Rng, s TextStyleSpec) json.RawMessage {
	style, fields := s.body()
	return raw(map[string]any{"updateTextStyle": map[string]any{"range": r.json(), "textStyle": style, "fields": strings.Join(fields, ",")}})
}

// ClearTextStyle resets every character property in the range to the
// paragraph's default style.
func ClearTextStyle(r Rng) json.RawMessage {
	return raw(map[string]any{"updateTextStyle": map[string]any{"range": r.json(), "textStyle": map[string]any{}, "fields": "*"}})
}

// ParagraphStyleSpec is a set of paragraph-formatting changes.
type ParagraphStyleSpec struct {
	NamedStyle      string // NORMAL_TEXT, HEADING_1..6, TITLE, SUBTITLE
	Alignment       string // START, CENTER, END, JUSTIFIED
	LineSpacing     float64
	SpaceAbovePt    *float64
	SpaceBelowPt    *float64
	IndentStartPt   *float64
	IndentFirstLine *float64
	KeepWithNext    *bool
	PageBreakBefore *bool
}

// IsZero reports whether the spec changes nothing.
func (s ParagraphStyleSpec) IsZero() bool { return s == ParagraphStyleSpec{} }

var alignments = map[string]bool{"START": true, "CENTER": true, "END": true, "JUSTIFIED": true}

// Validate checks enum values.
func (s ParagraphStyleSpec) Validate() error {
	if s.NamedStyle != "" && !slices.Contains(NamedStyleTypes, s.NamedStyle) {
		return fmt.Errorf("named_style must be NORMAL_TEXT, TITLE, SUBTITLE or HEADING_1 to HEADING_6")
	}
	if s.Alignment != "" && !alignments[s.Alignment] {
		return fmt.Errorf("alignment must be START, CENTER, END or JUSTIFIED")
	}
	if s.LineSpacing < 0 {
		return fmt.Errorf("line_spacing must be positive")
	}
	return nil
}

func (s ParagraphStyleSpec) body() (map[string]any, []string) {
	style := map[string]any{}
	var fields []string
	set := func(name string, v any) {
		style[name] = v
		fields = append(fields, name)
	}
	if s.NamedStyle != "" {
		set("namedStyleType", s.NamedStyle)
	}
	if s.Alignment != "" {
		set("alignment", s.Alignment)
	}
	if s.LineSpacing > 0 {
		set("lineSpacing", s.LineSpacing)
	}
	pt := func(v float64) map[string]any { return map[string]any{"magnitude": v, "unit": "PT"} }
	if s.SpaceAbovePt != nil {
		set("spaceAbove", pt(*s.SpaceAbovePt))
	}
	if s.SpaceBelowPt != nil {
		set("spaceBelow", pt(*s.SpaceBelowPt))
	}
	if s.IndentStartPt != nil {
		set("indentStart", pt(*s.IndentStartPt))
	}
	if s.IndentFirstLine != nil {
		set("indentFirstLine", pt(*s.IndentFirstLine))
	}
	if s.KeepWithNext != nil {
		set("keepWithNext", *s.KeepWithNext)
	}
	if s.PageBreakBefore != nil {
		set("pageBreakBefore", *s.PageBreakBefore)
	}
	return style, fields
}

// UpdateParagraphStyle applies a spec to the paragraphs overlapping r.
func UpdateParagraphStyle(r Rng, s ParagraphStyleSpec) json.RawMessage {
	style, fields := s.body()
	return raw(map[string]any{"updateParagraphStyle": map[string]any{"range": r.json(), "paragraphStyle": style, "fields": strings.Join(fields, ",")}})
}

// Bullet presets.
const (
	BulletPreset   = "BULLET_DISC_CIRCLE_SQUARE"
	NumberedPreset = "NUMBERED_DECIMAL_ALPHA_ROMAN"
	CheckboxPreset = "BULLET_CHECKBOX"
)

// CreateBullets turns the paragraphs in r into a list. Leading tabs set
// nesting and are consumed by the API.
func CreateBullets(r Rng, preset string) json.RawMessage {
	return raw(map[string]any{"createParagraphBullets": map[string]any{"range": r.json(), "bulletPreset": preset}})
}

// DeleteBullets removes list membership from the paragraphs in r.
func DeleteBullets(r Rng) json.RawMessage {
	return raw(map[string]any{"deleteParagraphBullets": map[string]any{"range": r.json()}})
}

// ReplaceAllText replaces every occurrence in the given tab. The tab is
// always named because the API's default is every tab.
func ReplaceAllText(find, replace string, matchCase bool, tabID string) json.RawMessage {
	req := map[string]any{"containsText": map[string]any{"text": find, "matchCase": matchCase}, "replaceText": replace}
	if tabID != "" {
		req["tabsCriteria"] = map[string]any{"tabIds": []string{tabID}}
	}
	return raw(map[string]any{"replaceAllText": req})
}

// InsertPageBreak inserts a page break at a location.
func InsertPageBreak(at Loc) json.RawMessage {
	return raw(map[string]any{"insertPageBreak": map[string]any{"location": at.json()}})
}

// CreateHeader creates a default header; the reply carries headerId.
func CreateHeader(tabID string) json.RawMessage { return createSegment("createHeader", tabID) }

// CreateFooter creates a default footer; the reply carries footerId.
func CreateFooter(tabID string) json.RawMessage { return createSegment("createFooter", tabID) }

func createSegment(kind, tabID string) json.RawMessage {
	req := map[string]any{"type": "DEFAULT"}
	if tabID != "" {
		req["sectionBreakLocation"] = map[string]any{"index": 0, "tabId": tabID}
	}
	return raw(map[string]any{kind: req})
}

// CreateFootnote inserts a footnote reference at a location; the reply
// carries footnoteId.
func CreateFootnote(at Loc) json.RawMessage {
	return raw(map[string]any{"createFootnote": map[string]any{"location": at.json()}})
}

// InsertComment anchors a comment to a range (Developer Preview).
func InsertComment(content string, r Rng, assignee string) json.RawMessage {
	req := map[string]any{"content": content, "range": r.json()}
	if assignee != "" {
		req["assigneeEmailAddress"] = assignee
	}
	return raw(map[string]any{"insertComment": req})
}

// AcceptSuggestion accepts a suggestion by id (Developer Preview).
func AcceptSuggestion(id string) json.RawMessage {
	return raw(map[string]any{"acceptSuggestion": map[string]any{"suggestionId": id}})
}

// RejectSuggestion rejects a suggestion by id (Developer Preview).
func RejectSuggestion(id string) json.RawMessage {
	return raw(map[string]any{"rejectSuggestion": map[string]any{"suggestionId": id}})
}

// DeleteSuggestion removes a suggestion without applying or declining
// it. Google allows only its author to; an editor rejects instead
// (Developer Preview).
func DeleteSuggestion(id string) json.RawMessage {
	return raw(map[string]any{"deleteSuggestion": map[string]any{"suggestionId": id}})
}

// Kind returns the request type name of a marshalled request.
func Kind(r json.RawMessage) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(r, &m); err != nil {
		return ""
	}
	for k := range m {
		return k
	}
	return ""
}
