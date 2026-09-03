package service

import (
	"context"
	"sync"
	"testing"

	"github.com/mmedum/google-docs-mcp/internal/doc"
)

func TestFindTextWithinParagraphs(t *testing.T) {
	svc, _ := newService(t)
	f, err := svc.Fetch(context.Background(), fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	seg := f.Doc.Tabs[0].Body
	handles := func(hits []textHit) []string {
		var out []string
		for _, h := range hits {
			out = append(out, h.block.Handle+"="+sliceUTF16(h.block.Paragraph, h.start, h.end))
		}
		return out
	}
	cases := []struct {
		needle   string
		caseFold bool
		want     []string
	}{
		{"Second point", false, []string{"p7=Second point"}},
		{"point", false, []string{"p5=point", "p6=point", "p7=point"}},
		{"point Second", false, nil}, // never across paragraphs
		{"REVENUE", false, nil},
		{"REVENUE", true, []string{"p3=Revenue"}},
		{"DONE 🎉 AND", true, []string{"p13=Done 🎉 and"}},
		{doc.Normalize("“quoted”"), false, []string{"p13=“quoted”"}},
		{"Alpha", false, []string{"tbl1:r2c1/p1=Alpha"}},
		{"", true, nil},
	}
	for _, c := range cases {
		got := handles(f.findText(seg, c.needle, c.caseFold))
		if len(got) != len(c.want) {
			t.Errorf("find(%q, fold=%t) = %v, want %v", c.needle, c.caseFold, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("find(%q, fold=%t) = %v, want %v", c.needle, c.caseFold, got, c.want)
			}
		}
	}
	// The index is shared safely across concurrent searches.
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if n := len(f.findText(seg, "point", true)); n != 3 {
				t.Errorf("concurrent find = %d hits", n)
			}
		}()
	}
	wg.Wait()
}

func TestAnchorsInReportsTheFirstOverlappingOccurrence(t *testing.T) {
	svc, _ := newService(t)
	f, err := svc.Fetch(context.Background(), fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	seg := f.Doc.Tabs[0].Body
	var ins, del *doc.Run
	for _, b := range seg.Blocks {
		if b.Paragraph == nil {
			continue
		}
		for _, r := range b.Paragraph.Runs {
			switch {
			case len(r.Inserted) > 0 && ins == nil:
				ins = r
			case len(r.Deleted) > 0 && del == nil:
				del = r
			}
		}
	}
	if ins == nil || del == nil || ins.Inserted[0] != del.Deleted[0] {
		t.Fatalf("fixture should carry one suggestion on an inserted and a deleted run: %+v %+v", ins, del)
	}
	first, second := ins, del
	if second.Start < first.Start {
		first, second = second, first
	}
	// A range covering only the later run reports the suggestion there,
	// not at its earlier run.
	as := f.anchorsIn(seg, second.Start, second.End, nil)
	if len(as) != 1 || as[0].Kind != "suggestion" || as[0].Start != second.Start || as[0].Text != second.Text {
		t.Fatalf("later run only: %+v", as)
	}
	// A range covering both reports it once, at the first.
	as = f.anchorsIn(seg, first.Start, second.End, nil)
	if len(as) != 1 || as[0].Start != first.Start {
		t.Fatalf("both runs: %+v", as)
	}
	// Ranges without anchors report nothing; the image, footnote and
	// comment kinds are found in the block that holds them.
	if as := f.anchorsIn(seg, 1, 5, nil); len(as) != 0 {
		t.Fatalf("title: %+v", as)
	}
	p11, _ := f.Doc.FindHandle("p11")
	kinds := map[string]bool{}
	for _, a := range f.anchorsIn(seg, p11.Start, p11.End, []CommentThread{{ID: "c1", Handle: "p11", Tab: seg.Tab.ID, Segment: seg.ID, Start: p11.Start, End: p11.End}}) {
		kinds[a.Kind] = true
	}
	if !kinds["image"] || !kinds["footnote"] || !kinds["comment"] {
		t.Fatalf("p11 anchors: %v", kinds)
	}
}
