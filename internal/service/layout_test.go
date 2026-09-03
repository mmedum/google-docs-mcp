package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mmedum/google-docs-mcp/internal/plan"
)

func floatp(f float64) *float64 { return &f }
func boolp(b bool) *bool        { return &b }

// requestKinds names the requests a dry run planned.
func requestKinds(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var reqs []json.RawMessage
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &reqs); err != nil {
			t.Fatal(err)
		}
	}
	return kindsOf(t, reqs)
}

func layoutEdit(ops ...EditOp) EditRequest {
	return EditRequest{Document: fixtureID, Mode: "direct", DryRun: true, Ops: ops}
}

func TestLayoutOpsResolve(t *testing.T) {
	svc, _ := writable(t, false)
	ctx := context.Background()
	cases := []struct {
		name  string
		op    EditOp
		kinds string
		want  []string
	}{
		{"page setup of the first tab",
			EditOp{Kind: plan.OpPageSetup, Target: &Target{}, Layout: &LayoutOp{Page: plan.PageSpec{
				WidthPt: 595, HeightPt: 842, PageMargins: plan.PageMargins{TopPt: floatp(36)}}}},
			"updateDocumentStyle", []string{`"tabId":"t.0"`, `"magnitude":595`, `"fields":"pageSize,marginTop"`}},
		{"page setup of another tab",
			EditOp{Kind: plan.OpPageSetup, Target: &Target{Tab: "Notes"}, Layout: &LayoutOp{Page: plan.PageSpec{Background: "none"}}},
			"updateDocumentStyle", []string{`"tabId":"t.1"`, `"fields":"background"`}},
		{"named style",
			EditOp{Kind: plan.OpNamedStyle, Target: &Target{}, Layout: &LayoutOp{NamedStyle: plan.NamedStyleSpec{
				Style: "HEADING_1", Text: plan.TextStyleSpec{Foreground: "#1a73e8"}}}},
			"updateNamedStyle", []string{`"namedStyleType":"HEADING_1"`, `textStyle.foregroundColor`}},
		{"section around a passage",
			EditOp{Kind: plan.OpSectionStyle, Target: &Target{Text: "Revenue grew"}, Layout: &LayoutOp{Section: plan.SectionSpec{Columns: 2}}},
			"updateSectionStyle[29,41)", []string{`"columnProperties"`, `"startIndex":29`}},
		{"section break after a block",
			EditOp{Kind: plan.OpSectionBreak, Location: &Location{At: "after", Of: &Target{Handle: "p5"}},
				Layout: &LayoutOp{SectionType: plan.SectionContinuous}},
			"insertSectionBreak@81", []string{`"sectionType":"CONTINUOUS"`}},
	}
	for _, tc := range cases {
		res, err := svc.Edit(ctx, layoutEdit(tc.op))
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if got := requestKinds(t, res.Requests); got != tc.kinds {
			t.Errorf("%s: kinds = %s", tc.name, got)
		}
		for _, w := range tc.want {
			if !strings.Contains(compact(res.Requests), w) {
				t.Errorf("%s: requests lack %s:\n%s", tc.name, w, res.Requests)
			}
		}
	}
}

func TestLayoutOpErrors(t *testing.T) {
	svc, _ := writable(t, false)
	ctx := context.Background()
	cases := []struct {
		name  string
		op    EditOp
		class string
		msg   string
	}{
		{"nothing to change", EditOp{Kind: plan.OpPageSetup, Target: &Target{}, Layout: &LayoutOp{}}, "invalid", "page changes nothing"},
		{"section with nothing to change", EditOp{Kind: plan.OpSectionStyle, Target: &Target{Handle: "p2"}, Layout: &LayoutOp{}}, "invalid", "section changes nothing"},
		{"unknown tab", EditOp{Kind: plan.OpPageSetup, Target: &Target{Tab: "Nope"}, Layout: &LayoutOp{Page: plan.PageSpec{Landscape: boolp(true)}}}, "not_found", "tab"},
		{"named style without a style", EditOp{Kind: plan.OpNamedStyle, Target: &Target{}, Layout: &LayoutOp{
			NamedStyle: plan.NamedStyleSpec{Text: plan.TextStyleSpec{Bold: boolp(true)}}}}, "invalid", "names the named style"},
	}
	for _, tc := range cases {
		_, err := svc.Edit(ctx, layoutEdit(tc.op))
		if classOf(err) != tc.class || !strings.Contains(messageOf(err), tc.msg) {
			t.Errorf("%s: got %v, want [%s] …%s…", tc.name, err, tc.class, tc.msg)
		}
	}
}

func TestObjectOpsResolve(t *testing.T) {
	svc, _ := writable(t, false)
	ctx := context.Background()
	// An inline image is replaced by id and deleted with the one index
	// its run occupies, so the guard can see what goes with it.
	res, err := svc.Edit(ctx, layoutEdit(EditOp{Kind: plan.OpReplaceImage, Target: &Target{},
		Object: &plan.ObjectParams{ID: "kix.img1", URL: "https://example.test/new.png", Crop: true}}))
	if err != nil || requestKinds(t, res.Requests) != "replaceImage" || !strings.Contains(compact(res.Requests), `"imageObjectId":"kix.img1"`) {
		t.Fatalf("replace inline image: %v %s", err, res.Requests)
	}
	res, err = svc.Edit(ctx, layoutEdit(EditOp{Kind: plan.OpDeleteObject, Target: &Target{}, Object: &plan.ObjectParams{ID: "kix.img1"}}))
	if err != nil || requestKinds(t, res.Requests) != "deleteContentRange[162,163)" {
		t.Fatalf("delete inline image: %v %+v", err, res)
	}
	if !strings.Contains(res.Changes[0].Description, "image kix.img1") {
		t.Errorf("description = %q", res.Changes[0].Description)
	}
	// A floating object has no range at all, so it goes by id.
	res, err = svc.Edit(ctx, layoutEdit(EditOp{Kind: plan.OpDeleteObject, Target: &Target{}, Object: &plan.ObjectParams{ID: "kix.pos1"}}))
	if err != nil || requestKinds(t, res.Requests) != "deletePositionedObject" || !strings.Contains(compact(res.Requests), `"objectId":"kix.pos1"`) {
		t.Fatalf("delete floating image: %v %s", err, res.Requests)
	}
	_, err = svc.Edit(ctx, layoutEdit(EditOp{Kind: plan.OpDeleteObject, Target: &Target{}, Object: &plan.ObjectParams{ID: "kix.nope"}}))
	if classOf(err) != "not_found" || !strings.Contains(messageOf(err), "no object kix.nope") {
		t.Errorf("unknown object: %v", err)
	}
	_, err = svc.Edit(ctx, layoutEdit(EditOp{Kind: plan.OpReplaceImage, Target: &Target{}, Object: &plan.ObjectParams{URL: "https://example.test/x.png"}}))
	if classOf(err) != "invalid" || !strings.Contains(messageOf(err), "name the object") {
		t.Errorf("object without an id: %v", err)
	}
}

func TestNamedRangeOps(t *testing.T) {
	svc, _ := writable(t, false)
	ctx := context.Background()
	res, err := svc.Edit(ctx, layoutEdit(EditOp{Kind: plan.OpCreateNamedRange, Target: &Target{Handle: "p5"},
		NamedRange: &plan.NamedRangeParams{Name: "intro"}}))
	if err != nil || requestKinds(t, res.Requests) != "createNamedRange[69,81)" || !strings.Contains(compact(res.Requests), `"name":"intro"`) {
		t.Fatalf("create: %v %s", err, res.Requests)
	}
	// Delete and replace name a range that exists, so a typo is refused
	// rather than silently doing nothing.
	res, err = svc.Edit(ctx, layoutEdit(EditOp{Kind: plan.OpReplaceNamedRange, Target: &Target{},
		NamedRange: &plan.NamedRangeParams{Name: "key finding", Text: "Third point"}}))
	if err != nil || requestKinds(t, res.Requests) != "replaceNamedRangeContent" || !strings.Contains(compact(res.Requests), `"tabIds":["t.0"]`) {
		t.Fatalf("replace: %v %s", err, res.Requests)
	}
	res, err = svc.Edit(ctx, layoutEdit(EditOp{Kind: plan.OpDeleteNamedRange, Target: &Target{},
		NamedRange: &plan.NamedRangeParams{ID: "kix.nr1"}}))
	if err != nil || requestKinds(t, res.Requests) != "deleteNamedRange" {
		t.Fatalf("delete: %v %s", err, res.Requests)
	}
	_, err = svc.Edit(ctx, layoutEdit(EditOp{Kind: plan.OpDeleteNamedRange, Target: &Target{},
		NamedRange: &plan.NamedRangeParams{Name: "nothing"}}))
	if classOf(err) != "not_found" || !strings.Contains(messageOf(err), `no named range "nothing"`) {
		t.Errorf("unknown range: %v", err)
	}
}

// A replace overwrites everything the name covers, so the guard sees
// what sits in those ranges; forgetting the name destroys nothing and
// is never guarded.
func TestReplaceNamedRangeIsGuarded(t *testing.T) {
	svc, _ := writable(t, true)
	ctx := context.Background()
	// The "figure" range covers the inline image, which a replace would
	// destroy without saying so.
	_, err := svc.Edit(ctx, layoutEdit(EditOp{Kind: plan.OpReplaceNamedRange, Target: &Target{},
		NamedRange: &plan.NamedRangeParams{Name: "figure", Text: "See the chart"}}))
	if classOf(err) != "blocked" || !strings.Contains(messageOf(err), "would destroy") {
		t.Fatalf("guard: %v", err)
	}
	req := layoutEdit(EditOp{Kind: plan.OpReplaceNamedRange, Target: &Target{},
		NamedRange: &plan.NamedRangeParams{Name: "figure", Text: "See the chart"}})
	req.Force = true
	if _, err := svc.Edit(ctx, req); err != nil {
		t.Errorf("forced: %v", err)
	}
	if _, err := svc.Edit(ctx, layoutEdit(EditOp{Kind: plan.OpDeleteNamedRange, Target: &Target{},
		NamedRange: &plan.NamedRangeParams{Name: "key finding"}})); err != nil {
		t.Errorf("delete is not guarded: %v", err)
	}
}

func TestNamedRangeTarget(t *testing.T) {
	svc, _ := writable(t, false)
	ctx := context.Background()
	// A named range is a target like any other, and it survives edits
	// that would renumber a handle.
	res, err := svc.Edit(ctx, EditRequest{Document: fixtureID, Mode: "direct", DryRun: true, Ops: []EditOp{
		{Kind: plan.OpReplace, Target: &Target{NamedRange: "key finding"}, Content: "Second thought"},
	}})
	if err != nil {
		t.Fatalf("replace by named range: %v", err)
	}
	if !strings.Contains(res.Text, `named range "key finding"`) {
		t.Errorf("description: %s", res.Text)
	}
	// The minimal diff works, so only the changed word is rewritten.
	if !strings.Contains(compact(res.Requests), `"text":"though"`) {
		t.Errorf("not a minimal diff: %s", res.Requests)
	}
	r, err := svc.ResolveTarget(fetched(t, svc), Target{NamedRange: "kix.nr1"})
	if err != nil || r.Text != "Second point" {
		t.Fatalf("by id: %v %+v", err, r)
	}
	if _, err := svc.ResolveTarget(fetched(t, svc), Target{NamedRange: "nope"}); classOf(err) != "not_found" {
		t.Errorf("unknown named range: %v", err)
	}
}

// The API refuses some of these requests in SUGGEST mode; the planner
// says so by name before anything is sent.
func TestNewOpsInSuggestMode(t *testing.T) {
	svc, _ := writable(t, true)
	ctx := context.Background()
	cases := []struct {
		name string
		op   EditOp
		kind string
	}{
		{"named range", EditOp{Kind: plan.OpCreateNamedRange, Target: &Target{Handle: "p5"},
			NamedRange: &plan.NamedRangeParams{Name: "intro"}}, "createNamedRange"},
		{"column width", EditOp{Kind: plan.OpStyleColumns, Table: &TableOp{Table: "tbl1", ColList: []int{1}, WidthPt: floatp(120)}},
			"updateTableColumnProperties"},
	}
	for _, tc := range cases {
		req := layoutEdit(tc.op)
		req.Mode = "suggest"
		_, err := svc.Edit(ctx, req)
		if classOf(err) != "invalid" || !strings.Contains(messageOf(err), tc.kind) || !strings.Contains(messageOf(err), "use mode direct") {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	// Page setup and row styles are not on that list, so they plan.
	req := layoutEdit(EditOp{Kind: plan.OpPageSetup, Target: &Target{}, Layout: &LayoutOp{Page: plan.PageSpec{Landscape: boolp(true)}}})
	req.Mode = "suggest"
	if _, err := svc.Edit(ctx, req); err != nil {
		t.Errorf("page setup in suggest mode: %v", err)
	}
}

// Comment mode posts a change as a comment on the passage it changes, so
// an op that changes a whole tab says it cannot and names a mode that
// can, rather than posting an empty comment somewhere.
func TestNewOpsInCommentMode(t *testing.T) {
	svc, _ := writable(t, false)
	ctx := context.Background()
	commentEdit := func(ops ...EditOp) EditRequest {
		req := layoutEdit(ops...)
		req.Mode, req.DryRun = "comment", true
		return req
	}
	for _, tc := range []struct {
		name string
		op   EditOp
	}{
		{"page", EditOp{Kind: plan.OpPageSetup, Target: &Target{}, Layout: &LayoutOp{Page: plan.PageSpec{Landscape: boolp(true)}}}},
		{"named style", EditOp{Kind: plan.OpNamedStyle, Target: &Target{}, Layout: &LayoutOp{
			NamedStyle: plan.NamedStyleSpec{Style: "HEADING_1", Text: plan.TextStyleSpec{Bold: boolp(true)}}}}},
		{"floating object", EditOp{Kind: plan.OpDeleteObject, Target: &Target{}, Object: &plan.ObjectParams{ID: "kix.pos1"}}},
	} {
		_, err := svc.Edit(ctx, commentEdit(tc.op))
		if err == nil || !strings.Contains(messageOf(err), "use mode direct") {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	// The ops that do change a passage propose one, with words a person
	// can act on.
	for _, tc := range []struct {
		name string
		op   EditOp
		want string
	}{
		{"section", EditOp{Kind: plan.OpSectionStyle, Target: &Target{Handle: "p5"}, Layout: &LayoutOp{
			Section: plan.SectionSpec{Columns: 2, ColumnSeparator: "BETWEEN_EACH_COLUMN"}}},
			"2 columns with a separating line"},
		{"name a range", EditOp{Kind: plan.OpCreateNamedRange, Target: &Target{Handle: "p5"},
			NamedRange: &plan.NamedRangeParams{Name: "intro"}}, "remember this passage as “intro”"},
		{"replace a range", EditOp{Kind: plan.OpReplaceNamedRange, Target: &Target{},
			NamedRange: &plan.NamedRangeParams{Name: "key finding", Text: "Third point"}}, "Third point"},
		{"delete an inline object", EditOp{Kind: plan.OpDeleteObject, Target: &Target{},
			Object: &plan.ObjectParams{ID: "kix.img1"}}, "Proposed deletion of the image kix.img1"},
	} {
		res, err := svc.Edit(ctx, commentEdit(tc.op))
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if len(res.Proposals) != 1 || !strings.Contains(res.Proposals[0].Content, tc.want) {
			t.Errorf("%s: proposals = %+v", tc.name, res.Proposals)
		}
	}
}
