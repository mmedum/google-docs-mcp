//go:build integration

package gapi

// Spike D: what can the API do with a Markdown file, now that Docs edits
// .md files in place without converting them (Workspace Updates,
// 2026-10-05)? Runs against the caller's own account and leaves two
// clearly named scratch files behind.
//
//	GDOCS_INTEGRATION=1 go test -tags=integration ./internal/gapi -run TestRawMarkdownFile -v
//
// The second half needs a person: open the .md file the first test
// leaves behind in Docs, edit it, comment on a word, then
//
//	GDOCS_INTEGRATION=1 GDOCS_MD_PROBE_ID=<id> go test -tags=integration ./internal/gapi -run TestRawMarkdownAfterDocs -v
//
// and once more with GDOCS_MD_PROBE_OVERWRITE=1 to see what a Drive-side
// rewrite of the bytes does to that comment.
//
// Every call goes through rawCall rather than the client's methods: a
// refusal's status and body are the finding, and the client's error
// mapping would hide both.

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mmedum/google-docs-mcp/v2/internal/gdocs"
)

// markdownCorpus is synthetic and covers what people put in Markdown
// files, with a few alternative spellings (star bullets, underscore
// emphasis) so an editor's normalization shows.
var markdownCorpus = strings.Join([]string{
	"---",
	"title: Markdown probe",
	"tags: [probe, synthetic]",
	"---",
	"",
	"# Markdown probe",
	"",
	"A paragraph with **bold**, *italic*, __underscore bold__, _underscore italic_, `inline code`, ~~strike~~ and a [link](https://example.com/).",
	"",
	"## Lists",
	"",
	"- dash bullet one",
	"- dash bullet two",
	"  - nested bullet",
	"",
	"* star bullet",
	"",
	"1. first",
	"2. second",
	"",
	"- [ ] open task",
	"- [x] done task",
	"",
	"## Table",
	"",
	"| Name | Value |",
	"|:-----|------:|",
	"| alpha | 1 |",
	"| beta | 2 |",
	"",
	"## Code",
	"",
	"```go",
	"func main() {}",
	"```",
	"",
	"> A blockquote line.",
	"",
	"Text with a footnote.[^1]",
	"",
	"[^1]: The footnote body.",
	"",
	"![An image](https://example.com/probe.png)",
	"",
	`<span style="color:red">inline html</span>`,
	"",
	"---",
	"",
	"Final line with a hard break  ",
	"after two trailing spaces.",
}, "\n") + "\n"

const markdownProbeName = "google-docs-mcp markdown probe (safe to delete)"

// rawCall sends one request through the authorized HTTP client, skipping
// retries and error parsing.
func rawCall(t *testing.T, c *Client, method, u, contentType string, body []byte) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.httpc.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, u, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(data)
}

func logCall(t *testing.T, what string, status int, body string, limit int) {
	t.Helper()
	if limit > 0 && len(body) > limit {
		body = body[:limit] + fmt.Sprintf("\n… (%d bytes in all)", len(body))
	}
	t.Logf("--- %s: HTTP %d ---\n%s", what, status, body)
}

func uploadBase(c *Client) string {
	return strings.Replace(c.drive, "/drive/v3", "/upload/drive/v3", 1)
}

// uploadMarkdown creates a file from the corpus. targetMime is the file's
// own type: text/markdown keeps it a Markdown file, the Docs type asks
// Drive to convert it on import.
func uploadMarkdown(t *testing.T, c *Client, name, targetMime string) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	meta, err := json.Marshal(map[string]string{"name": name, "mimeType": targetMime})
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []struct {
		contentType string
		data        []byte
	}{
		{"application/json; charset=UTF-8", meta},
		{"text/markdown", []byte(markdownCorpus)},
	} {
		h := textproto.MIMEHeader{}
		h.Set("Content-Type", part.contentType)
		pw, err := w.CreatePart(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pw.Write(part.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	status, body := rawCall(t, c, http.MethodPost, uploadBase(c)+"/files?uploadType=multipart&fields=id,name,mimeType,webViewLink",
		"multipart/related; boundary="+w.Boundary(), buf.Bytes())
	logCall(t, "upload as "+targetMime, status, body, 0)
	if status != http.StatusOK {
		t.Fatalf("upload as %s refused", targetMime)
	}
	var f map[string]any
	if err := json.Unmarshal([]byte(body), &f); err != nil {
		t.Fatal(err)
	}
	return f
}

// lineDiff lists the lines only one side has, which is enough to see
// what an editor normalized. A pure reordering does not show.
func lineDiff(before, after string) string {
	count := map[string]int{}
	for _, l := range strings.Split(before, "\n") {
		count[l]++
	}
	for _, l := range strings.Split(after, "\n") {
		count[l]--
	}
	var gone, added []string
	for _, l := range strings.Split(before, "\n") {
		if count[l] > 0 {
			gone = append(gone, "- "+l)
			count[l]--
		}
	}
	for _, l := range strings.Split(after, "\n") {
		if count[l] < 0 {
			added = append(added, "+ "+l)
			count[l]++
		}
	}
	if len(gone)+len(added) == 0 {
		return "(identical lines)"
	}
	return strings.Join(append(gone, added...), "\n")
}

func docsGet(t *testing.T, c *Client, id string) (int, string) {
	t.Helper()
	return rawCall(t, c, http.MethodGet, c.docs+"/v1/documents/"+url.PathEscape(id)+"?includeTabsContent=true", "", nil)
}

func TestRawMarkdownFile(t *testing.T) {
	c := probeClient(t)

	// What Drive itself says it can import and export.
	status, body := rawCall(t, c, http.MethodGet, c.drive+"/about?fields=importFormats,exportFormats", "", nil)
	if status != http.StatusOK {
		logCall(t, "about", status, body, 2000)
	} else {
		var about struct {
			ImportFormats map[string][]string `json:"importFormats"`
			ExportFormats map[string][]string `json:"exportFormats"`
		}
		if err := json.Unmarshal([]byte(body), &about); err != nil {
			t.Fatal(err)
		}
		for k, v := range about.ImportFormats {
			if strings.Contains(k, "markdown") {
				t.Logf("about.importFormats[%s] = %v", k, v)
			}
		}
		t.Logf("about.exportFormats[%s] = %v", DocumentMimeType, about.ExportFormats[DocumentMimeType])
	}

	// The file as a Markdown file, unconverted.
	md := uploadMarkdown(t, c, markdownProbeName+".md", "text/markdown")
	id, _ := md["id"].(string)
	fileURL := c.drive + "/files/" + url.PathEscape(id)

	status, body = rawCall(t, c, http.MethodGet, fileURL+"?fields=*", "", nil)
	logCall(t, "files.get fields=*", status, body, 0)

	status, body = rawCall(t, c, http.MethodGet, fileURL+"?alt=media", "", nil)
	t.Logf("--- alt=media: HTTP %d, bytes identical to the upload: %t ---", status, body == markdownCorpus)
	if body != markdownCorpus {
		t.Logf("%s", lineDiff(markdownCorpus, body))
	}

	status, body = rawCall(t, c, http.MethodGet, fileURL+"/export?mimeType="+url.QueryEscape("text/markdown"), "", nil)
	logCall(t, "files.export text/markdown", status, body, 1500)

	// The question that decides the most: does the Docs API take the id?
	status, body = docsGet(t, c, id)
	logCall(t, "documents.get on the .md id", status, body, 4000)

	insert := marshal(t, map[string]any{"requests": []json.RawMessage{insertAt(t, "Inserted by the probe. ", 1)}})
	status, body = rawCall(t, c, http.MethodPost, c.docs+"/v1/documents/"+url.PathEscape(id)+":batchUpdate", acceptJSON, insert)
	logCall(t, "documents.batchUpdate insertText on the .md id", status, body, 2000)

	// Comments through Drive, the one comments API a non-Docs file has.
	comment := marshal(t, map[string]any{
		"content":           "Probe comment quoting a word.",
		"quotedFileContent": map[string]string{"mimeType": "text/plain", "value": "bold"},
	})
	status, body = rawCall(t, c, http.MethodPost, fileURL+"/comments?fields=*", acceptJSON, comment)
	logCall(t, "comments.create", status, body, 0)
	status, body = rawCall(t, c, http.MethodGet, fileURL+"/comments?fields=*", "", nil)
	logCall(t, "comments.list", status, body, 0)

	status, body = rawCall(t, c, http.MethodGet, fileURL+"/revisions?fields=*", "", nil)
	logCall(t, "revisions.list", status, body, 0)

	// Spike C: the same Markdown converted to a Google Doc on import, and
	// exported back.
	conv := uploadMarkdown(t, c, markdownProbeName+" (converted)", DocumentMimeType)
	convID, _ := conv["id"].(string)
	status, body = docsGet(t, c, convID)
	t.Logf("--- documents.get on the converted Doc: HTTP %d, %d bytes ---", status, len(body))
	status, body = rawCall(t, c, http.MethodGet, c.drive+"/files/"+url.PathEscape(convID)+"/export?mimeType="+url.QueryEscape("text/markdown"), "", nil)
	logCall(t, "converted Doc exported back to text/markdown", status, body, 0)
	if status == http.StatusOK {
		t.Logf("--- round trip, lines only one side has ---\n%s", lineDiff(markdownCorpus, body))
	}

	t.Logf("=== stage 2: open %v in Docs, edit it, comment on a word, then rerun with GDOCS_MD_PROBE_ID=%s ===", md["webViewLink"], id)
}

func TestRawMarkdownAfterDocs(t *testing.T) {
	id := os.Getenv("GDOCS_MD_PROBE_ID")
	if id == "" {
		t.Skip("set GDOCS_MD_PROBE_ID to the .md file TestRawMarkdownFile left behind")
	}
	c := probeClient(t)
	fileURL := c.drive + "/files/" + url.PathEscape(id)

	status, body := rawCall(t, c, http.MethodGet, fileURL+"?fields=*", "", nil)
	logCall(t, "files.get fields=*", status, body, 0)

	status, body = rawCall(t, c, http.MethodGet, fileURL+"/revisions?fields=*", "", nil)
	logCall(t, "revisions.list", status, body, 0)

	status, content := rawCall(t, c, http.MethodGet, fileURL+"?alt=media", "", nil)
	logCall(t, "alt=media", status, content, 0)
	t.Logf("--- against the uploaded corpus, lines only one side has ---\n%s", lineDiff(markdownCorpus, content))

	status, body = rawCall(t, c, http.MethodGet, fileURL+"/comments?fields=*&includeDeleted=true", "", nil)
	logCall(t, "comments.list", status, body, 0)

	status, body = docsGet(t, c, id)
	logCall(t, "documents.get on the .md id", status, body, 4000)

	if os.Getenv("GDOCS_MD_PROBE_OVERWRITE") == "" {
		t.Logf("=== set GDOCS_MD_PROBE_OVERWRITE=1 to append a line through Drive and see what happens to the comments ===")
		return
	}
	// The gentlest Drive-side edit there is: the bytes Docs wrote, with a
	// line appended, so every commented word is still where it was.
	status, body = rawCall(t, c, http.MethodPatch, uploadBase(c)+"/files/"+url.PathEscape(id)+"?uploadType=media&fields=id,mimeType,modifiedTime",
		"text/markdown", []byte(content+"\nAppended through Drive by the probe.\n"))
	logCall(t, "files.update media (append one line)", status, body, 0)
	status, body = rawCall(t, c, http.MethodGet, fileURL+"/comments?fields=*&includeDeleted=true", "", nil)
	logCall(t, "comments.list after the Drive rewrite", status, body, 0)
	t.Logf("=== now look in Docs: is the comment still attached to its word? ===")
}

// TestRawMarkdownRewrite follows up stage 2, which found that Docs keeps
// a comment made in its editor inside the file — markers around the
// quoted text and a gdoc_comments block at the end — and that Drive's
// comments.list does not list it while the Docs comments view does. It
// rewrites the bytes through Drive four ways, each starting from what
// Docs wrote, asks the comments view after each whether the comment is
// still there, and puts Docs' bytes back after each. Last, it asks
// whether Drive honors If-Match on a media update, re-sending Docs'
// own bytes so an ignored precondition changes nothing.
//
//	GDOCS_INTEGRATION=1 GDOCS_MD_PROBE_ID=<id> GDOCS_MD_PROBE_REWRITE=1 go test -tags=integration ./internal/gapi -run TestRawMarkdownRewrite -v
func TestRawMarkdownRewrite(t *testing.T) {
	id := os.Getenv("GDOCS_MD_PROBE_ID")
	if id == "" || os.Getenv("GDOCS_MD_PROBE_REWRITE") == "" {
		t.Skip("set GDOCS_MD_PROBE_ID to a .md file holding a comment made in Docs, and GDOCS_MD_PROBE_REWRITE=1")
	}
	c := probeClient(t)
	mediaURL := c.drive + "/files/" + url.PathEscape(id) + "?alt=media"
	status, hdr, docsBytes := rawCallHeaders(t, c, http.MethodGet, mediaURL, "", nil, nil)
	if status != http.StatusOK {
		t.Fatalf("alt=media: HTTP %d", status)
	}
	t.Logf("alt=media ETag %q, Last-Modified %q", hdr.Get("ETag"), hdr.Get("Last-Modified"))
	block := strings.Index(docsBytes, "\n---\ngdoc_comments:")
	if block < 0 || !strings.Contains(docsBytes, "gdoc_comment_begin") {
		t.Fatal("the file carries no comment made in Docs; comment on a word there first")
	}
	markers := regexp.MustCompile(`<!-- gdoc_comment_(begin|end): \S+ -->`)
	rev := commentView(t, c, id, "before any rewrite", "")

	for _, v := range []struct{ name, content string }{
		{"a line added in the body, markers and block kept", strings.Replace(docsBytes, "## Lists", "Inserted through Drive by the probe.\n\n## Lists", 1)},
		{"a line appended after the gdoc_comments block", docsBytes + "\nAppended through Drive by the probe.\n"},
		{"the block kept, the markers removed", markers.ReplaceAllString(docsBytes, "")},
		{"the markers and the block removed", markers.ReplaceAllString(docsBytes[:block+1], "")},
	} {
		putMedia(t, c, id, v.content, nil)
		rev = commentView(t, c, id, v.name, rev)
		putMedia(t, c, id, docsBytes, nil)
		rev = commentView(t, c, id, "Docs' bytes put back after: "+v.name, rev)
	}

	// Preconditions. Each upload sends the bytes the file already holds.
	status, hdr, _ = rawCallHeaders(t, c, http.MethodGet, mediaURL, "", nil, nil)
	etag := hdr.Get("ETag")
	t.Logf("alt=media after the rewrites: HTTP %d, ETag %q", status, etag)
	for _, p := range []struct{ name, value string }{
		{"If-Match with a made-up ETag", `"probe-not-an-etag"`},
		{"If-Match with the ETag alt=media gave", etag},
	} {
		if p.value == "" {
			t.Logf("--- %s: skipped, no ETag ---", p.name)
			continue
		}
		status, hdr, body := rawCallHeaders(t, c, http.MethodPatch, uploadBase(c)+"/files/"+url.PathEscape(id)+"?uploadType=media&fields=size,headRevisionId",
			"text/markdown", map[string]string{"If-Match": p.value}, []byte(docsBytes))
		t.Logf("--- %s: HTTP %d, ETag %q ---\n%s", p.name, status, hdr.Get("ETag"), body)
	}
}

// TestRawMarkdownFollowups asks what stages 1 and 2 left open without
// needing a person: the whole Docs model of the file, whether the Docs
// API takes a suggestion or an anchored comment on it, whether old
// revisions download, and what type Drive gives Markdown uploaded under
// other names and types. The typing half leaves three more scratch
// files behind.
//
//	GDOCS_INTEGRATION=1 GDOCS_MD_PROBE_ID=<id> go test -tags=integration ./internal/gapi -run TestRawMarkdownFollowups -v
func TestRawMarkdownFollowups(t *testing.T) {
	id := os.Getenv("GDOCS_MD_PROBE_ID")
	if id == "" {
		t.Skip("set GDOCS_MD_PROBE_ID to the .md file TestRawMarkdownFile left behind")
	}
	c := probeClient(t)
	fileURL := c.drive + "/files/" + url.PathEscape(id)
	docURL := c.docs + "/v1/documents/" + url.PathEscape(id)
	viewURL := docURL + "?includeTabsContent=true&suggestionsViewMode=SUGGESTIONS_INLINE&commentsViewMode=" + CommentsIncluded

	status, body := rawCall(t, c, http.MethodGet, viewURL, "", nil)
	logCall(t, "documents.get, comments view, whole", status, body, 0)
	var d gdocs.Document
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		t.Fatal(err)
	}

	suggest := marshal(t, map[string]any{
		"requests":     []json.RawMessage{insertAt(t, "Suggested by the probe. ", 1)},
		"writeControl": map[string]string{"writeMode": "SUGGEST"},
	})
	status, body = rawCall(t, c, http.MethodPost, docURL+":batchUpdate", acceptJSON, suggest)
	logCall(t, "batchUpdate insertText in SUGGEST mode", status, body, 2000)

	// An anchored comment on the bold run "bold".
	var target *gdocs.ParagraphElement
	if len(d.Tabs) > 0 && d.Tabs[0].DocumentTab != nil && d.Tabs[0].DocumentTab.Body != nil {
		for _, se := range d.Tabs[0].DocumentTab.Body.Content {
			if se.Paragraph == nil || target != nil {
				continue
			}
			for _, el := range se.Paragraph.Elements {
				if el.TextRun != nil && el.TextRun.Content == "bold" {
					target = el
					break
				}
			}
		}
	}
	if target == nil {
		t.Logf("--- insertComment: skipped, no run reads exactly \"bold\" ---")
	} else {
		comment := marshal(t, map[string]any{"requests": []json.RawMessage{marshal(t, map[string]any{"insertComment": map[string]any{
			"content": "Probe comment through the Docs API.",
			"range":   map[string]any{"startIndex": target.StartIndex, "endIndex": target.EndIndex, "tabId": d.Tabs[0].TabProperties.TabID},
		}})}})
		status, body = rawCall(t, c, http.MethodPost, docURL+":batchUpdate", acceptJSON, comment)
		logCall(t, "batchUpdate insertComment on \"bold\"", status, body, 2000)
		if status == http.StatusOK {
			status, body = rawCall(t, c, http.MethodGet, fileURL+"?alt=media", "", nil)
			logCall(t, "alt=media after the API comment", status, body, 0)
			commentView(t, c, id, "after the API comment", "")
		}
	}

	// Old revisions of a blob: revisions.get alt=media and files.download.
	status, body = rawCall(t, c, http.MethodGet, fileURL+"/revisions?fields=revisions(id,keepForever,size,modifiedTime)", "", nil)
	logCall(t, "revisions.list", status, body, 0)
	var revs struct {
		Revisions []struct {
			ID string `json:"id"`
		} `json:"revisions"`
	}
	if err := json.Unmarshal([]byte(body), &revs); err != nil {
		t.Fatal(err)
	}
	for i, r := range revs.Revisions {
		status, body = rawCall(t, c, http.MethodGet, fileURL+"/revisions/"+url.PathEscape(r.ID)+"?alt=media", "", nil)
		logCall(t, fmt.Sprintf("revisions.get alt=media, revision %d of %d", i+1, len(revs.Revisions)), status, body, 300)
		status, body = rawCall(t, c, http.MethodPost, fileURL+"/download?revisionId="+url.QueryEscape(r.ID), acceptJSON, []byte("{}"))
		logCall(t, fmt.Sprintf("files.download, revision %d of %d", i+1, len(revs.Revisions)), status, body, 1000)
		if status != http.StatusOK {
			continue
		}
		var op struct {
			Name     string `json:"name"`
			Done     bool   `json:"done"`
			Response struct {
				DownloadURI string `json:"downloadUri"`
			} `json:"response"`
		}
		if err := json.Unmarshal([]byte(body), &op); err != nil {
			t.Fatal(err)
		}
		for try := 0; !op.Done && try < 10; try++ {
			time.Sleep(2 * time.Second)
			status, body = rawCall(t, c, http.MethodGet, c.drive+"/operations/"+url.PathEscape(op.Name), "", nil)
			if status != http.StatusOK {
				logCall(t, "operations.get", status, body, 1000)
				break
			}
			if err := json.Unmarshal([]byte(body), &op); err != nil {
				t.Fatal(err)
			}
		}
		if op.Done && op.Response.DownloadURI != "" {
			status, body = rawCall(t, c, http.MethodGet, op.Response.DownloadURI, "", nil)
			logCall(t, fmt.Sprintf("download content, revision %d", i+1), status, body, 300)
		}
	}

	// What Drive calls Markdown uploaded under other names and types, and
	// whether the Docs API then reads it.
	for _, u := range []struct{ name, metaMime, partMime string }{
		{markdownProbeName + " (no type).md", "", "application/octet-stream"},
		{markdownProbeName + " (no type).markdown", "", "application/octet-stream"},
		{markdownProbeName + " (stored as text).md", "text/plain", "text/plain"},
	} {
		f := uploadAs(t, c, u.name, u.metaMime, u.partMime)
		fid, _ := f["id"].(string)
		t.Logf("--- %q uploaded with metadata type %q and media type %q: Drive says %v ---", u.name, u.metaMime, u.partMime, f["mimeType"])
		status, body = docsGet(t, c, fid)
		logCall(t, "documents.get on it", status, body, 600)
	}
}

// rawCallHeaders is rawCall with request headers in and response
// headers out.
func rawCallHeaders(t *testing.T, c *Client, method, u, contentType string, headers map[string]string, body []byte) (int, http.Header, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.httpc.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, u, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header, string(data)
}

func putMedia(t *testing.T, c *Client, id, content string, headers map[string]string) {
	t.Helper()
	status, _, body := rawCallHeaders(t, c, http.MethodPatch, uploadBase(c)+"/files/"+url.PathEscape(id)+"?uploadType=media&fields=size",
		"text/markdown", headers, []byte(content))
	if status != http.StatusOK {
		logCall(t, "files.update media", status, body, 2000)
		t.Fatal("rewrite refused")
	}
}

// uploadAs creates a file from the corpus with the given name, metadata
// type (left out when empty) and media part type.
func uploadAs(t *testing.T, c *Client, name, metaMime, partMime string) map[string]any {
	t.Helper()
	m := map[string]string{"name": name}
	if metaMime != "" {
		m["mimeType"] = metaMime
	}
	meta, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, part := range []struct {
		contentType string
		data        []byte
	}{
		{"application/json; charset=UTF-8", meta},
		{partMime, []byte(markdownCorpus)},
	} {
		h := textproto.MIMEHeader{}
		h.Set("Content-Type", part.contentType)
		pw, err := w.CreatePart(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pw.Write(part.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	status, body := rawCall(t, c, http.MethodPost, uploadBase(c)+"/files?uploadType=multipart&fields=id,mimeType,fileExtension",
		"multipart/related; boundary="+w.Boundary(), buf.Bytes())
	if status != http.StatusOK {
		logCall(t, "upload "+name, status, body, 2000)
		t.Fatalf("upload of %q refused", name)
	}
	var f map[string]any
	if err := json.Unmarshal([]byte(body), &f); err != nil {
		t.Fatal(err)
	}
	return f
}

// commentView reads the file through the Docs comments view, waiting a
// little for a revision other than prev, and logs what each side holds
// of comments: the Docs threads with their anchors' ranges, and Drive's
// own list. It returns the revision it read.
func commentView(t *testing.T, c *Client, id, what, prev string) string {
	t.Helper()
	u := c.docs + "/v1/documents/" + url.PathEscape(id) +
		"?includeTabsContent=true&suggestionsViewMode=SUGGESTIONS_INLINE&commentsViewMode=" + CommentsIncluded
	var d gdocs.Document
	tries := 0
	for ; ; tries++ {
		status, body := rawCall(t, c, http.MethodGet, u, "", nil)
		if status != http.StatusOK {
			logCall(t, "documents.get comments view, "+what, status, body, 2000)
			return prev
		}
		d = gdocs.Document{}
		if err := json.Unmarshal([]byte(body), &d); err != nil {
			t.Fatal(err)
		}
		if prev == "" || d.RevisionID != prev || tries == 3 {
			break
		}
		time.Sleep(5 * time.Second)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Docs revision changed: %t (after %d extra reads)\n", prev != "" && d.RevisionID != prev, tries)
	anchors := map[string][]*gdocs.Range{}
	var bodyJSON []byte
	for _, tab := range d.Tabs {
		if tab.DocumentTab == nil {
			continue
		}
		for k, a := range tab.DocumentTab.CommentAnchors {
			anchors[k] = a.Ranges
		}
		raw, err := json.Marshal(tab.DocumentTab.Body)
		if err != nil {
			t.Fatal(err)
		}
		bodyJSON = append(bodyJSON, raw...)
	}
	for _, s := range []string{"Inserted through Drive", "Appended through Drive", "gdoc_comment", "this is a comment"} {
		fmt.Fprintf(&b, "body text holds %q: %t\n", s, bytes.Contains(bodyJSON, []byte(s)))
	}
	fmt.Fprintf(&b, "Docs comment threads: %d\n", len(d.Comments))
	for _, th := range d.Comments {
		fmt.Fprintf(&b, "  %s status=%s anchor=%s quote=%q head=%q replies=%d\n",
			th.CommentID, th.Status, th.AnchorID, th.PlainTextQuote, th.HeadPost.Content, len(th.Replies))
		for _, r := range anchors[th.AnchorID] {
			fmt.Fprintf(&b, "    range [%d,%d)\n", r.StartIndex, r.EndIndex)
		}
	}
	status, body := rawCall(t, c, http.MethodGet, c.drive+"/files/"+url.PathEscape(id)+
		"/comments?includeDeleted=true&fields=comments(id,content,deleted,resolved)", "", nil)
	fmt.Fprintf(&b, "Drive comments.list: HTTP %d %s", status, body)
	t.Logf("=== %s ===\n%s", what, b.String())
	return d.RevisionID
}

// TestRawMarkdownEdges asks what the rewrite probe left open that needs
// no person: whether Drive's comment endpoints reach a comment made in
// the editor, whether that comment survives an edit inside its quoted
// text, whether comment markup written through Drive makes a comment,
// which file fields move on an upload and whether md5Checksum is the
// bytes' MD5, whether an upload can pin its own revision, and what a
// media update sent as text/plain does to the file's type. Every change
// to the bytes is put back, and the type too.
//
//	GDOCS_INTEGRATION=1 GDOCS_MD_PROBE_ID=<id> GDOCS_MD_PROBE_REWRITE=1 go test -tags=integration ./internal/gapi -run 'TestRawMarkdownEdges$' -v -count=1
func TestRawMarkdownEdges(t *testing.T) {
	id := os.Getenv("GDOCS_MD_PROBE_ID")
	if id == "" || os.Getenv("GDOCS_MD_PROBE_REWRITE") == "" {
		t.Skip("set GDOCS_MD_PROBE_ID to a .md file holding a comment made in Docs, and GDOCS_MD_PROBE_REWRITE=1")
	}
	c := probeClient(t)
	fileURL := c.drive + "/files/" + url.PathEscape(id)
	meta := func(what string) map[string]any {
		t.Helper()
		status, body := rawCall(t, c, http.MethodGet, fileURL+"?fields=mimeType,size,version,headRevisionId,md5Checksum,sha256Checksum,modifiedTime,"+
			"capabilities(canEdit,canModifyContent,canComment,canModifyContentRestriction),contentRestrictions", "", nil)
		logCall(t, "files.get, "+what, status, body, 4000)
		var m map[string]any
		_ = json.Unmarshal([]byte(body), &m)
		return m
	}

	before := meta("before")
	status, docsBytes := rawCall(t, c, http.MethodGet, fileURL+"?alt=media", "", nil)
	if status != http.StatusOK {
		t.Fatalf("alt=media: HTTP %d", status)
	}
	sum := md5.Sum([]byte(docsBytes))
	t.Logf("local MD5 of the bytes %s, md5Checksum %v, equal %t", hex.EncodeToString(sum[:]), before["md5Checksum"], hex.EncodeToString(sum[:]) == before["md5Checksum"])
	if !strings.Contains(docsBytes, "gdoc_comment_begin: c1") {
		t.Fatal("the file carries no comment c1 made in Docs")
	}

	// 1. Drive's comment endpoints, addressed by the id the Docs view gives
	// the editor's comment.
	status, body := rawCall(t, c, http.MethodGet, c.docs+"/v1/documents/"+url.PathEscape(id)+
		"?includeTabsContent=true&suggestionsViewMode=SUGGESTIONS_INLINE&commentsViewMode="+CommentsIncluded, "", nil)
	if status != http.StatusOK {
		t.Fatalf("documents.get: HTTP %d", status)
	}
	var d gdocs.Document
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		t.Fatal(err)
	}
	var cid string
	for _, th := range d.Comments {
		if strings.Contains(th.PlainTextQuote, "blockquote") {
			cid = th.CommentID
		}
	}
	t.Logf("editor thread found in the Docs view: %t", cid != "")
	if cid != "" {
		cu := fileURL + "/comments/" + url.PathEscape(cid)
		status, body = rawCall(t, c, http.MethodGet, cu+"?includeDeleted=true&fields=id,content,resolved,deleted,anchor,quotedFileContent,replies(id,content,action)", "", nil)
		logCall(t, "Drive comments.get with the editor thread's id", status, body, 2000)
		status, body = rawCall(t, c, http.MethodPost, cu+"/replies?fields=id,content,action", "application/json",
			[]byte(`{"content":"Probe reply through Drive (safe to ignore)."}`))
		logCall(t, "Drive replies.create on the editor thread's id", status, body, 2000)
	}
	rev := commentView(t, c, id, "after Drive's comment endpoints", "")

	// 2. The quoted text changed, the markers kept.
	putMedia(t, c, id, strings.Replace(docsBytes, "c1 -->A blockquote line.", "c1 -->A blockquote line, edited by the probe.", 1), nil)
	rev = commentView(t, c, id, "the quoted text changed between the markers", rev)
	putMedia(t, c, id, docsBytes, nil)
	rev = commentView(t, c, id, "Docs' bytes put back after the quoted-text edit", rev)

	// 3. Markup for a second comment, written through Drive with no token.
	closing := strings.LastIndex(docsBytes, "\n---")
	if closing < 0 || !strings.Contains(docsBytes, "Final line with a hard break") {
		t.Fatal("the bytes are not the ones the rewrite probe saw")
	}
	c2 := strings.Join([]string{
		"",
		"  c2:",
		"    status: ACTIVE",
		"    head:",
		"      author: Probe",
		"      time: '2026-10-14T00:00:00.000Z'",
		"      body: Probe comment written into the bytes.",
		"      quote: |-",
		"        Final line",
	}, "\n")
	fake := docsBytes[:closing] + c2 + docsBytes[closing:]
	fake = strings.Replace(fake, "Final line with a hard break", "<!-- gdoc_comment_begin: c2 -->Final line<!-- gdoc_comment_end: c2 --> with a hard break", 1)
	putMedia(t, c, id, fake, nil)
	rev = commentView(t, c, id, "markup for c2 written through Drive, no token", rev)

	// 4. The bytes put back by an upload asking to keep its revision.
	status, body = rawCall(t, c, http.MethodPatch, uploadBase(c)+"/files/"+url.PathEscape(id)+
		"?uploadType=media&keepRevisionForever=true&fields=headRevisionId,version,md5Checksum", "text/markdown", []byte(docsBytes))
	logCall(t, "files.update media, keepRevisionForever=true", status, body, 2000)
	rev = commentView(t, c, id, "Docs' bytes put back after the c2 markup", rev)
	after := meta("after the pinned upload")
	if head, ok := after["headRevisionId"].(string); ok {
		ru := fileURL + "/revisions/" + url.PathEscape(head)
		status, body = rawCall(t, c, http.MethodGet, ru+"?fields=id,keepForever,size,md5Checksum", "", nil)
		logCall(t, "revisions.get of the pinned head", status, body, 2000)
		status, body = rawCall(t, c, http.MethodPatch, ru+"?fields=id,keepForever", "application/json", []byte(`{"keepForever":false}`))
		logCall(t, "revisions.update keepForever=false on it", status, body, 2000)
	}
	if prev, ok := before["headRevisionId"].(string); ok {
		ru := fileURL + "/revisions/" + url.PathEscape(prev)
		status, body = rawCall(t, c, http.MethodPatch, ru+"?fields=id,keepForever", "application/json", []byte(`{"keepForever":true}`))
		logCall(t, "revisions.update keepForever=true on the head before the probe", status, body, 2000)
		status, body = rawCall(t, c, http.MethodPatch, ru+"?fields=id,keepForever", "application/json", []byte(`{"keepForever":false}`))
		logCall(t, "revisions.update keepForever=false on it again", status, body, 2000)
	}

	// 5. A media update sent as text/plain.
	status, body = rawCall(t, c, http.MethodPatch, uploadBase(c)+"/files/"+url.PathEscape(id)+
		"?uploadType=media&fields=mimeType,headRevisionId", "text/plain", []byte(docsBytes))
	logCall(t, "files.update media sent as text/plain", status, body, 2000)
	if m := meta("after the text/plain media update"); m["mimeType"] != "text/markdown" {
		status, body = rawCall(t, c, http.MethodPatch, fileURL+"?fields=mimeType", "application/json", []byte(`{"mimeType":"text/markdown"}`))
		logCall(t, "files.update metadata, mimeType put back", status, body, 2000)
	}
	meta("at the end")
	commentView(t, c, id, "at the end", "")
}

// TestRawMarkdownComments follows TestRawMarkdownEdges: whether a media
// update's own type puts a retyped file back to text/markdown, what a
// media update sent as application/octet-stream does to the type, what
// Drive's comment endpoints make of the editor's comment and of one
// written into the bytes, and who the Docs view says wrote the latter.
//
//	GDOCS_INTEGRATION=1 GDOCS_MD_PROBE_ID=<id> GDOCS_MD_PROBE_REWRITE=1 go test -tags=integration ./internal/gapi -run 'TestRawMarkdownComments$' -v -count=1
func TestRawMarkdownComments(t *testing.T) {
	id := os.Getenv("GDOCS_MD_PROBE_ID")
	if id == "" || os.Getenv("GDOCS_MD_PROBE_REWRITE") == "" {
		t.Skip("set GDOCS_MD_PROBE_ID to a .md file holding a comment made in Docs, and GDOCS_MD_PROBE_REWRITE=1")
	}
	c := probeClient(t)
	fileURL := c.drive + "/files/" + url.PathEscape(id)
	upload := uploadBase(c) + "/files/" + url.PathEscape(id) + "?uploadType=media&fields=mimeType,headRevisionId"
	status, docsBytes := rawCall(t, c, http.MethodGet, fileURL+"?alt=media", "", nil)
	if status != http.StatusOK || !strings.Contains(docsBytes, "gdoc_comment_begin: c1") {
		t.Fatalf("alt=media: HTTP %d, or no comment c1", status)
	}

	// 1. Types.
	for _, ct := range []string{"text/markdown", "application/octet-stream", "text/markdown"} {
		status, body := rawCall(t, c, http.MethodPatch, upload, ct, []byte(docsBytes))
		logCall(t, "files.update media sent as "+ct, status, body, 2000)
	}

	threads := func(what string) []gdocs.CommentThread {
		t.Helper()
		status, body := rawCall(t, c, http.MethodGet, c.docs+"/v1/documents/"+url.PathEscape(id)+
			"?includeTabsContent=true&suggestionsViewMode=SUGGESTIONS_INLINE&commentsViewMode="+CommentsIncluded, "", nil)
		if status != http.StatusOK {
			logCall(t, "documents.get, "+what, status, body, 2000)
			return nil
		}
		var d struct {
			RevisionID string            `json:"revisionId"`
			Comments   []json.RawMessage `json:"comments"`
		}
		if err := json.Unmarshal([]byte(body), &d); err != nil {
			t.Fatal(err)
		}
		var out []gdocs.CommentThread
		for _, raw := range d.Comments {
			t.Logf("--- Docs comment thread, %s ---\n%s", what, raw)
			var th gdocs.CommentThread
			if err := json.Unmarshal(raw, &th); err != nil {
				t.Fatal(err)
			}
			out = append(out, th)
		}
		return out
	}
	drive := func(what, cid string) {
		t.Helper()
		cu := fileURL + "/comments/" + url.PathEscape(cid)
		status, body := rawCall(t, c, http.MethodGet, cu+"?includeDeleted=true&fields=id,content,resolved,deleted,anchor,quotedFileContent,replies(id,content,action)", "", nil)
		logCall(t, "Drive comments.get, "+what, status, body, 2000)
		status, body = rawCall(t, c, http.MethodPost, cu+"/replies?fields=id,content,action", "application/json",
			[]byte(`{"content":"Probe reply through Drive (safe to ignore)."}`))
		logCall(t, "Drive replies.create, "+what, status, body, 2000)
	}

	// 2. The editor's comment.
	for _, th := range threads("as found") {
		if th.HeadPost.Content == "this is a comment" {
			drive("the editor's comment", th.CommentID)
		}
	}

	// 3. A comment written into the bytes.
	closing := strings.LastIndex(docsBytes, "\n---")
	c2 := strings.Join([]string{
		"",
		"  c2:",
		"    status: ACTIVE",
		"    head:",
		"      author: Probe",
		"      time: '2026-10-14T00:00:00.000Z'",
		"      body: Probe comment written into the bytes.",
		"      quote: |-",
		"        Final line",
	}, "\n")
	fake := docsBytes[:closing] + c2 + docsBytes[closing:]
	fake = strings.Replace(fake, "Final line with a hard break", "<!-- gdoc_comment_begin: c2 -->Final line<!-- gdoc_comment_end: c2 --> with a hard break", 1)
	putMedia(t, c, id, fake, nil)
	time.Sleep(6 * time.Second)
	for _, th := range threads("with c2 written into the bytes") {
		if th.HeadPost.Content == "Probe comment written into the bytes." {
			drive("the comment written into the bytes", th.CommentID)
		}
	}
	threads("after Drive's endpoints on c2")
	putMedia(t, c, id, docsBytes, nil)
	time.Sleep(6 * time.Second)
	threads("Docs' bytes put back")
	status, body := rawCall(t, c, http.MethodGet, fileURL+"?fields=mimeType,headRevisionId", "", nil)
	logCall(t, "files.get at the end", status, body, 2000)
}

// TestRawMarkdownWithPerson is stage 3, run in two steps around a
// person with the file open in the Docs editor.
//
// GDOCS_MD_PROBE_STEP=seed writes a comment into the bytes, as a server
// would, and leaves it there; the person then looks for it in the
// editor, replies to their own comment, resolves the seeded one, adds a
// second comment, edits a word, and keeps the file open.
//
// GDOCS_MD_PROBE_STEP=upload inserts a line through Drive while the
// editor is open; the person then looks for the line, edits another
// word and waits for the save. TestRawMarkdownAfterDocs reads the
// outcome.
//
//	GDOCS_INTEGRATION=1 GDOCS_MD_PROBE_ID=<id> GDOCS_MD_PROBE_STEP=seed go test -tags=integration ./internal/gapi -run 'TestRawMarkdownWithPerson$' -v -count=1
func TestRawMarkdownWithPerson(t *testing.T) {
	id := os.Getenv("GDOCS_MD_PROBE_ID")
	step := os.Getenv("GDOCS_MD_PROBE_STEP")
	if id == "" || step == "" {
		t.Skip("set GDOCS_MD_PROBE_ID and GDOCS_MD_PROBE_STEP=seed or upload")
	}
	c := probeClient(t)
	fileURL := c.drive + "/files/" + url.PathEscape(id)
	status, content := rawCall(t, c, http.MethodGet, fileURL+"?alt=media", "", nil)
	if status != http.StatusOK {
		t.Fatalf("alt=media: HTTP %d", status)
	}
	switch step {
	case "seed":
		closing := strings.LastIndex(content, "\n---")
		if closing < 0 || !strings.Contains(content, "\n---\ngdoc_comments:") || !strings.Contains(content, "Final line with a hard break") {
			t.Fatal("the bytes are not the ones the rewrite probe saw")
		}
		seeded := strings.Join([]string{
			"",
			"  c2:",
			"    status: ACTIVE",
			"    head:",
			"      author: Probe written through Drive",
			"      time: '2026-10-14T00:00:00.000Z'",
			"      body: Probe comment written into the bytes.",
			"      quote: |-",
			"        Final line",
		}, "\n")
		content = content[:closing] + seeded + content[closing:]
		content = strings.Replace(content, "Final line with a hard break", "<!-- gdoc_comment_begin: c2 -->Final line<!-- gdoc_comment_end: c2 --> with a hard break", 1)
	case "upload":
		if !strings.Contains(content, "## Lists") {
			t.Fatal("no \"## Lists\" heading to insert before")
		}
		content = strings.Replace(content, "## Lists", "Inserted through Drive while the editor was open.\n\n## Lists", 1)
	case "look":
		// Read only: the comments view as it is now.
		commentView(t, c, id, "as it is now", "")
		return
	case "rev":
		// Read only: one old revision against the file as it is now.
		rev := os.Getenv("GDOCS_MD_PROBE_REV")
		status, old := rawCall(t, c, http.MethodGet, fileURL+"/revisions/"+url.PathEscape(rev)+"?alt=media", "", nil)
		logCall(t, "revisions.get alt=media", status, old, 0)
		t.Logf("--- lines only one side has (- the revision, + now) ---\n%s", lineDiff(old, content))
		return
	default:
		t.Fatalf("unknown step %q", step)
	}
	putMedia(t, c, id, content, nil)
	time.Sleep(6 * time.Second)
	commentView(t, c, id, "after step "+step, "")
}

// TestRawMarkdownBuild asks what building comments, a working copy and a
// safe write needs to know, without a person. It makes fresh scratch
// files ("safe to delete") and, with GDOCS_MD_PROBE_ID set, also writes a
// reply and a resolve into the bytes of a file whose comment carries a
// token, then puts that file's bytes back.
//
//	GDOCS_INTEGRATION=1 [GDOCS_MD_PROBE_ID=<id>] go test -tags=integration ./internal/gapi -run 'TestRawMarkdownBuild$' -v -count=1
func TestRawMarkdownBuild(t *testing.T) {
	c := probeClient(t)
	threads := func(id, what string) []gdocs.CommentThread {
		t.Helper()
		status, body := rawCall(t, c, http.MethodGet, c.docs+"/v1/documents/"+url.PathEscape(id)+
			"?includeTabsContent=true&suggestionsViewMode=SUGGESTIONS_INLINE&commentsViewMode="+CommentsIncluded, "", nil)
		if status != http.StatusOK {
			logCall(t, "documents.get, "+what, status, body, 2000)
			return nil
		}
		var d gdocs.Document
		if err := json.Unmarshal([]byte(body), &d); err != nil {
			t.Fatal(err)
		}
		for _, th := range d.Comments {
			t.Logf("--- %s: thread status=%s quote=%q head=%q imported=%t replies=%d", what, th.Status, th.PlainTextQuote, th.HeadPost.Content, th.HeadPost.FromImportedDocument, len(th.Replies))
			for _, r := range th.Replies {
				t.Logf("      reply %q action=%s imported=%t", r.Content, r.CommentAction, r.FromImportedDocument)
			}
		}
		t.Logf("--- %s: revision %s, %d thread(s)", what, d.RevisionID, len(d.Comments))
		return d.Comments
	}

	// 1. A comment written into the bytes with double-quoted scalars, and a
	// reply and a resolve on it.
	body := "# Build probe\n\nWe ship in May.\n\nA second paragraph.\n"
	f := uploadAs(t, c, markdownProbeName+" (build).md", "text/markdown", "text/markdown")
	id := f["id"].(string)
	block := func(entry string) string {
		return strings.Replace(body, "ship in May", "<!-- gdoc_comment_begin: c1 -->ship in May<!-- gdoc_comment_end: c1 -->", 1) +
			"\n---\ngdoc_comments:\n  c1:\n" + entry + "---\n"
	}
	head := "    head:\n      author: \"Probe \\\"quoted\\\" name\"\n      time: '2026-10-14T00:00:00.000Z'\n" +
		"      body: \"Line one: with a colon\\nline two\"\n      quote: \"ship in May\"\n"
	putMedia(t, c, id, block("    status: ACTIVE\n"+head), nil)
	time.Sleep(6 * time.Second)
	threads(id, "double-quoted entry")
	reply := "    replies:\n    - author: Probe\n      time: '2026-10-14T00:01:00.000Z'\n      body: a reply in the bytes\n"
	putMedia(t, c, id, block("    status: ACTIVE\n"+head+reply), nil)
	time.Sleep(6 * time.Second)
	threads(id, "with a reply")
	resolve := reply + "    - author: Probe\n      time: '2026-10-14T00:02:00.000Z'\n      action: RESOLVE\n"
	putMedia(t, c, id, block("    status: RESOLVED\n"+head+resolve), nil)
	time.Sleep(6 * time.Second)
	threads(id, "resolved")

	// 2. How the Docs revision moves after one upload: when, and whether it
	// settles.
	putMedia(t, c, id, body+"\nOne more line.\n", nil)
	last := ""
	for i := range 10 {
		status, raw := rawCall(t, c, http.MethodGet, c.docs+"/v1/documents/"+url.PathEscape(id)+"?fields=revisionId", "", nil)
		var d struct {
			RevisionID string `json:"revisionId"`
		}
		_ = json.Unmarshal([]byte(raw), &d)
		t.Logf("revision read %d (%ds after the upload): HTTP %d, changed %t", i, 2*i, status, d.RevisionID != last)
		last = d.RevisionID
		time.Sleep(2 * time.Second)
	}

	// 3. The revision before the head, from revisions.list.
	status, raw := rawCall(t, c, http.MethodGet, c.drive+"/files/"+url.PathEscape(id)+"/revisions?fields=nextPageToken,revisions(id,modifiedTime,size)&pageSize=2", "", nil)
	logCall(t, "revisions.list pageSize=2", status, raw, 2000)
	status, raw = rawCall(t, c, http.MethodGet, c.drive+"/files/"+url.PathEscape(id)+"?fields=headRevisionId", "", nil)
	logCall(t, "files.get headRevisionId", status, raw, 2000)

	// 4. A working copy: a Google Doc imported from the text, carrying where
	// it came from, then exported as markdown.
	meta, err := json.Marshal(map[string]any{"name": markdownProbeName + " (working copy)", "mimeType": "application/vnd.google-apps.document",
		"appProperties": map[string]string{"md_source": id, "md_base": "probe-revision"}})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, part := range []struct {
		ct   string
		data []byte
	}{{"application/json; charset=UTF-8", meta}, {"text/markdown", []byte(markdownCorpus)}} {
		h := textproto.MIMEHeader{}
		h.Set("Content-Type", part.ct)
		pw, _ := w.CreatePart(h)
		_, _ = pw.Write(part.data)
	}
	_ = w.Close()
	status, raw = rawCall(t, c, http.MethodPost, uploadBase(c)+"/files?uploadType=multipart&fields=id,mimeType,appProperties,webViewLink",
		"multipart/related; boundary="+w.Boundary(), buf.Bytes())
	logCall(t, "files.create working copy", status, raw, 2000)
	var doc map[string]any
	_ = json.Unmarshal([]byte(raw), &doc)
	if docID, ok := doc["id"].(string); ok {
		status, raw = rawCall(t, c, http.MethodGet, c.drive+"/files/"+url.PathEscape(docID)+"?fields=appProperties,mimeType", "", nil)
		logCall(t, "files.get appProperties of the working copy", status, raw, 2000)
		status, raw = rawCall(t, c, http.MethodGet, c.drive+"/files/"+url.PathEscape(docID)+"/export?mimeType=text/markdown", "", nil)
		logCall(t, "export of the working copy as markdown", status, raw, 0)
		t.Logf("--- export against the corpus ---\n%s", lineDiff(markdownCorpus, raw))
		status, raw = rawCall(t, c, http.MethodPatch, c.drive+"/files/"+url.PathEscape(docID)+"?fields=appProperties", "application/json",
			[]byte(`{"appProperties":{"md_base":"probe-revision-2"}}`))
		logCall(t, "files.update appProperties", status, raw, 2000)
	}

	// 5. A reply and a resolve written into the bytes of a comment with a
	// token, Google's own thread.
	tok := os.Getenv("GDOCS_MD_PROBE_ID")
	if tok == "" {
		return
	}
	status, orig := rawCall(t, c, http.MethodGet, c.drive+"/files/"+url.PathEscape(tok)+"?alt=media", "", nil)
	if status != http.StatusOK {
		t.Fatalf("alt=media: HTTP %d", status)
	}
	before := threads(tok, "tokened, as found")
	_ = before
	// The first entry carrying a token, and the line its head starts on.
	lines := strings.Split(orig, "\n")
	key, at := "", -1
	for i, l := range lines {
		if strings.HasPrefix(l, "  c") && strings.HasSuffix(l, ":") && !strings.HasPrefix(l, "   ") {
			key, at = strings.TrimSuffix(strings.TrimSpace(l), ":"), i
		}
		if at >= 0 && strings.HasPrefix(l, "    token: ") {
			break
		}
	}
	if at < 0 {
		t.Fatal("no entry with a token")
	}
	t.Logf("writing into entry %s", key)
	// Insert a reply list at the end of that entry (before the next key or the closing rule).
	end := at + 1
	for end < len(lines) && (strings.HasPrefix(lines[end], "    ") || strings.HasPrefix(lines[end], "      ")) {
		end++
	}
	hasReplies := false
	for _, l := range lines[at:end] {
		if strings.HasPrefix(l, "    replies:") {
			hasReplies = true
		}
	}
	add := []string{"    - author: Probe", "      time: '2026-10-14T00:03:00.000Z'", "      body: a reply written into a token's entry"}
	if !hasReplies {
		add = append([]string{"    replies:"}, add...)
	}
	withReply := strings.Join(append(append(append([]string{}, lines[:end]...), add...), lines[end:]...), "\n")
	putMedia(t, c, tok, withReply, nil)
	time.Sleep(6 * time.Second)
	threads(tok, "tokened, reply written")
	resolved := strings.Replace(withReply, lines[at]+"\n    status: ACTIVE", lines[at]+"\n    status: RESOLVED", 1)
	putMedia(t, c, tok, resolved, nil)
	time.Sleep(6 * time.Second)
	threads(tok, "tokened, status RESOLVED written")
	putMedia(t, c, tok, orig, nil)
	time.Sleep(6 * time.Second)
	threads(tok, "tokened, bytes put back")
}

// TestRawMarkdownMask asks whether the comments view honors a fields
// mask, which decides what a check for edits not yet saved costs.
//
//	GDOCS_INTEGRATION=1 GDOCS_MD_PROBE_ID=<id> go test -tags=integration ./internal/gapi -run 'TestRawMarkdownMask$' -v -count=1
func TestRawMarkdownMask(t *testing.T) {
	id := os.Getenv("GDOCS_MD_PROBE_ID")
	if id == "" {
		t.Skip("set GDOCS_MD_PROBE_ID")
	}
	c := probeClient(t)
	base := c.docs + "/v1/documents/" + url.PathEscape(id) + "?suggestionsViewMode=SUGGESTIONS_INLINE&commentsViewMode=" + CommentsIncluded
	for _, q := range []string{
		"&includeTabsContent=true",
		"&includeTabsContent=true&fields=revisionId,comments,tabs(tabProperties(tabId),documentTab(commentAnchors))",
		"&fields=revisionId,comments",
	} {
		status, body := rawCall(t, c, http.MethodGet, base+q, "", nil)
		var d gdocs.Document
		_ = json.Unmarshal([]byte(body), &d)
		anchors := 0
		for _, tab := range d.Tabs {
			if tab.DocumentTab != nil {
				anchors += len(tab.DocumentTab.CommentAnchors)
			}
		}
		t.Logf("%s: HTTP %d, %d bytes, revision set %t, %d thread(s), %d anchor(s)", q, status, len(body), d.RevisionID != "", len(d.Comments), anchors)
		if status != http.StatusOK {
			logCall(t, "refused", status, body, 800)
		}
	}
}

// TestRawMarkdownWatch reads a Markdown file once a second and logs, with
// the time, every change in the live session (its revision, its comment
// threads and their state) and in the file (its head revision), while a
// person works on it in the Docs editor. It writes nothing. It answers
// how far the live session runs ahead of the file, which decides what a
// check before a write can see.
//
//	GDOCS_INTEGRATION=1 GDOCS_MD_PROBE_ID=<id> GDOCS_MD_WATCH_SECONDS=240 go test -tags=integration ./internal/gapi -run 'TestRawMarkdownWatch$' -v -count=1 -timeout 15m
func TestRawMarkdownWatch(t *testing.T) {
	id := os.Getenv("GDOCS_MD_PROBE_ID")
	if id == "" {
		t.Skip("set GDOCS_MD_PROBE_ID")
	}
	seconds := 240
	if v := os.Getenv("GDOCS_MD_WATCH_SECONDS"); v != "" {
		_, _ = fmt.Sscan(v, &seconds)
	}
	c := probeClient(t)
	live := c.docs + "/v1/documents/" + url.PathEscape(id) + "?includeTabsContent=true&suggestionsViewMode=SUGGESTIONS_INLINE&commentsViewMode=" +
		CommentsIncluded + "&fields=" + url.QueryEscape("revisionId,comments,tabs(tabProperties(tabId),documentTab(commentAnchors))")
	file := c.drive + "/files/" + url.PathEscape(id) + "?fields=headRevisionId,md5Checksum,size"
	start := time.Now()
	var lastLive, lastFile string
	for time.Since(start) < time.Duration(seconds)*time.Second {
		at := time.Since(start).Round(100 * time.Millisecond)
		status, body := rawCall(t, c, http.MethodGet, live, "", nil)
		var d gdocs.Document
		_ = json.Unmarshal([]byte(body), &d)
		anchors := 0
		for _, tab := range d.Tabs {
			if tab.DocumentTab != nil {
				anchors += len(tab.DocumentTab.CommentAnchors)
			}
		}
		var threads []string
		for _, th := range d.Comments {
			threads = append(threads, fmt.Sprintf("%s/%d", th.Status, len(th.Replies)))
		}
		liveNow := fmt.Sprintf("HTTP %d revision …%s, %d thread(s) %v, %d anchor(s)", status, tail(d.RevisionID), len(d.Comments), threads, anchors)
		_, fb := rawCall(t, c, http.MethodGet, file, "", nil)
		var f struct{ HeadRevisionID, Size string }
		_ = json.Unmarshal([]byte(fb), &f)
		fileNow := fmt.Sprintf("head …%s, %s bytes", tail(f.HeadRevisionID), f.Size)
		if liveNow != lastLive {
			t.Logf("%6s live: %s", at, liveNow)
			lastLive = liveNow
		}
		if fileNow != lastFile {
			t.Logf("%6s file: %s", at, fileNow)
			lastFile = fileNow
		}
		time.Sleep(time.Second)
	}
}

// tail is the end of an id, enough to tell revisions apart in a log.
func tail(s string) string {
	if len(s) > 6 {
		return s[len(s)-6:]
	}
	return s
}

// TestRawMarkdownNew makes a fresh scratch Markdown file from the corpus,
// for a run of TestRawMarkdownWatch, and logs its id.
//
//	GDOCS_INTEGRATION=1 go test -tags=integration ./internal/gapi -run 'TestRawMarkdownNew$' -v -count=1
func TestRawMarkdownNew(t *testing.T) {
	c := probeClient(t)
	f := uploadAs(t, c, markdownProbeName+" (watch).md", "text/markdown", "text/markdown")
	t.Logf("made %v", f["id"])
}
