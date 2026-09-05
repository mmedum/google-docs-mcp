//go:build live

package livecheck

import (
	"regexp"
	"testing"
)

var (
	ownerEmail = regexp.MustCompile(`(?m)^owner .*<([^>]+@[^>]+)>`)
	firstURL   = regexp.MustCompile(`(?m)^(https://\S+)`)
	imageID    = regexp.MustCompile(`\(([a-z0-9._]*kix\.[A-Za-z0-9_]+)`)
)

// liveObjects covers the chips and the whole life of an image: insert,
// replace the source in place, then delete it — the last of which is the
// only way to remove a floating image, since no text range covers one.
func liveObjects(t *testing.T, d *driver, doc string) {
	d.ok("insert date chip", "insert_object", map[string]any{"document": doc, "kind": "date",
		"date": "2026-09-04", "date_format": "iso", "mode": "direct",
		"location": map[string]any{"at": "end", "of": map[string]any{"text": "Appended paragraph at the very end."}}})

	info := d.ok("get_document for the owner and URL", "get_document", map[string]any{"document": doc})
	if owner := first(ownerEmail, info); owner != "" {
		d.ok("insert person chip", "insert_object", map[string]any{"document": doc, "kind": "person",
			"email": owner, "mode": "direct",
			"location": map[string]any{"at": "end", "of": map[string]any{"text": "Review the numbers"}}})
	}
	if url := first(firstURL, info); url != "" {
		d.ok("insert rich link chip", "insert_object", map[string]any{"document": doc, "kind": "rich_link",
			"url": url, "mode": "direct",
			"location": map[string]any{"at": "end", "of": map[string]any{"text": "Send the summary"}}})
	}

	const logo = "https://www.gstatic.com/images/branding/product/1x/docs_2020q4_48dp.png"
	d.ok("insert image", "insert_object", map[string]any{"document": doc, "kind": "image", "url": logo,
		"width_pt": 48, "mode": "direct",
		"location": map[string]any{"at": "after", "of": map[string]any{"text": "Closing line"}}})

	read := d.ok("read the image back", "read_document", map[string]any{"document": doc, "with_handles": true})
	obj := first(imageID, read)
	if obj == "" {
		t.Log("no inline image id in the read; skipping replace and delete")
		return
	}
	d.ok("replace the image source", "insert_object", map[string]any{"document": doc, "action": "replace",
		"object_id": obj, "url": logo, "mode": "direct"})
	d.ok("delete the image", "insert_object", map[string]any{"document": doc, "action": "delete",
		"object_id": obj, "mode": "direct"})
}

// liveTabs covers add, rename, nesting and both directions of move, then
// headers and footers, which are per tab.
func liveTabs(t *testing.T, d *driver, doc string) {
	d.ok("add tab", "manage_tabs", map[string]any{"document": doc, "action": "add",
		"title": "Appendix", "content": "# Appendix\n\nAdded by the live test."})
	d.ok("rename tab", "manage_tabs", map[string]any{"document": doc, "action": "rename", "tab": "Appendix", "title": "Appendix A"})
	_, sc := d.okStruct("add parent tab", "manage_tabs", map[string]any{"document": doc, "action": "add", "title": "Parent"})
	parent := str(sc, "tab_id")
	d.ok("nest tab under parent", "manage_tabs", map[string]any{"document": doc, "action": "move", "tab": "Appendix A", "parent": "Parent"})
	d.ok("move parent tab first", "manage_tabs", map[string]any{"document": doc, "action": "move", "tab": "Parent", "position": 1})
	d.ok("tabs after the moves", "get_document", map[string]any{"document": doc})
	d.ok("read the nested tab", "read_document", map[string]any{"document": doc, "tab": "Appendix A", "with_handles": true})

	// Moving Parent first made an empty tab number 1, and every later op
	// that names no tab addresses the first one. Put it back at the end so
	// the rest of the run drives the tab that has the content. Moving a
	// tab later is also the direction that used to land it one short.
	d.ok("move parent tab back to the end", "manage_tabs", map[string]any{"document": doc, "action": "move", "tab": "Parent", "position": 2})
	after := d.ok("tabs after moving it back", "get_document", map[string]any{"document": doc})
	if i, j := indexOf(after, `tab 1 "Tab 1"`), indexOf(after, `tab 1 "Parent"`); i < 0 && j >= 0 {
		t.Errorf("the content tab should be first again:\n%s", truncate(after, 500))
	}

	d.ok("create header and footer", "edit_document", map[string]any{"document": doc, "mode": "direct", "ops": []any{
		map[string]any{"op": "create_header", "content": "Live test header"},
		map[string]any{"op": "create_footer", "content": "Live test footer"},
	}})
	d.ok("read the header", "read_document", map[string]any{"document": doc, "segment": "header"})
	d.ok("raw read cut by the budget", "read_document", map[string]any{"document": doc, "tab": "Tab 1", "format": "raw", "max_chars": 700})

	deleteFooter := map[string]any{"document": doc, "mode": "suggest", "ops": []any{map[string]any{"op": "delete_footer"}}}
	if d.preview {
		d.refused("delete footer in suggest mode", "edit_document", deleteFooter, "")
	} else {
		d.refused("delete footer in suggest mode without preview", "edit_document", deleteFooter, "Developer Preview")
	}
	d.ok("delete header and footer", "edit_document", map[string]any{"document": doc, "mode": "direct", "ops": []any{
		map[string]any{"op": "delete_header"}, map[string]any{"op": "delete_footer"},
	}})

	if d.destructive && parent != "" {
		d.ok("delete the parent tab, child and all", "delete_tab", map[string]any{"document": doc, "tab": parent})
	}
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
