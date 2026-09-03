package service

import (
	"context"
	"strings"
	"testing"

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
