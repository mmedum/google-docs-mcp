package plan

import (
	"strings"
	"testing"
)

func boolp(b bool) *bool { return &b }
func intp(i int) *int    { return &i }

func TestPageRequest(t *testing.T) {
	spec := PageSpec{
		WidthPt: 595, HeightPt: 842,
		PageMargins:     PageMargins{TopPt: floatp(56), LeftPt: floatp(0)},
		Background:      "#eeeeee",
		PageNumberStart: intp(3),
		Landscape:       boolp(true),
	}
	v := view(t, UpdateDocumentStyle(spec, "t.0"))
	if v.kind != "updateDocumentStyle" || v.body["tabId"] != "t.0" {
		t.Fatalf("page = %+v", v.body)
	}
	fields := v.body["fields"].(string)
	// Only what the spec sets is in the mask, so nothing else is reset.
	for _, want := range []string{"pageSize", "marginTop", "marginLeft", "background", "pageNumberStart", "flipPageOrientation"} {
		if !strings.Contains(fields, want) {
			t.Errorf("fields %q lacks %s", fields, want)
		}
	}
	if strings.Contains(fields, "marginBottom") || strings.Contains(fields, "marginRight") {
		t.Errorf("fields %q names a margin the spec left alone", fields)
	}
	style := v.body["documentStyle"].(map[string]any)
	size := style["pageSize"].(map[string]any)["width"].(map[string]any)
	if size["magnitude"] != 595.0 || size["unit"] != "PT" {
		t.Errorf("page size = %v", size)
	}
	// A zero margin is a margin, not an absent one.
	if m := style["marginLeft"].(map[string]any); m["magnitude"] != 0.0 {
		t.Errorf("margin left = %v", m)
	}
	// DocumentStyle.background wraps an OptionalColor, which itself wraps
	// the colour: background.color.color.rgbColor.
	bg := style["background"].(map[string]any)["color"].(map[string]any)["color"].(map[string]any)
	if _, ok := bg["rgbColor"]; !ok {
		t.Errorf("background = %v", style["background"])
	}
}

func TestPageAndSectionValidation(t *testing.T) {
	big := 5000.0
	cases := []struct {
		name string
		err  string
		run  func() error
	}{
		{"empty page", "", func() error { return PageSpec{}.Validate() }},
		{"half a page size", "set together", func() error { return PageSpec{WidthPt: 595}.Validate() }},
		{"huge page", "0 and 4000", func() error { return PageSpec{WidthPt: big, HeightPt: big}.Validate() }},
		{"bad colour", "#rrggbb", func() error { return PageSpec{Background: "blue"}.Validate() }},
		{"negative margin", "0 and 720", func() error {
			return PageSpec{PageMargins: PageMargins{TopPt: floatp(-1)}}.Validate()
		}},
		{"negative header margin", "0 and 720", func() error {
			return PageSpec{MarginHeaderPt: floatp(-50)}.Validate()
		}},
		{"too many columns", "between 1 and 3", func() error { return SectionSpec{Columns: 9}.Validate() }},
		{"gap without columns", "needs columns", func() error { return SectionSpec{ColumnGapPt: floatp(12)}.Validate() }},
		{"bad separator", "none or between", func() error { return SectionSpec{ColumnSeparator: "DOTTED"}.Validate() }},
		{"unknown named style", "must be one of", func() error { return NamedStyleSpec{Style: "HEADING_9"}.Validate() }},
		{"no named style", "names the named style", func() error { return NamedStyleSpec{}.Validate() }},
	}
	for _, tc := range cases {
		err := tc.run()
		switch {
		case tc.err == "" && err != nil:
			t.Errorf("%s: %v", tc.name, err)
		case tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)):
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.err)
		}
	}
}

func TestSectionAndNamedStyleRequests(t *testing.T) {
	sec := SectionSpec{Columns: 2, ColumnGapPt: floatp(18),
		ColumnSeparator: "BETWEEN_EACH_COLUMN", PageMargins: PageMargins{LeftPt: floatp(36)}}
	v := view(t, UpdateSectionStyle(Rng{Start: 10, End: 40, TabID: "t"}, sec))
	if v.kind != "updateSectionStyle" || v.rng != [2]int64{10, 40} {
		t.Fatalf("section = %+v", v)
	}
	style := v.body["sectionStyle"].(map[string]any)
	cols := style["columnProperties"].([]any)
	if len(cols) != 2 || cols[0].(map[string]any)["paddingEnd"].(map[string]any)["magnitude"] != 18.0 {
		t.Errorf("columns = %v", cols)
	}
	if style["columnSeparatorStyle"] != "BETWEEN_EACH_COLUMN" {
		t.Errorf("section style = %v", style)
	}
	// SectionStyle.sectionType is output only, so it must never reach the
	// mask: a section is continuous or starts a page by its break.
	if _, ok := style["sectionType"]; ok || strings.Contains(v.body["fields"].(string), "sectionType") {
		t.Errorf("a read-only field was sent: %v %v", style, v.body["fields"])
	}

	brk := view(t, InsertSectionBreak(Loc{Index: 40, TabID: "t"}, ""))
	if brk.kind != "insertSectionBreak" || brk.index != 40 || brk.body["sectionType"] != "NEXT_PAGE" {
		t.Errorf("section break = %+v", brk)
	}

	ns := view(t, UpdateNamedStyle(NamedStyleSpec{Style: "HEADING_2",
		Text: TextStyleSpec{Bold: boolp(true), SizePt: 16},
		Para: ParagraphStyleSpec{SpaceAbovePt: floatp(12)}}, ""))
	if ns.kind != "updateNamedStyle" {
		t.Fatalf("named style = %+v", ns)
	}
	if _, ok := ns.body["tabId"]; ok {
		t.Error("an empty tab id should be left out, which means the first tab")
	}
	fields := ns.body["fields"].(string)
	// The mask is rooted at the named style, so its style fields are
	// named through textStyle and paragraphStyle.
	// The parent counts as well as the leaf, per the request reference.
	for _, want := range []string{"namedStyleType", "textStyle,", "textStyle.bold", "textStyle.fontSize", "paragraphStyle,", "paragraphStyle.spaceAbove"} {
		if !strings.Contains(fields, want) {
			t.Errorf("fields %q lacks %s", fields, want)
		}
	}
	style = ns.body["namedStyle"].(map[string]any)
	if style["namedStyleType"] != "HEADING_2" || style["textStyle"].(map[string]any)["bold"] != true {
		t.Errorf("named style = %v", style)
	}
}

func TestNamedRangeRequests(t *testing.T) {
	create := view(t, CreateNamedRange("key finding", Rng{Start: 94, End: 106, TabID: "t"}))
	if create.kind != "createNamedRange" || create.body["name"] != "key finding" || create.rng != [2]int64{94, 106} {
		t.Fatalf("create = %+v", create)
	}
	del := view(t, DeleteNamedRange(NamedRangeParams{ID: "kix.nr1"}, "t.0"))
	tabs := del.body["tabsCriteria"].(map[string]any)["tabIds"].([]any)
	if del.body["namedRangeId"] != "kix.nr1" || tabs[0] != "t.0" {
		t.Fatalf("delete = %+v", del.body)
	}
	// Without a tab a named-range request reaches every tab, so the
	// service always names one.
	if _, ok := view(t, DeleteNamedRange(NamedRangeParams{Name: "x"}, "")).body["tabsCriteria"]; ok {
		t.Error("no tab should mean no criteria")
	}
	rep := view(t, ReplaceNamedRangeContent(NamedRangeParams{Name: "key finding", Text: "new text"}, "t.0"))
	if rep.kind != "replaceNamedRangeContent" || rep.body["namedRangeName"] != "key finding" || rep.body["text"] != "new text" {
		t.Fatalf("replace = %+v", rep.body)
	}
}

func TestNamedRangeValidation(t *testing.T) {
	cases := []struct {
		name string
		op   Op
		err  string
	}{
		{"create without a name", Op{Kind: OpCreateNamedRange}, "needs a name"},
		{"create with an id", Op{Kind: OpCreateNamedRange, NamedRange: NamedRangeParams{Name: "a", ID: "b"}}, "only a name"},
		{"delete with neither", Op{Kind: OpDeleteNamedRange}, "by name or by id"},
		{"delete with both", Op{Kind: OpDeleteNamedRange, NamedRange: NamedRangeParams{Name: "a", ID: "b"}}, "by name or by id"},
		{"a name too long", Op{Kind: OpCreateNamedRange, NamedRange: NamedRangeParams{Name: strings.Repeat("x", 257)}}, "at most 256"},
		{"newline in the text", Op{Kind: OpReplaceNamedRange, NamedRange: NamedRangeParams{Name: "a", Text: "one\ntwo"}}, "cannot contain a newline"},
	}
	for _, tc := range cases {
		err := validateNamedRange(&tc.op)
		if err == nil || !strings.Contains(err.Error(), tc.err) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.err)
		}
	}
}

func TestTableLineStyleRequests(t *testing.T) {
	tbl := Loc{Index: 133, TabID: "t"}
	cols := view(t, UpdateTableColumnProperties(tbl, []int{0, 2}, floatp(120), false))
	if cols.kind != "updateTableColumnProperties" {
		t.Fatalf("columns = %+v", cols)
	}
	props := cols.body["tableColumnProperties"].(map[string]any)
	if props["widthType"] != "FIXED_WIDTH" || props["width"].(map[string]any)["magnitude"] != 120.0 {
		t.Errorf("column properties = %v", props)
	}
	if idx := cols.body["columnIndices"].([]any); len(idx) != 2 || idx[1] != 2.0 {
		t.Errorf("column indices = %v", idx)
	}
	even := view(t, UpdateTableColumnProperties(tbl, []int{0}, nil, true))
	props = even.body["tableColumnProperties"].(map[string]any)
	if props["widthType"] != "EVENLY_DISTRIBUTED" {
		t.Errorf("even columns = %v", props)
	}
	if _, ok := props["width"]; ok {
		t.Error("an even share takes no width")
	}
	rows := view(t, UpdateTableRowStyle(tbl, []int{0}, floatp(30), boolp(true)))
	style := rows.body["tableRowStyle"].(map[string]any)
	if rows.kind != "updateTableRowStyle" || style["minRowHeight"].(map[string]any)["magnitude"] != 30.0 ||
		style["preventOverflow"] != true {
		t.Fatalf("rows = %+v", rows.body)
	}
	// tableHeader is in the schema but the API answers "Unallowed
	// field"; pin_header_rows is how a header row is set.
	if _, ok := style["tableHeader"]; ok {
		t.Error("tableHeader was sent")
	}
}

func TestImageObjectRequests(t *testing.T) {
	rep := view(t, ReplaceImage("kix.img1", "https://example.test/new.png", true, "t.0"))
	if rep.kind != "replaceImage" || rep.body["imageObjectId"] != "kix.img1" ||
		rep.body["imageReplaceMethod"] != "CENTER_CROP" || rep.body["tabId"] != "t.0" {
		t.Fatalf("replace = %+v", rep.body)
	}
	if _, ok := view(t, ReplaceImage("kix.img1", "https://example.test/new.png", false, "")).body["imageReplaceMethod"]; ok {
		t.Error("without crop the method should be left to Google")
	}
	del := view(t, DeletePositionedObject("kix.pos1", ""))
	if del.kind != "deletePositionedObject" || del.body["objectId"] != "kix.pos1" {
		t.Fatalf("delete = %+v", del.body)
	}
}

// TestBorderShorthand covers the form callers write borders in: tokens
// in any order, defaults for the parts left out, "none" to clear, and
// the errors for the rest.
func TestBorderShorthand(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		width float64
		dash  string
		color string
	}{
		{"1pt solid #cccccc", 1, "SOLID", "#cccccc"},
		{"#ff0000 dot 2.5pt", 2.5, "DOT", "#ff0000"},
		{"dash", 1, "DASH", "#000000"},
		{"3pt", 3, "SOLID", "#000000"},
	} {
		b, ok, err := ParseBorder(tc.raw)
		if !ok || err != nil || b.WidthPt != tc.width || b.DashStyle != tc.dash || b.Color != tc.color {
			t.Errorf("%q: %+v ok=%t err=%v", tc.raw, b, ok, err)
		}
	}
	if b, ok, err := ParseBorder("none"); !ok || err != nil || !b.Clear {
		t.Errorf("none: %+v %t %v", b, ok, err)
	}
	if _, ok, err := ParseBorder("   "); ok || err != nil {
		t.Errorf("blank should be unset: %t %v", ok, err)
	}
	for _, bad := range []string{"1pt dotted #ccc", "#12345", "-2pt", "1pt solid #cccccc extra"} {
		if _, _, err := ParseBorder(bad); err == nil {
			t.Errorf("%q should not parse", bad)
		}
	}
}

// TestParagraphBordersAndShading checks the request a full paragraph
// style produces: every side, the padding that goes on each, the
// shading, and the fields mask naming all of them.
func TestParagraphBordersAndShading(t *testing.T) {
	pad := 4.0
	spec := ParagraphStyleSpec{Border: "1pt solid #cccccc", BorderBottom: "2pt dash #ff0000",
		BorderBetween: "none", BorderPaddingPt: &pad, Shading: "#eeeeee",
		Direction: "RIGHT_TO_LEFT", SpacingMode: "COLLAPSE_LISTS", IndentEndPt: &pad,
		KeepLinesTogether: new(true), AvoidWidowAndOrphan: new(true)}
	if err := spec.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	v := view(t, UpdateParagraphStyle(Rng{Start: 1, End: 2}, spec))
	style := v.body["paragraphStyle"].(map[string]any)
	top := style["borderTop"].(map[string]any)
	if top["dashStyle"] != "SOLID" || top["padding"].(map[string]any)["magnitude"] != 4.0 {
		t.Fatalf("top border: %v", top)
	}
	if bottom := style["borderBottom"].(map[string]any); bottom["dashStyle"] != "DASH" {
		t.Fatalf("per-side border overrides the shorthand: %v", bottom)
	}
	// Clearing sends an empty border, which is how the API removes one.
	if between := style["borderBetween"].(map[string]any); len(between) != 0 {
		t.Fatalf("border between should be cleared: %v", between)
	}
	if style["shading"].(map[string]any)["backgroundColor"] == nil || style["direction"] != "RIGHT_TO_LEFT" {
		t.Fatalf("shading and direction: %v", style)
	}
	for _, want := range []string{"borderTop", "borderBottom", "borderLeft", "borderRight", "borderBetween",
		"shading", "direction", "spacingMode", "indentEnd", "keepLinesTogether", "avoidWidowAndOrphan"} {
		if !strings.Contains(v.body["fields"].(string), want) {
			t.Errorf("fields mask lacks %s: %s", want, v.body["fields"])
		}
	}
	if (ParagraphStyleSpec{Direction: "SIDEWAYS"}).Validate() == nil ||
		(ParagraphStyleSpec{SpacingMode: "NOPE"}).Validate() == nil ||
		(ParagraphStyleSpec{Shading: "red"}).Validate() == nil ||
		(ParagraphStyleSpec{Border: "1pt dotted"}).Validate() == nil {
		t.Fatal("invalid values should be refused")
	}
}
