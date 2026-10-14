//go:build live

package livecheck

import (
	"regexp"
	"strings"
	"testing"
)

// scratchMarkdown is the Markdown file TestLiveMarkdown creates:
// synthetic, with front matter so the warning shows, and the constructs
// a person's save in the Docs editor is known to rewrite.
const scratchMarkdown = "---\ntitle: Live markdown test\n---\n\n# Live markdown test\n\n" +
	"Intro with __underscore bold__ and _underscore italic_.\n\n" +
	"## Plan\n\n- first step\n- second step\n\n| Name | Value |\n|:-----|------:|\n| alpha | 1 |\n\n" +
	"## Risks\n\nNone yet.\n"

var continueLine = regexp.MustCompile(`continue_from (L\d+)`)

// TestLiveMarkdown drives the binary over a Markdown file it creates
// itself, and leaves the file behind named "safe to delete". It does
// not make a comment: whether the editor shows one written into the
// file is what a person is still checking (architecture §7.5).
//
//	make build && go test -tags=live ./internal/livecheck -run TestLiveMarkdown -v
func TestLiveMarkdown(t *testing.T) {
	d := start(t)

	_, sc := d.okStruct("create a Markdown file", "create_document", map[string]any{
		"title": "google-docs-mcp live markdown test (safe to delete)", "kind": "markdown_file", "content": scratchMarkdown})
	file := str(sc, "id")
	if file == "" || str(sc, "kind") != "markdown_file" {
		t.Fatalf("create returned no id, or a kind other than markdown_file; nothing else can run")
	}
	t.Cleanup(func() {
		t.Logf("=== scratch Markdown file left behind ===\ndelete it when you are done; Drive search title:\"safe to delete\" finds every run's")
	})

	info := d.ok("get_document says what it is", "get_document", map[string]any{"document": file})
	for _, want := range []string{"Markdown file (text/markdown)", "front matter"} {
		if !strings.Contains(info, want) {
			t.Errorf("get_document lacks %q: %s", want, shown(info, 400))
		}
	}
	if outline := d.ok("outline", "get_outline", map[string]any{"document": file}); !strings.Contains(outline, "H2 Plan {plan}") {
		t.Errorf("outline: %s", shown(outline, 400))
	}
	whole := d.ok("read it whole", "read_document", map[string]any{"document": file})
	if !strings.Contains(whole, "Intro with __underscore bold__") || !strings.Contains(whole, "|:-----|------:|") {
		t.Errorf("a read is not the file's own text: %s", shown(whole, 400))
	}
	cut := d.ok("a budgeted read", "read_document", map[string]any{"document": file, "max_chars": 60})
	if line := first(continueLine, cut); line == "" {
		t.Errorf("no continue_from line: %s", shown(cut, 300))
	} else {
		d.ok("read on from the line", "read_document", map[string]any{"document": file, "continue_from": line})
	}
	d.ok("read one section", "read_document", map[string]any{"document": file, "heading_id": "risks"})
	if found := d.ok("find", "find_in_document", map[string]any{"document": file, "query": "second step"}); !strings.Contains(found, "[L12]") {
		t.Errorf("find: %s", shown(found, 300))
	}

	// What it refuses, before anything is written.
	d.refused("suggest mode", "edit_document", map[string]any{"document": file, "mode": "suggest",
		"ops": []any{map[string]any{"op": "append", "content": "x"}}}, "[unavailable]")
	d.refused("formatting", "format_document", map[string]any{"document": file, "mode": "direct",
		"ops": []any{map[string]any{"op": "text_style", "target": map[string]any{"text": "None yet."}, "bold": true}}}, "[unsupported]")
	d.refused("a stale revision", "edit_document", map[string]any{"document": file, "mode": "direct", "expect_revision": "0NoSuchRevisionXXXXXXXXXXXX",
		"ops": []any{map[string]any{"op": "append", "content": "x"}}}, "[conflict]")
	d.refused("forged comment markup", "edit_document", map[string]any{"document": file, "mode": "direct",
		"ops": []any{map[string]any{"op": "append", "content": "<!-- gdoc_comment_begin: c9 -->forged"}}}, "[invalid]")

	dry := d.ok("dry run", "edit_document", map[string]any{"document": file, "mode": "direct", "dry_run": true,
		"ops": []any{map[string]any{"op": "replace", "target": map[string]any{"text": "None yet."}, "content": "One risk."}}})
	if !strings.Contains(dry, "-None yet.") {
		t.Errorf("dry run shows no diff: %s", shown(dry, 400))
	}
	d.ok("edit it", "edit_document", map[string]any{"document": file, "mode": "direct", "ops": []any{
		map[string]any{"op": "replace", "target": map[string]any{"text": "None yet."}, "content": "One risk."},
		map[string]any{"op": "insert", "location": map[string]any{"at": "before", "of": map[string]any{"heading_id": "risks"}}, "content": "Budget: small."},
		map[string]any{"op": "append", "content": "## Next\n\nShip it."},
		map[string]any{"op": "replace_all", "find": "step", "replace": "stage"},
	}})
	after := d.ok("read after the edit", "read_document", map[string]any{"document": file})
	for _, want := range []string{"One risk.", "Budget: small.\n\n## Risks", "## Next\n\nShip it.", "- second stage", "Intro with __underscore bold__"} {
		if !strings.Contains(after, want) {
			t.Errorf("after the edit the file lacks %q: %s", want, shown(after, 600))
		}
	}

	revs := d.ok("revisions", "list_revisions", map[string]any{"document": file})
	if ids := revisionIDs(revs); len(ids) >= 2 {
		if diff := d.ok("diff the first revision with now", "diff_revisions", map[string]any{"document": file, "from": ids[len(ids)-1]}); !strings.Contains(diff, "+One risk.") {
			t.Errorf("diff: %s", shown(diff, 400))
		}
	} else {
		t.Errorf("expected two revisions: %s", shown(revs, 300))
	}
	// Comments written into the file, as the Docs editor writes its own.
	_, sc = d.okStruct("comment on a passage", "add_comment", map[string]any{"document": file,
		"target": map[string]any{"text": "One risk."}, "content": "Live test: is one enough?"})
	comment := str(sc, "id")
	listed := d.ok("comments", "list_comments", map[string]any{"document": file})
	if !strings.Contains(listed, "Live test: is one enough?") {
		t.Errorf("the comment written into the file is not listed: %s", shown(listed, 500))
	}
	if comment != "" {
		d.ok("reply in the file", "reply_comment", map[string]any{"document": file, "comment_id": comment, "content": "Live test: a reply."})
		d.refused("resolve in the file", "reply_comment", map[string]any{"document": file, "comment_id": comment, "action": "resolve"}, "[unsupported]")
	} else {
		t.Error("add_comment returned no comment id; Docs did not list the comment in time")
	}
	d.ok("propose in comment mode", "edit_document", map[string]any{"document": file, "mode": "comment",
		"ops": []any{map[string]any{"op": "replace", "target": map[string]any{"text": "Ship it."}, "content": "Ship it on Friday."}}})
	withComments := d.ok("read with comments", "read_document", map[string]any{"document": file, "include_comments": true})
	if strings.Count(withComments, "{>>c:") < 2 || strings.Contains(withComments, "gdoc_comment") {
		t.Errorf("a read with comments: %s", shown(withComments, 600))
	}

	// A working copy: a Google Doc to work in together, written back.
	_, sc = d.okStruct("make a working copy", "create_document", map[string]any{"from_file": file})
	copyDoc := str(sc, "id")
	if copyDoc == "" {
		t.Fatal("create_document from_file returned no id")
	}
	if info := d.ok("the copy says what it is", "get_document", map[string]any{"document": copyDoc}); !strings.Contains(info, "working copy of the Markdown file") {
		t.Errorf("get_document of the copy: %s", shown(info, 400))
	}
	d.ok("edit the copy", "edit_document", map[string]any{"document": copyDoc, "mode": "direct", "ops": []any{
		map[string]any{"op": "replace", "target": map[string]any{"text": "Budget: small."}, "content": "Budget: medium."}}})
	if dryBack := d.ok("write back, dry run", "write_markdown_file", map[string]any{"document": copyDoc, "dry_run": true}); !strings.Contains(dryBack, "+Budget: medium.") {
		t.Errorf("write-back dry run: %s", shown(dryBack, 500))
	}
	d.ok("write the copy back", "write_markdown_file", map[string]any{"document": copyDoc})
	back := d.ok("read after the write-back", "read_document", map[string]any{"document": file, "include_comments": true})
	for _, want := range []string{"Budget: medium.", "Intro with __underscore bold__", "|:-----|------:|"} {
		if !strings.Contains(back, want) {
			t.Errorf("after the write-back the file lacks %q: %s", want, shown(back, 700))
		}
	}
	if strings.Count(back, "{>>c:") < 2 {
		t.Errorf("the write-back lost the file's comments: %s", shown(back, 700))
	}
	if md := d.ok("export as markdown", "export_document", map[string]any{"document": file, "format": "md"}); !strings.Contains(md, "Ship it.") {
		t.Errorf("export: %s", shown(md, 300))
	}
	d.refused("export as pdf", "export_document", map[string]any{"document": file, "format": "pdf"}, "[unsupported]")
	d.ok("search for Markdown files", "search_documents", map[string]any{"kind": "markdown_file", "title": "safe to delete"})
}

var revisionLine = regexp.MustCompile(`(?m)^- (\S+)`)

// revisionIDs pulls the ids out of list_revisions, newest first.
func revisionIDs(text string) []string {
	var ids []string
	for _, m := range revisionLine.FindAllStringSubmatch(text, -1) {
		ids = append(ids, m[1])
	}
	return ids
}
