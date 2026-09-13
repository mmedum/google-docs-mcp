package render_test

import (
	"strings"
	"testing"

	"github.com/mmedum/google-docs-mcp/internal/doc"
	"github.com/mmedum/google-docs-mcp/internal/render"
)

// restyled builds a one-paragraph segment where the middle run carries a
// pending suggested restyling and nothing is inserted or deleted.
func restyled(t *testing.T, changes []doc.StyleChange) *doc.Segment {
	t.Helper()
	seg := &doc.Segment{ID: "", Kind: doc.SegmentBody}
	seg.Tab = &doc.Tab{ID: "t.0", Number: 1, Body: seg}
	p := &doc.Paragraph{NamedStyle: "NORMAL_TEXT", Runs: []*doc.Run{
		{Kind: doc.RunText, Text: "Plain ", Start: 1, End: 7},
		{Kind: doc.RunText, Text: "restyled", Start: 7, End: 15, StyleChanges: changes},
		{Kind: doc.RunText, Text: " tail\n", Start: 15, End: 21},
	}}
	seg.Blocks = []*doc.Block{{Handle: "p1", Kind: doc.KindParagraph, Start: 1, End: 21, Paragraph: p}}
	return seg
}

// TestRestyleUsesHighlightNotInsertion pins the CriticMarkup choice: a
// restyling is not an insertion or a deletion, so it gets the highlight
// form and a note carrying the suggestion id, which is what
// review_suggestion takes.
func TestRestyleUsesHighlightNotInsertion(t *testing.T) {
	seg := restyled(t, []doc.StyleChange{{ID: "suggest.a", Props: []string{"bold", "weightedFontFamily"}}})
	got := render.Markdown(seg, 0, 1, render.Options{Suggestions: true}).Text
	if !strings.Contains(got, "{==restyled==}{>>s:suggest.a suggests bold, font<<}") {
		t.Errorf("want the highlight form with the id and the properties, got:\n%s", got)
	}
	for _, wrong := range []string{"{++restyled++}", "{--restyled--}"} {
		if strings.Contains(got, wrong) {
			t.Errorf("a restyling adds and removes nothing, so %s is wrong:\n%s", wrong, got)
		}
	}
	// The runs either side are untouched: Google splits a run to hang a
	// suggestion on part of it, and the marker must not spread.
	if strings.Contains(got, "{==Plain") || strings.Contains(got, "tail==}") {
		t.Errorf("the marker spread beyond the suggested run:\n%s", got)
	}
}

// TestRestyleShowsUnderWithStyles covers the other read: with
// CriticMarkup off there is no marker to carry the suggestion, so
// with_styles is the only place it can appear.
func TestRestyleShowsUnderWithStyles(t *testing.T) {
	seg := restyled(t, []doc.StyleChange{{ID: "suggest.a", Props: []string{"foregroundColor"}}})
	got := render.Markdown(seg, 0, 1, render.Options{WithStyles: true}).Text
	if !strings.Contains(got, "{suggested: color}") {
		t.Errorf("want the suggested property annotated, got:\n%s", got)
	}
	// With both on, the id-carrying form is the only one: saying it twice
	// in one line is noise.
	both := render.Markdown(seg, 0, 1, render.Options{WithStyles: true, Suggestions: true}).Text
	if strings.Contains(both, "{suggested:") {
		t.Errorf("with CriticMarkup on, the annotation duplicates the marker:\n%s", both)
	}
	if !strings.Contains(both, ">>s:suggest.a suggests color<<") {
		t.Errorf("with both on the marker should carry the id:\n%s", both)
	}
}

// TestRestyleOnSuggestedInsertionKeepsBothMarkers is the case the issue
// started from: one run can be both suggested text and suggested
// formatting, and neither marker may hide the other.
func TestRestyleOnSuggestedInsertionKeepsBothMarkers(t *testing.T) {
	seg := restyled(t, []doc.StyleChange{{ID: "suggest.style", Props: []string{"bold"}}})
	seg.Blocks[0].Paragraph.Runs[1].Inserted = []string{"suggest.text"}
	got := render.Markdown(seg, 0, 1, render.Options{Suggestions: true}).Text
	if !strings.Contains(got, "{++restyled++}{>>s:suggest.text<<}{>>s:suggest.style suggests bold<<}") {
		t.Errorf("want the insertion marker and the restyle note side by side, got:\n%s", got)
	}
	if strings.Contains(got, "{==") {
		t.Errorf("an already-marked insertion should not also be highlighted:\n%s", got)
	}
}

// TestNoSuggestionNoMarker guards the quiet case: every read of every
// document without a formatting suggestion must be byte-identical to
// what it was, which is what the goldens assert in bulk and this states
// as an intention.
func TestNoSuggestionNoMarker(t *testing.T) {
	seg := restyled(t, nil)
	for _, o := range []render.Options{
		{Suggestions: true}, {WithStyles: true}, {WithStyles: true, Suggestions: true},
	} {
		got := render.Markdown(seg, 0, 1, o).Text
		if strings.Contains(got, "{==") || strings.Contains(got, "suggested:") || strings.Contains(got, ">>s:") {
			t.Errorf("options %+v marked a document with no suggestion:\n%s", o, got)
		}
	}
}

// TestParagraphRestyleIsMarked covers the half of the model the
// renderers could not see: a suggested paragraph style or bullet change
// is reported by list_suggestions, and read_document said nothing at all
// while its own description promised a marker.
func TestParagraphRestyleIsMarked(t *testing.T) {
	seg := restyled(t, nil)
	p := seg.Blocks[0].Paragraph
	p.StyleChanges = []doc.StyleChange{{ID: "suggest.p", Props: []string{"alignment"}}}
	p.BulletChanges = []doc.StyleChange{{ID: "suggest.b", Props: []string{"listId"}}}

	got := render.Markdown(seg, 0, 1, render.Options{Suggestions: true}).Text
	for _, want := range []string{
		"{>>s:suggest.p suggests paragraph alignment<<}",
		"{>>s:suggest.b suggests bullet list id<<}",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("want %s, got:\n%s", want, got)
		}
	}
	// Not the highlight form: a paragraph-level suggestion covers the
	// whole paragraph, and wrapping the line would swallow a heading's
	// own marker.
	if strings.Contains(got, "{==") {
		t.Errorf("a paragraph restyle should not wrap the line:\n%s", got)
	}
	// And with CriticMarkup off there is no marker at all.
	if quiet := render.Markdown(seg, 0, 1, render.Options{}).Text; strings.Contains(quiet, ">>s:") {
		t.Errorf("markers belong to include_suggestions:\n%s", quiet)
	}
	// with_styles alone has no marker to carry the suggestion, so the
	// annotation is where it has to appear — otherwise the read prints
	// the paragraph's committed alignment and says nothing about the
	// pending suggestion to change it, which is what the run path was
	// already careful about.
	styled := render.Markdown(seg, 0, 1, render.Options{WithStyles: true}).Text
	for _, want := range []string{"suggested:", "alignment", "list id"} {
		if !strings.Contains(styled, want) {
			t.Errorf("with_styles should report the pending paragraph restyle (%q):\n%s", want, styled)
		}
	}
}
