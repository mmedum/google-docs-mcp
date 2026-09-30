//go:build live

package livecheck

import (
	"regexp"
	"strings"
	"testing"
)

// dryRunArgs is one plausible call per tool that accepts dry_run. The
// ops are deliberately real work — a replace, a cell, a style, an
// object, a page setup — because a dry run of nothing proves nothing.
// comment and tab are a thread and a spare tab the sweep made for the
// dry runs that delete, and suggesting whether a suggestion is pending.
func dryRunArgs(doc, table, comment, tab string, suggesting bool) map[string]map[string]any {
	args := map[string]map[string]any{
		"edit_document": {"document": doc, "mode": "direct", "dry_run": true, "ops": []any{
			map[string]any{"op": "replace", "target": map[string]any{"text": "Closing line"}, "content": "Closing line, rewritten by a dry run"}}},
		"format_document": {"document": doc, "mode": "direct", "dry_run": true, "ops": []any{
			map[string]any{"op": "text_style", "target": map[string]any{"text": "Closing line"}, "italic": true}}},
		"insert_object": {"document": doc, "mode": "direct", "dry_run": true, "kind": "date", "date": "2026-09-05",
			"location": map[string]any{"at": "end", "of": map[string]any{"text": "Closing line"}}},
		"layout_document": {"document": doc, "mode": "direct", "dry_run": true, "ops": []any{
			map[string]any{"op": "page", "margin_top_pt": 72}}},
	}
	args["add_comment"] = map[string]any{"document": doc, "dry_run": true, "content": "written by a dry run",
		"target": map[string]any{"text": "Closing line"}}
	if comment != "" {
		args["delete_comment"] = map[string]any{"document": doc, "comment_id": comment, "confirm_comment_id": comment, "dry_run": true}
	}
	if tab != "" {
		args["delete_tab"] = map[string]any{"document": doc, "tab": tab, "confirm_tab": tab, "dry_run": true}
	}
	if suggesting {
		args["review_suggestion"] = map[string]any{"document": doc, "action": "accept", "all": true, "dry_run": true}
	}
	if table != "" {
		args["edit_table"] = map[string]any{"document": doc, "mode": "direct", "dry_run": true, "ops": []any{
			map[string]any{"op": "set_cells", "table": table, "cells": []any{map[string]any{"cell": "r1c1", "content": "written by a dry run"}}}}}
	}
	return args
}

var revisionOf = regexp.MustCompile(`(?m)^revision (\S+)`)

// plans are the tools whose dry run reports planned ops.
var plans = map[string]bool{"edit_document": true, "format_document": true, "insert_object": true,
	"layout_document": true, "edit_table": true}

// dryRunTargets makes what the delete and review dry runs act on, before
// the sweep reads the revision it holds still: a document-level comment,
// a spare tab when deletes are registered, and a pending suggestion
// with Developer Preview. The spare tab is deleted again afterwards.
func dryRunTargets(d *driver, doc string) (comment, tab string, suggesting bool) {
	_, sc := d.okStruct("a comment for the dry runs", "add_comment", map[string]any{"document": doc,
		"content": "Live test: the target of a dry-run delete."})
	comment = str(sc, "id")
	if d.destructive {
		tab = "Dry run tab"
		d.ok("a tab for the dry runs", "manage_tabs", map[string]any{"document": doc, "action": "add", "title": tab})
		d.t.Cleanup(func() {
			d.person.answer("accept")
			d.ok("delete the dry-run tab", "delete_tab", map[string]any{"document": doc, "tab": tab, "confirm_tab": tab})
			d.wasAsked("delete the dry-run tab", "delete_tab: delete the tab `Dry run tab`")
		})
	}
	if d.preview {
		d.ok("a suggestion for the dry runs", "edit_document", map[string]any{"document": doc, "mode": "suggest", "ops": []any{
			map[string]any{"op": "append", "content": "Suggested by the live test for a dry-run review."}}})
		suggesting = true
	}
	return comment, tab, suggesting
}

// liveDryRuns is the check a fake cannot perform: that a dry run sends
// nothing. A test double accepts the batch whether or not the code meant
// to send it, so "nothing was sent" can only be established against the
// real document, by watching its revision.
//
// It also holds the rendering to its promise. A sibling server found
// three dry runs that described a request body beside a null field; the
// bug is invisible until someone reads every one of them, which is what
// this does.
func liveDryRuns(t *testing.T, d *driver, doc, table string) {
	comment, tab, suggesting := dryRunTargets(d, doc)
	before := first(revisionOf, d.ok("revision before the dry runs", "get_document", map[string]any{"document": doc}))
	if before == "" {
		t.Fatal("cannot read the revision from get_document; the wording changed")
	}

	args := dryRunArgs(doc, table, comment, tab, suggesting)
	tools, err := d.cs.ListTools(d.ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	var covered int
	for _, tool := range tools.Tools {
		in, ok := tool.InputSchema.(map[string]any)
		if !ok {
			continue
		}
		props, _ := in["properties"].(map[string]any)
		if _, takesDryRun := props["dry_run"]; !takesDryRun {
			continue
		}
		a, ok := args[tool.Name]
		switch {
		case !ok && tool.Name == "review_suggestion" && !d.preview:
			t.Log("review_suggestion needs Developer Preview; not swept")
			continue
		case !ok:
			t.Errorf("%s accepts dry_run and this sweep has no call for it", tool.Name)
			continue
		}
		covered++
		text := d.ok("dry run: "+tool.Name, tool.Name, a)
		switch {
		case !strings.Contains(text, "dry run"):
			t.Errorf("%s: the rendering does not say it was a dry run: %s", tool.Name, shown(text, 300))
		case !plans[tool.Name]:
			// A delete, a comment or a review names what it would act on
			// in a sentence rather than as planned ops.
		case strings.Contains(text, "0 op(s) planned"):
			t.Errorf("%s: planned nothing, so the sweep proved nothing: %s", tool.Name, shown(text, 300))
		case !strings.Contains(text, "- op "):
			t.Errorf("%s: named no operation it would perform: %s", tool.Name, shown(text, 300))
		}
	}
	if covered < 4 {
		t.Fatalf("only %d tools swept; the schema probably renamed dry_run", covered)
	}

	after := first(revisionOf, d.ok("revision after the dry runs", "get_document", map[string]any{"document": doc}))
	if after != before {
		// The ids are not printed. They would reach the transcript
		// without passing through shown, and the failure is the fact
		// that the revision moved, not which revision it moved to.
		t.Errorf("a dry run changed the document: the revision moved after %d dry runs", covered)
	}
	t.Logf("=== dry runs ===\n%d tools swept, revision unchanged", covered)

	// The review the sweep previewed, made: every pending suggestion in
	// the scratch document, which asks the person first.
	if suggesting {
		d.person.answer("accept")
		d.ok("accept every pending suggestion", "review_suggestion", map[string]any{"document": doc, "action": "accept", "all": true})
		d.wasAsked("accept every pending suggestion", "review_suggestion: accept all")
	}
}
