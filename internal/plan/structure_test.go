package plan

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStructureRequestBuilders(t *testing.T) {
	tbl := Loc{Index: 133, TabID: "t"}
	cell := CellLoc{Table: tbl, Row: 1, Col: 0}
	idx := 2
	checks := map[string]json.RawMessage{
		"insertTable":                 InsertTable(2, 3, Loc{Index: 80, TabID: "t"}),
		"insertTableRow":              InsertTableRow(cell, true),
		"deleteTableRow":              DeleteTableRow(cell),
		"insertTableColumn":           InsertTableColumn(cell, false),
		"deleteTableColumn":           DeleteTableColumn(cell),
		"mergeTableCells":             MergeTableCells(cell, 1, 2),
		"unmergeTableCells":           UnmergeTableCells(cell, 1, 2),
		"updateTableCellStyle":        UpdateTableCellStyle(cell, 1, 1, CellStyleSpec{Background: "#ffff00", Align: "MIDDLE", PaddingPt: floatp(4)}),
		"pinTableHeaderRows":          PinTableHeaderRows(tbl, 1),
		"insertInlineImage":           InsertInlineImage("https://example.test/a.png", Loc{Index: 5}, 200, 0),
		"addDocumentTab":              AddDocumentTab(TabProperties{Title: "New", Index: &idx, ParentID: "t.0", Emoji: "📎"}),
		"updateDocumentTabProperties": UpdateDocumentTabProperties("t.1", TabProperties{Title: "Renamed"}),
		"deleteTab":                   DeleteTab("t.1"),
		"deleteHeader":                DeleteHeader("h1", "t"),
		"deleteFooter":                DeleteFooter("f1", ""),
		"insertPerson":                InsertPerson("Ann", "ann@example.test", Loc{Index: 5}),
		"insertRichLink":              InsertRichLink("https://docs.google.com/x", "Doc", Loc{Index: 5}),
		"insertDate":                  InsertDate(DateSpec{Timestamp: "2026-09-03T00:00:00Z", TimeZoneID: "Europe/Copenhagen", DateFormat: "iso"}, Loc{Index: 5}),
	}
	for kind, req := range checks {
		if Kind(req) != kind {
			t.Errorf("%s: kind = %s (%s)", kind, Kind(req), req)
		}
	}
	row := view(t, checks["insertTableRow"]).body
	loc := row["tableCellLocation"].(map[string]any)
	if loc["rowIndex"] != 1.0 || loc["columnIndex"] != 0.0 || loc["tableStartLocation"].(map[string]any)["index"] != 133.0 || loc["tableStartLocation"].(map[string]any)["tabId"] != "t" || row["insertBelow"] != true {
		t.Fatalf("insertTableRow = %v", row)
	}
	merge := view(t, checks["mergeTableCells"]).body["tableRange"].(map[string]any)
	if merge["rowSpan"] != 1.0 || merge["columnSpan"] != 2.0 {
		t.Fatalf("merge = %v", merge)
	}
	cs := view(t, checks["updateTableCellStyle"]).body
	if cs["fields"] != "backgroundColor,contentAlignment,paddingTop,paddingBottom,paddingLeft,paddingRight" {
		t.Fatalf("cell style fields = %v", cs["fields"])
	}
	img := view(t, checks["insertInlineImage"]).body
	if img["uri"] != "https://example.test/a.png" || img["objectSize"].(map[string]any)["height"] != nil || img["objectSize"].(map[string]any)["width"].(map[string]any)["magnitude"] != 200.0 {
		t.Fatalf("image = %v", img)
	}
	if strings.Contains(string(InsertInlineImage("u", Loc{Index: 1}, 0, 0)), "objectSize") {
		t.Fatal("image without size should omit objectSize")
	}
	add := view(t, checks["addDocumentTab"]).body["tabProperties"].(map[string]any)
	if add["title"] != "New" || add["index"] != 2.0 || add["parentTabId"] != "t.0" || add["iconEmoji"] != "📎" {
		t.Fatalf("addDocumentTab = %v", add)
	}
	upd := view(t, checks["updateDocumentTabProperties"]).body
	if upd["fields"] != "title" || upd["tabProperties"].(map[string]any)["tabId"] != "t.1" {
		t.Fatalf("updateDocumentTabProperties = %v", upd)
	}
	if strings.Contains(string(checks["deleteFooter"]), "tabId") || !strings.Contains(string(checks["deleteHeader"]), `"tabId":"t"`) {
		t.Fatal("header/footer tab ids")
	}
	date := view(t, checks["insertDate"]).body["dateElementProperties"].(map[string]any)
	if date["timeZoneId"] != "Europe/Copenhagen" || date["timeFormat"] != nil || date["dateFormat"] != "DATE_FORMAT_ISO8601" {
		t.Fatalf("date = %v", date)
	}
	for _, bad := range []CellStyleSpec{{Background: "yellow"}, {Align: "LEFT"}, {PaddingPt: floatp(-1)}} {
		if bad.Validate() == nil {
			t.Errorf("%+v should not validate", bad)
		}
	}
	if (CellStyleSpec{Background: "none", Align: "TOP"}).Validate() != nil || !(CellStyleSpec{}).IsZero() {
		t.Fatal("CellStyleSpec")
	}
}
