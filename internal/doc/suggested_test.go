package doc_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mmedum/google-docs-mcp/internal/doc"
	"github.com/mmedum/google-docs-mcp/internal/gdocs"
)

// styled is a run carrying one pending suggested text-style change. The
// style is deliberately fuller than the state, which is the shape the
// API really sends: it reports the style as it would be once accepted,
// every inherited property filled in, so a reader that trusts the style
// reports five changes where the person asked for one.
func styled(content, id string, state *gdocs.TextStyleSuggestionState) *gdocs.ParagraphElement {
	return &gdocs.ParagraphElement{TextRun: &gdocs.TextRun{
		Content:   content,
		TextStyle: &gdocs.TextStyle{},
		SuggestedStyle: gdocs.SuggestedStyle{SuggestedTextStyleChanges: map[string]gdocs.SuggestedTextStyle{
			id: {
				TextStyle: &gdocs.TextStyle{Bold: true, Italic: true, Underline: true,
					FontSize: &gdocs.Dimension{Magnitude: 11, Unit: "PT"}},
				TextStyleSuggestionState: state,
			},
		}},
	}}
}

// TestSuggestedStyleReadsTheStateNotTheStyle is the heart of #46: a
// style-only suggestion must reach the model, and it must report the
// properties the person asked for rather than every property the API
// echoes back.
func TestSuggestedStyleReadsTheStateNotTheStyle(t *testing.T) {
	w := &gdocs.Document{
		DocumentID: "1SyntheticFixtureDocumentIdXXXXXXXXXXXXXXXXXX", Title: "Styles", RevisionID: "r",
		Tabs: []*gdocs.Tab{{
			TabProperties: &gdocs.TabProperties{TabID: "t.0", Title: "Tab 1"},
			DocumentTab: &gdocs.DocumentTab{Body: &gdocs.Body{Content: []*gdocs.StructuralElement{
				{Paragraph: &gdocs.Paragraph{
					ParagraphStyle: &gdocs.ParagraphStyle{NamedStyleType: "NORMAL_TEXT"},
					Elements: []*gdocs.ParagraphElement{
						styled("bold me", "suggest.bold", &gdocs.TextStyleSuggestionState{BoldSuggested: true}),
						styled(" and this", "suggest.two", &gdocs.TextStyleSuggestionState{
							ItalicSuggested: true, WeightedFontFamilySuggested: true}),
						{TextRun: &gdocs.TextRun{Content: " plain\n", TextStyle: &gdocs.TextStyle{}}},
					},
					SuggestedParagraphStyleChanges: map[string]gdocs.SuggestedParagraphStyle{
						"suggest.para": {ParagraphStyleSuggestionState: &gdocs.ParagraphStyleSuggestionState{
							AlignmentSuggested: true,
							ShadingSuggestionState: &gdocs.ShadingSuggestionState{
								BackgroundColorSuggested: true,
							},
						}},
					},
				}},
			}}},
		}},
	}
	d, err := doc.Parse(w)
	if err != nil {
		t.Fatal(err)
	}
	runs := d.Tabs[0].Body.Blocks[0].Paragraph.Runs
	if got := runs[0].StyleChanges; len(got) != 1 || got[0].ID != "suggest.bold" ||
		strings.Join(got[0].Props, ",") != "bold" {
		t.Errorf("first run: want one change suggest.bold setting bold, got %+v", got)
	}
	// The state names two properties and the style four; the model must
	// follow the state.
	if got := runs[1].StyleChanges; len(got) != 1 ||
		strings.Join(got[0].Props, ",") != "italic,weightedFontFamily" {
		t.Errorf("second run: want italic,weightedFontFamily from the state, got %+v", got)
	}
	if got := runs[2].StyleChanges; len(got) != 0 {
		t.Errorf("a run with no suggestion must carry no change, got %+v", got)
	}
	// A nested state is named for the thing it describes.
	para := d.Tabs[0].Body.Blocks[0].Paragraph.StyleChanges
	if len(para) != 1 || strings.Join(para[0].Props, ",") != "alignment,shading.backgroundColor" {
		t.Errorf("paragraph: want alignment and shading.backgroundColor, got %+v", para)
	}

	// And the whole point: the document-level index sees them, where a
	// walk over inserted and deleted ids sees nothing at all.
	for _, r := range runs {
		if len(r.Inserted) != 0 || len(r.Deleted) != 0 {
			t.Fatal("the fixture must hold no inserted or deleted text, or it proves nothing")
		}
	}
	if len(d.FormatSuggestions) != 3 {
		t.Fatalf("want 3 formatting suggestions indexed, got %d: %+v", len(d.FormatSuggestions), d.FormatSuggestions)
	}
	first := d.FormatSuggestions[0]
	if first.Target != "paragraph" || first.ID != "suggest.para" || first.Handle == "" {
		t.Errorf("paragraph suggestion should come first with a handle, got %+v", first)
	}
	if last := d.FormatSuggestions[2]; last.Target != "text" || last.Text != "and this" {
		t.Errorf("want the second run's suggestion last, got %+v", last)
	}
	if got := doc.Props(runs[1].StyleChanges); strings.Join(got, ",") != "italic,weightedFontFamily" {
		t.Errorf("Props: %v", got)
	}
}

// TestSuggestedStyleOnEveryCarrier checks the field is read wherever the
// API can put it, not only on a text run.
func TestSuggestedStyleOnEveryCarrier(t *testing.T) {
	bold := gdocs.SuggestedStyle{SuggestedTextStyleChanges: map[string]gdocs.SuggestedTextStyle{
		"s1": {TextStyleSuggestionState: &gdocs.TextStyleSuggestionState{BoldSuggested: true}},
	}}
	els := []*gdocs.ParagraphElement{
		{TextRun: &gdocs.TextRun{Content: "t", SuggestedStyle: bold}},
		{PageBreak: &gdocs.Break{SuggestedStyle: bold}},
		{ColumnBreak: &gdocs.Break{SuggestedStyle: bold}},
		{HorizontalRule: &gdocs.Break{SuggestedStyle: bold}},
		{AutoText: &gdocs.AutoText{Type: "PAGE_NUMBER", SuggestedStyle: bold}},
		{Person: &gdocs.Person{PersonProperties: &gdocs.PersonProperties{Name: "Ada"}, SuggestedStyle: bold}},
		{RichLink: &gdocs.RichLink{RichLinkProperties: &gdocs.RichLinkProperties{Title: "S"}, SuggestedStyle: bold}},
		{DateElement: &gdocs.DateElement{DateElementProperties: &gdocs.DateElementProperties{DisplayText: "d"}, SuggestedStyle: bold}},
		{FootnoteReference: &gdocs.FootnoteReference{FootnoteID: "f1", SuggestedStyle: bold}},
		{InlineObjectElement: &gdocs.InlineObjectElement{InlineObjectID: "o1", SuggestedStyle: bold}},
	}
	d, err := doc.Parse(&gdocs.Document{
		DocumentID: "1SyntheticFixtureDocumentIdXXXXXXXXXXXXXXXXXX", RevisionID: "r",
		Tabs: []*gdocs.Tab{{
			TabProperties: &gdocs.TabProperties{TabID: "t.0"},
			DocumentTab: &gdocs.DocumentTab{Body: &gdocs.Body{Content: []*gdocs.StructuralElement{
				{Paragraph: &gdocs.Paragraph{ParagraphStyle: &gdocs.ParagraphStyle{NamedStyleType: "NORMAL_TEXT"}, Elements: els}},
			}}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	runs := d.Tabs[0].Body.Blocks[0].Paragraph.Runs
	if len(runs) != len(els) {
		t.Fatalf("want %d runs, got %d", len(els), len(runs))
	}
	for i, r := range runs {
		if len(r.StyleChanges) != 1 || r.StyleChanges[0].ID != "s1" {
			t.Errorf("run %d (%s) lost its suggested text style: %+v", i, r.Kind, r.StyleChanges)
		}
	}
}

// TestSuggestedStyleBeyondText covers the carriers that are not inline
// elements: a table cell, its row, a list, an object and the
// document-wide styles.
func TestSuggestedStyleBeyondText(t *testing.T) {
	cell := &gdocs.TableCell{
		StartIndex: 2, EndIndex: 4,
		Content: []*gdocs.StructuralElement{{Paragraph: &gdocs.Paragraph{
			ParagraphStyle: &gdocs.ParagraphStyle{NamedStyleType: "NORMAL_TEXT"},
			Elements:       []*gdocs.ParagraphElement{{TextRun: &gdocs.TextRun{Content: "c\n"}}},
		}}},
		SuggestedTableCellStyleChanges: map[string]gdocs.SuggestedTableCellStyle{
			"s.cell": {TableCellStyleSuggestionState: &gdocs.TableCellStyleSuggestionState{BackgroundColorSuggested: true}},
		},
	}
	row := &gdocs.TableRow{StartIndex: 1, EndIndex: 5, TableCells: []*gdocs.TableCell{cell},
		SuggestedTableRowStyleChanges: map[string]gdocs.SuggestedTableRowStyle{
			"s.row": {TableRowStyleSuggestionState: &gdocs.TableRowStyleSuggestionState{MinRowHeightSuggested: true}},
		}}
	d, err := doc.Parse(&gdocs.Document{
		DocumentID: "1SyntheticFixtureDocumentIdXXXXXXXXXXXXXXXXXX", RevisionID: "r",
		Tabs: []*gdocs.Tab{{
			TabProperties: &gdocs.TabProperties{TabID: "t.0"},
			DocumentTab: &gdocs.DocumentTab{
				Body: &gdocs.Body{Content: []*gdocs.StructuralElement{
					{Table: &gdocs.Table{Rows: 1, Columns: 1, TableRows: []*gdocs.TableRow{row}}},
				}},
				Lists: map[string]gdocs.List{"list.1": {SuggestedListPropertiesChanges: map[string]gdocs.SuggestedListProperties{
					"s.list": {ListPropertiesSuggestionState: &gdocs.ListPropertiesSuggestionState{
						NestingLevelsSuggestionStates: []gdocs.NestingLevelSuggestionState{{BulletAlignmentSuggested: true}},
					}},
				}}},
				InlineObjects: map[string]gdocs.InlineObject{"obj.1": {SuggestedInlineObjectPropertiesChanges: map[string]gdocs.SuggestedInlineObjectProperties{
					"s.obj": {InlineObjectPropertiesSuggestionState: &gdocs.InlineObjectPropertiesSuggestionState{
						EmbeddedObjectSuggestionState: &gdocs.EmbeddedObjectSuggestionState{TitleSuggested: true},
					}},
				}}},
				SuggestedDocumentStyleChanges: map[string]gdocs.SuggestedDocumentStyle{
					"s.docstyle": {DocumentStyleSuggestionState: &gdocs.DocumentStyleSuggestionState{MarginTopSuggested: true}},
				},
				SuggestedNamedStylesChanges: map[string]gdocs.SuggestedNamedStyles{
					"s.named": {NamedStylesSuggestionState: &gdocs.NamedStylesSuggestionState{
						StylesSuggestionStates: []gdocs.NamedStyleSuggestionState{{
							NamedStyleType:           "HEADING_1",
							TextStyleSuggestionState: &gdocs.TextStyleSuggestionState{BoldSuggested: true},
						}},
					}},
				},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"s.cell":     "table cell",
		"s.row":      "table row",
		"s.list":     "list",
		"s.obj":      "inline object",
		"s.docstyle": "document style",
		"s.named":    "named styles",
	}
	got := map[string]string{}
	for _, fs := range d.FormatSuggestions {
		got[fs.ID] = fs.Target
	}
	for id, target := range want {
		if got[id] != target {
			t.Errorf("suggestion %s: want target %q, got %q (all: %+v)", id, target, got[id], d.FormatSuggestions)
		}
	}
	// A nested state under a repeated field still names a property, or
	// the suggestion would be indexed with an empty Props and dropped.
	for _, fs := range d.FormatSuggestions {
		if len(fs.Props) == 0 {
			t.Errorf("suggestion %s reached the index with no properties", fs.ID)
		}
	}
}

// TestSuggestedPropsUseTheAPIsNames guards the invariant StyleChange's
// doc comment states: a property name must be the API's, so it can be
// compared with an updateTextStyle fields mask with no translation.
//
// Deriving the names from the Go field names broke that on every
// initialism — thirteen properties, of which headingId is one an op can
// set — because the Go name is HeadingIDSuggested and lowering its first
// letter gives "headingID", which no mask ever contains. The names come
// off the json tags now, and this is the test that says so: it walks the
// state types the way the collector does and checks each name against
// the tag it came from.
func TestSuggestedPropsUseTheAPIsNames(t *testing.T) {
	// A state with every initialism-named property set at once, across
	// three different state types and one nested level.
	d, err := doc.Parse(&gdocs.Document{
		DocumentID: "1SyntheticFixtureDocumentIdXXXXXXXXXXXXXXXXXX", RevisionID: "r",
		Tabs: []*gdocs.Tab{{
			TabProperties: &gdocs.TabProperties{TabID: "t.0"},
			DocumentTab: &gdocs.DocumentTab{
				Body: &gdocs.Body{Content: []*gdocs.StructuralElement{
					{Paragraph: &gdocs.Paragraph{
						ParagraphStyle: &gdocs.ParagraphStyle{NamedStyleType: "NORMAL_TEXT"},
						Elements:       []*gdocs.ParagraphElement{{TextRun: &gdocs.TextRun{Content: "t\n"}}},
						SuggestedParagraphStyleChanges: map[string]gdocs.SuggestedParagraphStyle{
							"s.para": {ParagraphStyleSuggestionState: &gdocs.ParagraphStyleSuggestionState{
								HeadingIDSuggested: true,
							}},
						},
						SuggestedBulletChanges: map[string]gdocs.SuggestedBullet{
							"s.bullet": {BulletSuggestionState: &gdocs.BulletSuggestionState{ListIDSuggested: true}},
						},
					}},
				}},
				SuggestedDocumentStyleChanges: map[string]gdocs.SuggestedDocumentStyle{
					"s.doc": {DocumentStyleSuggestionState: &gdocs.DocumentStyleSuggestionState{
						DefaultHeaderIDSuggested: true, FirstPageFooterIDSuggested: true,
					}},
				},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"s.para":   {"headingId"},
		"s.bullet": {"listId"},
		"s.doc":    {"defaultHeaderId", "firstPageFooterId"},
	}
	got := map[string][]string{}
	for _, fs := range d.FormatSuggestions {
		got[fs.ID] = fs.Props
	}
	for id, props := range want {
		if strings.Join(got[id], ",") != strings.Join(props, ",") {
			t.Errorf("suggestion %s: want props %v, got %v", id, props, got[id])
		}
		for _, p := range props {
			if strings.Contains(p, "ID") && strings.Contains(strings.Join(got[id], ","), "ID") {
				t.Errorf("suggestion %s named a property with Go's spelling, not the API's: %v", id, got[id])
			}
		}
	}
	// And the reader-facing form of an initialism is a word, not letters.
	if lbl := doc.PropList([]string{"headingId"}); lbl != "heading id" {
		t.Errorf(`PropList("headingId") = %q, want "heading id"`, lbl)
	}
}

// TestEverySuggestionStateFieldIsNamedByItsTag is the general form: for
// every state type the wire package declares, every bool field's json
// tag must end in "Suggested" and every nested one in "SuggestionState"
// or "SuggestionStates" — the convention the collector walks. A field
// Google adds that breaks it would otherwise be dropped in silence.
func TestEverySuggestionStateFieldIsNamedByItsTag(t *testing.T) {
	types := []any{
		gdocs.TextStyleSuggestionState{}, gdocs.ParagraphStyleSuggestionState{},
		gdocs.BulletSuggestionState{}, gdocs.DocumentStyleSuggestionState{},
		gdocs.NamedStylesSuggestionState{}, gdocs.NamedStyleSuggestionState{},
		gdocs.ListPropertiesSuggestionState{}, gdocs.NestingLevelSuggestionState{},
		gdocs.TableCellStyleSuggestionState{}, gdocs.TableRowStyleSuggestionState{},
		gdocs.InlineObjectPropertiesSuggestionState{}, gdocs.EmbeddedObjectSuggestionState{},
		gdocs.ImagePropertiesSuggestionState{}, gdocs.CropPropertiesSuggestionState{},
		gdocs.PositionedObjectPropertiesSuggestionState{}, gdocs.ShadingSuggestionState{},
		gdocs.SizeSuggestionState{}, gdocs.BackgroundSuggestionState{},
		gdocs.DateElementPropertiesSuggestionState{},
	}
	checked := 0
	for _, v := range types {
		rt := reflect.TypeOf(v)
		for i := range rt.NumField() {
			f := rt.Field(i)
			tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if tag == "" {
				t.Errorf("%s.%s has no json tag, so the collector cannot name it", rt.Name(), f.Name)
				continue
			}
			checked++
			switch f.Type.Kind() {
			case reflect.Bool:
				if !strings.HasSuffix(tag, "Suggested") {
					t.Errorf("%s.%s: bool tag %q does not end in Suggested", rt.Name(), f.Name, tag)
				}
			case reflect.Pointer, reflect.Slice:
				if !strings.HasSuffix(tag, "SuggestionState") && !strings.HasSuffix(tag, "SuggestionStates") {
					t.Errorf("%s.%s: nested tag %q is neither a state nor a list of them", rt.Name(), f.Name, tag)
				}
			default:
				// NamedStyleSuggestionState.namedStyleType is a string
				// saying which style the entry is about, not a change.
				if f.Type.Kind() != reflect.String {
					t.Errorf("%s.%s: unexpected kind %s", rt.Name(), f.Name, f.Type.Kind())
				}
			}
		}
	}
	if checked < 100 {
		t.Fatalf("only %d fields checked; the list of state types has gone stale", checked)
	}
}

// TestRowRestyleIsReportedOncePerRow is the bug a one-column fixture
// hid: parse hangs a row's suggestion on every cell of the row, so a
// three-column row reported one suggestion three times.
func TestRowRestyleIsReportedOncePerRow(t *testing.T) {
	cell := func(text string) *gdocs.TableCell {
		return &gdocs.TableCell{Content: []*gdocs.StructuralElement{{Paragraph: &gdocs.Paragraph{
			ParagraphStyle: &gdocs.ParagraphStyle{NamedStyleType: "NORMAL_TEXT"},
			Elements:       []*gdocs.ParagraphElement{{TextRun: &gdocs.TextRun{Content: text + "\n"}}},
		}}}}
	}
	row := &gdocs.TableRow{
		TableCells: []*gdocs.TableCell{cell("a"), cell("b"), cell("c")},
		SuggestedTableRowStyleChanges: map[string]gdocs.SuggestedTableRowStyle{
			"s.row": {TableRowStyleSuggestionState: &gdocs.TableRowStyleSuggestionState{MinRowHeightSuggested: true}},
		},
	}
	d, err := doc.Parse(&gdocs.Document{
		DocumentID: "1SyntheticFixtureDocumentIdXXXXXXXXXXXXXXXXXX", RevisionID: "r",
		Tabs: []*gdocs.Tab{{
			TabProperties: &gdocs.TabProperties{TabID: "t.0"},
			DocumentTab: &gdocs.DocumentTab{Body: &gdocs.Body{Content: []*gdocs.StructuralElement{
				{Table: &gdocs.Table{Rows: 1, Columns: 3, TableRows: []*gdocs.TableRow{row}}},
			}}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, fs := range d.FormatSuggestions {
		if fs.ID == "s.row" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("one row suggestion on a three-column row should be reported once, got %d: %+v", n, d.FormatSuggestions)
	}
	// Every cell still knows its row has a pending change, which is what
	// a guard on one cell needs.
	for _, c := range d.Tabs[0].Body.Blocks[0].Table.Cells[0] {
		if len(c.RowChanges) != 1 {
			t.Errorf("cell %s lost its row's change: %+v", c.Handle, c.RowChanges)
		}
	}
}

// TestLegacyResponseKeepsItsCollectionSuggestions covers the response
// shape without tabs content, which Parse supports by copying the
// top-level collections into a synthetic tab. Reading the suggestions
// only from the tabs lost every one of them.
func TestLegacyResponseKeepsItsCollectionSuggestions(t *testing.T) {
	d, err := doc.Parse(&gdocs.Document{
		DocumentID: "1SyntheticFixtureDocumentIdXXXXXXXXXXXXXXXXXX", RevisionID: "r",
		Body: &gdocs.Body{Content: []*gdocs.StructuralElement{
			{Paragraph: &gdocs.Paragraph{
				ParagraphStyle: &gdocs.ParagraphStyle{NamedStyleType: "NORMAL_TEXT"},
				Elements:       []*gdocs.ParagraphElement{{TextRun: &gdocs.TextRun{Content: "t\n"}}},
			}},
		}},
		Lists: map[string]gdocs.List{"list.1": {SuggestedListPropertiesChanges: map[string]gdocs.SuggestedListProperties{
			"s.list": {ListPropertiesSuggestionState: &gdocs.ListPropertiesSuggestionState{
				NestingLevelsSuggestionStates: []gdocs.NestingLevelSuggestionState{{BulletAlignmentSuggested: true}},
			}},
		}}},
		SuggestedDocumentStyleChanges: map[string]gdocs.SuggestedDocumentStyle{
			"s.docstyle": {DocumentStyleSuggestionState: &gdocs.DocumentStyleSuggestionState{MarginTopSuggested: true}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, fs := range d.FormatSuggestions {
		seen[fs.ID]++
	}
	if seen["s.list"] != 1 {
		t.Errorf("a legacy response's list suggestion should be reported once, got %d: %+v", seen["s.list"], d.FormatSuggestions)
	}
	// And exactly once, not once for the tabs and again for the document.
	if seen["s.docstyle"] != 1 {
		t.Errorf("the document style suggestion should be reported once, got %d", seen["s.docstyle"])
	}
}

// TestTabbedResponseDoesNotDoubleReportDocumentStyle is the other half:
// a response with tabs must not have the document-level collections read
// a second time.
func TestTabbedResponseDoesNotDoubleReportDocumentStyle(t *testing.T) {
	change := map[string]gdocs.SuggestedDocumentStyle{
		"s.docstyle": {DocumentStyleSuggestionState: &gdocs.DocumentStyleSuggestionState{MarginTopSuggested: true}},
	}
	d, err := doc.Parse(&gdocs.Document{
		DocumentID: "1SyntheticFixtureDocumentIdXXXXXXXXXXXXXXXXXX", RevisionID: "r",
		SuggestedDocumentStyleChanges: change,
		Tabs: []*gdocs.Tab{{
			TabProperties: &gdocs.TabProperties{TabID: "t.0"},
			DocumentTab: &gdocs.DocumentTab{
				Body:                          &gdocs.Body{Content: []*gdocs.StructuralElement{}},
				SuggestedDocumentStyleChanges: change,
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, fs := range d.FormatSuggestions {
		if fs.ID == "s.docstyle" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("want one document-style suggestion, got %d: %+v", n, d.FormatSuggestions)
	}
}

// TestAStateWithNoFieldsIsNamedByItsStem covers the one suggestion state
// the API publishes with no fields in it. A suggested change to a
// drawing sets only EmbeddedDrawingPropertiesSuggestionState, which is
// `struct{}`, so walking it for set properties finds none — and a read
// that reports no properties reports no suggestion at all, which is the
// bug #46 was about. The stem is the property name.
func TestAStateWithNoFieldsIsNamedByItsStem(t *testing.T) {
	d, err := doc.Parse(&gdocs.Document{
		DocumentID: "1SyntheticFixtureDocumentIdXXXXXXXXXXXXXXXXXX", RevisionID: "r",
		Body: &gdocs.Body{Content: []*gdocs.StructuralElement{
			{Paragraph: &gdocs.Paragraph{
				ParagraphStyle: &gdocs.ParagraphStyle{NamedStyleType: "NORMAL_TEXT"},
				Elements: []*gdocs.ParagraphElement{{InlineObjectElement: &gdocs.InlineObjectElement{
					InlineObjectID: "kix.obj",
				}}},
			}},
		}},
		InlineObjects: map[string]gdocs.InlineObject{"kix.obj": {
			ObjectID: "kix.obj",
			SuggestedInlineObjectPropertiesChanges: map[string]gdocs.SuggestedInlineObjectProperties{
				"s.drawing": {InlineObjectPropertiesSuggestionState: &gdocs.InlineObjectPropertiesSuggestionState{
					EmbeddedObjectSuggestionState: &gdocs.EmbeddedObjectSuggestionState{
						EmbeddedDrawingPropertiesSuggestionState: &gdocs.EmbeddedDrawingPropertiesSuggestionState{},
					},
				}},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, fs := range d.FormatSuggestions {
		if fs.ID != "s.drawing" {
			continue
		}
		want := "embeddedObject.embeddedDrawingProperties"
		if len(fs.Props) != 1 || fs.Props[0] != want {
			t.Fatalf("want the stem %q as the property, got %v", want, fs.Props)
		}
		// And it reaches a person as words, not as a dangling sentence.
		if got := doc.PropList(fs.Props); got == "" || strings.Contains(got, ".") {
			t.Errorf("PropList(%v) = %q", fs.Props, got)
		}
		return
	}
	t.Errorf("a suggestion whose state names no property was dropped: %+v", d.FormatSuggestions)
}

// The other side of the same rule: a state that has fields and sets none
// of them is a suggestion to change nothing, and reporting one would be
// worse than missing it.
func TestAStateThatSetsNothingIsNotReported(t *testing.T) {
	d, err := doc.Parse(&gdocs.Document{
		DocumentID: "1SyntheticFixtureDocumentIdXXXXXXXXXXXXXXXXXX", RevisionID: "r",
		Body: &gdocs.Body{Content: []*gdocs.StructuralElement{
			{Paragraph: &gdocs.Paragraph{
				ParagraphStyle: &gdocs.ParagraphStyle{NamedStyleType: "NORMAL_TEXT"},
				Elements: []*gdocs.ParagraphElement{{TextRun: &gdocs.TextRun{
					Content: "t\n",
					SuggestedStyle: gdocs.SuggestedStyle{SuggestedTextStyleChanges: map[string]gdocs.SuggestedTextStyle{
						"s.nothing": {TextStyleSuggestionState: &gdocs.TextStyleSuggestionState{}},
					}},
				}}},
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, fs := range d.FormatSuggestions {
		if fs.ID == "s.nothing" {
			t.Errorf("a state that sets nothing was reported as a suggestion: %+v", fs)
		}
	}
}

// TestAnEmptyNestedStateInventsNoProperty is the other side of
// TestAStateWithNoFieldsIsNamedByItsStem. Google sends a nested state
// object beside the property it does set — a paragraph restyling carries
// an empty shadingSuggestionState — and naming that stem would report a
// change to a property nobody suggested, which guardRestyle would then
// warn about.
func TestAnEmptyNestedStateInventsNoProperty(t *testing.T) {
	d, err := doc.Parse(&gdocs.Document{
		DocumentID: "1SyntheticFixtureDocumentIdXXXXXXXXXXXXXXXXXX", RevisionID: "r",
		Body: &gdocs.Body{Content: []*gdocs.StructuralElement{
			{Paragraph: &gdocs.Paragraph{
				ParagraphStyle: &gdocs.ParagraphStyle{NamedStyleType: "NORMAL_TEXT"},
				Elements:       []*gdocs.ParagraphElement{{TextRun: &gdocs.TextRun{Content: "t\n"}}},
				SuggestedParagraphStyleChanges: map[string]gdocs.SuggestedParagraphStyle{
					"s.p": {ParagraphStyleSuggestionState: &gdocs.ParagraphStyleSuggestionState{
						AlignmentSuggested:     true,
						ShadingSuggestionState: &gdocs.ShadingSuggestionState{},
					}},
				},
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, fs := range d.FormatSuggestions {
		if fs.ID != "s.p" {
			continue
		}
		if len(fs.Props) != 1 || fs.Props[0] != "alignment" {
			t.Errorf("want only the property the state actually sets, got %v", fs.Props)
		}
		return
	}
	t.Error("the suggestion was dropped entirely")
}
