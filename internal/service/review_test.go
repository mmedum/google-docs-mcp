package service

// Regression tests for the findings of the Phase 1 and Phase 2 reviews.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mmedum/google-docs-mcp/internal/gapi"
	"github.com/mmedum/google-docs-mcp/internal/gdocs"
	"github.com/mmedum/google-docs-mcp/internal/plan"
)

// p11 holds an inline image and a footnote reference: "See " [img] " chart" [fn] " and the site".
func TestAlignedMinimalDiffPastObjects(t *testing.T) {
	svc, api := writable(t, false)
	ctx := context.Background()
	f := fetched(t, svc)
	r, err := svc.ResolveTarget(f, Target{Handle: "p11"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Aligned) == 0 || !strings.Contains(r.Aligned, string(objectPlaceholder)) || int64(len([]rune(r.Aligned))) != r.End-r.Start-1 {
		t.Fatalf("aligned text %q for [%d,%d)", r.Aligned, r.Start, r.End)
	}
	// Replacing the whole paragraph's text edits the last word at its real
	// index; the objects, which the new text cannot express, are deleted
	// as their own one-unit hunks (the guard blocks that without force).
	_, err = svc.Edit(ctx, EditRequest{Document: fixtureID, Mode: "direct", DryRun: true, Ops: []EditOp{{Kind: plan.OpReplace, Target: &Target{Handle: "p11"}, Content: "See  chart and the page"}}})
	if classOf(err) != "blocked" {
		t.Fatalf("objects in a replaced paragraph: %v", err)
	}
	res, err := svc.Edit(ctx, EditRequest{Document: fixtureID, Mode: "direct", DryRun: true, Force: true, Ops: []EditOp{{Kind: plan.OpReplace, Target: &Target{Handle: "p11"}, Content: "See  chart and the page"}}})
	if err != nil {
		t.Fatal(err)
	}
	var reqs []json.RawMessage
	_ = json.Unmarshal(res.Requests, &reqs)
	got := kindsOf(t, reqs)
	if !strings.HasPrefix(got, "deleteContentRange[179,182) insertText@179 ") || strings.Count(got, "deleteContentRange") != 3 || !res.Changes[0].Minimal {
		t.Fatalf("minimal diff past objects: %s (%+v)", got, res.Changes[0])
	}
	// A text target inside the same paragraph keeps the objects untouched.
	res, err = svc.Edit(ctx, EditRequest{Document: fixtureID, Mode: "direct", DryRun: true, Ops: []EditOp{{Kind: plan.OpReplace, Target: &Target{Text: "the site"}, Content: "the page"}}})
	if err != nil || !res.Changes[0].Minimal {
		t.Fatalf("text target: %v %+v", err, res.Changes)
	}
	_ = json.Unmarshal(res.Requests, &reqs)
	if got := kindsOf(t, reqs); got != "deleteContentRange[179,182) insertText@179" {
		t.Fatalf("text target requests: %s", got)
	}
	_ = api
}

func TestInlineInsertKeepsParagraphStyle(t *testing.T) {
	svc, _ := writable(t, false)
	res, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Mode: "direct", DryRun: true, Ops: []EditOp{
		{Kind: plan.OpInsert, Location: &Location{At: "after", Of: &Target{Text: "Background"}}, Content: " (draft)"},
		{Kind: plan.OpInsert, Location: &Location{At: "end", Of: &Target{Text: "First point"}}, Content: " more"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(compact(res.Requests), "namedStyleType") || strings.Contains(compact(res.Requests), "deleteParagraphBullets") {
		t.Fatalf("inline insert restyled the host paragraph:\n%s", res.Requests)
	}
	// A block-boundary insert still styles its new paragraph.
	res, err = svc.Edit(context.Background(), EditRequest{Document: fixtureID, Mode: "direct", DryRun: true, Ops: []EditOp{{Kind: plan.OpInsert, Location: &Location{At: "after", Of: &Target{Handle: "p5"}}, Content: "New item"}}})
	if err != nil || !strings.Contains(compact(res.Requests), "NORMAL_TEXT") {
		t.Fatalf("boundary insert: %v\n%s", err, res.Requests)
	}
}

func TestReplaceAllAndMergeAreGuarded(t *testing.T) {
	svc, api := writable(t, false)
	ctx := context.Background()
	api.comments = []*gapi.DriveComment{{ID: "c1", Content: "x", QuotedFileContent: &gapi.QuotedText{Value: "Second point"}}}
	_, err := svc.Edit(ctx, EditRequest{Document: fixtureID, Mode: "direct", Ops: []EditOp{{Kind: plan.OpReplaceAll, Params: plan.Params{Find: "point", Replace: "item"}}}})
	if classOf(err) != "blocked" || !strings.Contains(messageOf(err), "c1") {
		t.Fatalf("replace_all over a comment: %v", err)
	}
	res, err := svc.Edit(ctx, EditRequest{Document: fixtureID, Mode: "direct", Ops: []EditOp{{Kind: plan.OpReplaceAll, Params: plan.Params{Find: "Step", Replace: "Stage"}}}})
	if err != nil || len(res.Changes) != 1 {
		t.Fatalf("replace_all elsewhere: %v", err)
	}
	api.comments = []*gapi.DriveComment{{ID: "c2", Content: "x", QuotedFileContent: &gapi.QuotedText{Value: "Value"}}}
	svc.Invalidate(fixtureID)
	_, err = svc.Edit(ctx, EditRequest{Document: fixtureID, Mode: "direct", Ops: []EditOp{{Kind: plan.OpMergeCells, Table: &TableOp{Table: "tbl1", FromCell: "r1c1", ToCell: "r1c2"}}}})
	if classOf(err) != "blocked" || !strings.Contains(messageOf(err), "c2") {
		t.Fatalf("merge over a comment: %v", err)
	}
	// The head cell's own anchors are not lost by a merge.
	api.comments = []*gapi.DriveComment{{ID: "c3", Content: "x", QuotedFileContent: &gapi.QuotedText{Value: "Name"}}}
	svc.Invalidate(fixtureID)
	if _, err := svc.Edit(ctx, EditRequest{Document: fixtureID, Mode: "direct", Ops: []EditOp{{Kind: plan.OpMergeCells, Table: &TableOp{Table: "tbl1", FromCell: "r1c1", ToCell: "r1c2"}}}}); err != nil {
		t.Fatalf("merge with the comment in the head cell: %v", err)
	}
}

func TestSuggestModeRefusesUnsupportedRequests(t *testing.T) {
	svc, _ := writable(t, true)
	_, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Mode: "suggest", DryRun: true, Ops: []EditOp{
		{Kind: plan.OpReplace, Target: &Target{Text: "First point"}, Content: "First item"},
		{Kind: plan.OpDeleteHeader},
	}})
	if classOf(err) != "invalid" || !strings.Contains(messageOf(err), "op 1 (delete_header) cannot be made as a suggestion") {
		t.Fatalf("suggest delete_header: %v", err)
	}
}

func TestEmptySectionReplaceInsertsParagraph(t *testing.T) {
	svc, api := writable(t, false)
	// Make "Summary", the last section, empty: drop its two paragraphs.
	var w gdocs.Document
	if err := json.Unmarshal(api.raw, &w); err != nil {
		t.Fatal(err)
	}
	body := w.Tabs[0].DocumentTab.Body
	var kept []*gdocs.StructuralElement
	for _, se := range body.Content {
		if se.StartIndex == 192 || se.StartIndex == 213 {
			continue
		}
		kept = append(kept, se)
	}
	body.Content = kept
	api.raw = mustJSON(&w)
	svc.Invalidate(fixtureID)
	fetched(t, svc)
	res, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Mode: "direct", DryRun: true, Ops: []EditOp{{Kind: plan.OpReplace, Target: &Target{HeadingID: "h.sum", IncludeHeading: new(false)}, Content: "Run make."}}})
	if err != nil {
		t.Fatal(err)
	}
	c := compact(res.Requests)
	// Inserted before the heading's final newline as its own NORMAL_TEXT
	// paragraph, not glued onto the heading.
	if !strings.Contains(c, `"index":191`) || !strings.Contains(c, `"text":"\nRun make."`) || !strings.Contains(c, "NORMAL_TEXT") || res.Changes[0].Kind != plan.OpInsert {
		t.Fatalf("empty section replace:\n%s\n%+v", res.Requests, res.Changes)
	}
}

func TestExpectRevisionRecheckedOnConflict(t *testing.T) {
	svc, api := writable(t, false)
	api.batchErrs = []error{&gapi.APIError{Status: 400, Message: "The provided revision id does not match the current revision"}}
	var w gdocs.Document
	_ = json.Unmarshal(api.raw, &w)
	w.RevisionID = "rev-0002"
	api.afterBatch = mustJSON(&w)
	_, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Mode: "direct", ExpectRevision: "rev-0001", Ops: []EditOp{{Kind: plan.OpReplace, Target: &Target{Text: "First point"}, Content: "First item"}}})
	if classOf(err) != "conflict" || !strings.Contains(messageOf(err), "rev-0002, not rev-0001") || len(api.batches) != 1 {
		t.Fatalf("conflict retry ignored expect_revision: %v (batches %d)", err, len(api.batches))
	}
}

func TestMultiRangeCommentAnchors(t *testing.T) {
	svc, api := writable(t, true)
	var w gdocs.Document
	_ = json.Unmarshal(api.raw, &w)
	w.Comments = []gdocs.CommentThread{{CommentID: "c9", AnchorID: "a1", HeadPost: gdocs.Post{Content: "hi"}, Status: "OPEN"}}
	w.Tabs[0].DocumentTab.CommentAnchors = map[string]gdocs.CommentAnchor{"a1": {AnchorID: "a1", Ranges: []*gdocs.Range{{StartIndex: 69, EndIndex: 80}, {StartIndex: 94, EndIndex: 106}}}}
	api.raw = mustJSON(&w)
	svc.Invalidate(fixtureID)
	f := fetched(t, svc)
	threads, err := svc.comments(context.Background(), f)
	if err != nil || len(threads) != 1 || threads[0].Start != 69 || threads[0].End != 106 {
		t.Fatalf("union of ranges: %+v %v", threads, err)
	}
	if as := anchorsIn(f.Doc.Tabs[0], f.Doc.Tabs[0].Body, 95, 100, threads); len(as) != 1 {
		t.Fatalf("second range guarded: %+v", as)
	}
}

func TestDeleteOnlyRootTabRefused(t *testing.T) {
	_, api := writable(t, false)
	var w gdocs.Document
	_ = json.Unmarshal(api.raw, &w)
	// Nest the second tab under the first.
	w.Tabs[0].ChildTabs = []*gdocs.Tab{w.Tabs[1]}
	w.Tabs[1].TabProperties.ParentTabID = "t.0"
	w.Tabs = w.Tabs[:1]
	api.raw = mustJSON(&w)
	svc := New(api, Options{Destructive: true, DefaultWriteMode: "direct", CacheTTL: 1})
	_, err := svc.DeleteTab(context.Background(), TabRequest{Document: fixtureID, Tab: "Main"})
	if classOf(err) != "invalid" || !strings.Contains(messageOf(err), "top-level tab") {
		t.Fatalf("only root tab: %v", err)
	}
	res, err := svc.DeleteTab(context.Background(), TabRequest{Document: fixtureID, Tab: "Notes"})
	if err != nil || res.TabID != "t.1" {
		t.Fatalf("child tab: %+v %v", res, err)
	}
}

func TestFindOffsetsPastObjects(t *testing.T) {
	svc, _ := writable(t, false)
	res, err := svc.Find(context.Background(), FindRequest{Document: fixtureID, Query: "site"})
	if err != nil || len(res.Matches) != 1 || res.Matches[0].Match != "site" || res.Matches[0].Handle != "p11" || !strings.Contains(res.Matches[0].Context, "«site»") || strings.Contains(res.Matches[0].Context, string(objectPlaceholder)) {
		t.Fatalf("find past objects: %+v %v", res.Matches, err)
	}
	res, err = svc.Find(context.Background(), FindRequest{Document: fixtureID, Query: "the s.te", Regex: true})
	if err != nil || len(res.Matches) != 1 || res.Matches[0].Match != "the site" {
		t.Fatalf("regex past objects: %+v %v", res.Matches, err)
	}
}

func TestBlockShiftWarning(t *testing.T) {
	svc, api := writable(t, false)
	var w gdocs.Document
	_ = json.Unmarshal(api.raw, &w)
	w.RevisionID = "rev-0002"
	body := w.Tabs[0].DocumentTab.Body
	extra := &gdocs.StructuralElement{StartIndex: 226, EndIndex: 227, Paragraph: &gdocs.Paragraph{Elements: []*gdocs.ParagraphElement{{StartIndex: 226, EndIndex: 227, TextRun: &gdocs.TextRun{Content: "\n"}}}}}
	// A new block at the end renumbers nothing.
	body.Content = append(body.Content, extra)
	api.afterBatch = mustJSON(&w)
	res, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Mode: "direct", Ops: []EditOp{{Kind: plan.OpAppend, Content: "Tail"}}})
	if err != nil || strings.Contains(strings.Join(res.Warnings, "\n"), "different blocks") {
		t.Fatalf("append should not warn: %v %+v", err, res.Warnings)
	}
	// A new block in the middle does.
	body.Content = append([]*gdocs.StructuralElement{body.Content[0], body.Content[1], extra}, body.Content[2:len(body.Content)-1]...)
	api.afterBatch = mustJSON(&w)
	api.batches = nil
	svc.Invalidate(fixtureID)
	res, err = svc.Edit(context.Background(), EditRequest{Document: fixtureID, Mode: "direct", Ops: []EditOp{{Kind: plan.OpInsert, Location: &Location{At: "after", Of: &Target{Handle: "p1"}}, Content: "Middle"}}})
	if err != nil || !strings.Contains(strings.Join(res.Warnings, "\n"), "handles after the edited region now name different blocks") {
		t.Fatalf("shift warning: %v %+v", err, res.Warnings)
	}
}
