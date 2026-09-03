package doc_test

import (
	"strings"
	"testing"

	"github.com/mmedum/google-docs-mcp/internal/doc"
	"github.com/mmedum/google-docs-mcp/internal/gdocs"
)

func text(s string, st *gdocs.TextStyle) *gdocs.ParagraphElement {
	return &gdocs.ParagraphElement{TextRun: &gdocs.TextRun{Content: s, TextStyle: st}}
}

func para(style string, els ...*gdocs.ParagraphElement) *gdocs.StructuralElement {
	return &gdocs.StructuralElement{Paragraph: &gdocs.Paragraph{ParagraphStyle: &gdocs.ParagraphStyle{NamedStyleType: style}, Elements: els}}
}

// TestParseEveryElementKind builds a document in Go with the element
// kinds the fixture does not cover: chips, breaks, equations, auto text,
// a table of contents, nested tabs and footnotes without numbers.
func TestParseEveryElementKind(t *testing.T) {
	sugg := gdocs.Suggested{SuggestedInsertionIDs: []string{"s9"}}
	w := &gdocs.Document{
		DocumentID: "1SyntheticFixtureDocumentIdXXXXXXXXXXXXXXXXXX", Title: "Kinds", RevisionID: "r",
		Tabs: []*gdocs.Tab{{
			TabProperties: &gdocs.TabProperties{TabID: "t.a", Title: "Parent"},
			DocumentTab: &gdocs.DocumentTab{
				Body: &gdocs.Body{Content: []*gdocs.StructuralElement{
					para("NORMAL_TEXT",
						&gdocs.ParagraphElement{Person: &gdocs.Person{PersonProperties: &gdocs.PersonProperties{Name: "Ada", Email: "ada@example.test"}}},
						&gdocs.ParagraphElement{Person: &gdocs.Person{PersonProperties: &gdocs.PersonProperties{Email: "only@example.test"}}},
						&gdocs.ParagraphElement{RichLink: &gdocs.RichLink{RichLinkProperties: &gdocs.RichLinkProperties{Title: "Sheet", URI: "https://example.test/s"}}},
						&gdocs.ParagraphElement{RichLink: &gdocs.RichLink{RichLinkProperties: &gdocs.RichLinkProperties{URI: "https://example.test/u"}}},
						&gdocs.ParagraphElement{DateElement: &gdocs.DateElement{DateElementProperties: &gdocs.DateElementProperties{DisplayText: "3 Sep 2026"}}},
						&gdocs.ParagraphElement{DateElement: &gdocs.DateElement{DateElementProperties: &gdocs.DateElementProperties{Timestamp: "2026-09-03T00:00:00Z"}}},
						&gdocs.ParagraphElement{Equation: &gdocs.Suggested{}},
						&gdocs.ParagraphElement{AutoText: &gdocs.AutoText{Type: "PAGE_NUMBER"}},
						&gdocs.ParagraphElement{PageBreak: &gdocs.Break{Suggested: sugg}},
						&gdocs.ParagraphElement{ColumnBreak: &gdocs.Break{}},
						&gdocs.ParagraphElement{HorizontalRule: &gdocs.Break{}},
						&gdocs.ParagraphElement{},
						text("\n", &gdocs.TextStyle{BaselineOffset: "SUPERSCRIPT", ForegroundColor: &gdocs.OptionalColor{Color: &gdocs.Color{RgbColor: &gdocs.RgbColor{Red: 2, Green: -1, Blue: 0.5}}}, Link: &gdocs.Link{Heading: &gdocs.HeadingLink{ID: "h.z"}, BookmarkID: "b1", TabID: "t.a"}}),
					),
					{TableOfContents: &gdocs.TableOfContents{Content: []*gdocs.StructuralElement{para("NORMAL_TEXT", text("Entry\n", nil))}}},
					{Table: &gdocs.Table{TableRows: []*gdocs.TableRow{{TableCells: []*gdocs.TableCell{{Content: []*gdocs.StructuralElement{para("NORMAL_TEXT", text("c\n", nil))}, TableCellStyle: &gdocs.TableCellStyle{RowSpan: 2, ColumnSpan: 3}}, nil}}, nil}}},
					{},
					nil,
					para("HEADING_9", text("not a level\n", nil)),
					para("SUBTITLE", text("sub\n", nil)),
					para("NORMAL_TEXT", &gdocs.ParagraphElement{FootnoteReference: &gdocs.FootnoteReference{FootnoteID: "fn.b"}}, &gdocs.ParagraphElement{FootnoteReference: &gdocs.FootnoteReference{FootnoteID: "fn.a", FootnoteNumber: "2"}}, text("\n", nil)),
					{Paragraph: &gdocs.Paragraph{Bullet: &gdocs.Bullet{ListID: "unknown", NestingLevel: 7}, Elements: []*gdocs.ParagraphElement{text("x\n", nil)}}},
				}},
				Footers:   map[string]gdocs.Footer{"f1": {FooterID: "f1", Content: []*gdocs.StructuralElement{para("NORMAL_TEXT", text("foot\n", nil))}}},
				Footnotes: map[string]gdocs.Footnote{"fn.a": {Content: []*gdocs.StructuralElement{para("NORMAL_TEXT", text("A\n", nil))}}, "fn.b": {Content: []*gdocs.StructuralElement{para("NORMAL_TEXT", text("B\n", nil))}}, "fn.c": {}},
				Lists:     map[string]gdocs.List{"l": {ListProperties: &gdocs.ListProperties{NestingLevels: []*gdocs.NestingLevel{nil, {GlyphType: "ROMAN"}}}}},
				InlineObjects: map[string]gdocs.InlineObject{
					"draw":  {InlineObjectProperties: &gdocs.InlineObjectProperties{EmbeddedObject: &gdocs.EmbeddedObject{EmbeddedDrawingProperties: &struct{}{}}}},
					"chart": {InlineObjectProperties: &gdocs.InlineObjectProperties{EmbeddedObject: &gdocs.EmbeddedObject{LinkedContentReference: &gdocs.LinkedContentReference{}}}},
					"bare":  {},
				},
			},
			ChildTabs: []*gdocs.Tab{{TabProperties: &gdocs.TabProperties{TabID: "t.b", Title: "Child", ParentTabID: "t.a", NestingLevel: 1}}},
		}},
	}
	d, err := doc.Parse(w)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Tabs) != 2 || d.Tabs[1].ID != "t.b" || d.Tabs[1].Nesting != 1 || d.Tabs[1].Body == nil || len(d.Tabs[1].Body.Blocks) != 0 {
		t.Fatalf("nested tab wrong: %+v", d.Tabs)
	}
	body := d.Tabs[0].Body
	runs := body.Blocks[0].Paragraph.Runs
	got := []string{}
	for _, r := range runs {
		got = append(got, string(r.Kind)+":"+r.Text)
	}
	want := "person:Ada person:only@example.test rich_link:Sheet rich_link:https://example.test/u date:3 Sep 2026 date:2026-09-03T00:00:00Z equation: auto_text: page_break: column_break: horizontal_rule: text:\n"
	if strings.Join(got, " ") != want {
		t.Fatalf("runs = %q", strings.Join(got, " "))
	}
	if !runs[8].IsSuggestedInsertion() || runs[7].AutoTextType != "PAGE_NUMBER" {
		t.Fatalf("suggestion/autotext lost: %+v", runs[8])
	}
	last := runs[len(runs)-1].Style
	if last.Baseline != "SUPERSCRIPT" || last.Foreground != "#ff0080" || last.LinkHeadingID != "h.z" || last.LinkBookmark != "b1" || last.LinkTabID != "t.a" {
		t.Fatalf("style wrong: %+v", last)
	}
	if b := body.Blocks[1]; b.Kind != doc.KindTOC || b.Handle != "toc1" || b.TOC.Blocks[0].Handle != "toc1/p1" {
		t.Fatalf("toc wrong: %+v", b)
	}
	if tbl := body.Blocks[2].Table; tbl.Rows != 1 || tbl.Cols != 1 || tbl.Cells[0][0].RowSpan != 2 || tbl.Cells[0][0].ColSpan != 3 {
		t.Fatalf("table defaults wrong: %+v", tbl)
	}
	if len(body.Blocks) != 7 {
		t.Fatalf("empty and nil elements should be skipped: %d blocks", len(body.Blocks))
	}
	if p := body.Blocks[3].Paragraph; p.Level != 0 || p.IsTitle {
		t.Fatalf("HEADING_9 should not be a level: %+v", p)
	}
	if !body.Blocks[4].Paragraph.IsSubtitle {
		t.Fatal("subtitle not detected")
	}
	if len(body.Blocks[5].Paragraph.Runs) != 3 {
		t.Fatalf("footnote refs lost: %d", len(body.Blocks[5].Paragraph.Runs))
	}
	if bl := body.Blocks[6].Paragraph.Bullet; bl == nil || bl.Ordered || bl.Nesting != 7 {
		t.Fatalf("unknown list bullet wrong: %+v", bl)
	}
	if len(d.Tabs[0].Footers) != 1 || d.Tabs[0].Footers[0].Label() != "footer1" || d.Tabs[0].Footers[0].Blocks[0].Handle != "footer1/p1" {
		t.Fatalf("footer wrong: %+v", d.Tabs[0].Footers)
	}
	labels := []string{}
	for _, fn := range d.Tabs[0].Footnotes {
		labels = append(labels, fn.Label()+"="+fn.Prefix)
	}
	// fn.a has number 2 so it sorts first; unnumbered ones follow by id
	// and get sequential labels.
	if got := strings.Join(labels, " "); got != "footnote2=footnote2/ footnote2=footnote2/ footnote3=footnote3/" {
		t.Fatalf("footnote order: %v", labels)
	}
	if d.Tabs[0].Footnotes[0].FootnoteNumber != "2" || d.Tabs[0].Footnotes[1].FootnoteNumber != "" || len(d.Tabs[0].Footnotes[2].Blocks) != 0 {
		t.Fatalf("footnote numbers: %+v", d.Tabs[0].Footnotes)
	}
	if lvls := d.Tabs[0].Lists["l"].Levels; len(lvls) != 1 || !lvls[0].Ordered {
		t.Fatalf("list levels: %+v", lvls)
	}
	kinds := d.Tabs[0].InlineObjects["draw"].Kind + " " + d.Tabs[0].InlineObjects["chart"].Kind + " " + d.Tabs[0].InlineObjects["bare"].Kind
	if kinds != "drawing chart object" {
		t.Fatalf("object kinds = %s", kinds)
	}
	if got := d.Tabs[0].Footnotes[0].Blocks[0].Text(doc.ViewInline); got != "A" {
		t.Fatalf("footnote text = %q", got)
	}
	if got := body.Blocks[1].Text(doc.ViewInline); got != "Entry" {
		t.Fatalf("toc text = %q", got)
	}
	if got := d.Tabs[0].Body.AllBlocks(); len(got) != 9 {
		t.Fatalf("AllBlocks = %d", len(got))
	}
	if _, ok := d.Tab("tab2"); !ok {
		t.Fatal("tab2 lookup")
	}
	if _, ok := d.Tab("tab9"); ok {
		t.Fatal("tab9 lookup should fail")
	}
	if (&doc.Document{}).Stats().Tabs != 0 {
		t.Fatal("empty stats")
	}
	if _, ok := (&doc.Document{}).Tab(""); ok {
		t.Fatal("no tabs should fail")
	}
}

func TestSegmentLabelsAndText(t *testing.T) {
	seg := &doc.Segment{Kind: doc.SegmentHeader, Number: 2}
	if seg.Label() != "header2" {
		t.Fatal(seg.Label())
	}
	fn := &doc.Segment{Kind: doc.SegmentFootnote, Number: 3}
	if fn.Label() != "footnote3" {
		t.Fatal(fn.Label())
	}
	if (&doc.Block{Kind: doc.KindSectionBreak}).Text(doc.ViewInline) != "" {
		t.Fatal("section break text should be empty")
	}
	st := doc.TextStyle{LinkBookmark: "b"}
	if !st.HasLink() || (doc.TextStyle{}).HasLink() {
		t.Fatal("HasLink")
	}
	if !(doc.TextStyle{FontFamily: "Consolas"}).Monospace() || (doc.TextStyle{FontFamily: "Arial"}).Monospace() {
		t.Fatal("Monospace")
	}
	body := &doc.Segment{Kind: doc.SegmentBody}
	if s, ok := body.SectionByHeadingID(""); ok || s.Heading != nil {
		t.Fatal("empty id")
	}
	if len(body.Sections()) != 0 || body.Preamble().To != 0 {
		t.Fatal("empty segment sections")
	}
	if _, _, ok := (&doc.Document{}).HeadingByID("x"); ok {
		t.Fatal("HeadingByID on empty doc")
	}
	if _, ok := (&doc.Document{}).FindCell("x"); ok {
		t.Fatal("FindCell on empty doc")
	}
}
