package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mmedum/google-docs-mcp/internal/doc/doctest"
	"github.com/mmedum/google-docs-mcp/internal/gdocs"
	"github.com/mmedum/google-docs-mcp/internal/plan"
	"github.com/mmedum/google-docs-mcp/internal/render"
)

func TestDryRunListsFollowups(t *testing.T) {
	svc, api := writable(t, false)
	ctx := context.Background()
	res, err := svc.Edit(ctx, EditRequest{Document: fixtureID, DryRun: true, Ops: []EditOp{
		{Kind: plan.OpInsertTable, Location: &Location{At: "end"}, Table: &TableOp{Rows: 2, Cols: 2, Data: [][]string{{"a", "b"}, {"c", "d"}}}},
		{Kind: plan.OpCreateHeader, Target: &Target{Tab: "Notes"}, Content: "Draft"},
		{Kind: plan.OpAppend, Content: "plain"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(api.batches) != 0 || len(res.Followups) != 2 {
		t.Fatalf("followups = %v (batches %d)", res.Followups, len(api.batches))
	}
	if !strings.Contains(res.Followups[0], "op 0: a second batch fills the new 2×2 table with 4 cell(s)") || !strings.Contains(res.Followups[1], "op 1: a second batch writes the header content") {
		t.Errorf("followups = %v", res.Followups)
	}
	if !strings.Contains(res.Text, "then op 0: a second batch fills") {
		t.Errorf("text lacks the follow-ups:\n%s", res.Text)
	}
	// Comment mode changes nothing, so it has no second batch.
	res, err = svc.Edit(ctx, EditRequest{Document: fixtureID, DryRun: true, Mode: "comment", Ops: []EditOp{
		{Kind: plan.OpCreateHeader, Target: &Target{Tab: "Notes"}, Content: "Draft"},
	}})
	if err != nil || len(res.Followups) != 0 {
		t.Fatalf("comment mode: %v %v", res.Followups, err)
	}
}

func TestSegmentByID(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	byID, err := svc.Read(ctx, ReadRequest{Document: fixtureID, Scope: ReadScope{Segment: "kix.h1"}, Options: render.Options{MaxChars: DefaultMaxChars}})
	if err != nil {
		t.Fatal(err)
	}
	byKind, err := svc.Read(ctx, ReadRequest{Document: fixtureID, Scope: ReadScope{Segment: "header1"}, Options: render.Options{MaxChars: DefaultMaxChars}})
	if err != nil {
		t.Fatal(err)
	}
	if byID.Text != byKind.Text || byID.Segment != "header1" {
		t.Fatalf("by id: %+v\nby kind: %+v", byID, byKind)
	}
	if _, err := svc.Read(ctx, ReadRequest{Document: fixtureID, Scope: ReadScope{Segment: "kix.nope"}}); err == nil {
		t.Fatal("unknown segment id accepted")
	}
}

func TestFootnoteFollowupFillsTheBlankParagraph(t *testing.T) {
	svc, api := writable(t, false)
	ctx := context.Background()
	api.replies = []string{`{"replies":[{"createFootnote":{"footnoteId":"kix.newfn"}}],"writeControl":{"requiredRevisionId":"rev-0002"}}`}
	// Google creates a footnote whose paragraph holds one space.
	var after gdocs.Document
	if err := json.Unmarshal(doctest.RawFixture(t), &after); err != nil {
		t.Fatal(err)
	}
	after.RevisionID = "rev-0002"
	after.Tabs[0].DocumentTab.Footnotes["kix.newfn"] = gdocs.Footnote{FootnoteID: "kix.newfn", Content: []*gdocs.StructuralElement{{StartIndex: 0, EndIndex: 2,
		Paragraph: &gdocs.Paragraph{Elements: []*gdocs.ParagraphElement{{StartIndex: 0, EndIndex: 2, TextRun: &gdocs.TextRun{Content: " \n"}}}}}}}
	api.afterBatch, _ = json.Marshal(&after)
	res, err := svc.Edit(ctx, EditRequest{Document: fixtureID, Mode: "direct", Ops: []EditOp{{Kind: plan.OpFootnote, Location: &Location{At: "after", Of: &Target{Text: "Revenue grew"}}, Content: "Source: finance report."}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(api.batches) != 2 {
		t.Fatalf("batches = %d; warnings %v", len(api.batches), res.Warnings)
	}
	second := kindsOf(t, api.batches[1].Requests)
	if !strings.Contains(second, "deleteContentRange[0,1)") || !strings.Contains(second, "insertText@0") {
		t.Fatalf("second batch should replace the space, got %s", second)
	}
	var m map[string]map[string]any
	_ = json.Unmarshal(api.batches[1].Requests[0], &m)
	for _, req := range m {
		if loc, ok := req["range"].(map[string]any); ok && loc["segmentId"] != "kix.newfn" {
			t.Fatalf("second batch targets %v", loc)
		}
	}
	if !strings.Contains(res.Changes[0].Description, "content written") {
		t.Fatalf("changes: %+v", res.Changes)
	}
}

// A blank paragraph is filled wherever an insertion lands in one, not
// only on the follow-up path: appending into a segment whose only
// paragraph holds a space replaces that space and takes the content's
// own paragraph style.
func TestAppendIntoABlankParagraphFillsIt(t *testing.T) {
	svc, api := writable(t, false)
	var w gdocs.Document
	if err := json.Unmarshal(doctest.RawFixture(t), &w); err != nil {
		t.Fatal(err)
	}
	w.Tabs[0].DocumentTab.Headers["kix.h1"] = gdocs.Header{HeaderID: "kix.h1", Content: []*gdocs.StructuralElement{{StartIndex: 0, EndIndex: 2,
		Paragraph: &gdocs.Paragraph{Elements: []*gdocs.ParagraphElement{{StartIndex: 0, EndIndex: 2, TextRun: &gdocs.TextRun{Content: " \n"}}}}}}}
	api.raw, _ = json.Marshal(&w)
	res, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Mode: "direct", DryRun: true,
		Ops: []EditOp{{Kind: plan.OpAppend, Content: "# Draft", Target: nil, Location: &Location{At: "end", Of: &Target{Segment: "header1"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(res.RequestKinds, " "); got != "deleteContentRange insertText updateTextStyle updateParagraphStyle" {
		t.Fatalf("requests = %s", got)
	}

	// A header whose only content is a logo is not blank: filling it
	// would delete the image, which no insertion may do.
	w.Tabs[0].DocumentTab.Headers["kix.h1"] = gdocs.Header{HeaderID: "kix.h1", Content: []*gdocs.StructuralElement{{StartIndex: 0, EndIndex: 2,
		Paragraph: &gdocs.Paragraph{Elements: []*gdocs.ParagraphElement{
			{StartIndex: 0, EndIndex: 1, InlineObjectElement: &gdocs.InlineObjectElement{InlineObjectID: "kix.img1"}},
			{StartIndex: 1, EndIndex: 2, TextRun: &gdocs.TextRun{Content: "\n"}}}}}}}
	api.raw, _ = json.Marshal(&w)
	svc.Invalidate(fixtureID)
	res, err = svc.Edit(context.Background(), EditRequest{Document: fixtureID, Mode: "direct", DryRun: true,
		Ops: []EditOp{{Kind: plan.OpAppend, Content: "# Draft", Location: &Location{At: "end", Of: &Target{Segment: "header1"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range res.RequestKinds {
		if k == "deleteContentRange" {
			t.Fatalf("appending into a header holding an image deletes it: %v", res.RequestKinds)
		}
	}
}
