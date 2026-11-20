package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mmedum/google-docs-mcp/v4/internal/config"
	"github.com/mmedum/google-docs-mcp/v4/internal/doc/doctest"
	"github.com/mmedum/google-docs-mcp/v4/internal/gdocs"
	"github.com/mmedum/google-docs-mcp/v4/internal/plan"
)

// restyledFixture is the fixture with one pending suggested text-style
// change hung on the run "substantially", the shape the API really
// returns: no inserted or deleted ids anywhere, a style echoing back
// every inherited property, and a state naming the one the person asked
// for.
func restyledFixture(t *testing.T, id string, state *gdocs.TextStyleSuggestionState) []byte {
	t.Helper()
	var d gdocs.Document
	if err := json.Unmarshal(doctest.RawFixture(t), &d); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tab := range d.Tabs {
		for _, el := range tab.DocumentTab.Body.Content {
			if el.Paragraph == nil {
				continue
			}
			for _, pe := range el.Paragraph.Elements {
				if pe.TextRun == nil || pe.TextRun.Content != "substantially" {
					continue
				}
				pe.TextRun.SuggestedTextStyleChanges = map[string]gdocs.SuggestedTextStyle{
					id: {
						TextStyle:                &gdocs.TextStyle{Bold: true, Italic: true, FontSize: &gdocs.Dimension{Magnitude: 11, Unit: "PT"}},
						TextStyleSuggestionState: state,
					},
				}
				found = true
			}
		}
	}
	if !found {
		t.Fatal(`the fixture no longer has a run "substantially"; point this test at another one`)
	}
	raw, err := json.Marshal(&d)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func restyledService(t *testing.T, id string, state *gdocs.TextStyleSuggestionState) *Service {
	t.Helper()
	api := &fakeAPI{raw: restyledFixture(t, id, state)}
	return New(api, Options{DefaultWriteMode: config.WriteDirect})
}

// TestListSuggestionsSeesAFormattingSuggestion is the reported bug at
// the service boundary: list_suggestions said "0 pending suggestion(s)"
// for a document holding a style-only suggestion, because it collected
// suggestions by walking inserted and deleted ids and a restyling has
// neither.
func TestListSuggestionsSeesAFormattingSuggestion(t *testing.T) {
	svc := restyledService(t, "suggest.bold1", &gdocs.TextStyleSuggestionState{BoldSuggested: true})
	res, err := svc.ListSuggestions(context.Background(), fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	var got *Suggestion
	for i := range res.Suggestions {
		if res.Suggestions[i].ID == "suggest.bold1" {
			got = &res.Suggestions[i]
		}
	}
	if got == nil {
		t.Fatalf("the style-only suggestion is missing from the list: %+v", res.Suggestions)
	}
	if got.Kind != "format" {
		t.Errorf("kind: want format, got %q", got.Kind)
	}
	if strings.Join(got.Formats, "|") != "text: bold" {
		t.Errorf("formats: want [text: bold], got %v", got.Formats)
	}
	if got.Handle == "" {
		t.Error("a formatting suggestion on a run should name the block it sits in")
	}
	if got.Inserted != "" || got.Deleted != "" {
		t.Errorf("a restyling adds and removes nothing: %+v", got)
	}
	if !strings.Contains(res.Text, "suggest.bold1") || !strings.Contains(res.Text, "text: bold") {
		t.Errorf("the rendered list should name it:\n%s", res.Text)
	}
	if strings.HasPrefix(res.Text, "0 pending") {
		t.Errorf("the count still ignores it:\n%s", res.Text)
	}
}

// TestDirectFormatWarnsOnACollidingSuggestion covers the second half of
// #46: Google accepts a direct style change over a property a pending
// suggestion already sets and may apply nothing, so ops_applied on its
// own is misleading.
func TestDirectFormatWarnsOnACollidingSuggestion(t *testing.T) {
	bold := plan.TextStyleSpec{Bold: boolp(true)}
	italic := plan.TextStyleSpec{Italic: boolp(true)}
	suggestsBold := &gdocs.TextStyleSuggestionState{BoldSuggested: true}

	for _, tc := range []struct {
		name, mode string
		style      plan.TextStyleSpec
		state      *gdocs.TextStyleSuggestionState
		wantWarn   bool
	}{
		{"direct, same property", "direct", bold, suggestsBold, true},
		{"direct, different property", "direct", italic, suggestsBold, false},
		{"suggest mode collides with nothing", "suggest", bold, suggestsBold, false},
		{"comment mode writes nothing", "comment", bold, suggestsBold, false},
		{"direct, a size where a font is suggested", "direct",
			plan.TextStyleSpec{SizePt: 14},
			&gdocs.TextStyleSuggestionState{WeightedFontFamilySuggested: true}, false},
		// Google applies these over a suggestion of the same property, at
		// the same value or another (TestRawSuggestedStyleKinds), so only
		// bold, a paragraph property or one unprobed warns (#106).
		{"direct, a size where a size is suggested", "direct",
			plan.TextStyleSpec{SizePt: 14},
			&gdocs.TextStyleSuggestionState{FontSizeSuggested: true}, false},
		{"direct, italic where italic is suggested", "direct", italic,
			&gdocs.TextStyleSuggestionState{ItalicSuggested: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := restyledService(t, "suggest.bold1", tc.state)
			op := EditOp{Kind: plan.OpTextStyle, Target: &Target{Text: "substantially"}}
			op.Text = tc.style
			res, err := svc.Edit(context.Background(), EditRequest{
				Document: fixtureID, Mode: tc.mode, DryRun: true, Ops: []EditOp{op},
			})
			if err != nil {
				t.Fatal(err)
			}
			warned := false
			for _, w := range res.Warnings {
				if strings.Contains(w, "suggest.bold1") {
					warned = true
				}
			}
			if warned != tc.wantWarn {
				t.Errorf("warned=%t, want %t; warnings: %v", warned, tc.wantWarn, res.Warnings)
			}
			if tc.wantWarn && warned {
				w := strings.Join(res.Warnings, " ")
				// The warning has to say which property, or it sends the
				// reader back to the document to work it out.
				if !strings.Contains(w, "bold") {
					t.Errorf("the warning should name the property: %v", res.Warnings)
				}
			}
		})
	}
}

// TestOnlyAValueSetLandsOverASuggestion covers the edges of
// landsOverASuggestion: a boolean set false and a reset are unprobed,
// since on unstyled text they look like no change, so they still warn;
// and an op mixing bold with italic warns of bold alone.
func TestOnlyAValueSetLandsOverASuggestion(t *testing.T) {
	for _, tc := range []struct {
		name          string
		style         plan.TextStyleSpec
		state         *gdocs.TextStyleSuggestionState
		named, absent string
	}{
		{"italic off where italic is suggested", plan.TextStyleSpec{Italic: boolp(false)},
			&gdocs.TextStyleSuggestionState{ItalicSuggested: true}, "italic", ""},
		{"color reset where a color is suggested", plan.TextStyleSpec{Foreground: "none"},
			&gdocs.TextStyleSuggestionState{ForegroundColorSuggested: true}, "color", ""},
		{"bold and italic where both are suggested", plan.TextStyleSpec{Bold: boolp(true), Italic: boolp(true)},
			&gdocs.TextStyleSuggestionState{BoldSuggested: true, ItalicSuggested: true}, "bold", "italic"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := restyledService(t, "suggest.s1", tc.state)
			op := EditOp{Kind: plan.OpTextStyle, Target: &Target{Text: "substantially"}}
			op.Text = tc.style
			res, err := svc.Edit(context.Background(), EditRequest{
				Document: fixtureID, Mode: "direct", DryRun: true, Ops: []EditOp{op},
			})
			if err != nil {
				t.Fatal(err)
			}
			w := strings.Join(res.Warnings, " ")
			if !strings.Contains(w, "suggest.s1") || !strings.Contains(w, tc.named) {
				t.Errorf("want a warning naming %s: %v", tc.named, res.Warnings)
			}
			if tc.absent != "" && strings.Contains(w, tc.absent) {
				t.Errorf("Google applies %s, so the warning should leave it out: %v", tc.absent, res.Warnings)
			}
		})
	}
}

// TestClearFormattingCollidesWithEverySuggestedProperty checks the
// "*" fields mask, which clear_formatting compiles to.
// A size is one Google applies over a suggested size, so the warning
// here shows a reset lands nothing.
func TestClearFormattingCollidesWithEverySuggestedProperty(t *testing.T) {
	svc := restyledService(t, "suggest.size", &gdocs.TextStyleSuggestionState{FontSizeSuggested: true})
	res, err := svc.Edit(context.Background(), EditRequest{
		Document: fixtureID, Mode: "direct", DryRun: true,
		Ops: []EditOp{{Kind: plan.OpClearFormatting, Target: &Target{Text: "substantially"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	w := strings.Join(res.Warnings, " ")
	if !strings.Contains(w, "suggest.size") || !strings.Contains(w, "size") {
		t.Errorf("clear_formatting resets every property, so it collides with a suggested size: %v", res.Warnings)
	}
}

// TestABlankLineEditKeepsItsDirection guards the classification.
// collectEdits trims a run's trailing newline for display, so a
// suggestion that adds or removes a paragraph's lone "\n" run leaves
// both Inserted and Deleted empty — the two are the same string. Kind is
// decided on which loop recorded the edit, so the direction survives.
func TestABlankLineEditKeepsItsDirection(t *testing.T) {
	var d gdocs.Document
	if err := json.Unmarshal(doctest.RawFixture(t), &d); err != nil {
		t.Fatal(err)
	}
	// One blank line marked deleted, the next marked inserted.
	var marked int
	for _, tab := range d.Tabs {
		for _, el := range tab.DocumentTab.Body.Content {
			if el.Paragraph == nil {
				continue
			}
			for _, pe := range el.Paragraph.Elements {
				if pe.TextRun == nil || pe.TextRun.Content != "\n" || marked > 1 {
					continue
				}
				if marked == 0 {
					pe.TextRun.SuggestedDeletionIDs = []string{"suggest.blank.del"}
				} else {
					pe.TextRun.SuggestedInsertionIDs = []string{"suggest.blank.ins"}
				}
				marked++
			}
		}
	}
	if marked < 2 {
		t.Skip("the fixture needs two paragraphs whose only run is a newline")
	}
	raw, err := json.Marshal(&d)
	if err != nil {
		t.Fatal(err)
	}
	svc := New(&fakeAPI{raw: raw}, Options{DefaultWriteMode: config.WriteDirect})
	res, err := svc.ListSuggestions(context.Background(), fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"suggest.blank.del": "delete", "suggest.blank.ins": "insert"}
	seen := map[string]bool{}
	for _, sg := range res.Suggestions {
		w, ok := want[sg.ID]
		if !ok {
			continue
		}
		seen[sg.ID] = true
		if sg.Kind != w {
			t.Errorf("%s: kind = %q, want %q (inserted %q, deleted %q, formats %v)",
				sg.ID, sg.Kind, w, sg.Inserted, sg.Deleted, sg.Formats)
		}
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("%s is missing from the list: %+v", id, res.Suggestions)
		}
	}
}

// TestARestyleSpanningRunsIsReportedOnce is the shape Google produces on
// the first try: it splits a text run wherever the existing style
// changes, so one suggested bold across a link or an already-italic word
// is recorded on several runs. Each was reported as its own formatting
// change, and the quoted text was the first run's alone.
func TestARestyleSpanningRunsIsReportedOnce(t *testing.T) {
	style := map[string]gdocs.SuggestedTextStyle{
		"s.bold": {TextStyleSuggestionState: &gdocs.TextStyleSuggestionState{BoldSuggested: true}},
	}
	run := func(text string, italic bool) *gdocs.ParagraphElement {
		return &gdocs.ParagraphElement{TextRun: &gdocs.TextRun{
			Content:        text,
			TextStyle:      &gdocs.TextStyle{Italic: italic},
			SuggestedStyle: gdocs.SuggestedStyle{SuggestedTextStyleChanges: style},
		}}
	}
	raw, err := json.Marshal(&gdocs.Document{
		DocumentID: fixtureID, RevisionID: "r",
		Body: &gdocs.Body{Content: []*gdocs.StructuralElement{
			{Paragraph: &gdocs.Paragraph{
				ParagraphStyle: &gdocs.ParagraphStyle{NamedStyleType: "NORMAL_TEXT"},
				// One suggestion, three runs, because the middle word is
				// already italic.
				Elements: []*gdocs.ParagraphElement{run("sentence ", false), run("one", true), run(" here\n", false)},
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := New(&fakeAPI{raw: raw}, Options{DefaultWriteMode: config.WriteDirect})
	res, err := svc.ListSuggestions(context.Background(), fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	for _, sg := range res.Suggestions {
		if sg.ID != "s.bold" {
			continue
		}
		if len(sg.Formats) != 1 {
			t.Errorf("one suggestion over three runs is one formatting change, got %v", sg.Formats)
		}
		if sg.Restyled != "sentence one here" {
			t.Errorf("restyled = %q, want the whole span it covers", sg.Restyled)
		}
		return
	}
	t.Errorf("the suggestion is missing: %+v", res.Suggestions)
}

// TestASuggestedRowOrColumnIsStructure is the shape Google returns for
// a row or a column inserted as a suggestion
// (TestRawSuggestedTableInsertions): the id on each new cell and its
// newline, and for a row on the row too.
func TestASuggestedRowOrColumnIsStructure(t *testing.T) {
	const id = "s.grid"
	ins := gdocs.Suggested{SuggestedInsertionIDs: []string{id}}
	for _, tc := range []struct {
		name       string
		suggested  func(r, c int) bool
		onRow      int // the row that carries the id itself, or -1
		wantHandle string
	}{
		{"row", func(r, _ int) bool { return r == 1 }, 1, "tbl1:r2c1"},
		{"column", func(_, c int) bool { return c == 1 }, -1, "tbl1:r1c2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A 2×2 table at 1: each row opens at one index, each cell at
			// the next and holds a lone newline, and the table closes one
			// past its last cell.
			tbl := &gdocs.Table{Rows: 2, Columns: 2}
			at := int64(2)
			for r := range 2 {
				row := &gdocs.TableRow{StartIndex: at}
				if r == tc.onRow {
					row.Suggested = ins
				}
				for c := range 2 {
					run := &gdocs.TextRun{Content: "\n"}
					cell := &gdocs.TableCell{StartIndex: at, EndIndex: at + 2}
					if tc.suggested(r, c) {
						run.Suggested, cell.Suggested = ins, ins
					}
					cell.Content = []*gdocs.StructuralElement{{StartIndex: at + 1, EndIndex: at + 2, Paragraph: &gdocs.Paragraph{
						ParagraphStyle: &gdocs.ParagraphStyle{NamedStyleType: "NORMAL_TEXT"},
						Elements:       []*gdocs.ParagraphElement{{StartIndex: at + 1, EndIndex: at + 2, TextRun: run}},
					}}}
					row.TableCells = append(row.TableCells, cell)
					at += 2
				}
				row.EndIndex = at
				tbl.TableRows = append(tbl.TableRows, row)
			}
			raw, err := json.Marshal(&gdocs.Document{
				DocumentID: fixtureID, RevisionID: "r",
				Body: &gdocs.Body{Content: []*gdocs.StructuralElement{{StartIndex: 1, EndIndex: at + 1, Table: tbl}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			svc := New(&fakeAPI{raw: raw}, Options{DefaultWriteMode: config.WriteDirect})
			res, err := svc.ListSuggestions(context.Background(), fixtureID)
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Suggestions) != 1 {
				t.Fatalf("one suggested %s is one suggestion, got %+v", tc.name, res.Suggestions)
			}
			if sg := res.Suggestions[0]; sg.ID != id || sg.Kind != "structure" || sg.Handle != tc.wantHandle {
				t.Errorf("got %s %s at %s, want %s structure at %s", sg.ID, sg.Kind, sg.Handle, id, tc.wantHandle)
			}
		})
	}
}

// TestASuggestionInAPlainParagraphSendsNoReset covers where the reset
// after inserted text, and a paragraph's named style where it is the one
// it has, are left out: in suggest mode, for an op alone in its segment,
// in a paragraph with no style and no pending suggestion, where they
// would change nothing and Google would file them as a suggestion of
// their own (TestRawSuggestedInheritance).
func TestASuggestionInAPlainParagraphSendsNoReset(t *testing.T) {
	var blank string
	for _, b := range doctest.Fixture(t).AllBlocks() {
		if blank == "" && b.IsBlankParagraph() {
			blank = b.Handle
		}
	}
	if blank == "" {
		t.Fatal("the fixture needs a blank paragraph")
	}
	insert := func(at string, of *Target) EditOp {
		return EditOp{Kind: plan.OpInsert, Location: &Location{At: at, Of: of}, Content: "new"}
	}
	afterStepOne := insert("after", &Target{Text: "Step one"})
	for _, tc := range []struct {
		name, mode string
		pending    bool // a suggestion restyles "Step one"
		ops        []EditOp
		// wantReset is the reset after the text; wantNamed the named style
		// of a paragraph the content styles, which an insertion inside a
		// paragraph never sends.
		wantReset, wantNamed bool
	}{
		{"suggest, after plain text", "suggest", false, []EditOp{afterStepOne}, false, false},
		{"suggest, filling a blank paragraph", "suggest", false, []EditOp{insert("start", &Target{Handle: blank})}, false, false},
		{"suggest, end of the body", "suggest", false, []EditOp{insert("end", nil)}, true, true},
		{"suggest, after text in a code font", "suggest", false, []EditOp{insert("after", &Target{Text: "go build"})}, true, false},
		{"suggest, beside a pending suggestion", "suggest", true, []EditOp{afterStepOne}, true, false},
		{"suggest, two ops in the segment", "suggest", false, []EditOp{afterStepOne, insert("after", &Target{Text: "Step two"})}, true, false},
		{"direct, after plain text", "direct", false, []EditOp{afterStepOne}, true, false},
		{"direct, filling a blank paragraph", "direct", false, []EditOp{insert("start", &Target{Handle: blank})}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, api := writable(t, true)
			if tc.pending {
				api.raw = withStepOneRestyled(t, api.raw)
			}
			for i := range tc.ops {
				tc.ops[i].Seq = i
			}
			if _, err := svc.Edit(context.Background(), EditRequest{Document: fixtureID, Mode: tc.mode, Ops: tc.ops}); err != nil {
				t.Fatal(err)
			}
			if len(api.batches) != 1 {
				t.Fatalf("want one batch, got %d", len(api.batches))
			}
			reset, named := false, false
			for _, r := range api.batches[0].Requests {
				reset = reset || strings.Contains(string(r), `"fields":"*"`)
				named = named || strings.Contains(string(r), `"fields":"namedStyleType"`)
			}
			if reset != tc.wantReset || named != tc.wantNamed {
				t.Errorf("reset sent = %t, named style sent = %t, want %t and %t; requests: %s",
					reset, named, tc.wantReset, tc.wantNamed, kindsOf(t, api.batches[0].Requests))
			}
		})
	}
}

// withStepOneRestyled hangs a pending suggested italic on the fixture's
// run "Step one".
func withStepOneRestyled(t *testing.T, raw []byte) []byte {
	t.Helper()
	var d gdocs.Document
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tab := range d.Tabs {
		for _, el := range tab.DocumentTab.Body.Content {
			if el.Paragraph == nil {
				continue
			}
			for _, pe := range el.Paragraph.Elements {
				if pe.TextRun != nil && strings.HasPrefix(pe.TextRun.Content, "Step one") {
					pe.TextRun.SuggestedTextStyleChanges = map[string]gdocs.SuggestedTextStyle{"s.italic": {
						TextStyle:                &gdocs.TextStyle{Italic: true},
						TextStyleSuggestionState: &gdocs.TextStyleSuggestionState{ItalicSuggested: true}}}
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal(`the fixture no longer has a run "Step one"`)
	}
	out, err := json.Marshal(&d)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
