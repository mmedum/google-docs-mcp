package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mmedum/google-docs-mcp/internal/config"
	"github.com/mmedum/google-docs-mcp/internal/doc/doctest"
	"github.com/mmedum/google-docs-mcp/internal/gapi"
	"github.com/mmedum/google-docs-mcp/internal/plan"
)

func writable(t *testing.T, preview bool) (*Service, *fakeAPI) {
	t.Helper()
	api := &fakeAPI{raw: doctest.RawFixture(t)}
	modes := []config.WriteMode{config.WriteDirect, config.WriteComment}
	if preview {
		modes = append([]config.WriteMode{config.WriteSuggest}, modes...)
	}
	svc := New(api, Options{Preview: preview, DefaultWriteMode: config.WriteDirect, WriteModes: modes, CacheTTL: time.Nanosecond})
	return svc, api
}

func fetched(t *testing.T, svc *Service) *Fetched {
	t.Helper()
	f, err := svc.Fetch(context.Background(), fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func kindsOf(t *testing.T, reqs []json.RawMessage) string {
	t.Helper()
	var out []string
	for _, r := range reqs {
		var m map[string]map[string]any
		if err := json.Unmarshal(r, &m); err != nil {
			t.Fatal(err)
		}
		for k, v := range m {
			s := k
			if rg, ok := v["range"].(map[string]any); ok {
				s += rangeOf(rg)
			}
			if loc, ok := v["location"].(map[string]any); ok {
				s += "@" + numStr(loc["index"])
			}
			out = append(out, s)
		}
	}
	return strings.Join(out, " ")
}

func rangeOf(rg map[string]any) string {
	return "[" + numStr(rg["startIndex"]) + "," + numStr(rg["endIndex"]) + ")"
}

func numStr(v any) string {
	f, _ := v.(float64)
	return strings.TrimSuffix(strings.TrimSuffix(json.Number(strings.TrimSpace(strings.Replace(strings.Replace(string(mustJSON(f)), ".0", "", 1), "e+", "", 1))).String(), ".0"), "")
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func TestResolveTargetText(t *testing.T) {
	svc, _ := writable(t, false)
	f := fetched(t, svc)
	r, err := svc.ResolveTarget(f, Target{Text: "Second point"})
	if err != nil || r.Start != 94 || r.End != 106 || r.Text != "Second point" || r.Block.Handle != "p7" || r.IsBlock {
		t.Fatalf("text target: %+v %v", r, err)
	}
	// Normalised quotes and case fallback.
	r, err = svc.ResolveTarget(f, Target{Text: `and "quoted"`})
	if err != nil || r.Block.Handle != "p13" || r.Text != "and “quoted”" {
		t.Fatalf("normalised: %+v %v", r, err)
	}
	r, err = svc.ResolveTarget(f, Target{Text: "REVENUE GREW"})
	if err != nil || r.Start != 29 || r.End != 41 {
		t.Fatalf("case fallback: %+v %v", r, err)
	}
	// Suggested text is part of the inline index space.
	r, err = svc.ResolveTarget(f, Target{Text: "a lotsubstantially in"})
	if err != nil || r.Start != 42 || r.End != 63 {
		t.Fatalf("inline suggestion span: %+v %v", r, err)
	}
	_, err = svc.ResolveTarget(f, Target{Text: "point"})
	var se *Error
	if !errors.As(err, &se) || se.Class != "ambiguous" || !strings.Contains(se.Message, "p5, p6, p7") {
		t.Fatalf("ambiguous: %v", err)
	}
	r, err = svc.ResolveTarget(f, Target{Text: "point", Occurrence: 3})
	if err != nil || r.Block.Handle != "p7" {
		t.Fatalf("occurrence: %+v %v", r, err)
	}
	r, err = svc.ResolveTarget(f, Target{Text: "point", Within: "p5"})
	if err != nil || r.Block.Handle != "p5" {
		t.Fatalf("within handle: %+v %v", r, err)
	}
	r, err = svc.ResolveTarget(f, Target{Text: "Step", Within: "heading:Details", Occurrence: 1})
	if err != nil || r.Block.Handle != "p9" {
		t.Fatalf("within heading: %+v %v", r, err)
	}
	r, err = svc.ResolveTarget(f, Target{Text: "Alpha"})
	if err != nil || r.Block.Handle != "tbl1:r2c1/p1" || r.Start != 148 {
		t.Fatalf("cell text: %+v %v", r, err)
	}
	for _, tc := range []struct {
		target Target
		class  string
	}{
		{Target{Text: "nowhere to be found"}, "not_found"},
		{Target{Text: "point", Occurrence: 9}, "not_found"},
		{Target{}, "invalid"},
		{Target{Text: "x", Handle: "p1"}, "invalid"},
		{Target{Text: "   "}, "invalid"},
		{Target{Text: "x", Within: "p99"}, "not_found"},
		{Target{Text: "x", Tab: "zzz"}, "not_found"},
	} {
		_, err := svc.ResolveTarget(f, tc.target)
		if !errors.As(err, &se) || se.Class != tc.class {
			t.Errorf("%+v: got %v, want %s", tc.target, err, tc.class)
		}
	}
}

func TestResolveTargetStructural(t *testing.T) {
	svc, _ := writable(t, false)
	f := fetched(t, svc)
	r, err := svc.ResolveTarget(f, Target{Handle: "p5"})
	if err != nil || !r.IsBlock || r.Start != 69 || r.End != 81 || r.Text != "First point" || r.Block.Handle != "p5" {
		t.Fatalf("handle: %+v %v", r, err)
	}
	r, err = svc.ResolveTarget(f, Target{From: "p5", To: "p7"})
	if err != nil || r.Start != 69 || r.End != 107 || len(r.Blocks) != 3 || r.Block != nil {
		t.Fatalf("range: %+v %v", r, err)
	}
	r, err = svc.ResolveTarget(f, Target{HeadingID: "h.det"})
	if err != nil || r.Start != 107 || r.End != 184 || !r.IsBlock {
		t.Fatalf("section: %+v %v", r, err)
	}
	no := false
	r, err = svc.ResolveTarget(f, Target{Heading: "Details", IncludeHeading: &no})
	if err != nil || r.Start != 115 || r.End != 184 {
		t.Fatalf("section body: %+v %v", r, err)
	}
	r, err = svc.ResolveTarget(f, Target{Cell: "tbl1:r2c1"})
	if err != nil || r.Start != 148 || r.End != 153 || r.IsBlock || r.Text != "Alpha" {
		t.Fatalf("cell: %+v %v", r, err)
	}
	r, err = svc.ResolveTarget(f, Target{Segment: "header", Handle: "header1/p1"})
	if err != nil || r.Segment.Label() != "header1" || r.Start != 0 {
		t.Fatalf("header block: %+v %v", r, err)
	}
	var se *Error
	if _, err := svc.ResolveTarget(f, Target{Cell: "tbl9:r1c1"}); !errors.As(err, &se) || se.Class != "not_found" {
		t.Fatalf("missing cell: %v", err)
	}
	if _, err := svc.ResolveTarget(f, Target{From: "p7", To: "p5"}); !errors.As(err, &se) || se.Class != "invalid" {
		t.Fatalf("reversed: %v", err)
	}
	if _, err := svc.ResolveTarget(f, Target{HeadingID: "h.nope"}); !errors.As(err, &se) || se.Class != "not_found" {
		t.Fatalf("missing heading: %v", err)
	}
}

func TestHandleMemoryStaleness(t *testing.T) {
	svc, _ := writable(t, false)
	f := fetched(t, svc)
	// Pretend the last read was an older revision where p5 said something else.
	svc.mu.Lock()
	mem := svc.handles[fixtureID]
	mem.RevisionID = "rev-0000"
	mem.Text["p5"] = "Vanished paragraph"
	mem.Text["p9"] = "Second point" // p9's old text now lives in p7: relocate
	svc.handles[fixtureID] = mem
	svc.mu.Unlock()
	var se *Error
	if _, err := svc.ResolveTarget(f, Target{Handle: "p5"}); !errors.As(err, &se) || se.Class != "stale" {
		t.Fatalf("stale: %v", err)
	}
	r, err := svc.ResolveTarget(f, Target{Handle: "p9"})
	if err != nil || r.Block.Handle != "p7" {
		t.Fatalf("relocated: %+v %v", r, err)
	}
	r, err = svc.ResolveTarget(f, Target{Handle: "p2"})
	if err != nil || r.Block.Handle != "p2" {
		t.Fatalf("unchanged block should resolve: %+v %v", r, err)
	}
	svc.mu.Lock()
	svc.handles[fixtureID].Text["p3"] = "Step two"
	svc.mu.Unlock()
	if r, err := svc.ResolveTarget(f, Target{Handle: "p3"}); err != nil || r.Block.Handle != "p10" {
		t.Fatalf("moved text should relocate to p10: %+v %v", r, err)
	}
	if _, err := svc.ResolveTarget(f, Target{Handle: "p42"}); !errors.As(err, &se) || se.Class != "not_found" {
		t.Fatalf("unknown handle: %v", err)
	}
}

func TestResolveLocation(t *testing.T) {
	svc, _ := writable(t, false)
	f := fetched(t, svc)
	cases := []struct {
		name  string
		loc   Location
		index int64
		atEnd bool
		inl   bool
		class string
	}{
		{"end of body", Location{At: "end"}, 225, true, false, ""},
		{"start of body", Location{At: "start"}, 1, false, false, ""},
		{"default is end", Location{}, 225, true, false, ""},
		{"after heading", Location{At: "after", Of: &Target{Handle: "p2"}}, 29, false, false, ""},
		{"before block", Location{At: "before", Of: &Target{Handle: "p5"}}, 69, false, false, ""},
		{"after last block", Location{At: "after", Of: &Target{Handle: "p14"}}, 225, true, false, ""},
		{"before table", Location{At: "before", Of: &Target{Handle: "tbl1"}}, 132, true, false, ""},
		{"after table", Location{At: "after", Of: &Target{Handle: "tbl1"}}, 158, false, false, ""},
		{"after section", Location{At: "after", Of: &Target{HeadingID: "h.det"}}, 184, false, false, ""},
		{"after text", Location{At: "after", Of: &Target{Text: "Second point"}}, 106, false, true, ""},
		{"before text", Location{At: "before", Of: &Target{Text: "Second point"}}, 94, false, true, ""},
		{"start of block", Location{At: "start", Of: &Target{Handle: "p5"}}, 69, false, true, ""},
		{"header end", Location{At: "end", Of: &Target{Segment: "header"}}, 18, true, false, ""},
		{"bad at", Location{At: "sideways", Of: &Target{Handle: "p5"}}, 0, false, false, "invalid"},
		{"before needs target", Location{At: "before"}, 0, false, false, "invalid"},
		{"missing target", Location{At: "after", Of: &Target{Handle: "p99"}}, 0, false, false, "not_found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ip, err := svc.resolveLocation(f, tc.loc)
			if tc.class != "" {
				var se *Error
				if !errors.As(err, &se) || se.Class != tc.class {
					t.Fatalf("got %v, want %s", err, tc.class)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if ip.index != tc.index || ip.atEnd != tc.atEnd || ip.inline != tc.inl {
				t.Fatalf("got index %d atEnd %v inline %v (%s)", ip.index, ip.atEnd, ip.inline, ip.description)
			}
			if ip.anchor == nil || ip.description == "" {
				t.Fatal("anchor/description missing")
			}
		})
	}
	ip, _ := svc.resolveLocation(f, Location{At: "after", Of: &Target{Handle: "p5"}})
	if !ip.nearBullet {
		t.Fatal("after a bullet should be near a bullet")
	}
}

func TestEditDirect(t *testing.T) {
	svc, api := writable(t, false)
	ctx := context.Background()
	res, err := svc.Edit(ctx, EditRequest{Document: fixtureID, Ops: []EditOp{
		{Kind: plan.OpReplace, Target: &Target{Text: "Second point"}, Content: "Second item"},
		{Kind: plan.OpInsert, Location: &Location{At: "after", Of: &Target{Handle: "p2"}}, Content: "Intro **bold**"},
		{Kind: plan.OpDelete, Target: &Target{Handle: "p13"}},
		{Kind: plan.OpAppend, Content: "- tail item"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(api.batches) != 1 {
		t.Fatalf("batches = %d", len(api.batches))
	}
	b := api.batches[0]
	if b.WriteControl == nil || b.WriteControl.RequiredRevisionID != "rev-0001" || b.WriteControl.WriteMode != "" {
		t.Fatalf("write control = %+v", b.WriteControl)
	}
	got := kindsOf(t, b.Requests)
	// Descending: append (225), delete p13 (192..213), replace at 101, insert at 29.
	for _, want := range []string{"insertText@225 updateTextStyle[226,235) updateParagraphStyle[226,235) createParagraphBullets[226,235) deleteContentRange[192,213) deleteContentRange[101,106) insertText@101 insertText@29 "} {
		if !strings.HasPrefix(got, want) {
			t.Fatalf("requests:\n got  %s\n want prefix %s", got, want)
		}
	}
	if !strings.HasSuffix(got, "updateTextStyle[35,39)") {
		t.Fatalf("bold run of the inserted paragraph missing: %s", got)
	}
	if res.Applied != 4 || res.RevisionID != "rev-0001" || len(res.Changes) != 4 || !res.Changes[0].Minimal || res.Mode != "direct" {
		t.Fatalf("result = %+v", res)
	}
	if res.Preview == "" || !strings.Contains(res.Preview, "[p") {
		t.Fatalf("preview missing: %q", res.Preview)
	}
}

func TestEditSuggestAndModes(t *testing.T) {
	svc, api := writable(t, true)
	ctx := context.Background()
	api.replies = []string{`{"replies":[{}],"writeControl":{"requiredRevisionId":"rev-0002"},"suggestionResponses":[{"createdSuggestionIds":["suggest.a"]}]}`}
	res, err := svc.Edit(ctx, EditRequest{Document: fixtureID, Mode: "suggest", Ops: []EditOp{{Kind: plan.OpDelete, Target: &Target{Handle: "p3"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if api.batches[0].WriteControl.WriteMode != "SUGGEST" || len(res.SuggestionIDs) != 1 || res.SuggestionIDs[0] != "suggest.a" {
		t.Fatalf("suggest: %+v %+v", api.batches[0].WriteControl, res)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "suggestion") {
		t.Fatalf("guard warning expected in suggest mode: %v", res.Warnings)
	}
	// Default mode comes from options; unknown modes and suggest without preview fail.
	plain, _ := writable(t, false)
	var se *Error
	if _, err := plain.Edit(ctx, EditRequest{Document: fixtureID, Mode: "suggest", Ops: []EditOp{{Kind: plan.OpDelete, Target: &Target{Handle: "p5"}}}}); !errors.As(err, &se) || se.Class != "unavailable" {
		t.Fatalf("suggest without preview: %v", err)
	}
	if _, err := plain.Edit(ctx, EditRequest{Document: fixtureID, Mode: "yolo", Ops: []EditOp{{Kind: plan.OpDelete, Target: &Target{Handle: "p5"}}}}); !errors.As(err, &se) || se.Class != "invalid" {
		t.Fatalf("bad mode: %v", err)
	}
	ro := New(&fakeAPI{raw: doctest.RawFixture(t)}, Options{ReadOnly: true})
	if _, err := ro.Edit(ctx, EditRequest{Document: fixtureID, Ops: []EditOp{{Kind: plan.OpDelete, Target: &Target{Handle: "p5"}}}}); !errors.As(err, &se) || se.Class != "forbidden" {
		t.Fatalf("read-only: %v", err)
	}
	if _, err := plain.Edit(ctx, EditRequest{Document: fixtureID}); !errors.As(err, &se) || se.Class != "invalid" {
		t.Fatalf("empty ops: %v", err)
	}
	if _, err := plain.Edit(ctx, EditRequest{Document: fixtureID, ExpectRevision: "rev-9", Ops: []EditOp{{Kind: plan.OpDelete, Target: &Target{Handle: "p5"}}}}); !errors.As(err, &se) || se.Class != "conflict" {
		t.Fatalf("expect revision: %v", err)
	}
}

func TestGuardBlocksDirect(t *testing.T) {
	svc, api := writable(t, false)
	ctx := context.Background()
	api.comments = []*gapi.DriveComment{{ID: "dc1", Content: "hmm", QuotedFileContent: &gapi.QuotedText{Value: "Second point"}}}
	var se *Error
	_, err := svc.Edit(ctx, EditRequest{Document: fixtureID, Ops: []EditOp{{Kind: plan.OpDelete, Target: &Target{Handle: "p7"}}}})
	if !errors.As(err, &se) || se.Class != "blocked" || !strings.Contains(se.Message, "1 comment (dc1)") || !strings.Contains(se.Message, "force: true") {
		t.Fatalf("guard: %v", err)
	}
	if len(api.batches) != 0 {
		t.Fatal("blocked edit must not reach Google")
	}
	res, err := svc.Edit(ctx, EditRequest{Document: fixtureID, Force: true, Ops: []EditOp{{Kind: plan.OpDelete, Target: &Target{Handle: "p7"}}}})
	if err != nil || len(api.batches) != 1 || len(res.Warnings) != 1 {
		t.Fatalf("forced: %+v %v", res, err)
	}
	// A suggestion inside p3 also blocks.
	_, err = svc.Edit(ctx, EditRequest{Document: fixtureID, Ops: []EditOp{{Kind: plan.OpReplace, Target: &Target{Handle: "p3"}, Content: "x"}}})
	if !errors.As(err, &se) || se.Class != "blocked" || !strings.Contains(se.Message, "suggestion (s1)") {
		t.Fatalf("suggestion guard: %v", err)
	}
	// Comment lookup failure degrades to a warning in the log, not an error.
	api.listErr = &gapi.APIError{Status: 403, Message: "no"}
	if _, err := svc.Edit(ctx, EditRequest{Document: fixtureID, Ops: []EditOp{{Kind: plan.OpDelete, Target: &Target{Handle: "p5"}}}}); err != nil {
		t.Fatalf("comment lookup failure should not block: %v", err)
	}
}

func TestCommentMode(t *testing.T) {
	// Preview: anchored insertComment requests.
	svc, api := writable(t, true)
	ctx := context.Background()
	api.replies = []string{`{"replies":[{"insertComment":{"commentThread":{"commentId":"cm1"}}},{"insertComment":{"commentThread":{"commentId":"cm2"}}}],"writeControl":{"requiredRevisionId":"rev-0002"}}`}
	res, err := svc.Edit(ctx, EditRequest{Document: fixtureID, Mode: "comment", Ops: []EditOp{
		{Kind: plan.OpReplace, Target: &Target{Text: "Second point"}, Content: "Second item"},
		{Kind: plan.OpInsert, Location: &Location{At: "after", Of: &Target{Handle: "p2"}}, Content: "Intro"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := kindsOf(t, api.batches[0].Requests); got != "insertComment[94,106) insertComment[18,29)" {
		t.Fatalf("comment requests: %s", got)
	}
	if len(res.CommentIDs) != 2 || res.CommentIDs[1] != "cm2" || res.Mode != "comment" {
		t.Fatalf("result: %+v", res)
	}
	var body map[string]map[string]any
	_ = json.Unmarshal(api.batches[0].Requests[0], &body)
	if !strings.HasPrefix(body["insertComment"]["content"].(string), "Proposed change to “Second point”") {
		t.Fatalf("content: %v", body["insertComment"]["content"])
	}
	// Without preview: Drive comments with the quote, plus a warning.
	plain, papi := writable(t, false)
	res, err = plain.Edit(ctx, EditRequest{Document: fixtureID, Mode: "comment", Ops: []EditOp{{Kind: plan.OpDelete, Target: &Target{Handle: "p5"}}}})
	if err != nil || len(papi.comments) != 1 || papi.comments[0].QuotedFileContent.Value != "First point" || len(res.CommentIDs) != 1 {
		t.Fatalf("drive comment: %+v %+v %v", papi.comments, res, err)
	}
	if len(papi.batches) != 0 || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "not pinned") {
		t.Fatalf("drive path: %+v", res)
	}
}

func TestDryRunAndConflictRetry(t *testing.T) {
	svc, api := writable(t, false)
	ctx := context.Background()
	res, err := svc.Edit(ctx, EditRequest{Document: fixtureID, DryRun: true, Ops: []EditOp{{Kind: plan.OpReplace, Target: &Target{Handle: "p5"}, Content: "First **item**"}}})
	if err != nil || len(api.batches) != 0 || !res.DryRun || len(res.Requests) == 0 || !strings.Contains(string(res.Requests), "deleteContentRange") || !strings.Contains(res.Preview, "[p5]") {
		t.Fatalf("dry run: %+v %v", res, err)
	}
	api.batchErrs = []error{&gapi.APIError{Status: 400, Message: "the provided revision id does not match"}}
	res, err = svc.Edit(ctx, EditRequest{Document: fixtureID, Ops: []EditOp{{Kind: plan.OpDelete, Target: &Target{Handle: "p5"}}}})
	if err != nil || len(api.batches) != 2 || res.Applied != 1 {
		t.Fatalf("conflict retry: batches=%d %v", len(api.batches), err)
	}
	api.batches = nil
	api.batchErrs = []error{&gapi.APIError{Status: 400, Message: "revision mismatch"}, &gapi.APIError{Status: 400, Message: "revision mismatch"}}
	var se *Error
	if _, err := svc.Edit(ctx, EditRequest{Document: fixtureID, Ops: []EditOp{{Kind: plan.OpDelete, Target: &Target{Handle: "p5"}}}}); !errors.As(err, &se) || se.Class != "conflict" {
		t.Fatalf("double conflict: %v", err)
	}
	api.batches = nil
	api.batchErrs = []error{&gapi.APIError{Status: 400, Message: "Invalid requests[0].insertText"}}
	if _, err := svc.Edit(ctx, EditRequest{Document: fixtureID, Ops: []EditOp{{Kind: plan.OpDelete, Target: &Target{Handle: "p5"}}}}); !errors.As(err, &se) || se.Class != "invalid" || !strings.Contains(se.Message, "nothing was applied") {
		t.Fatalf("rejected batch: %v", err)
	}
}

func TestFollowupsAndValidation(t *testing.T) {
	svc, api := writable(t, false)
	ctx := context.Background()
	api.replies = []string{`{"replies":[{"createHeader":{"headerId":"kix.newh"}}],"writeControl":{"requiredRevisionId":"rev-0002"}}`}
	res, err := svc.Edit(ctx, EditRequest{Document: fixtureID, Ops: []EditOp{{Kind: plan.OpCreateHeader, Target: &Target{Tab: "Notes"}, Content: "Draft **v2**"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(api.batches) != 2 {
		t.Fatalf("batches = %d", len(api.batches))
	}
	if got := kindsOf(t, api.batches[1].Requests); got != "insertText@0 updateTextStyle[0,8) updateParagraphStyle[0,8) updateTextStyle[6,8)" {
		t.Fatalf("second batch: %s", got)
	}
	var m map[string]map[string]any
	_ = json.Unmarshal(api.batches[1].Requests[0], &m)
	if m["insertText"]["location"].(map[string]any)["segmentId"] != "kix.newh" || api.batches[1].WriteControl.RequiredRevisionID != "rev-0002" {
		t.Fatalf("followup targeting: %v %+v", m, api.batches[1].WriteControl)
	}
	if res.Applied != 1 {
		t.Fatalf("result: %+v", res)
	}
	var se *Error
	cases := []struct {
		ops   []EditOp
		class string
	}{
		{[]EditOp{{Kind: plan.OpCreateHeader, Content: "x"}}, "invalid"}, // tab 1 already has one
		{[]EditOp{{Kind: plan.OpInsert, Content: "x"}}, "invalid"},       // no location
		{[]EditOp{{Kind: plan.OpReplace, Content: "x"}}, "invalid"},      // no target
		{[]EditOp{{Kind: plan.OpReplace, Target: &Target{Handle: "p5"}, Content: "> quote"}}, "unsupported"},
		{[]EditOp{{Kind: plan.OpKind("teleport"), Target: &Target{Handle: "p5"}}}, "invalid"},
		{[]EditOp{{Kind: plan.OpReplaceAll, Find: "a", Replace: "b", Target: &Target{Tab: "zzz"}}}, "not_found"},
		{[]EditOp{{Kind: plan.OpDelete, Target: &Target{From: "p5", To: "p7"}}, {Kind: plan.OpInsert, Location: &Location{At: "after", Of: &Target{Handle: "p5"}}, Content: "x"}}, "invalid"},
	}
	for _, tc := range cases {
		if _, err := svc.Edit(ctx, EditRequest{Document: fixtureID, Ops: tc.ops}); !errors.As(err, &se) || se.Class != tc.class {
			t.Errorf("%+v: got %v, want %s", tc.ops[0].Kind, err, tc.class)
		}
	}
	// A delete and an insert at the same index apply delete-first.
	api.batches = nil
	if _, err := svc.Edit(ctx, EditRequest{Document: fixtureID, Ops: []EditOp{
		{Kind: plan.OpInsert, Location: &Location{At: "start", Of: &Target{Handle: "p5"}}, Content: "x"},
		{Kind: plan.OpDelete, Target: &Target{Handle: "p5"}},
	}}); err != nil {
		t.Fatal(err)
	}
	if got := kindsOf(t, api.batches[0].Requests); !strings.HasPrefix(got, "deleteContentRange[69,81) insertText@69") {
		t.Fatalf("delete should precede the insert at the same index: %s", got)
	}
	// Plain-text content becomes one paragraph per line; replace_all always names the tab.
	api.batches = nil
	if _, err := svc.Edit(ctx, EditRequest{Document: fixtureID, Ops: []EditOp{
		{Kind: plan.OpAppend, Content: "# not a heading\nsecond line", ContentFormat: "text"},
		{Kind: plan.OpReplaceAll, Find: "Q3", Replace: "Q4", MatchCase: true},
	}}); err != nil {
		t.Fatal(err)
	}
	got := kindsOf(t, api.batches[0].Requests)
	if !strings.HasSuffix(got, "replaceAllText") || !strings.Contains(string(api.batches[0].Requests[0]), `\n# not a heading\nsecond line`) {
		t.Fatalf("text content / replace_all: %s\n%s", got, api.batches[0].Requests[0])
	}
	if !strings.Contains(string(api.batches[0].Requests[len(api.batches[0].Requests)-1]), `"tabIds":["t.0"]`) {
		t.Fatalf("replace_all must name the tab: %s", api.batches[0].Requests[len(api.batches[0].Requests)-1])
	}
}

func TestFormatOps(t *testing.T) {
	svc, api := writable(t, false)
	ctx := context.Background()
	bold := true
	res, err := svc.Edit(ctx, EditRequest{Document: fixtureID, Ops: []EditOp{
		{Kind: plan.OpTextStyle, Target: &Target{Text: "Revenue"}, Text: plan.TextStyleSpec{Bold: &bold, Foreground: "#ff0000"}},
		{Kind: plan.OpParagraphStyle, Target: &Target{Handle: "p11"}, Para: plan.ParagraphStyleSpec{NamedStyle: "HEADING_3"}},
		{Kind: plan.OpBullets, Target: &Target{From: "p9", To: "p10"}, Bullets: "none"},
		{Kind: plan.OpClearFormatting, Target: &Target{Handle: "p14"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := kindsOf(t, api.batches[0].Requests); got != "updateTextStyle[29,36) updateParagraphStyle[158,184) deleteParagraphBullets[115,133) updateTextStyle[213,226)" {
		t.Fatalf("format requests: %s", got)
	}
	if res.Applied != 4 {
		t.Fatalf("applied = %d", res.Applied)
	}
}

func TestFindSearchCreateExportSuggestions(t *testing.T) {
	svc, api := writable(t, true)
	ctx := context.Background()
	fr, err := svc.Find(ctx, FindRequest{Document: fixtureID, Query: "point"})
	if err != nil || fr.Total != 3 || fr.Matches[0].Handle != "p5" || fr.Matches[1].Handle != "p6" || !strings.Contains(fr.Matches[2].Context, "«point»") {
		t.Fatalf("find: %+v %v", fr, err)
	}
	fr, err = svc.Find(ctx, FindRequest{Document: fixtureID, Query: `Q\d`, Regex: true, Limit: 1})
	if err != nil || fr.Total != 1 || fr.Matches[0].Match != "Q3" || fr.Matches[0].Offset != 35 {
		t.Fatalf("regex find: %+v %v", fr, err)
	}
	fr, err = svc.Find(ctx, FindRequest{Document: fixtureID, Query: "e", Limit: 3})
	if err != nil || !fr.Truncated || len(fr.Matches) != 3 || fr.Total <= 3 {
		t.Fatalf("truncated find: %+v %v", fr, err)
	}
	var se *Error
	if _, err := svc.Find(ctx, FindRequest{Document: fixtureID, Query: "(", Regex: true}); !errors.As(err, &se) || se.Class != "invalid" {
		t.Fatalf("bad regex: %v", err)
	}
	if _, err := svc.Find(ctx, FindRequest{Document: fixtureID, Query: " "}); !errors.As(err, &se) || se.Class != "invalid" {
		t.Fatalf("empty query: %v", err)
	}

	sr, err := svc.Search(ctx, SearchRequest{Title: "Report", Query: "revenue", ModifiedAfter: "2026-01-01", Owner: "o@example.test", Limit: 5})
	if err != nil || len(sr.Hits) != 1 || sr.NextPageToken != "next" || sr.Hits[0].Title != "Doc One" {
		t.Fatalf("search: %+v %v", sr, err)
	}
	if api.searchQ != "mimeType = 'application/vnd.google-apps.document' and trashed = false and name contains 'Report' and fullText contains 'revenue' and modifiedTime > '2026-01-01T00:00:00Z' and 'o@example.test' in owners" {
		t.Fatalf("drive query: %s", api.searchQ)
	}
	if _, err := svc.Search(ctx, SearchRequest{ModifiedAfter: "yesterday"}); !errors.As(err, &se) || se.Class != "invalid" {
		t.Fatalf("bad date: %v", err)
	}

	cr, err := svc.Create(ctx, CreateRequest{Title: "  New Doc ", Content: "# Hello\n\nBody"})
	if err != nil || cr.Title != "New Doc" || len(api.created) != 1 || len(api.batches) != 1 || cr.URL == "" {
		t.Fatalf("create: %+v %v (batches %d)", cr, err, len(api.batches))
	}
	if _, err := svc.Create(ctx, CreateRequest{Title: " "}); !errors.As(err, &se) || se.Class != "invalid" {
		t.Fatalf("empty title: %v", err)
	}

	ex, err := svc.Export(ctx, ExportRequest{Document: fixtureID, Format: "markdown", MaxChars: 20})
	if err != nil || !ex.Inline || !ex.Truncated || len(ex.Text) > 20 || api.exported != "text/markdown" {
		t.Fatalf("export md: %+v %v", ex, err)
	}
	if _, err := svc.Export(ctx, ExportRequest{Document: fixtureID, Format: "pdf"}); !errors.As(err, &se) || se.Class != "unavailable" {
		t.Fatalf("pdf without dir: %v", err)
	}
	svc.opts.ExportDir = t.TempDir()
	ex, err = svc.Export(ctx, ExportRequest{Document: fixtureID, Format: "pdf"})
	if err != nil || ex.Inline || ex.Path == "" || filepath.Dir(ex.Path) != svc.opts.ExportDir {
		t.Fatalf("export pdf: %+v %v", ex, err)
	}
	if data, err := os.ReadFile(ex.Path); err != nil || !strings.HasPrefix(string(data), "%PDF") {
		t.Fatalf("pdf file: %q %v", data, err)
	}
	if _, err := svc.Export(ctx, ExportRequest{Document: fixtureID, Format: "xls"}); !errors.As(err, &se) || se.Class != "invalid" {
		t.Fatalf("bad format: %v", err)
	}

	ls, err := svc.ListSuggestions(ctx, fixtureID)
	if err != nil || len(ls.Suggestions) != 1 || ls.Suggestions[0].ID != "s1" || ls.Suggestions[0].Kind != "replace" || ls.Suggestions[0].Deleted != "a lot" || ls.Suggestions[0].Inserted != "substantially" || ls.Suggestions[0].Handle != "p3" {
		t.Fatalf("suggestions: %+v %v", ls, err)
	}
	api.batches = nil
	rv, err := svc.Review(ctx, ReviewRequest{Document: fixtureID, Action: "reject", All: true})
	if err != nil || len(api.batches) != 1 || kindsOf(t, api.batches[0].Requests) != "rejectSuggestion" || rv.Remaining != 0 || rv.RevisionID != "rev-0002" {
		t.Fatalf("review: %+v %v", rv, err)
	}
	for _, tc := range []struct {
		req   ReviewRequest
		class string
	}{
		{ReviewRequest{Document: fixtureID, Action: "maybe", All: true}, "invalid"},
		{ReviewRequest{Document: fixtureID, Action: "accept"}, "invalid"},
		{ReviewRequest{Document: fixtureID, Action: "accept", IDs: []string{"s9"}}, "not_found"},
		{ReviewRequest{Document: fixtureID, Action: "accept", All: true, ExpectRevision: "old"}, "conflict"},
	} {
		if _, err := svc.Review(ctx, tc.req); !errors.As(err, &se) || se.Class != tc.class {
			t.Errorf("%+v: got %v, want %s", tc.req, err, tc.class)
		}
	}
	plain, _ := writable(t, false)
	if _, err := plain.Review(ctx, ReviewRequest{Document: fixtureID, Action: "accept", All: true}); !errors.As(err, &se) || se.Class != "unavailable" {
		t.Fatalf("review without preview: %v", err)
	}
}
