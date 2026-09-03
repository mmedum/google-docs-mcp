package doc_test

import (
	"strings"
	"testing"

	"github.com/mmedum/google-docs-mcp/internal/doc"
	"github.com/mmedum/google-docs-mcp/internal/doc/doctest"
)

func TestParseStructure(t *testing.T) {
	d := doctest.Fixture(t)
	if d.ID == "" || d.Title != "Quarterly Report" || d.RevisionID != "rev-0001" {
		t.Fatalf("header fields wrong: %+v", d)
	}
	if len(d.Tabs) != 2 {
		t.Fatalf("tabs = %d, want 2", len(d.Tabs))
	}
	main := d.Tabs[0]
	if main.Number != 1 || main.ID != "t.0" || main.Prefix() != "" {
		t.Fatalf("tab 1 wrong: %+v", main)
	}
	if got := d.Tabs[1].Prefix(); got != "tab2/" {
		t.Fatalf("tab 2 prefix = %q", got)
	}
	handles := make([]string, 0, len(main.Body.Blocks))
	for _, b := range main.Body.Blocks {
		handles = append(handles, b.Handle)
	}
	want := "sb1 p1 p2 p3 p4 p5 p6 p7 p8 p9 p10 tbl1 p11 p12 p13 p14"
	if got := strings.Join(handles, " "); got != want {
		t.Fatalf("handles = %q, want %q", got, want)
	}
	if len(main.Headers) != 1 || main.Headers[0].Blocks[0].Handle != "header1/p1" {
		t.Fatalf("header handles wrong: %+v", main.Headers)
	}
	if len(main.Footnotes) != 1 || main.Footnotes[0].FootnoteNumber != "1" || main.Footnotes[0].Blocks[0].Handle != "footnote1/p1" {
		t.Fatalf("footnote wrong: %+v", main.Footnotes[0])
	}
}

func TestParseParagraphDetails(t *testing.T) {
	d := doctest.Fixture(t)
	body := d.Tabs[0].Body
	title := body.Blocks[1].Paragraph
	if !title.IsTitle || title.Level != 0 {
		t.Fatalf("title not detected: %+v", title)
	}
	h1 := body.Blocks[2]
	if !h1.IsHeading() || h1.Paragraph.Level != 1 || h1.Paragraph.HeadingID != "h.bg" {
		t.Fatalf("heading wrong: %+v", h1.Paragraph)
	}
	mixed := body.Blocks[3].Paragraph
	if got := mixed.Text(doc.ViewInline); got != "Revenue grew a lotsubstantially in Q3." {
		t.Fatalf("inline text = %q", got)
	}
	if got := mixed.Text(doc.ViewCurrent); got != "Revenue grew a lot in Q3." {
		t.Fatalf("current text = %q", got)
	}
	if got := mixed.Text(doc.ViewAccepted); got != "Revenue grew substantially in Q3." {
		t.Fatalf("accepted text = %q", got)
	}
	if !mixed.Runs[2].Style.Bold || !mixed.Runs[2].IsSuggestedInsertion() {
		t.Fatalf("run style/suggestion wrong: %+v", mixed.Runs[2])
	}
	bullet := body.Blocks[6].Paragraph.Bullet
	if bullet == nil || bullet.Nesting != 1 || bullet.Ordered {
		t.Fatalf("nested bullet wrong: %+v", bullet)
	}
	num := body.Blocks[9].Paragraph.Bullet
	if num == nil || !num.Ordered {
		t.Fatalf("numbered list not detected: %+v", num)
	}
	tbl := body.Blocks[11].Table
	if tbl == nil || tbl.Rows != 2 || tbl.Cols != 2 || tbl.Cells[1][0].Handle != "tbl1:r2c1" || tbl.Cells[1][0].Blocks[0].Handle != "tbl1:r2c1/p1" {
		t.Fatalf("table wrong: %+v", tbl)
	}
	objects := body.Blocks[12].Paragraph
	kinds := ""
	for _, r := range objects.Runs {
		kinds += string(r.Kind) + ","
	}
	if kinds != "text,inline_object,text,footnote_ref,text,text,text," {
		t.Fatalf("run kinds = %s", kinds)
	}
	if got := objects.Runs[5].Style.LinkURL; got != "https://example.com" {
		t.Fatalf("link = %q", got)
	}
	if info := d.Tabs[0].InlineObjects["kix.img1"]; info == nil || info.Kind != "image" || info.WidthPt != 320 {
		t.Fatalf("inline object wrong: %+v", info)
	}
	colored := body.Blocks[14].Paragraph
	if colored.Runs[0].Style.Foreground != "#cc0000" || colored.Alignment != "CENTER" {
		t.Fatalf("color/alignment wrong: %+v", colored.Runs[0].Style)
	}
	if !body.Blocks[15].Paragraph.Runs[1].Style.Monospace() {
		t.Fatal("monospace not detected")
	}
	if sb := body.Blocks[0].SectionBreak; sb == nil || sb.Type != "CONTINUOUS" || sb.DefaultHeaderID != "kix.h1" {
		t.Fatalf("section break wrong: %+v", sb)
	}
}

func TestSections(t *testing.T) {
	d := doctest.Fixture(t)
	body := d.Tabs[0].Body
	secs := body.Sections()
	if len(secs) != 3 {
		t.Fatalf("sections = %d, want 3", len(secs))
	}
	bg := secs[0]
	if bg.Heading.Handle != "p2" || bg.Level != 1 || bg.From != 2 || bg.To != 13 {
		t.Fatalf("Background section wrong: %+v", bg)
	}
	det := secs[1]
	if det.Level != 2 || det.From != 8 || det.To != 13 {
		t.Fatalf("Details section wrong: %+v", det)
	}
	sum := secs[2]
	if sum.From != 13 || sum.To != len(body.Blocks) {
		t.Fatalf("Summary section wrong: %+v", sum)
	}
	if pre := body.Preamble(); pre.From != 0 || pre.To != 2 {
		t.Fatalf("preamble = %+v", pre)
	}
	if s, ok := body.SectionByHeadingID("h.det"); !ok || s.Heading.Handle != "p8" {
		t.Fatalf("by id: %+v %v", s, ok)
	}
	if got := body.SectionsByHeading("background", 0); len(got) != 1 {
		t.Fatalf("by text: %d", len(got))
	}
	if got := body.SectionsByHeading("Background", 2); len(got) != 0 {
		t.Fatalf("by text with wrong level: %d", len(got))
	}
	if tab, s, ok := d.HeadingByID("h.notes"); !ok || tab.Number != 2 || s.Heading.Handle != "tab2/p1" {
		t.Fatalf("cross-tab heading lookup: %v %+v", ok, s)
	}
}

func TestTabLookupAndHandles(t *testing.T) {
	d := doctest.Fixture(t)
	for _, ref := range []string{"", "t.0", "Main", "main", "1", "tab1"} {
		if tab, ok := d.Tab(ref); !ok || tab.Number != 1 {
			t.Fatalf("Tab(%q) = %v %v", ref, tab, ok)
		}
	}
	if tab, ok := d.Tab("notes"); !ok || tab.Number != 2 {
		t.Fatalf("Tab(notes) failed")
	}
	if _, ok := d.Tab("missing"); ok {
		t.Fatal("Tab(missing) should fail")
	}
	if b, ok := d.FindHandle("tbl1:r1c2/p1"); !ok || b.Text(doc.ViewInline) != "Value" {
		t.Fatalf("FindHandle cell block: %v", ok)
	}
	if c, ok := d.FindCell("tbl1:r2c2"); !ok || c.Text(doc.ViewInline) != "1" {
		t.Fatalf("FindCell: %v", ok)
	}
	if _, ok := d.FindHandle("p99"); ok {
		t.Fatal("FindHandle(p99) should fail")
	}
}

func TestStats(t *testing.T) {
	st := doctest.Fixture(t).Stats()
	if st.Tabs != 2 || st.Tables != 1 || st.InlineObjects != 1 || st.Footnotes != 1 || st.Headings != 4 || st.Suggestions != 1 {
		t.Fatalf("stats = %+v", st)
	}
	if st.Paragraphs < 15 || st.Words < 30 {
		t.Fatalf("counts too small: %+v", st)
	}
}

func TestParseLegacyWithoutTabs(t *testing.T) {
	w := doctest.WireFixture(t)
	legacy := *w
	legacy.Body = w.Tabs[0].DocumentTab.Body
	legacy.Headers = w.Tabs[0].DocumentTab.Headers
	legacy.Tabs = nil
	d, err := doc.Parse(&legacy)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Tabs) != 1 || len(d.Tabs[0].Body.Blocks) != 16 || len(d.Tabs[0].Headers) != 1 {
		t.Fatalf("legacy parse wrong: %d tabs", len(d.Tabs))
	}
	if _, err := doc.Parse(nil); err == nil {
		t.Fatal("nil document should error")
	}
}

func TestUTF16(t *testing.T) {
	s := "Done 🎉 and “quoted”"
	if got := doc.UTF16Len(s); got != 20 {
		t.Fatalf("UTF16Len = %d, want 20", got)
	}
	if got := doc.CodePointToUTF16(s, 6); got != 7 {
		t.Fatalf("CodePointToUTF16(6) = %d, want 7", got)
	}
	if got := doc.UTF16ToCodePoint(s, 7); got != 6 {
		t.Fatalf("UTF16ToCodePoint(7) = %d, want 6", got)
	}
	if got := doc.UTF16ToCodePoint(s, 6); got != 6 {
		t.Fatalf("mid-surrogate should round up: %d", got)
	}
	if got := doc.UTF16ToByte(s, 7); s[got:got+1] != " " {
		t.Fatalf("UTF16ToByte(7) = %d (%q)", got, s[got:])
	}
	if got := doc.CodePointToUTF16(s, 100); got != 20 {
		t.Fatalf("clamp = %d", got)
	}
	// Every code-point offset round-trips.
	runes := []rune(s)
	for i := 0; i <= len(runes); i++ {
		if back := doc.UTF16ToCodePoint(s, doc.CodePointToUTF16(s, i)); back != i {
			t.Fatalf("round trip at %d gave %d", i, back)
		}
	}
}

func TestNormalizeAndWords(t *testing.T) {
	cases := map[string]string{
		"“Smart”  quotes and ‘apostrophes’": `"Smart" quotes and 'apostrophes'`,
		"  dash – and — em\t":               "dash - and - em",
		"a\u200bb\u2026":                    "ab...",
	}
	for in, want := range cases {
		if got := doc.Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
	if got := doc.WordCount("one two\tthree\nfour "); got != 4 {
		t.Fatalf("WordCount = %d", got)
	}
}

func TestParseID(t *testing.T) {
	id := "1SyntheticFixtureDocumentIdXXXXXXXXXXXXXXXXXX"
	for _, in := range []string{
		id,
		"https://docs.google.com/document/d/" + id + "/edit",
		"https://docs.google.com/document/d/" + id + "/edit?tab=t.0#heading=h.bg",
		"https://docs.google.com/open?id=" + id,
		"  " + id + "\n",
	} {
		got, err := doc.ParseID(in)
		if err != nil || got != id {
			t.Errorf("ParseID(%q) = %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "short", "https://example.com/", "not an id!"} {
		if _, err := doc.ParseID(in); err == nil {
			t.Errorf("ParseID(%q) should fail", in)
		}
	}
	if got := doc.DocumentURL(id); !strings.Contains(got, id) {
		t.Fatal(got)
	}
}
