//go:build integration

package gapi

// Spike: how long may a comment's or a reply's text be, and in what unit
// does Drive count it? Drive's reference states no limit.
//
//	GDOCS_INTEGRATION=1 go test -tags=integration ./internal/gapi -run TestRawCommentLength -v
//
// Each run of text is posted at the limit and one over, in three
// alphabets: ASCII (one byte, one UTF-16 unit), é (two bytes, one unit)
// and an emoji (four bytes, two units). Where the three limits agree in
// bytes, units or characters says which one Drive counts. Every comment
// it makes is deleted again; the scratch document stays.
//
// What it established, 2026-10-31, by halving from one character up
// before the limit was known: Drive counts bytes. It takes 4096 x, 2048 é
// and 1024 emoji whole, 4096 bytes each, and refuses one more with 403
// commentLengthLimitExceeded, "Comment content is limited to 4096 bytes
// in UTF-8 encoding." A reply has the same limit. The Docs API's
// insertComment, below, takes half that. plan.MaxCommentBytes acts on
// both.
import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestRawCommentLength(t *testing.T) {
	c := probeClient(t)
	ctx := context.Background()
	id := probeDoc(t, c, ctx, "comments", "google-docs-mcp comment length probe (safe to delete)",
		[]json.RawMessage{insertAt(t, "Probe text", 1)}, nil)
	parent, err := c.CreateComment(ctx, id, "reply probe", "")
	if err != nil {
		t.Fatalf("reply parent: %v", err)
	}
	for _, tc := range []struct {
		unit string
		n    int
	}{{"x", 4096}, {"x", 4097}, {"é", 2048}, {"é", 2049}, {"😀", 1024}, {"😀", 1025}} {
		content := strings.Repeat(tc.unit, tc.n)
		if cm, err := c.CreateComment(ctx, id, content, ""); err != nil {
			t.Logf("comment %q x %d: refused: %v", tc.unit, tc.n, err)
		} else {
			t.Logf("comment %q x %d: accepted, stored whole: %v", tc.unit, tc.n, cm.Content == content)
			if err := c.DeleteComment(ctx, id, cm.ID); err != nil {
				t.Logf("cleanup: %v", err)
			}
		}
		if r, err := c.CreateReply(ctx, id, parent.ID, content, ""); err != nil {
			t.Logf("reply %q x %d: refused: %v", tc.unit, tc.n, err)
		} else {
			t.Logf("reply %q x %d: accepted, stored whole: %v", tc.unit, tc.n, r.Content == content)
		}
	}
	if err := c.DeleteComment(ctx, id, parent.ID); err != nil {
		t.Logf("cleanup: %v", err)
	}
	t.Logf("=== scratch document left behind; Drive search title:\"safe to delete\" finds it ===")
}

// TestRawDocsCommentLength asks the same of the Docs API's insertComment,
// the path an anchored comment and a comment-mode edit take when the
// comments view is on. Each run of text is posted at the Drive limit and
// one over; the comments stay on the scratch document.
//
// What it established, 2026-10-31: insertComment refuses all six, 4096 x
// included, with 400 "content must not exceed 2048 UTF-8 code units". So
// it counts bytes too, and takes 2048 of them.
//
//	GDOCS_INTEGRATION=1 go test -tags=integration ./internal/gapi -run TestRawDocsCommentLength -v
func TestRawDocsCommentLength(t *testing.T) {
	c := probeClient(t)
	ctx := context.Background()
	id := probeDoc(t, c, ctx, "docs-comments", "google-docs-mcp docs comment length probe (safe to delete)",
		[]json.RawMessage{insertAt(t, "Probe text", 1)}, nil)
	got, err := c.GetDocument(ctx, id, GetOptions{SuggestionsViewMode: SuggestionsInline, CommentsViewMode: CommentsIncluded})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	tabID := got.Document.Tabs[0].TabProperties.TabID
	for _, tc := range []struct {
		unit string
		n    int
	}{{"x", 4096}, {"x", 4097}, {"é", 2048}, {"é", 2049}, {"😀", 1024}, {"😀", 1025}} {
		res, err := c.BatchUpdate(ctx, id, &BatchUpdateRequest{Requests: []json.RawMessage{marshal(t, map[string]any{
			"insertComment": map[string]any{
				"content": strings.Repeat(tc.unit, tc.n),
				"range":   map[string]any{"startIndex": 1, "endIndex": 6, "tabId": tabID},
			}})}})
		if err != nil {
			t.Logf("insertComment %q x %d: refused: %v", tc.unit, tc.n, err)
			continue
		}
		t.Logf("insertComment %q x %d: reply %s", tc.unit, tc.n, res.Raw)
	}
	t.Logf("=== scratch document left behind; Drive search title:\"safe to delete\" finds it ===")
}

// TestRawNamedRangeLength checks the unit of a named range's name, which
// the reference gives as "no more than 256 characters, measured in
// UTF-16 code units". 128 emoji are 256 units, 512 bytes and 128
// characters; 129 are 258 units and 129 characters. So the first is taken
// only if Google counts units, and the second refused unless it counts
// characters.
//
// What it established, 2026-10-31: 128 emoji were taken and 129 refused,
// "Invalid name: must be at least 1 and no more than 256 characters". So
// Google counts UTF-16 units, as the reference says. internal/plan acts
// on it.
//
//	GDOCS_INTEGRATION=1 go test -tags=integration ./internal/gapi -run TestRawNamedRangeLength -v
func TestRawNamedRangeLength(t *testing.T) {
	c := probeClient(t)
	ctx := context.Background()
	id := probeDoc(t, c, ctx, "named-ranges", "google-docs-mcp named range length probe (safe to delete)",
		[]json.RawMessage{insertAt(t, "Probe text", 1)}, nil)
	for _, n := range []int{128, 129} {
		_, err := c.BatchUpdate(ctx, id, &BatchUpdateRequest{Requests: []json.RawMessage{marshal(t, map[string]any{
			"createNamedRange": map[string]any{
				"name":  strings.Repeat("😀", n),
				"range": map[string]any{"startIndex": 1, "endIndex": 6},
			}})}})
		t.Logf("createNamedRange of %d emoji: err %v", n, err)
	}
	t.Logf("=== scratch document left behind; Drive search title:\"safe to delete\" finds it ===")
}
