package doc_test

import (
	"encoding/json"
	"testing"

	"github.com/mmedum/google-docs-mcp/internal/doc/doctest"
	"github.com/mmedum/google-docs-mcp/internal/gdocs"
)

// TestLargeFixtureIsConsistent checks the generator's index bookkeeping:
// blocks tile the segment, nested blocks sit inside their cell, and the
// counts match the spec.
func TestLargeFixtureIsConsistent(t *testing.T) {
	spec := doctest.LargeSpec{Sections: 3, Subsections: 2, Paragraphs: 4, ListItems: 4, TableEvery: 2, Comments: 5, Anchored: true, Suggestions: 3, Footnotes: 2, Tabs: 2}
	d := doctest.LargeDoc(t, spec)
	if len(d.Tabs) != 2 {
		t.Fatalf("tabs = %d", len(d.Tabs))
	}
	for _, tab := range d.Tabs {
		var prev int64
		for _, b := range tab.Body.Blocks {
			if b.Start != prev {
				t.Fatalf("block %s starts at %d, previous ended at %d", b.Handle, b.Start, prev)
			}
			if b.Table != nil {
				for _, row := range b.Table.Cells {
					for _, c := range row {
						if c.Start < b.Start || c.End > b.End || len(c.Blocks) != 1 || c.Blocks[0].Start != c.Start+1 || c.Blocks[0].End != c.End {
							t.Fatalf("cell %s [%d,%d) does not fit table [%d,%d)", c.Handle, c.Start, c.End, b.Start, b.End)
						}
					}
				}
			}
			prev = b.End
		}
		if len(tab.Footnotes) != 2 {
			t.Errorf("tab %d footnotes = %d", tab.Number, len(tab.Footnotes))
		}
	}
	st := d.Stats()
	if st.Tables != 3*2 || st.Headings != 2*(3+3*2) || st.Suggestions != 6 || st.Footnotes != 4 {
		t.Errorf("stats = %+v", st)
	}
	for i, h := range []string{"h.t1.s1", "h.t2.s3.2"} {
		if _, _, ok := d.HeadingByID(h); !ok {
			t.Errorf("heading %d %s missing", i, h)
		}
	}
	// The JSON form decodes back to the same structure.
	var w gdocs.Document
	if err := json.Unmarshal(doctest.LargeJSON(spec), &w); err != nil {
		t.Fatal(err)
	}
	if len(w.Comments) != 5 || len(w.Tabs[0].DocumentTab.CommentAnchors) != 5 {
		t.Errorf("comments = %d, anchors = %d", len(w.Comments), len(w.Tabs[0].DocumentTab.CommentAnchors))
	}
}
