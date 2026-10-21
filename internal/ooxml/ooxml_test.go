package ooxml

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
)

// brief is a Google Doc exported as .docx (spike H): headings, a link, a
// nested bulleted list, a numbered one, a table. Synthetic content.
func brief(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/brief.docx")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// docx builds a package around a body.
func docx(t *testing.T, body string) []byte {
	t.Helper()
	return docxWith(t, body, nil)
}

// docxWith builds a package around a body, with more parts.
func docxWith(t *testing.T, body string, more map[string]string) []byte {
	t.Helper()
	parts := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><w:body>` +
			body + `<w:sectPr/></w:body></w:document>`,
	}
	names := []string{"[Content_Types].xml", "_rels/.rels", "word/document.xml"}
	for name, body := range more {
		parts[name] = body
		names = append(names, name)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range names {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(w, parts[name])
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func open(t *testing.T, data []byte) *File {
	t.Helper()
	f, err := Open(data)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// texts is each paragraph's text, quoted, one per line.
func texts(f *File) string {
	var out []string
	for _, p := range f.Paras {
		out = append(out, p.Text)
	}
	return strings.Join(out, "|")
}

// The paragraphs are the ones the Docs API lists for the same file, in its
// order and with its text (spike H).
func TestOpenBrief(t *testing.T) {
	f := open(t, brief(t))
	want := "Brief|Intro paragraph with bold and a link.|Points|First point|Nested point|Second point|One|Two||A|B|1|2||Closing line."
	if got := texts(f); got != want {
		t.Fatalf("paragraphs:\n got %s\nwant %s", got, want)
	}
	p := f.Paras[4]
	// Each table cell holds one paragraph, the last of its cell.
	if p.NumID != "2" || p.Ilvl != 1 || f.Paras[0].Style != "Heading1" || f.Paras[9].cell < 0 || !f.Paras[9].last || p.last || p.cell >= 0 {
		t.Fatalf("nested point %+v, heading %q, cells %d/%v %d/%v", p, f.Paras[0].Style, f.Paras[9].cell, f.Paras[9].last, f.Paras[10].cell, f.Paras[10].last)
	}
	if f.w != "w" || f.r != "r" {
		t.Fatalf("prefixes %q %q", f.w, f.r)
	}
}

// What the Docs API counts in a paragraph: deleted text, tabs, breaks,
// inline objects and a content control's text; not a field's code, a
// floating drawing, a bookmark or a content control's properties.
func TestParagraphText(t *testing.T) {
	f := open(t, docx(t, `<w:p><w:r><w:t>a</w:t><w:tab/><w:t>b</w:t><w:br/></w:r><w:bookmarkStart w:id="1" w:name="x"/>`+
		`<w:del w:id="2" w:author="A"><w:r><w:delText>gone</w:delText></w:r></w:del>`+
		`<w:r><w:fldChar w:fldCharType="begin"/></w:r><w:r><w:instrText>PAGE</w:instrText></w:r><w:r><w:fldChar w:fldCharType="separate"/></w:r><w:r><w:t>7</w:t></w:r><w:r><w:fldChar w:fldCharType="end"/></w:r>`+
		`<w:r><w:drawing><wp:inline xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"/></w:drawing></w:r>`+
		`<w:r><w:drawing><wp:anchor xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"/></w:drawing></w:r>`+
		`<w:r><w:br w:type="page"/><w:t>😀</w:t></w:r>`+
		`<w:sdt><w:sdtPr><w:alias w:val="Name"/></w:sdtPr><w:sdtContent><w:r><w:t>Ann</w:t></w:r></w:sdtContent></w:sdt></w:p>`))
	if got, want := f.Paras[0].Text, "a\tb\vgone7￼￼😀Ann"; got != want {
		t.Fatalf("text %q, want %q", got, want)
	}
	if n := f.Paras[0].Len(); n != 16 {
		t.Fatalf("%d units, want 16 (the emoji is two)", n)
	}
}

// written is the file with its edits made, read again, and its
// document.xml.
func written(t *testing.T, f *File) (*File, string) {
	t.Helper()
	_, g, err := f.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return g, string(g.doc)
}

func para(t *testing.T, f *File, text string) int {
	t.Helper()
	for i, p := range f.Paras {
		if p.Text == text {
			return i
		}
	}
	t.Fatalf("no paragraph %q", text)
	return -1
}

var track = &Track{Author: "Probe Author", Date: "2026-10-21T12:00:00Z"}

// A change inside one run keeps the run's own look, and every byte of
// document.xml outside the run.
func TestReplaceInRun(t *testing.T) {
	f := open(t, brief(t))
	i := para(t, f, "Closing line.")
	r := f.Paras[i].runs[0]
	before, after := string(f.doc[:r.el.start]), string(f.doc[r.el.end:])
	if err := f.Replace(i, 8, 12, []Run{{Text: "words"}}, nil); err != nil {
		t.Fatal(err)
	}
	g, doc := written(t, f)
	if got := g.Paras[i].Text; got != "Closing words." {
		t.Fatalf("text %q", got)
	}
	if !strings.HasPrefix(doc, before) || !strings.HasSuffix(doc, after) {
		t.Fatal("bytes outside the run changed")
	}
	if !strings.Contains(doc, `<w:rtl w:val="0"/></w:rPr><w:t xml:space="preserve">Closing </w:t></w:r><w:r><w:rPr><w:rtl w:val="0"/></w:rPr><w:t xml:space="preserve">words</w:t></w:r>`) {
		t.Fatalf("the new run does not take the run's look:\n%s", doc[r.el.start:r.el.start+400])
	}
}

// Tracked, the removed text is marked deleted and the new text inserted;
// Docs reads both as one suggestion (spike H). Two changes in one run come
// out as one rewrite of it.
func TestReplaceTracked(t *testing.T) {
	f := open(t, brief(t))
	i := para(t, f, "Closing line.")
	if err := f.Replace(i, 0, 1, []Run{{Text: "c"}}, track); err != nil {
		t.Fatal(err)
	}
	if err := f.Replace(i, 8, 12, []Run{{Text: "words"}}, track); err != nil {
		t.Fatal(err)
	}
	g, doc := written(t, f)
	if got := g.Paras[i].Text; got != "Cclosing linewords." {
		t.Fatalf("text with the deletions inline %q", got)
	}
	for _, want := range []string{`<w:delText xml:space="preserve">line</w:delText>`, `w:author="Probe Author" w:date="2026-10-21T12:00:00Z"><w:r><w:rPr><w:rtl w:val="0"/></w:rPr><w:t xml:space="preserve">words</w:t></w:r></w:ins>`} {
		if !strings.Contains(doc, want) {
			t.Errorf("missing %s", want)
		}
	}
	if n := strings.Count(doc, "<w:del "); n != 2 {
		t.Errorf("%d deletions, want 2", n)
	}
}

// Across runs: text typed over a bold word is bold, and a change may not
// cross into a link.
func TestReplaceAcrossRuns(t *testing.T) {
	f := open(t, brief(t))
	i := para(t, f, "Intro paragraph with bold and a link.")
	if err := f.Replace(i, 21, 27, []Run{{Text: "big"}}, nil); err != nil { // "bold a" becomes "big"
		t.Fatal(err)
	}
	if err := f.Replace(i, 6, 6, []Run{{Text: "short "}}, nil); err != nil {
		t.Fatal(err)
	}
	g, doc := written(t, f)
	if got := g.Paras[i].Text; got != "Intro short paragraph with bignd a link." {
		t.Fatalf("text %q", got)
	}
	if !strings.Contains(doc, `<w:b w:val="1"/><w:bCs w:val="1"/><w:rtl w:val="0"/></w:rPr><w:t xml:space="preserve">big</w:t>`) {
		t.Fatal("text typed over a bold word is not bold")
	}
	f = open(t, brief(t))
	if err := f.Replace(i, 30, 37, nil, nil); !errors.Is(err, ErrUnsupported) { // " a link"
		t.Fatalf("a change into a link: %v", err)
	}
}

// What a change may not reach, in either mode: deleted text, a pending
// insertion, or a point inside either, where a direct change would go with
// the suggestion. A field's code counts no unit, so a change across a
// field keeps it.
func TestReplaceRefusals(t *testing.T) {
	f := open(t, docx(t, `<w:p><w:r><w:t>ab</w:t></w:r><w:r><w:fldChar w:fldCharType="begin"/></w:r><w:r><w:instrText>PAGE</w:instrText></w:r><w:r><w:fldChar w:fldCharType="separate"/></w:r><w:r><w:t>7</w:t></w:r><w:r><w:fldChar w:fldCharType="end"/></w:r>`+
		`<w:del w:id="1" w:author="A"><w:r><w:delText>cd</w:delText></w:r></w:del><w:ins w:id="2" w:author="A"><w:r><w:t>ef</w:t></w:r></w:ins></w:p>`))
	for name, c := range map[string]struct {
		a, b int
		tr   *Track
	}{
		"deleted":                            {3, 4, nil},
		"inside a pending insertion":         {5, 6, track},
		"inside a pending insertion, direct": {5, 6, nil},
		"a point after deleted text, direct": {5, 5, nil},
		"a point in a pending insertion":     {6, 6, nil},
	} {
		if err := f.Replace(0, c.a, c.b, []Run{{Text: "x"}}, c.tr); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := f.Replace(0, 1, 3, nil, nil); err != nil { // "b7"
		t.Fatal(err)
	}
	g, doc := written(t, f)
	if g.Paras[0].Text != "acdef" || strings.Count(doc, "<w:fldChar ") != 3 || !strings.Contains(doc, "<w:instrText>PAGE</w:instrText>") {
		t.Fatalf("across a field: %q\n%s", g.Paras[0].Text, doc)
	}
}

// New paragraphs: a heading by the file's style for it, list items in an
// existing list at a level, tracked with their marks inserted.
func TestInsertParas(t *testing.T) {
	f := open(t, brief(t))
	i := para(t, f, "Second point")
	paras := []NewPara{
		{Heading: 2, Runs: []Run{{Text: "New section"}}},
		{NumID: "2", Ilvl: 1, Runs: []Run{{Text: "Deeper", Styled: true, Bold: true}}},
		{Runs: []Run{{Text: "Plain "}, {Text: "linked", Link: "https://example.com/a?b=1&c=2"}}},
	}
	if err := f.InsertParas(i, true, paras, track); err != nil {
		t.Fatal(err)
	}
	g, doc := written(t, f)
	got := []string{}
	for _, p := range g.Paras[i+1 : i+4] {
		got = append(got, p.Text+"/"+p.Style+"/"+p.NumID+"/"+string(rune('0'+p.Ilvl)))
	}
	if strings.Join(got, "|") != "New section/Heading2//0|Deeper//2/1|Plain linked///0" {
		t.Fatalf("paragraphs %v", got)
	}
	if n := strings.Count(doc, `<w:rPr><w:ins w:id=`); n != 3 {
		t.Errorf("%d inserted paragraph marks, want 3", n)
	}
	rels, _ := g.part(partRels)
	if !strings.Contains(string(rels), `Target="https://example.com/a?b=1&amp;c=2" TargetMode="External"`) {
		t.Errorf("no relationship for the link:\n%s", rels)
	}
}

// Deleting paragraphs: direct removes them; tracked marks their text and
// their marks deleted; a cell keeps its last paragraph unless it is being
// replaced; a table between is refused.
func TestDeleteParas(t *testing.T) {
	f := open(t, brief(t))
	i := para(t, f, "Closing line.")
	if err := f.DeleteParas(i-1, i-1, false, nil); err != nil {
		t.Fatal(err)
	}
	g, _ := written(t, f)
	if len(g.Paras) != len(f.Paras)-1 || g.Paras[len(g.Paras)-1].Text != "Closing line." {
		t.Fatalf("paragraphs %s", texts(g))
	}

	f = open(t, brief(t))
	if err := f.DeleteParas(i, i, true, track); err != nil {
		t.Fatal(err)
	}
	_, doc := written(t, f)
	if !strings.Contains(doc, `<w:rPr><w:del w:id=`) || !strings.Contains(doc, `<w:delText xml:space="preserve">Closing line.</w:delText>`) {
		t.Fatalf("tracked deletion missing:\n%s", doc[len(doc)-900:])
	}

	f = open(t, brief(t))
	for name, c := range map[string][2]int{"a cell's last paragraph": {9, 9}, "across a table": {para(t, f, "Two"), i}} {
		if err := f.DeleteParas(c[0], c[1], false, nil); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// A comment pins to a passage with range markers and a reference, its
// text in comments.xml, which a file without one gets, with its type and
// relationship. The text does not change.
func TestComment(t *testing.T) {
	f := open(t, brief(t))
	i := para(t, f, "Intro paragraph with bold and a link.")
	if _, err := f.Comment(i, 0, i, 5, NewComment{Author: "Probe Author", Initials: "PA", Date: "2026-10-21T12:00:00Z", Text: "One line\nand two"}); err != nil {
		t.Fatal(err)
	}
	g, doc := written(t, f)
	if g.Paras[i].Text != f.Paras[i].Text {
		t.Fatalf("text changed: %q", g.Paras[i].Text)
	}
	if got := g.Paras[i].Comments; !slices.Equal(got, []string{"2"}) || g.Paras[i+1].Comments != nil {
		t.Fatalf("comments read back %q and %q, want [2] and none", got, g.Paras[i+1].Comments)
	}
	// One across two paragraphs: the first holds only where it starts.
	if _, err := g.Comment(i, 6, i+1, 6, NewComment{Author: "Probe Author", Date: "2026-10-21T12:00:00Z", Text: "Across"}); err != nil {
		t.Fatal(err)
	}
	h, _ := written(t, g)
	if a, b := h.Paras[i].Comments, h.Paras[i+1].Comments; !slices.Equal(a, []string{"2", "3"}) || !slices.Equal(b, []string{"3"}) {
		t.Fatalf("comments read back %q and %q, want [2 3] and [3]", a, b)
	}
	if !strings.Contains(doc, `<w:commentRangeStart w:id="2"/><w:r w:rsidDel="00000000" w:rsidR="00000000" w:rsidRPr="00000000"><w:rPr><w:rtl w:val="0"/></w:rPr><w:t xml:space="preserve">Intro</w:t></w:r><w:commentRangeEnd w:id="2"/><w:r><w:commentReference w:id="2"/></w:r>`) {
		t.Fatalf("markers:\n%s", doc[f.Paras[i].el.start:f.Paras[i].el.start+700])
	}
	comments, _ := g.part(partComments)
	types, _ := g.part(partTypes)
	rels, _ := g.part(partRels)
	if !strings.Contains(string(comments), `<w:comment w:id="2" w:author="Probe Author" w:date="2026-10-21T12:00:00Z" w:initials="PA"><w:p><w:r><w:t xml:space="preserve">One line</w:t></w:r></w:p><w:p><w:r><w:t xml:space="preserve">and two</w:t></w:r></w:p></w:comment>`) ||
		!strings.Contains(string(types), `PartName="/word/comments.xml"`) || !strings.Contains(string(rels), `Target="comments.xml"`) {
		t.Fatalf("comments part:\n%s\n%s\n%s", comments, types, rels)
	}
}

// Lists: what kind an existing one is, and a new one of each kind, its
// definition before every list using one.
func TestLists(t *testing.T) {
	f := open(t, brief(t))
	for _, c := range []struct {
		id          string
		ilvl        int
		ordered, ok bool
	}{{"2", 0, false, true}, {"1", 0, true, true}, {"1", 1, false, true}, {"9", 0, false, false}} {
		if ordered, ok := f.ListOrdered(c.id, c.ilvl); ordered != c.ordered || ok != c.ok {
			t.Errorf("list %s level %d: ordered %v ok %v", c.id, c.ilvl, ordered, ok)
		}
	}
	id, err := f.NewList(true)
	if err != nil {
		t.Fatal(err)
	}
	if id != "3" {
		t.Fatalf("new list %s, want 3", id)
	}
	if ordered, ok := f.ListOrdered(id, 1); !ordered || !ok {
		t.Fatalf("the new numbered list's level 1: ordered %v ok %v", ordered, ok)
	}
	body, _ := f.part(partNumbering)
	if a, n := strings.Index(string(body), `w:abstractNumId="3"`), strings.Index(string(body), `<w:num w:numId="1"`); a < 0 || a > n {
		t.Fatal("the new definition is not before the first list")
	}
}

// A heading's style is found by its name, which is the same in every
// language; a file without one gets it.
func TestHeadingStyle(t *testing.T) {
	f := open(t, brief(t))
	if id, err := f.HeadingStyle(3); err != nil || id != "Heading3" {
		t.Fatalf("%q %v", id, err)
	}
	styles, _ := f.part(partStyles)
	// Another file's styles, read afresh.
	f.parts[partStyles], f.sheet = bytes.Replace(styles, []byte(`w:styleId="Heading3"`), []byte(`w:styleId="Overskrift3"`), 1), nil
	if id, err := f.HeadingStyle(3); err != nil || id != "Overskrift3" {
		t.Fatalf("a localized id: %q %v", id, err)
	}
	f.parts[partStyles], f.sheet = bytes.Replace(styles, []byte(`w:val="heading 3"`), []byte(`w:val="custom"`), 1), nil
	id, err := f.HeadingStyle(3)
	if err != nil || id != "Heading3x" {
		t.Fatalf("missing: %q %v", id, err)
	}
	if !strings.Contains(string(f.parts[partStyles]), `w:styleId="Heading3x"><w:name w:val="heading 3"/>`) {
		t.Fatal("the style was not added")
	}
}

// A tracked change to a paragraph's properties holds the ones it had,
// which are not its own: its style and its list are the current ones.
func TestTrackedPropertyChange(t *testing.T) {
	f := open(t, docx(t, `<w:p><w:pPr><w:pStyle w:val="ListParagraph"/><w:numPr><w:ilvl w:val="1"/><w:numId w:val="7"/></w:numPr>`+
		`<w:pPrChange w:id="3" w:author="A"><w:pPr><w:pStyle w:val="Normal"/><w:numPr><w:ilvl w:val="0"/><w:numId w:val="3"/></w:numPr></w:pPr></w:pPrChange>`+
		`</w:pPr><w:r><w:t>x</w:t></w:r></w:p>`))
	if p := f.Paras[0]; p.Style != "ListParagraph" || p.NumID != "7" || p.Ilvl != 1 {
		t.Fatalf("style %q list %q level %d, want ListParagraph 7 1", p.Style, p.NumID, p.Ilvl)
	}
}

// A list made by a paragraph style is numbered in styles.xml, by the
// style or one it is based on; numId 0 takes a style out of its base's.
func TestStyleList(t *testing.T) {
	styles := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
		`<w:style w:type="paragraph" w:styleId="ListBullet"><w:name w:val="List Bullet"/><w:pPr><w:numPr><w:numId w:val="4"/></w:numPr></w:pPr></w:style>` +
		`<w:style w:type="paragraph" w:styleId="ListBullet2"><w:name w:val="List Bullet 2"/><w:basedOn w:val="ListBullet"/><w:pPr><w:numPr><w:ilvl w:val="1"/><w:numId w:val="4"/></w:numPr></w:pPr></w:style>` +
		`<w:style w:type="paragraph" w:styleId="Mine"><w:name w:val="Mine"/><w:basedOn w:val="ListBullet"/></w:style>` +
		`<w:style w:type="paragraph" w:styleId="Off"><w:name w:val="Off"/><w:basedOn w:val="ListBullet"/><w:pPr><w:numPr><w:numId w:val="0"/></w:numPr></w:pPr></w:style>` +
		`</w:styles>`
	f := open(t, docxWith(t, `<w:p/>`, map[string]string{partStyles: styles}))
	for style, want := range map[string]struct {
		numID string
		ilvl  int
	}{"ListBullet": {"4", 0}, "ListBullet2": {"4", 1}, "Mine": {"4", 0}, "Off": {"", 0}, "Normal": {"", 0}} {
		if id, lvl := f.StyleList(style); id != want.numID || lvl != want.ilvl {
			t.Errorf("%s: list %q level %d, want %q %d", style, id, lvl, want.numID, want.ilvl)
		}
	}
}

// Tracked, deleting a paragraph leaves text and a mark a suggestion
// already deleted as they are, and refuses a paragraph a suggestion
// inserted.
func TestDeleteParasTrackedOverSuggestions(t *testing.T) {
	deleted := `<w:del w:id="1" w:author="A"><w:r><w:delText>gone</w:delText></w:r></w:del>`
	f := open(t, docx(t, `<w:p><w:r><w:t>keep </w:t></w:r>`+deleted+`<w:r><w:t> end</w:t></w:r></w:p>`+
		`<w:p><w:pPr><w:rPr><w:del w:id="5" w:author="A"/></w:rPr></w:pPr><w:r><w:t>marked</w:t></w:r></w:p>`+
		`<w:p><w:pPr><w:rPr><w:ins w:id="6" w:author="A"/></w:rPr></w:pPr><w:r><w:t>new</w:t></w:r></w:p><w:p/>`))
	if err := f.DeleteParas(0, 1, false, track); err != nil {
		t.Fatal(err)
	}
	g, doc := written(t, f)
	if g.Paras[0].Text != "keep gone end" || !strings.Contains(doc, deleted) {
		t.Fatalf("the earlier deletion did not stay as it was:\n%s", doc)
	}
	if n := strings.Count(doc, `w:author="Probe Author"`); n != 4 {
		t.Errorf("%d tracked changes, want 4: the two stretches of text, the first mark and the second paragraph's text", n)
	}
	if err := open(t, docx(t, `<w:p><w:pPr><w:rPr><w:ins w:id="6" w:author="A"/></w:rPr></w:pPr><w:r><w:t>new</w:t></w:r></w:p><w:p/>`)).DeleteParas(0, 0, false, track); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("a paragraph a suggestion inserted: %v", err)
	}
}

// An empty paragraph written out to take content keeps its attributes.
func TestEmptyParagraphKeepsAttributes(t *testing.T) {
	f := open(t, docx(t, `<w:p w:rsidR="00AB12CD" w:rsidRDefault="00AB12CD"/><w:p><w:r><w:t>last</w:t></w:r></w:p>`))
	if err := f.Replace(0, 0, 0, []Run{{Text: "first"}}, nil); err != nil {
		t.Fatal(err)
	}
	g, doc := written(t, f)
	if g.Paras[0].Text != "first" || !strings.Contains(doc, `<w:p w:rsidR="00AB12CD" w:rsidRDefault="00AB12CD"><w:r>`) {
		t.Fatalf("text %q:\n%s", g.Paras[0].Text, doc)
	}
}

// Parts the edits leave alone are copied from the zip as they were.
func TestBytesCopiesUntouchedParts(t *testing.T) {
	src := brief(t)
	f := open(t, src)
	if err := f.Replace(para(t, f, "Two"), 0, 3, []Run{{Text: "2"}}, nil); err != nil {
		t.Fatal(err)
	}
	out, _, err := f.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	a, _ := zip.NewReader(bytes.NewReader(src), int64(len(src)))
	b, _ := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if len(a.File) != len(b.File) {
		t.Fatalf("%d entries, want %d", len(b.File), len(a.File))
	}
	for k, zf := range a.File {
		same := zf.CRC32 == b.File[k].CRC32
		if same == (zf.Name == partDocument) {
			t.Errorf("%s: changed %v", zf.Name, !same)
		}
	}
}
