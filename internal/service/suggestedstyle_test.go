package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mmedum/google-docs-mcp/internal/config"
	"github.com/mmedum/google-docs-mcp/internal/doc/doctest"
	"github.com/mmedum/google-docs-mcp/internal/gdocs"
	"github.com/mmedum/google-docs-mcp/internal/plan"
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
	return New(api, Options{Preview: true, DefaultWriteMode: config.WriteDirect})
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
		{"direct, a size where a size is suggested", "direct",
			plan.TextStyleSpec{SizePt: 14},
			&gdocs.TextStyleSuggestionState{FontSizeSuggested: true}, true},
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

// TestClearFormattingCollidesWithEverySuggestedProperty checks the
// "*" fields mask, which clear_formatting compiles to.
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
	svc := New(&fakeAPI{raw: raw}, Options{Preview: true, DefaultWriteMode: config.WriteDirect})
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
	svc := New(&fakeAPI{raw: raw}, Options{Preview: true, DefaultWriteMode: config.WriteDirect})
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
