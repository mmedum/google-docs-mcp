//go:build integration

package gapi

// Spike H: what a real .docx looks like, and what Docs does with one this
// server edits. A Google Doc made from synthetic content is exported as
// .docx, which gives a file with the parts Word writes: styles, numbering,
// settings, relationships.
//
//	GDOCS_INTEGRATION=1 go test -tags=integration ./internal/gapi -run TestRawDocxExport -v

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mmedum/google-docs-mcp/v2/internal/gdocs"
	"github.com/mmedum/google-docs-mcp/v2/internal/ooxml"
)

const docxCorpus = `# Brief

Intro paragraph with **bold** and a [link](https://example.com).

## Points

- First point
    - Nested point
- Second point

1. One
2. Two

| A | B |
|---|---|
| 1 | 2 |

Closing line.`

func TestRawDocxExport(t *testing.T) {
	c := probeClient(t)
	ctx := context.Background()
	f, err := c.CreateFile(ctx, NewFile{Name: "google-docs-mcp docx export probe (safe to delete)", MimeType: DocumentMimeType}, "text/markdown", []byte(docxCorpus))
	if err != nil {
		t.Fatal(err)
	}
	data, err := c.Export(ctx, f.ID, WordMimeType)
	if err != nil {
		t.Fatal(err)
	}
	if out := os.Getenv("GDOCS_DOCX_OUT"); out != "" {
		if err := os.WriteFile(out, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, zf := range zr.File {
		names = append(names, zf.Name)
	}
	sort.Strings(names)
	t.Logf("parts: %s", strings.Join(names, ", "))
	for _, zf := range zr.File {
		if zf.Name != "docProps/core.xml" && zf.Name != "docProps/app.xml" {
			continue
		}
		rc, _ := zf.Open()
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		t.Logf("--- %s ---\n%s", zf.Name, b)
	}
	t.Logf("=== left behind: the Google Doc %s ===", f.ID)
}

// docxParagraphs is each w:p's text in document order, tables included,
// read crudely: w:t and w:delText, w:tab as a tab.
func docxParagraphs(xml string) []string {
	var out []string
	for _, p := range regexp.MustCompile(`(?s)<w:p[ >].*?</w:p>`).FindAllString(xml, -1) {
		var b strings.Builder
		for _, m := range regexp.MustCompile(`(?s)<w:(?:t|delText)(?: [^>]*)?>(.*?)</w:(?:t|delText)>|<w:tab/>`).FindAllStringSubmatch(p, -1) {
			if m[0] == "<w:tab/>" {
				b.WriteString("\t")
			} else {
				b.WriteString(m[1])
			}
		}
		out = append(out, b.String())
	}
	return out
}

// rezip copies a .docx, replacing the parts named in parts.
func rezip(t *testing.T, data []byte, parts map[string]string) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		if body, ok := parts[f.Name]; ok {
			w, err := zw.Create(f.Name)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.WriteString(w, body)
			delete(parts, f.Name)
			continue
		}
		if err := zw.Copy(f); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range parts {
		w, _ := zw.Create(name)
		_, _ = io.WriteString(w, body)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func zipPart(t *testing.T, data []byte, name string) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.Name == name {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			_ = rc.Close()
			return string(b)
		}
	}
	return ""
}

// TestRawDocxRevision: the exported file uploaded as a Word file, its
// paragraphs set beside the Docs model's, then a revision with tracked
// changes, a new list item and a comment uploaded over it, and read back.
func TestRawDocxRevision(t *testing.T) {
	c := probeClient(t)
	ctx := context.Background()
	src, err := os.ReadFile(os.Getenv("GDOCS_DOCX_IN"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := c.CreateFile(ctx, NewFile{Name: "google-docs-mcp docx revision probe (safe to delete).docx", MimeType: WordMimeType}, WordMimeType, src)
	if err != nil {
		t.Fatal(err)
	}
	docURL := c.docs + "/v1/documents/" + url.PathEscape(f.ID) + "?includeTabsContent=true&suggestionsViewMode=" + SuggestionsInline
	_, body := rawCall(t, c, http.MethodGet, docURL, "", nil)
	var d gdocs.Document
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		t.Fatal(err)
	}
	var docsParas []string
	for _, el := range flatten(d.Tabs[0].DocumentTab.Body.Content) {
		var b strings.Builder
		for _, e := range el.Elements {
			if e.TextRun != nil {
				b.WriteString(e.TextRun.Content)
			}
		}
		docsParas = append(docsParas, strings.TrimSuffix(b.String(), "\n"))
	}
	xml := zipPart(t, src, "word/document.xml")
	t.Logf("Docs paragraphs %d, file paragraphs %d", len(docsParas), len(docxParagraphs(xml)))
	t.Logf("Docs: %q", docsParas)
	t.Logf("file: %q", docxParagraphs(xml))
	t.Logf("Docs revision before: %s", d.RevisionID)

	// The revision: "Closing line." becomes "Closing words." as a tracked
	// change; a nested item after "Second point" as a tracked insertion;
	// a comment on "Intro".
	ins := `<w:ins w:id="901" w:author="Probe Author" w:date="2026-10-21T12:00:00Z">`
	xml2 := strings.Replace(xml, `<w:t xml:space="preserve">Closing line.</w:t></w:r>`,
		`<w:t xml:space="preserve">Closing </w:t></w:r><w:del w:id="902" w:author="Probe Author" w:date="2026-10-21T12:00:00Z"><w:r><w:delText xml:space="preserve">line</w:delText></w:r></w:del>`+
			ins+`<w:r><w:t xml:space="preserve">words</w:t></w:r></w:ins><w:r><w:t xml:space="preserve">.</w:t></w:r>`, 1)
	i := strings.Index(xml2, "Second point")
	j := i + strings.Index(xml2[i:], "</w:p>") + len("</w:p>")
	xml2 = xml2[:j] + `<w:p><w:pPr><w:numPr><w:ilvl w:val="1"/><w:numId w:val="2"/></w:numPr><w:ind w:left="1440" w:hanging="360"/><w:rPr><w:ins w:id="903" w:author="Probe Author" w:date="2026-10-21T12:00:00Z"/></w:rPr></w:pPr>` +
		`<w:ins w:id="904" w:author="Probe Author" w:date="2026-10-21T12:00:00Z"><w:r><w:t xml:space="preserve">Inserted nested</w:t></w:r></w:ins></w:p>` + xml2[j:]
	xml2 = strings.Replace(xml2, `<w:t xml:space="preserve">Intro paragraph with </w:t></w:r>`,
		`<w:t xml:space="preserve">Intro paragraph with </w:t></w:r><w:commentRangeStart w:id="905"/><w:commentRangeEnd w:id="905"/><w:r><w:commentReference w:id="905"/></w:r>`, 1)
	comments := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:comments xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:comment w:id="905" w:author="Probe Author" w:date="2026-10-21T12:00:00Z" w:initials="PA"><w:p><w:r><w:t>A probe comment.</w:t></w:r></w:p></w:comment></w:comments>`
	rels := strings.Replace(zipPart(t, src, "word/_rels/document.xml.rels"), "</Relationships>",
		`<Relationship Id="rId900" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/comments" Target="comments.xml"/></Relationships>`, 1)
	types := strings.Replace(zipPart(t, src, "[Content_Types].xml"), "</Types>",
		`<Override ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.comments+xml" PartName="/word/comments.xml"/></Types>`, 1)
	out := rezip(t, src, map[string]string{"word/document.xml": xml2, "word/comments.xml": comments,
		"word/_rels/document.xml.rels": rels, "[Content_Types].xml": types})
	start := time.Now()
	if _, err := c.UploadFile(ctx, f.ID, out, WordMimeType); err != nil {
		t.Fatal(err)
	}
	for k := 0; k < 10; k++ {
		_, body = rawCall(t, c, http.MethodGet, docURL+"&commentsViewMode="+CommentsIncluded, "", nil)
		var d2 gdocs.Document
		if err := json.Unmarshal([]byte(body), &d2); err != nil {
			t.Fatal(err)
		}
		if d2.RevisionID != d.RevisionID {
			t.Logf("Docs revision moved after %s (read %d)", time.Since(start).Round(time.Millisecond), k+1)
			t.Logf("after:\n%s", runsSummary(t, body))
			t.Logf("comment keys: %v", commentKeys(body))
			for _, el := range flatten(d2.Tabs[0].DocumentTab.Body.Content) {
				if el.Bullet != nil {
					var b strings.Builder
					for _, e := range el.Elements {
						if e.TextRun != nil {
							b.WriteString(e.TextRun.Content)
						}
					}
					t.Logf("list item %q level %d list %s", strings.TrimSpace(b.String()), el.Bullet.NestingLevel, el.Bullet.ListID)
				}
			}
			return
		}
		time.Sleep(time.Second)
	}
	t.Logf("Docs revision did not move in 10 s")
}

// flatten is every paragraph of a body, table cells included, in order.
func flatten(content []*gdocs.StructuralElement) []*gdocs.Paragraph {
	var out []*gdocs.Paragraph
	for _, el := range content {
		switch {
		case el.Paragraph != nil:
			out = append(out, el.Paragraph)
		case el.Table != nil:
			for _, row := range el.Table.TableRows {
				for _, cell := range row.TableCells {
					out = append(out, flatten(cell.Content)...)
				}
			}
		}
	}
	return out
}

// TestRawDocxEngine uploads a file internal/ooxml edited, every kind of
// edit at once, and reads it back through Docs.
func TestRawDocxEngine(t *testing.T) {
	c := probeClient(t)
	ctx := context.Background()
	src, err := os.ReadFile("../ooxml/testdata/brief.docx")
	if err != nil {
		t.Fatal(err)
	}
	f, err := ooxml.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	tr := &ooxml.Track{Author: "Probe Author", Date: "2026-10-21T12:00:00Z"}
	idx := func(text string) int {
		for i, p := range f.Paras {
			if p.Text == text {
				return i
			}
		}
		t.Fatalf("no %q", text)
		return -1
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(f.Replace(idx("Closing line."), 8, 12, []ooxml.Run{{Text: "words"}}, tr))
	numbered, err := f.NewList(true)
	must(err)
	must(f.InsertParas(idx("Second point"), true, []ooxml.NewPara{
		{NumID: "2", Ilvl: 1, Runs: []ooxml.Run{{Text: "Deeper"}}},
		{Heading: 3, Runs: []ooxml.Run{{Text: "New heading"}}},
		{NumID: numbered, Runs: []ooxml.Run{{Text: "Fresh one"}}},
		{NumID: numbered, Ilvl: 1, Runs: []ooxml.Run{{Text: "Fresh nested ", Styled: true, Italic: true}, {Text: "link", Link: "https://example.com/x"}}},
	}, tr))
	must(f.DeleteParas(idx("Two"), idx("Two"), false, tr))
	_, err = f.Comment(idx("Brief"), 0, idx("Brief"), 5, ooxml.NewComment{Author: "Probe Author", Initials: "PA", Date: "2026-10-21T12:00:00Z", Text: "On the title."})
	must(err)
	out, _, err := f.Bytes()
	must(err)
	up, err := c.CreateFile(ctx, NewFile{Name: "google-docs-mcp docx engine probe (safe to delete).docx", MimeType: WordMimeType}, WordMimeType, out)
	must(err)
	docURL := c.docs + "/v1/documents/" + url.PathEscape(up.ID) + "?includeTabsContent=true&suggestionsViewMode=" + SuggestionsInline + "&commentsViewMode=" + CommentsIncluded
	status, body := rawCall(t, c, http.MethodGet, docURL, "", nil)
	if status != http.StatusOK {
		logCall(t, "documents.get", status, body, 1500)
		return
	}
	t.Logf("runs:\n%s", runsSummary(t, body))
	var d gdocs.Document
	must(json.Unmarshal([]byte(body), &d))
	for _, p := range flatten(d.Tabs[0].DocumentTab.Body.Content) {
		var b strings.Builder
		for _, e := range p.Elements {
			if e.TextRun != nil {
				b.WriteString(e.TextRun.Content)
			}
		}
		style := ""
		if p.ParagraphStyle != nil {
			style = p.ParagraphStyle.NamedStyleType
		}
		list := ""
		if p.Bullet != nil {
			list = fmt.Sprintf(" list=%s level=%d", p.Bullet.ListID, p.Bullet.NestingLevel)
		}
		t.Logf("%-16s %q%s", style, strings.TrimSuffix(b.String(), "\n"), list)
	}
	t.Logf("comments: %d", len(d.Comments))
	t.Logf("=== left behind: %s ===", up.ID)
}

// TestRawDocxModel saves what documents.get answers for the fixture
// internal/ooxml/testdata/brief.docx, as the service reads it, for the
// service's tests: GDOCS_DOCX_MODEL names the file to write.
func TestRawDocxModel(t *testing.T) {
	c := probeClient(t)
	ctx := context.Background()
	src, err := os.ReadFile("../ooxml/testdata/brief.docx")
	if err != nil {
		t.Fatal(err)
	}
	up, err := c.CreateFile(ctx, NewFile{Name: "brief.docx", MimeType: WordMimeType}, WordMimeType, src)
	if err != nil {
		t.Fatal(err)
	}
	q := url.Values{}
	q.Set("includeTabsContent", "true")
	q.Set("suggestionsViewMode", SuggestionsInline)
	q.Set("commentsViewMode", CommentsIncluded)
	body, err := c.do(ctx, kindRead, http.MethodGet, c.docs+"/v1/documents/"+url.PathEscape(up.ID)+"?"+q.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv("GDOCS_DOCX_MODEL"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("=== left behind: %s ===", up.ID)
}
