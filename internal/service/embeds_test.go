package service

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mmedum/google-docs-mcp/v2/internal/config"
	"github.com/mmedum/google-docs-mcp/v2/internal/gapi"
	"github.com/mmedum/google-docs-mcp/v2/internal/plan"
)

// simService is a service over a simulated body, read once so handles
// can be targeted.
func simService(t *testing.T, sim *simDoc) (*Service, *fakeAPI) {
	t.Helper()
	api := &fakeAPI{sim: sim}
	svc := New(api, Options{DefaultWriteMode: config.WriteDirect, CacheTTL: time.Nanosecond})
	if _, err := svc.Fetch(context.Background(), fixtureID); err != nil {
		t.Fatal(err)
	}
	return svc, api
}

// tableAndImage has a table on line 3 and an image on line 7.
const tableAndImage = "Lead\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n![logo](https://e.test/logo.png)\n\nTail"

func contentEdit(mode string, op EditOp) EditRequest {
	return EditRequest{Document: fixtureID, Mode: mode, Ops: []EditOp{op}}
}

func after(handle string) *Location { return &Location{At: "after", Of: &Target{Handle: handle}} }

// firstKinds names the first request of each batch sent.
func firstKinds(api *fakeAPI) string {
	var out []string
	for _, b := range api.batches {
		out = append(out, plan.Kind(b.Requests[0]))
	}
	return strings.Join(out, " ")
}

// The text lands first with an empty paragraph per table and image; then
// a round per slot, highest first: the image, the table and its fill, and
// the deletion of the empty paragraphs the table leaves.
func TestEmbedsPlacedInRounds(t *testing.T) {
	sim := newSim(t, "Title", "Body text.", "More.", "Last.")
	svc, api := simService(t, sim)
	res, err := svc.Edit(context.Background(), contentEdit("direct", EditOp{Kind: plan.OpInsert, Location: after("p2"), Content: tableAndImage}))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := sim.String(), "Title\nBody text.\nLead\n[a|b / 1|2]\n<img>\nTail\nMore.\nLast."; got != want {
		t.Fatalf("body:\n%s\nwant:\n%s", got, want)
	}
	if got := firstKinds(api); got != "insertText insertInlineImage insertTable insertText deleteContentRange" {
		t.Fatalf("batches: %s", got)
	}
	if !slices.Equal(sim.images, []string{"https://e.test/logo.png"}) {
		t.Fatalf("images: %v", sim.images)
	}
	if res.Applied != 1 || len(res.Changes) != 1 || !strings.HasSuffix(res.Changes[0].Description, ", then placed 1 table and 1 image") {
		t.Fatalf("changes: %d %+v", res.Applied, res.Changes)
	}
	// The rounds each shift blocks; the call says so once.
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "number of blocks changed") {
		t.Fatalf("warnings: %q", res.Warnings)
	}
	if res.RevisionID != "rev-5" || !strings.Contains(res.Preview, "Lead") {
		t.Fatalf("revision %s, preview:\n%s", res.RevisionID, res.Preview)
	}
}

// Where a slot sits decides what a table leaves: the paragraph before it
// keeps its text, the body keeps a paragraph at its end and between two
// tables, and a table first in the body sits behind an empty paragraph.
func TestEmbedsAtTheEdges(t *testing.T) {
	for _, tc := range []struct {
		name  string
		paras []string
		table bool // a one-cell table after the first paragraph
		op    EditOp
		want  string
	}{
		{"appended into the blank last paragraph, a table first and last", []string{"Title", ""}, false,
			EditOp{Kind: plan.OpAppend, Content: "| x |\n|---|\n| y |\n\nMiddle\n\n| z |\n|---|\n| w |"},
			"Title\n[x / y]\nMiddle\n[z / w]\n"},
		{"at the start of the body", []string{"First"}, false,
			EditOp{Kind: plan.OpInsert, Location: &Location{At: "start"}, Content: "| a |\n|---|\n| b |"},
			"\n[a / b]\nFirst"},
		{"before a table", []string{"Intro", "After"}, true,
			EditOp{Kind: plan.OpInsert, Location: &Location{At: "before", Of: &Target{Handle: "tbl1"}}, Content: "| n |\n|---|\n| m |"},
			"Intro\n[n / m]\n\n[q]\nAfter"},
		{"replacing a middle block", []string{"One", "Two", "Three"}, false,
			EditOp{Kind: plan.OpReplace, Target: &Target{Handle: "p2"}, Content: "| # r |\n|---|\n| s |"},
			"One\n[# r / s]\nThree"},
		{"replacing the last block with an image last", []string{"One", "Two"}, false,
			EditOp{Kind: plan.OpReplace, Target: &Target{Handle: "p2"}, Content: "Text\n\n![i](https://e.test/i.png)"},
			"One\nText\n<img>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sim := newSim(t, tc.paras...)
			if tc.table {
				sim.elems = slices.Insert(sim.elems, 1, &simElem{cells: [][]string{{"q"}}})
			}
			svc, _ := simService(t, sim)
			res, err := svc.Edit(context.Background(), contentEdit("direct", tc.op))
			if err != nil {
				t.Fatal(err)
			}
			if got := sim.String(); got != tc.want {
				t.Fatalf("body:\n%s\nwant:\n%s", got, tc.want)
			}
			for _, w := range res.Warnings {
				if !strings.Contains(w, "number of blocks changed") {
					t.Errorf("warning: %s", w)
				}
			}
		})
	}
}

// A table or image that cannot be placed is a warning naming its line;
// the text stays, and so does everything else that could be placed.
func TestEmbedFailuresAreReported(t *testing.T) {
	refused := &gapi.APIError{Status: 400, Message: "Invalid requests[0].insertInlineImage: There was a problem retrieving the image."}
	for _, tc := range []struct {
		name    string
		content string
		setup   func(*simDoc, *fakeAPI)
		want    string
		warns   []string
		placed  string
		unsaid  string // a warning that must not appear
	}{
		{"an image Google cannot fetch", tableAndImage,
			func(_ *simDoc, api *fakeAPI) { api.batchErrs = []error{nil, refused} },
			"Title\nLead\n[a|b / 1|2]\n\nTail\nLast.",
			[]string{"op 0: the text was written, but the image on line 7 of the content was not placed: ", "; its empty paragraph is still there"},
			", then placed 1 table", ""},
		{"a write by somebody else before the rounds", tableAndImage,
			func(sim *simDoc, _ *fakeAPI) {
				sim.onBatch = func(n int) {
					if n == 1 {
						sim.elems[3].para = "typed\n" // the image's slot
						sim.edited()
					}
				}
			},
			"Title\nLead\n\ntyped\nTail\nLast.",
			[]string{"op 0: the text was written, but somebody else changed the document before 1 table and 1 image could be placed, so they were not"},
			"", ""},
		{"a write by somebody else before the tidy", "| a |\n|---|\n| 1 |",
			func(sim *simDoc, _ *fakeAPI) {
				sim.onBatch = func(n int) {
					if n == 3 { // the fill
						sim.elems[2].para = "typed\n"
						sim.edited()
					}
				}
			},
			"Title\n[a / 1]\ntyped\n\nLast.",
			[]string{"op 0: the table on line 1 of the content went in, but the empty paragraphs after it were not removed: " +
				"somebody else changed the document in between"},
			", then placed 1 table", ""},
		// The checks below the revision: an edit Google did not report.
		{"a slot that is not where the text left it", tableAndImage,
			func(sim *simDoc, _ *fakeAPI) {
				sim.onBatch = func(n int) {
					if n == 1 {
						sim.elems[3].para = "typed\n"
					}
				}
			},
			"Title\nLead\n[a|b / 1|2]\ntyped\nTail\nLast.",
			[]string{"op 0: the text was written, but the image on line 7 of the content was not placed: its empty paragraph is no " +
				"longer where the text left it, so the document changed in between"},
			", then placed 1 table", "still there"},
		{"a paragraph after the table that is not empty", "| a |\n|---|\n| 1 |",
			func(sim *simDoc, _ *fakeAPI) {
				sim.onBatch = func(n int) {
					if n == 3 {
						sim.elems[2].para = "typed\n"
					}
				}
			},
			"Title\n[a / 1]\ntyped\n\nLast.",
			[]string{"op 0: the table on line 1 of the content went in, but the empty paragraphs after it were not removed: " +
				"what follows the table is not the empty paragraphs it leaves"},
			", then placed 1 table", ""},
		{"a table that is gone", "| a |\n|---|\n| 1 |",
			func(sim *simDoc, _ *fakeAPI) {
				sim.onBatch = func(n int) {
					if n == 3 {
						sim.elems = slices.Delete(sim.elems, 1, 2)
					}
				}
			},
			"Title\n\n\nLast.",
			[]string{"op 0: the table on line 1 of the content went in, but the empty paragraphs after it were not removed: " +
				"the table could not be found again"},
			", then placed 1 table", ""},
		{"reads that fail after a table goes in", "| a |\n|---|\n| 1 |",
			func(sim *simDoc, api *fakeAPI) {
				sim.onBatch = func(n int) {
					if n == 2 {
						api.getErr = &gapi.APIError{Status: 500, Message: "backend error"}
					}
				}
			},
			"Title\n[ / ]\n\n\nLast.",
			[]string{"op 0: the table on line 1 of the content went in, but the empty paragraphs after it were not removed: re-reading the document failed"},
			", then placed 1 table", ""},
		{"a read that fails after the text", tableAndImage,
			func(sim *simDoc, api *fakeAPI) {
				sim.onBatch = func(int) { api.getErr = &gapi.APIError{Status: 500, Message: "backend error"} }
			},
			"Title\nLead\n\n\nTail\nLast.",
			[]string{"op 0: the text was written, but re-reading the document to place 1 table and 1 image failed: ", "; their empty paragraphs are still there"},
			"", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sim := newSim(t, "Title", "Last.")
			svc, api := simService(t, sim)
			tc.setup(sim, api)
			res, err := svc.Edit(context.Background(), contentEdit("direct", EditOp{Kind: plan.OpInsert, Location: after("p1"), Content: tc.content}))
			if err != nil {
				t.Fatal(err)
			}
			if got := sim.String(); got != tc.want {
				t.Fatalf("body:\n%s\nwant:\n%s", got, tc.want)
			}
			warnings := strings.Join(res.Warnings, "\n")
			for _, w := range tc.warns {
				if !strings.Contains(warnings, w) {
					t.Errorf("warnings lack %q:\n%s", w, warnings)
				}
			}
			if tc.unsaid != "" && strings.Contains(warnings, tc.unsaid) {
				t.Errorf("warnings say %q:\n%s", tc.unsaid, warnings)
			}
			if desc := res.Changes[0].Description; tc.placed == "" && strings.Contains(desc, "placed") || !strings.HasSuffix(desc, tc.placed) {
				t.Errorf("description %q, want it to end %q", desc, tc.placed)
			}
		})
	}
}

// What the rounds cannot do is refused before anything is written.
func TestEmbedRefusals(t *testing.T) {
	image := "![i](https://e.test/i.png)"
	for _, tc := range []struct {
		name  string
		mode  string // default direct
		ops   []EditOp
		class string
		msg   string
	}{
		{"with another op", "", []EditOp{{Kind: plan.OpAppend, Content: image}, {Kind: plan.OpAppend, Content: "more"}},
			"invalid", "op 0: content with a table or image must be the only op in its call"},
		{"with another op, as comments", "comment", []EditOp{{Kind: plan.OpAppend, Content: image}, {Kind: plan.OpAppend, Content: "more"}},
			"invalid", "op 0: content with a table or image must be the only op in its call"},
		{"in a footnote", "", []EditOp{{Kind: plan.OpFootnote, Location: after("p9"), Content: image}},
			"unsupported", "op 0: a table or image in content goes in an insert, append or replace"},
		{"eleven images", "", []EditOp{{Kind: plan.OpAppend, Content: strings.Repeat(image+"\n", 11)}},
			"invalid", "op 0: content holds 11 tables and images; at most 10 go in one call"},
		{"in a header", "", []EditOp{{Kind: plan.OpAppend, Location: &Location{At: "end", Of: &Target{Segment: "header"}}, Content: image}},
			"unsupported", "unsupported markdown: image at line 1; a table or image in content goes in an insert, append or replace in the body, between blocks"},
		{"in a table cell", "", []EditOp{{Kind: plan.OpInsert, Location: &Location{At: "end", Of: &Target{Cell: "tbl1:r2c1"}}, Content: image}},
			"unsupported", "unsupported markdown: image at line 1; a table or image in content goes in an insert, append or replace in the body, between blocks"},
		{"inside a paragraph", "", []EditOp{{Kind: plan.OpInsert, Location: &Location{At: "after", Of: &Target{Text: "Step", Occurrence: 1}}, Content: image}},
			"unsupported", "unsupported markdown: image at line 1; a table or image in content goes in an insert, append or replace in the body, between blocks"},
		{"a table too wide", "", []EditOp{{Kind: plan.OpAppend, Content: "|" + strings.Repeat(" x |", 21) + "\n|" + strings.Repeat("---|", 21)}},
			"unsupported", "1×21 table at line 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, api := writable(t, false)
			mode := tc.mode
			if mode == "" {
				mode = "direct"
			}
			_, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Mode: mode, Ops: tc.ops})
			if classOf(err) != tc.class || !strings.Contains(messageOf(err), tc.msg) {
				t.Fatalf("got [%s] %s, want [%s] %s", classOf(err), messageOf(err), tc.class, tc.msg)
			}
			if len(api.batches) != 0 || len(api.comments) != 0 {
				t.Fatalf("%d batch(es) sent, %d comment(s) posted", len(api.batches), len(api.comments))
			}
		})
	}
}

// A dry run plans the first batch, names the rounds after it, and sends
// nothing.
func TestEmbedsDryRun(t *testing.T) {
	sim := newSim(t, "Title", "Last.")
	svc, api := simService(t, sim)
	req := contentEdit("direct", EditOp{Kind: plan.OpInsert, Location: after("p1"), Content: tableAndImage})
	req.DryRun = true
	res, err := svc.Edit(context.Background(), req)
	if err != nil || len(api.batches) != 0 || sim.String() != "Title\nLast." {
		t.Fatalf("dry run: %v, %d batch(es)\n%s", err, len(api.batches), sim.String())
	}
	if slices.Contains(res.RequestKinds, "insertTable") || slices.Contains(res.RequestKinds, "insertInlineImage") {
		t.Fatalf("the dry run planned the rounds: %v", res.RequestKinds)
	}
	if !slices.Equal(res.Followups, []string{"op 0: later batches place 1 table and 1 image in the empty paragraphs this one leaves"}) {
		t.Fatalf("rounds: %q", res.Followups)
	}
}

// In suggest mode every round is a suggestion.
func TestEmbedsSuggested(t *testing.T) {
	sim := newSim(t, "Title", "Last.")
	svc, api := simService(t, sim)
	res, err := svc.Edit(context.Background(), contentEdit("suggest", EditOp{Kind: plan.OpInsert, Location: after("p1"), Content: tableAndImage}))
	if err != nil || sim.String() != "Title\nLead\n[a|b / 1|2]\n<img>\nTail\nLast." {
		t.Fatalf("suggest: %v\n%s", err, sim.String())
	}
	if len(api.batches) != 5 || !strings.HasSuffix(res.Changes[0].Description, ", then placed 1 table and 1 image") {
		t.Fatalf("%d batch(es), changes %+v", len(api.batches), res.Changes)
	}
	// The fill and the tidy work over the call's own suggestion, which
	// only the first batch's reply named.
	if !slices.Equal(res.SuggestionIDs, []string{simSuggestion}) || strings.Contains(strings.Join(res.Warnings, "\n"), "nothing is removed until accepted") {
		t.Fatalf("suggestion ids %q, warnings %q", res.SuggestionIDs, res.Warnings)
	}
	for i, b := range api.batches {
		if b.WriteControl.WriteMode != "SUGGEST" {
			t.Errorf("batch %d (%s) is not a suggestion", i, plan.Kind(b.Requests[0]))
		}
	}
}

// In comment mode the content is proposed whole, tables and images in it.
func TestEmbedsAsComments(t *testing.T) {
	svc, api := writable(t, false)
	if _, err := svc.Edit(context.Background(), contentEdit("comment", EditOp{Kind: plan.OpInsert, Location: after("p9"), Content: tableAndImage})); err != nil {
		t.Fatal(err)
	}
	if len(api.batches) != 0 || len(api.comments) != 1 ||
		!strings.Contains(api.comments[0].Content, "Lead\na\tb\n1\t2\n[image: logo (https://e.test/logo.png)]\nTail") {
		t.Fatalf("%d batch(es), comments %+v", len(api.batches), api.comments)
	}
}

// A new document takes tables and images in its content too. Content
// that is only a table has its slot in the new document's one paragraph,
// and a table first in a body sits behind an empty paragraph.
func TestCreateWithEmbeds(t *testing.T) {
	for _, tc := range []struct{ content, want string }{
		{"Intro\n\n| a |\n|---|\n| b |", "Intro\n[a / b]\n"},
		{"| a |\n|---|\n| b |", "\n[a / b]\n"},
	} {
		sim := newSim(t, "")
		sim.revision = "rev-new" // what the fake's create reply carries
		svc := New(&fakeAPI{sim: sim}, Options{DefaultWriteMode: config.WriteDirect})
		res, err := svc.Create(context.Background(), CreateRequest{Title: "New", Content: tc.content})
		if err != nil || len(res.Warnings) != 0 {
			t.Fatalf("%q: %v %q", tc.content, err, res.Warnings)
		}
		if got := sim.String(); got != tc.want {
			t.Errorf("%q: body:\n%s\nwant:\n%s", tc.content, got, tc.want)
		}
	}
}

// A later batch works over the suggestions its own call made, and the
// guard does not report them; anybody else's it still does.
func TestOwnSuggestionsAreNotGuarded(t *testing.T) {
	svc, _ := writable(t, true)
	req := EditRequest{Document: fixtureID, Mode: "suggest", DryRun: true, Ops: []EditOp{{Kind: plan.OpDelete, Target: &Target{Text: "substantially"}}}}
	res, _, err := svc.editFetched(context.Background(), fetched(t, svc), req)
	if err != nil || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "1 suggestion (s1)") {
		t.Fatalf("another's suggestion: %v %q", err, res.Warnings)
	}
	req.own = []string{"s1"}
	if res, _, err = svc.editFetched(context.Background(), fetched(t, svc), req); err != nil || len(res.Warnings) != 0 {
		t.Fatalf("its own suggestion: %v %q", err, res.Warnings)
	}
}
