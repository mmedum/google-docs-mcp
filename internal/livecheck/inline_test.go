//go:build live

package livecheck

import (
	"strings"
	"testing"
)

// TestLiveInlineContentKeepsEdgeSpaces: content placed inside a
// paragraph keeps the spaces it starts with, which Markdown drops, so
// text added after a word stays apart from it (D-26).
func TestLiveInlineContentKeepsEdgeSpaces(t *testing.T) {
	d := start(t)
	_, sc := d.okStruct("create the inline-content scratch document", "create_document", map[string]any{
		"title":   "google-docs-mcp inline content (safe to delete)",
		"content": "# Inline content\n\nClean sentence here."})
	doc := str(sc, "id")
	if doc == "" {
		t.Fatal("create returned no document id; nothing else can run")
	}
	t.Cleanup(func() {
		t.Logf("=== scratch document left behind ===\ndelete it when you are done; Drive search title:\"safe to delete\" finds every run's")
	})
	d.ok("insert text after a word", "edit_document", map[string]any{
		"document": doc, "mode": "direct", "ops": []any{
			map[string]any{"op": "insert", "location": map[string]any{"at": "after", "of": map[string]any{"text": "Clean sentence"}},
				"content": " and more"}}})
	if read := d.ok("read the text after the word", "read_document", map[string]any{"document": doc}); !strings.Contains(read, "Clean sentence and more here.") {
		t.Errorf("the inserted text lost its leading space:\n%s", shown(read, 400))
	}
}
