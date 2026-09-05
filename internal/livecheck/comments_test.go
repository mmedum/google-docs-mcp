//go:build live

package livecheck

import (
	"strings"
	"testing"
)

// liveComments covers bullets, the comment threads on both backends, and
// reading a section with its comments marked.
func liveComments(t *testing.T, d *driver, doc string) {
	d.ok("bullets and clear_formatting", "format_document", map[string]any{"document": doc, "mode": "direct", "ops": []any{
		map[string]any{"op": "bullets", "target": map[string]any{"text": "Review the numbers"}, "bullets": "checkbox"},
		map[string]any{"op": "clear_formatting", "target": map[string]any{"text": "Closing line"}},
	}})

	d.ok("list comments", "list_comments", map[string]any{"document": doc})
	_, sc := d.okStruct("add comment", "add_comment", map[string]any{"document": doc,
		"target": map[string]any{"text": "Nested point"}, "content": "Live test: comment on the nested bullet."})
	comment := str(sc, "id")

	// A comment posted through either backend must come back in the
	// listing, which is the only place a caller learns thread ids.
	listed := d.ok("list comments after adding one", "list_comments", map[string]any{"document": doc})
	if comment != "" && !strings.Contains(listed, comment) {
		t.Errorf("comment %s is not in the listing:\n%s", comment, truncate(listed, 500))
	}
	if comment != "" {
		d.ok("reply", "reply_comment", map[string]any{"document": doc, "comment_id": comment, "content": "Reply from the live test."})
		d.ok("resolve", "reply_comment", map[string]any{"document": doc, "comment_id": comment, "action": "resolve"})
		d.ok("reopen", "reply_comment", map[string]any{"document": doc, "comment_id": comment, "action": "reopen", "content": "Reopened."})
		d.ok("edit a comment", "reply_comment", map[string]any{"document": doc, "comment_id": comment, "action": "edit", "content": "Rewritten by the live test."})
	}
	d.ok("document-level comment", "add_comment", map[string]any{"document": doc, "content": "Live test: a comment on the document as a whole."})
	d.ok("read a section with its comments", "read_document", map[string]any{
		"document": doc, "with_handles": true, "include_comments": true, "heading": "Background"})

	if d.destructive && comment != "" {
		d.ok("delete comment", "delete_comment", map[string]any{"document": doc, "comment_id": comment})
	}
}

// liveHistory covers the revision list, a diff and a read of an old one.
func liveHistory(t *testing.T, d *driver, doc string) {
	text := d.ok("list revisions", "list_revisions", map[string]any{"document": doc, "limit": 5})
	var revs []string
	for _, m := range listedID.FindAllStringSubmatch(text, -1) {
		revs = append(revs, m[1])
	}
	if len(revs) < 2 {
		t.Log("fewer than two revisions; Drive has not split them yet, skipping the diff")
		return
	}
	d.ok("diff revisions", "diff_revisions", map[string]any{"document": doc, "from": revs[len(revs)-1], "to": revs[0]})
	d.ok("read an old revision", "read_document", map[string]any{"document": doc, "revision": revs[len(revs)-1], "max_chars": 600})
}
