//go:build integration

package gapi

// Spike G: what can the API do with a Word file (.docx) in Drive? A
// person asked for edits to one, directly or as suggestions. Runs against
// the caller's own account and leaves two clearly named scratch files
// behind: the .docx and a Google Doc converted from it.
//
//	GDOCS_INTEGRATION=1 go test -tags=integration ./internal/gapi -run TestRawDocx -v
//
// The file is built here, synthetic: plain text, a Word tracked insertion
// and deletion, and a Word comment, all by a made-up author. The
// questions are whether Docs reads such a file, whether it reads the
// tracked changes as suggestions and the comment as a comment, whether
// any write is accepted, and what a converted copy and its export keep.
//
// What it established, 2026-10-21: documents.get reads the file, the
// tracked changes as suggestions and the comment as an anchored comment
// (fromImportedDocument); every batchUpdate, direct or SUGGEST, is 400
// "The document must not be an Office file"; Drive's comments.list on it
// is empty; a converted copy keeps both, and its .docx export holds
// w:ins, w:del and the comment.

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"
)

const docxBody = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>
<w:p><w:r><w:t>Alpha bravo charlie.</w:t></w:r></w:p>
<w:p><w:r><w:t xml:space="preserve">Delta </w:t></w:r><w:ins w:id="1" w:author="Probe Author" w:date="2026-10-21T12:00:00Z"><w:r><w:t xml:space="preserve">inserted words </w:t></w:r></w:ins><w:r><w:t>echo.</w:t></w:r></w:p>
<w:p><w:r><w:t xml:space="preserve">Foxtrot </w:t></w:r><w:del w:id="2" w:author="Probe Author" w:date="2026-10-21T12:00:00Z"><w:r><w:delText xml:space="preserve">deleted words </w:delText></w:r></w:del><w:r><w:t>golf.</w:t></w:r></w:p>
<w:p><w:r><w:t xml:space="preserve">Hotel </w:t></w:r><w:commentRangeStart w:id="0"/><w:r><w:t>commented text</w:t></w:r><w:commentRangeEnd w:id="0"/><w:r><w:commentReference w:id="0"/></w:r><w:r><w:t xml:space="preserve"> india.</w:t></w:r></w:p>
</w:body>
</w:document>`

const docxComments = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:comments xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:comment w:id="0" w:author="Probe Author" w:date="2026-10-21T12:00:00Z" w:initials="PA"><w:p><w:r><w:t>A synthetic Word comment.</w:t></w:r></w:p></w:comment>
</w:comments>`

// buildDocx zips the smallest package Word opens: content types, the
// package relationship, the document, and its comments part.
func buildDocx(t *testing.T) []byte {
	t.Helper()
	parts := []struct{ name, data string }{
		{"[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
<Default Extension="xml" ContentType="application/xml"/>
<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
<Override PartName="/word/comments.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.comments+xml"/>
</Types>`},
		{"_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`},
		{"word/_rels/document.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/comments" Target="comments.xml"/>
</Relationships>`},
		{"word/document.xml", docxBody},
		{"word/comments.xml", docxComments},
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, p := range parts {
		w, err := zw.Create(p.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, p.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// runsSummary is one line per paragraph from a documents.get body: each
// run's text with any suggested insertion or deletion marked.
func runsSummary(t *testing.T, body string) string {
	t.Helper()
	var doc struct {
		Tabs []struct {
			DocumentTab struct {
				Body struct {
					Content []struct {
						Paragraph *struct {
							Elements []map[string]json.RawMessage `json:"elements"`
						} `json:"paragraph"`
					} `json:"content"`
				} `json:"body"`
			} `json:"documentTab"`
		} `json:"tabs"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var b strings.Builder
	for _, el := range doc.Tabs[0].DocumentTab.Body.Content {
		if el.Paragraph == nil {
			continue
		}
		for _, e := range el.Paragraph.Elements {
			var keys []string
			for k := range e {
				if k != "startIndex" && k != "endIndex" {
					keys = append(keys, k)
				}
			}
			sort.Strings(keys)
			var run struct {
				Content     string   `json:"content"`
				Insertions  []string `json:"suggestedInsertionIds"`
				Deletions   []string `json:"suggestedDeletionIds"`
				StyleChange any      `json:"suggestedTextStyleChanges"`
			}
			if raw, ok := e["textRun"]; ok {
				if err := json.Unmarshal(raw, &run); err != nil {
					t.Fatal(err)
				}
			}
			fmt.Fprintf(&b, "%q %v", run.Content, keys)
			if len(run.Insertions) > 0 {
				fmt.Fprintf(&b, " INSERTION %v", run.Insertions)
			}
			if len(run.Deletions) > 0 {
				fmt.Fprintf(&b, " DELETION %v", run.Deletions)
			}
			b.WriteString(" | ")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// commentKeys lists every JSON path in body whose key mentions a comment,
// which is how a comments view would show an anchor.
func commentKeys(body string) []string {
	var v any
	if json.Unmarshal([]byte(body), &v) != nil {
		return nil
	}
	var out []string
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, c := range x {
				if strings.Contains(strings.ToLower(k), "comment") {
					raw, _ := json.Marshal(c)
					out = append(out, path+"."+k+"="+string(raw))
				}
				walk(path+"."+k, c)
			}
		case []any:
			for i, c := range x {
				walk(fmt.Sprintf("%s[%d]", path, i), c)
			}
		}
	}
	walk("", v)
	sort.Strings(out)
	return out
}

func logComments(t *testing.T, c *Client, id, what string) {
	t.Helper()
	cs, err := c.ListComments(context.Background(), id, false)
	if err != nil {
		t.Logf("comments.list on %s: %v", what, err)
		return
	}
	t.Logf("comments.list on %s: %d comment(s)", what, len(cs))
	for _, cm := range cs {
		raw, _ := json.Marshal(cm)
		t.Logf("  %s", raw)
	}
}

// docxParts reports what an exported .docx carries: tracked changes in
// the document part, and the comments part.
func docxParts(t *testing.T, data []byte) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "not a zip: " + err.Error()
	}
	var b strings.Builder
	for _, f := range zr.File {
		if f.Name != "word/document.xml" && f.Name != "word/comments.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		x, _ := io.ReadAll(rc)
		_ = rc.Close()
		s := string(x)
		fmt.Fprintf(&b, "%s: %d bytes, <w:ins %d, <w:del %d, commentRangeStart %d, author=\"Probe Author\" %d\n",
			f.Name, len(s), strings.Count(s, "<w:ins "), strings.Count(s, "<w:del "),
			strings.Count(s, "commentRangeStart"), strings.Count(s, `w:author="Probe Author"`))
		if f.Name == "word/comments.xml" {
			fmt.Fprintf(&b, "  holds the comment text: %t\n", strings.Contains(s, "A synthetic Word comment."))
		}
	}
	return b.String()
}

func TestRawDocx(t *testing.T) {
	c := probeClient(t)
	ctx := context.Background()
	f, err := c.CreateFile(ctx, NewFile{Name: "google-docs-mcp docx probe (safe to delete).docx", MimeType: WordMimeType}, WordMimeType, buildDocx(t))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	t.Logf("uploaded %s as %s", f.ID, f.MimeType)

	// 1. Does Docs read it, and what does it make of the tracked changes?
	docURL := c.docs + "/v1/documents/" + url.PathEscape(f.ID) + "?includeTabsContent=true&suggestionsViewMode=" + SuggestionsInline
	status, body := rawCall(t, c, http.MethodGet, docURL, "", nil)
	if status == http.StatusOK {
		t.Logf("documents.get on the .docx: 200\n%s", runsSummary(t, body))
	} else {
		logCall(t, "documents.get on the .docx", status, body, 1500)
	}
	// 2. The comments view: is the Word comment an anchor?
	status, body = rawCall(t, c, http.MethodGet, docURL+"&commentsViewMode="+CommentsIncluded, "", nil)
	if status == http.StatusOK {
		t.Logf("documents.get comments view on the .docx: comment keys %v", commentKeys(body))
	} else {
		logCall(t, "documents.get comments view on the .docx", status, body, 1500)
	}
	// 3. Is any write accepted, directly or as a suggestion?
	insert := []byte(`{"requests":[{"insertText":{"text":"Probe ","location":{"index":1}}}]}`)
	status, body = rawCall(t, c, http.MethodPost, c.docs+"/v1/documents/"+url.PathEscape(f.ID)+":batchUpdate", "application/json", insert)
	logCall(t, "batchUpdate direct on the .docx", status, body, 1500)
	suggest := []byte(`{"requests":[{"insertText":{"text":"Probe ","location":{"index":1}}}],"writeControl":{"writeMode":"SUGGEST"}}`)
	status, body = rawCall(t, c, http.MethodPost, c.docs+"/v1/documents/"+url.PathEscape(f.ID)+":batchUpdate", "application/json", suggest)
	logCall(t, "batchUpdate SUGGEST on the .docx", status, body, 1500)
	// 4. Does Drive see the Word comment? And does a comment made through
	// Drive's API, the way add_comment posts one it cannot anchor, land,
	// and show in the Docs comments view?
	logComments(t, c, f.ID, "the .docx")
	if _, err := c.CreateComment(ctx, f.ID, "A synthetic Drive comment.", "Alpha bravo"); err != nil {
		t.Logf("comments.create on the .docx: %v", err)
	}
	logComments(t, c, f.ID, "the .docx after comments.create")
	if status, body := rawCall(t, c, http.MethodGet, docURL+"&commentsViewMode="+CommentsIncluded, "", nil); status == http.StatusOK {
		t.Logf("comments view after comments.create: %d comment key(s) mention the Drive comment: %t",
			len(commentKeys(body)), strings.Contains(body, "A synthetic Drive comment."))
	}

	// 5. A converted copy: suggestions and comments carried over?
	copyBody := []byte(`{"name":"google-docs-mcp docx probe converted (safe to delete)","mimeType":"` + DocumentMimeType + `"}`)
	status, body = rawCall(t, c, http.MethodPost, c.drive+"/files/"+url.PathEscape(f.ID)+"/copy?fields=id,mimeType", "application/json", copyBody)
	logCall(t, "files.copy converting to a Google Doc", status, body, 1500)
	if status != http.StatusOK {
		return
	}
	var cp struct{ ID string }
	if err := json.Unmarshal([]byte(body), &cp); err != nil {
		t.Fatal(err)
	}
	cpURL := c.docs + "/v1/documents/" + url.PathEscape(cp.ID) + "?includeTabsContent=true&suggestionsViewMode=" + SuggestionsInline
	status, body = rawCall(t, c, http.MethodGet, cpURL, "", nil)
	if status == http.StatusOK {
		t.Logf("documents.get on the converted copy:\n%s", runsSummary(t, body))
	} else {
		logCall(t, "documents.get on the converted copy", status, body, 1500)
	}
	status, body = rawCall(t, c, http.MethodGet, cpURL+"&commentsViewMode="+CommentsIncluded, "", nil)
	if status == http.StatusOK {
		t.Logf("comments view on the converted copy: comment keys %v", commentKeys(body))
	}
	logComments(t, c, cp.ID, "the converted copy")

	// 6. Exported back to .docx: tracked changes and comments kept?
	data, err := c.Export(ctx, cp.ID, WordMimeType)
	if err != nil {
		t.Logf("export: %v", err)
		return
	}
	t.Logf("export of the converted copy as .docx:\n%s", docxParts(t, data))
	t.Logf("=== left behind: %s (.docx) and %s (converted) ===", f.ID, cp.ID)
}
