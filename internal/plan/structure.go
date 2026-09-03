package plan

import (
	"encoding/json"
	"fmt"
	"strings"
)

// CellLoc names one cell of a table by the table's start index and
// zero-based row and column.
type CellLoc struct {
	Table Loc // the table's start index
	Row   int
	Col   int
}

func (c CellLoc) json() map[string]any {
	return map[string]any{"tableStartLocation": c.Table.json(), "rowIndex": c.Row, "columnIndex": c.Col}
}

// InsertTable inserts an empty rows×cols table at a location. The API
// puts a paragraph boundary before the table, so the table occupies
// [at+1, …).
func InsertTable(rows, cols int, at Loc) json.RawMessage {
	return raw(map[string]any{"insertTable": map[string]any{"rows": rows, "columns": cols, "location": at.json()}})
}

// InsertTableRow inserts a row above or below the cell's row.
func InsertTableRow(cell CellLoc, below bool) json.RawMessage {
	return raw(map[string]any{"insertTableRow": map[string]any{"tableCellLocation": cell.json(), "insertBelow": below}})
}

// DeleteTableRow deletes the cell's row.
func DeleteTableRow(cell CellLoc) json.RawMessage {
	return raw(map[string]any{"deleteTableRow": map[string]any{"tableCellLocation": cell.json()}})
}

// InsertTableColumn inserts a column left or right of the cell's column.
func InsertTableColumn(cell CellLoc, right bool) json.RawMessage {
	return raw(map[string]any{"insertTableColumn": map[string]any{"tableCellLocation": cell.json(), "insertRight": right}})
}

// DeleteTableColumn deletes the cell's column.
func DeleteTableColumn(cell CellLoc) json.RawMessage {
	return raw(map[string]any{"deleteTableColumn": map[string]any{"tableCellLocation": cell.json()}})
}

func tableRange(cell CellLoc, rowSpan, colSpan int) map[string]any {
	return map[string]any{"tableCellLocation": cell.json(), "rowSpan": rowSpan, "columnSpan": colSpan}
}

// MergeTableCells merges the rowSpan×colSpan block starting at cell.
func MergeTableCells(cell CellLoc, rowSpan, colSpan int) json.RawMessage {
	return raw(map[string]any{"mergeTableCells": map[string]any{"tableRange": tableRange(cell, rowSpan, colSpan)}})
}

// UnmergeTableCells splits merged cells in the block.
func UnmergeTableCells(cell CellLoc, rowSpan, colSpan int) json.RawMessage {
	return raw(map[string]any{"unmergeTableCells": map[string]any{"tableRange": tableRange(cell, rowSpan, colSpan)}})
}

// CellStyleSpec is a set of table-cell formatting changes.
type CellStyleSpec struct {
	Background string // #rrggbb or none
	Align      string // TOP, MIDDLE, BOTTOM (contentAlignment)
	// PaddingPt sets every side; the four below override one side each.
	PaddingPt       *float64
	PaddingTopPt    *float64
	PaddingBottomPt *float64
	PaddingLeftPt   *float64
	PaddingRightPt  *float64
	// Borders take the same shorthand as a paragraph's, minus padding:
	// "1pt solid #cccccc", or "none". Border sets all four sides.
	Border       string
	BorderTop    string
	BorderBottom string
	BorderLeft   string
	BorderRight  string
}

// cellSides pairs each border and padding field with its API name.
func (s CellStyleSpec) cellSides() []struct {
	raw     string
	padding *float64
	border  string
	pad     string
} {
	return []struct {
		raw     string
		padding *float64
		border  string
		pad     string
	}{
		{firstSet(s.BorderTop, s.Border), firstFloat(s.PaddingTopPt, s.PaddingPt), "borderTop", "paddingTop"},
		{firstSet(s.BorderBottom, s.Border), firstFloat(s.PaddingBottomPt, s.PaddingPt), "borderBottom", "paddingBottom"},
		{firstSet(s.BorderLeft, s.Border), firstFloat(s.PaddingLeftPt, s.PaddingPt), "borderLeft", "paddingLeft"},
		{firstSet(s.BorderRight, s.Border), firstFloat(s.PaddingRightPt, s.PaddingPt), "borderRight", "paddingRight"},
	}
}

// firstFloat is the per-side value when given, else the all-sides one.
func firstFloat(side, all *float64) *float64 {
	if side != nil {
		return side
	}
	return all
}

// IsZero reports whether the spec changes nothing.
func (s CellStyleSpec) IsZero() bool { return s == CellStyleSpec{} }

var cellAlignments = map[string]bool{"TOP": true, "MIDDLE": true, "BOTTOM": true}

// Validate checks enum and colour values.
func (s CellStyleSpec) Validate() error {
	if !ValidColor(s.Background) {
		return fmt.Errorf("background must be #rrggbb or none")
	}
	if s.Align != "" && !cellAlignments[s.Align] {
		return fmt.Errorf("align must be TOP, MIDDLE or BOTTOM")
	}
	for _, p := range []*float64{s.PaddingPt, s.PaddingTopPt, s.PaddingBottomPt, s.PaddingLeftPt, s.PaddingRightPt} {
		if p != nil && (*p < 0 || *p > 100) {
			return fmt.Errorf("padding_pt must be between 0 and 100")
		}
	}
	for _, side := range s.cellSides() {
		if _, _, err := ParseBorder(side.raw); err != nil {
			return err
		}
	}
	return nil
}

func (s CellStyleSpec) body() (map[string]any, []string) {
	style := map[string]any{}
	var fields []string
	if c, ok := colorJSON(s.Background); ok {
		style["backgroundColor"] = c
		fields = append(fields, "backgroundColor")
	} else if s.Background == "none" {
		fields = append(fields, "backgroundColor")
	}
	if s.Align != "" {
		style["contentAlignment"] = s.Align
		fields = append(fields, "contentAlignment")
	}
	for _, side := range s.cellSides() {
		if side.padding != nil {
			style[side.pad] = pt(*side.padding)
			fields = append(fields, side.pad)
		}
		if b, ok, err := ParseBorder(side.raw); ok && err == nil {
			style[side.border] = b.json(nil)
			fields = append(fields, side.border)
		}
	}
	return style, fields
}

// UpdateTableCellStyle styles the rowSpan×colSpan block starting at cell.
func UpdateTableCellStyle(cell CellLoc, rowSpan, colSpan int, s CellStyleSpec) json.RawMessage {
	style, fields := s.body()
	return raw(map[string]any{"updateTableCellStyle": map[string]any{"tableRange": tableRange(cell, rowSpan, colSpan), "tableCellStyle": style, "fields": strings.Join(fields, ",")}})
}

// PinTableHeaderRows pins the first n rows as repeating header rows.
func PinTableHeaderRows(table Loc, n int) json.RawMessage {
	return raw(map[string]any{"pinTableHeaderRows": map[string]any{"tableStartLocation": table.json(), "pinnedHeaderRowsCount": n}})
}

// UpdateTableColumnProperties sizes the named columns: a fixed width in
// points, or an even share of the table when even is set.
func UpdateTableColumnProperties(table Loc, columns []int, widthPt *float64, even bool) json.RawMessage {
	props := map[string]any{}
	fields := []string{"widthType"}
	if even {
		props["widthType"] = "EVENLY_DISTRIBUTED"
	} else {
		props["widthType"] = "FIXED_WIDTH"
		props["width"] = pt(*widthPt)
		fields = append(fields, "width")
	}
	return raw(map[string]any{"updateTableColumnProperties": map[string]any{
		"tableStartLocation": table.json(), "columnIndices": columns,
		"tableColumnProperties": props, "fields": strings.Join(fields, ",")}})
}

// UpdateTableRowStyle styles the named rows: a minimum height and
// whether the row may break across pages. TableRowStyle.tableHeader is
// in the schema but the API answers "Unallowed field" for it (confirmed
// live, 2026-09-03); pinTableHeaderRows is how a header row is set.
func UpdateTableRowStyle(table Loc, rows []int, minHeightPt *float64, preventOverflow *bool) json.RawMessage {
	style := map[string]any{}
	var fields []string
	set := func(name string, v any) {
		style[name] = v
		fields = append(fields, name)
	}
	if minHeightPt != nil {
		set("minRowHeight", pt(*minHeightPt))
	}
	if preventOverflow != nil {
		set("preventOverflow", *preventOverflow)
	}
	return raw(map[string]any{"updateTableRowStyle": map[string]any{
		"tableStartLocation": table.json(), "rowIndices": rows,
		"tableRowStyle": style, "fields": strings.Join(fields, ",")}})
}

// ReplaceImage swaps an image's source, keeping its place in the text.
// Crop asks Google to centre-crop the new image into the old one's
// frame instead of resizing the frame to it.
func ReplaceImage(objectID, uri string, crop bool, tabID string) json.RawMessage {
	req := map[string]any{"imageObjectId": objectID, "uri": uri}
	if crop {
		req["imageReplaceMethod"] = "CENTER_CROP"
	}
	if tabID != "" {
		req["tabId"] = tabID
	}
	return raw(map[string]any{"replaceImage": req})
}

// DeletePositionedObject removes a floating object, which no range
// covers and so no text deletion can reach.
func DeletePositionedObject(objectID, tabID string) json.RawMessage {
	req := map[string]any{"objectId": objectID}
	if tabID != "" {
		req["tabId"] = tabID
	}
	return raw(map[string]any{"deletePositionedObject": req})
}

// InsertInlineImage inserts an image fetched from a public URL. Width
// and height in points are optional; the API keeps the aspect ratio
// when only one is given.
func InsertInlineImage(uri string, at Loc, widthPt, heightPt float64) json.RawMessage {
	req := map[string]any{"uri": uri, "location": at.json()}
	size := map[string]any{}
	if widthPt > 0 {
		size["width"] = map[string]any{"magnitude": widthPt, "unit": "PT"}
	}
	if heightPt > 0 {
		size["height"] = map[string]any{"magnitude": heightPt, "unit": "PT"}
	}
	if len(size) > 0 {
		req["objectSize"] = size
	}
	return raw(map[string]any{"insertInlineImage": req})
}

// TabProperties are the mutable properties of a tab.
type TabProperties struct {
	Title    string
	Index    *int
	ParentID string
	Emoji    string
}

func (p TabProperties) body() (map[string]any, []string) {
	props := map[string]any{}
	var fields []string
	if p.Title != "" {
		props["title"] = p.Title
		fields = append(fields, "title")
	}
	if p.Index != nil {
		props["index"] = *p.Index
		fields = append(fields, "index")
	}
	if p.ParentID != "" {
		props["parentTabId"] = p.ParentID
		fields = append(fields, "parentTabId")
	}
	if p.Emoji != "" {
		props["iconEmoji"] = p.Emoji
		fields = append(fields, "iconEmoji")
	}
	return props, fields
}

// AddDocumentTab adds a tab; the reply carries the new tabProperties.
func AddDocumentTab(p TabProperties) json.RawMessage {
	props, _ := p.body()
	return raw(map[string]any{"addDocumentTab": map[string]any{"tabProperties": props}})
}

// UpdateDocumentTabProperties renames or moves a tab.
func UpdateDocumentTabProperties(tabID string, p TabProperties) json.RawMessage {
	props, fields := p.body()
	props["tabId"] = tabID
	return raw(map[string]any{"updateDocumentTabProperties": map[string]any{"tabProperties": props, "fields": strings.Join(fields, ",")}})
}

// DeleteTab deletes a tab and everything in it.
func DeleteTab(tabID string) json.RawMessage {
	return raw(map[string]any{"deleteTab": map[string]any{"tabId": tabID}})
}

// DeleteHeader removes a header segment.
func DeleteHeader(headerID, tabID string) json.RawMessage {
	return deleteSegment("deleteHeader", "headerId", headerID, tabID)
}

// DeleteFooter removes a footer segment.
func DeleteFooter(footerID, tabID string) json.RawMessage {
	return deleteSegment("deleteFooter", "footerId", footerID, tabID)
}

func deleteSegment(kind, idField, id, tabID string) json.RawMessage {
	req := map[string]any{idField: id}
	if tabID != "" {
		req["tabId"] = tabID
	}
	return raw(map[string]any{kind: req})
}

// InsertPerson inserts a people chip. The email is required; the name
// is what the chip shows when set.
func InsertPerson(name, email string, at Loc) json.RawMessage {
	props := map[string]any{"email": email}
	if name != "" {
		props["name"] = name
	}
	return raw(map[string]any{"insertPerson": map[string]any{"personProperties": props, "location": at.json()}})
}

// InsertRichLink inserts a smart chip linking to a Google resource (a
// Drive file, a Calendar event, a YouTube video).
func InsertRichLink(uri, title string, at Loc) json.RawMessage {
	props := map[string]any{"uri": uri}
	if title != "" {
		props["title"] = title
	}
	return raw(map[string]any{"insertRichLink": map[string]any{"richLinkProperties": props, "location": at.json()}})
}

// DateSpec describes a date chip.
type DateSpec struct {
	Timestamp  string // RFC 3339
	TimeZoneID string // CLDR id such as Europe/Copenhagen; default UTC
	DateFormat string // a DateFormats key; "" for the API default
	TimeFormat string // TIME_FORMAT_* enum
}

// DateFormats maps the friendly date_format names to the API enum.
var DateFormats = map[string]string{
	"": "", "iso": "DATE_FORMAT_ISO8601", "full": "DATE_FORMAT_MONTH_DAY_FULL", "abbreviated": "DATE_FORMAT_MONTH_DAY_YEAR_ABBREVIATED", "month_day": "DATE_FORMAT_MONTH_DAY_ABBREVIATED",
}

// InsertDate inserts a date chip.
func InsertDate(d DateSpec, at Loc) json.RawMessage {
	props := map[string]any{"timestamp": d.Timestamp}
	if d.TimeZoneID != "" {
		props["timeZoneId"] = d.TimeZoneID
	}
	if f := DateFormats[d.DateFormat]; f != "" {
		props["dateFormat"] = f
	}
	if d.TimeFormat != "" {
		props["timeFormat"] = d.TimeFormat
	}
	return raw(map[string]any{"insertDate": map[string]any{"dateElementProperties": props, "location": at.json()}})
}
