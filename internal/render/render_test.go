package render_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmedum/google-docs-mcp/internal/doc"
	"github.com/mmedum/google-docs-mcp/internal/doc/doctest"
	"github.com/mmedum/google-docs-mcp/internal/render"
)

var update = flag.Bool("update", false, "rewrite golden files")

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join(filepath.Dir(doctest.FixturePath(t)), "golden", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden %s (run with -update): %v", name, err)
	}
	if string(want) != got {
		t.Errorf("%s differs from golden.\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func body(t *testing.T) (*doc.Document, *doc.Segment) {
	d := doctest.Fixture(t)
	return d, d.Tabs[0].Body
}

func TestMarkdownGolden(t *testing.T) {
	_, seg := body(t)
	all := len(seg.Blocks)
	golden(t, "body.md", render.Markdown(seg, 0, all, render.Options{}).Text)
	golden(t, "body_handles.md", render.Markdown(seg, 0, all, render.Options{WithHandles: true}).Text)
	golden(t, "body_full.md", render.Markdown(seg, 0, all, render.Options{WithHandles: true, WithStyles: true, Suggestions: true}).Text)
}

func TestPlainGolden(t *testing.T) {
	_, seg := body(t)
	golden(t, "body.txt", render.Plain(seg, 0, len(seg.Blocks), render.Options{WithHandles: true}).Text)
}

func TestOutlineGolden(t *testing.T) {
	d, _ := body(t)
	tabs := render.OutlineData(d, nil)
	golden(t, "outline.md", render.Outline(d, tabs))
	if len(tabs) != 2 || tabs[0].Headings[0].HeadingID != "h.bg" || tabs[0].Headings[1].Level != 2 || tabs[0].Footnotes != 1 || tabs[0].Preamble != 2 {
		t.Fatalf("outline data: %+v", tabs[0])
	}
	if only := render.OutlineData(d, d.Tabs[1]); len(only) != 1 || only[0].Number != 2 {
		t.Fatalf("filtered outline: %+v", only)
	}
	empty := &doc.Document{Title: "Empty", Tabs: []*doc.Tab{{Number: 1, Title: "", Body: &doc.Segment{Kind: doc.SegmentBody}}}}
	if out := render.Outline(empty, render.OutlineData(empty, nil)); !strings.Contains(out, "(no headings)") || !strings.Contains(out, "Tab 1: Tab 1") {
		t.Fatalf("empty outline: %s", out)
	}
}

func TestMarkdownDetails(t *testing.T) {
	_, seg := body(t)
	md := render.Markdown(seg, 0, len(seg.Blocks), render.Options{}).Text
	checks := []string{
		"# Quarterly Report",
		"Revenue grew a lot in Q3.", // committed view hides the suggested insertion
		"- First point\n    - Nested point\n- Second point",
		"1. Step one\n2. Step two",
		"| **Name** | **Value** |\n| --- | --- |\n| Alpha | 1 |",
		"![image: Chart](kix.img1, 320×200pt)",
		"chart[^1]",
		"[the site](https://example.com)",
		"Run `go build`",
		"[^1]: See appendix.",
	}
	for _, c := range checks {
		if !strings.Contains(md, c) {
			t.Errorf("markdown lacks %q:\n%s", c, md)
		}
	}
	if strings.HasPrefix(md, "\n") {
		t.Error("markdown should not start with blank lines")
	}
	if strings.Contains(md, "{h.bg}") || strings.Contains(md, "[p1]") {
		t.Error("handles leaked into a plain read")
	}
	full := render.Markdown(seg, 0, len(seg.Blocks), render.Options{WithHandles: true, WithStyles: true, Suggestions: true}).Text
	for _, c := range []string{"{--a lot--}{>>s:s1<<}", "{++**substantially**++}{>>s:s1<<}", "[p2] # Background {h.bg}", "{color: #cc0000}", "{align: center}", "{style: TITLE}", "[sb1] <!-- section break: continuous -->", "[tbl1] table 2×2 (cells tbl1:r1c1 … tbl1:r2c2)"} {
		if !strings.Contains(full, c) {
			t.Errorf("full markdown lacks %q:\n%s", c, full)
		}
	}
}

func TestBudgetAndContinue(t *testing.T) {
	_, seg := body(t)
	r := render.Markdown(seg, 0, len(seg.Blocks), render.Options{MaxChars: 120})
	if !r.Truncated || r.ContinueFrom == "" || r.Blocks == 0 || r.Chars > 140 {
		t.Fatalf("budget: %+v", r)
	}
	// The continuation starts where the first read stopped and is never empty.
	var from int
	for i, b := range seg.Blocks {
		if b.Handle == r.ContinueFrom {
			from = i
		}
	}
	rest := render.Markdown(seg, from, len(seg.Blocks), render.Options{})
	if rest.Blocks == 0 || strings.Contains(rest.Text, "Quarterly Report") {
		t.Fatalf("continuation wrong: %+v", rest)
	}
	// A budget too small for one block still returns that block.
	one := render.Markdown(seg, 1, len(seg.Blocks), render.Options{MaxChars: 5})
	if one.Blocks != 1 || !one.Truncated {
		t.Fatalf("tiny budget: %+v", one)
	}
	p := render.Plain(seg, 0, len(seg.Blocks), render.Options{MaxChars: 60})
	if !p.Truncated || p.ContinueFrom == "" {
		t.Fatalf("plain budget: %+v", p)
	}
	if out := render.Markdown(seg, -5, 999, render.Options{}); out.Blocks != len(seg.Blocks)-1 {
		t.Fatalf("clamped range rendered %d blocks", out.Blocks)
	}
}

func TestPlainDetails(t *testing.T) {
	_, seg := body(t)
	p := render.Plain(seg, 0, len(seg.Blocks), render.Options{}).Text
	for _, c := range []string{"Quarterly Report\n", "- First point\n  - Nested point\n", "1. Step one\n2. Step two\n", "Name\tValue\nAlpha\t1", "Run go build"} {
		if !strings.Contains(p, c) {
			t.Errorf("plain lacks %q:\n%s", c, p)
		}
	}
	if strings.Contains(p, "**") || strings.Contains(p, "#") {
		t.Errorf("plain text contains markdown: %s", p)
	}
}

func TestEscapingAndEdgeCases(t *testing.T) {
	seg := &doc.Segment{Kind: doc.SegmentBody, Tab: &doc.Tab{Number: 1, InlineObjects: map[string]*doc.InlineObjectInfo{}}}
	para := func(handle, text string, style doc.TextStyle) *doc.Block {
		return &doc.Block{Kind: doc.KindParagraph, Handle: handle, Segment: seg, Paragraph: &doc.Paragraph{Runs: []*doc.Run{{Kind: doc.RunText, Text: text + "\n", Style: style}}}}
	}
	seg.Blocks = []*doc.Block{
		para("p1", "# not a heading", doc.TextStyle{}),
		para("p2", "- not a bullet", doc.TextStyle{}),
		para("p3", "1. not a list", doc.TextStyle{}),
		para("p4", " padded bold ", doc.TextStyle{Bold: true, Italic: true, Strikethrough: true}),
		para("p5", "a|b", doc.TextStyle{}),
		{Kind: doc.KindParagraph, Handle: "p6", Segment: seg, Paragraph: &doc.Paragraph{Runs: []*doc.Run{
			{Kind: doc.RunPerson, Text: "Ada"}, {Kind: doc.RunText, Text: " on "}, {Kind: doc.RunDate, Text: "2026-09-03"},
			{Kind: doc.RunRichLink, Text: "Sheet", LinkURI: "https://example.test/s"}, {Kind: doc.RunEquation}, {Kind: doc.RunAutoText, AutoTextType: "PAGE_NUMBER"},
			{Kind: doc.RunPageBreak}, {Kind: doc.RunInlineObject, ObjectID: "missing"}, {Kind: doc.RunText, Text: "\n"},
		}}},
		{Kind: doc.KindParagraph, Handle: "p7", Segment: seg, Paragraph: &doc.Paragraph{IsSubtitle: true, Runs: []*doc.Run{{Kind: doc.RunText, Text: "Sub\n"}}}},
		{Kind: doc.KindParagraph, Handle: "p8", Segment: seg, Paragraph: &doc.Paragraph{Runs: []*doc.Run{{Kind: doc.RunText, Text: "link\n", Style: doc.TextStyle{LinkHeadingID: "h.x", Underline: true, SmallCaps: true, Baseline: "SUPERSCRIPT", FontFamily: "Arial", FontSizePt: 11, Background: "#ffff00"}}}}},
	}
	md := render.Markdown(seg, 0, len(seg.Blocks), render.Options{}).Text
	for _, c := range []string{"\\# not a heading", "\\- not a bullet", "1\\. not a list", " ~~***padded bold***~~ ", "@Ada on 2026-09-03[Sheet](https://example.test/s){equation}{page number}<!-- page break -->![object](missing)", "*Sub*", "[link](#h.x)"} {
		if !strings.Contains(md, c) {
			t.Errorf("markdown lacks %q:\n%s", c, md)
		}
	}
	styled := render.Markdown(seg, 7, 8, render.Options{WithStyles: true}).Text
	if !strings.Contains(styled, "{font: Arial 11pt, background: #ffff00, small caps, superscript}") {
		t.Errorf("style annotation wrong: %s", styled)
	}
	// Table cells escape pipes and a nested table is summarised.
	inner := &doc.Table{Handle: "tbl1:r1c1/tbl1", Rows: 1, Cols: 1}
	tbl := &doc.Table{Handle: "tbl1", Rows: 1, Cols: 2}
	cellA := &doc.Cell{Table: tbl, Row: 1, Col: 1, Handle: "tbl1:r1c1"}
	cellA.Blocks = []*doc.Block{para("tbl1:r1c1/p1", "x|y", doc.TextStyle{}), {Kind: doc.KindTable, Handle: inner.Handle, Segment: seg, Cell: cellA, Table: inner}}
	cellB := &doc.Cell{Table: tbl, Row: 1, Col: 2, Handle: "tbl1:r1c2", Blocks: []*doc.Block{{Kind: doc.KindParagraph, Handle: "tbl1:r1c2/p1", Segment: seg, Paragraph: &doc.Paragraph{Bullet: &doc.BulletInfo{}, Runs: []*doc.Run{{Kind: doc.RunText, Text: "item\n"}}}}}}
	tbl.Cells = [][]*doc.Cell{{cellA, cellB}}
	seg.Blocks = []*doc.Block{{Kind: doc.KindTable, Handle: "tbl1", Segment: seg, Table: tbl}}
	md = render.Markdown(seg, 0, 1, render.Options{}).Text
	if !strings.Contains(md, `| x\|y<br>[nested table] | • item |`) {
		t.Errorf("table cell rendering: %s", md)
	}
	// A TOC renders its entries; a footnote reference without a number falls back to the id.
	seg.Blocks = []*doc.Block{
		{Kind: doc.KindTOC, Handle: "toc1", Segment: seg, TOC: &doc.TOC{Blocks: []*doc.Block{para("toc1/p1", "Entry", doc.TextStyle{})}}},
		{Kind: doc.KindParagraph, Handle: "p1", Segment: seg, Paragraph: &doc.Paragraph{Runs: []*doc.Run{{Kind: doc.RunFootnoteRef, FootnoteID: "kix.zz"}, {Kind: doc.RunText, Text: "\n"}}}},
	}
	md = render.Markdown(seg, 0, 2, render.Options{WithHandles: true}).Text
	if !strings.Contains(md, "[toc1] <!-- table of contents -->\n[toc1/p1] Entry") || !strings.Contains(md, "[^kix.zz]") {
		t.Errorf("toc/footnote rendering: %s", md)
	}
}

func TestCommentMarks(t *testing.T) {
	d, seg := body(t)
	// "Revenue grew " starts at 29; comment on "Revenue grew" and one that
	// ends at the paragraph's newline, plus one in another tab (ignored).
	marks := []render.Mark{
		{TabID: d.Tabs[0].ID, SegmentID: "", Start: 29, End: 41, Replies: 2, Thread: render.Thread{ID: "c1", Handle: "p3", Author: "Ann", Content: "Source?\nPlease cite."}},
		{TabID: d.Tabs[0].ID, SegmentID: "", Start: 60, End: 68, Thread: render.Thread{ID: "c2", Handle: "p3", Resolved: true, Content: "ok"}},
		{TabID: d.Tabs[1].ID, SegmentID: "", Start: 1, End: 4, Thread: render.Thread{ID: "c3", Handle: "tab2/p1", Content: "elsewhere"}},
		{Thread: render.Thread{ID: "c5", Content: "unlocated"}},
	}
	md := render.Markdown(seg, 0, len(seg.Blocks), render.Options{Marks: marks}).Text
	for _, c := range []string{"Revenue grew{>>c:c1<<} a lot in Q3.{>>c:c2<<}"} {
		if !strings.Contains(md, c) {
			t.Errorf("markdown lacks %q:\n%s", c, md)
		}
	}
	if strings.Contains(md, "c3") || strings.Contains(md, "comments:") {
		t.Error("mark from another tab leaked, or a footer appeared unasked")
	}
	// The footer lists the threads in the rendered range, one line each,
	// and counts the rest.
	md = render.Markdown(seg, 0, len(seg.Blocks), render.Options{Marks: marks, CommentFooter: true}).Text
	want := "\n\ncomments:\n- c:c1 [p3] by Ann: Source? Please cite. (2 replies)\n- c:c2 [p3] [resolved]: ok\n(2 more elsewhere or unlocated; use list_comments)"
	if !strings.HasSuffix(md, want) {
		t.Errorf("footer:\n%s", md)
	}
	plain := render.Plain(seg, 0, len(seg.Blocks), render.Options{Marks: marks, CommentFooter: true}).Text
	if !strings.HasSuffix(plain, want) || !strings.Contains(plain, "Revenue grew{>>c:c1<<}") {
		t.Errorf("plain footer:\n%s", plain)
	}
	if md := render.Markdown(seg, 0, 3, render.Options{Marks: marks[2:], CommentFooter: true}).Text; !strings.HasSuffix(md, "\n\ncomments: none in this range\n(2 more elsewhere or unlocated; use list_comments)") {
		t.Errorf("footer with nothing in range:\n%s", md)
	}
	if md := render.Markdown(seg, 0, 3, render.Options{CommentFooter: true}).Text; !strings.HasSuffix(md, "<!-- no comments -->") {
		t.Errorf("footer without threads:\n%s", md)
	}
	// A mark ending inside a styled run splits it without breaking markdown.
	marks = []render.Mark{{TabID: d.Tabs[0].ID, Start: 41, End: 44, Thread: render.Thread{ID: "c4", Handle: "p3"}}}
	md = render.Markdown(seg, 0, len(seg.Blocks), render.Options{Marks: marks, Suggestions: true}).Text
	if !strings.Contains(md, "{--a --}{>>s:s1<<}{>>c:c4<<}") && !strings.Contains(md, "{>>c:c4<<}") {
		t.Errorf("split mark missing:\n%s", md)
	}
}

func TestMergedCellsRender(t *testing.T) {
	d, seg := body(t)
	var tbl *doc.Table
	for _, b := range seg.Blocks {
		if b.Table != nil {
			tbl = b.Table
		}
	}
	tbl.Cells[1][0].ColSpan = 2
	tbl.Cells[1][1].MergedInto = tbl.Cells[1][0]
	md := render.Markdown(seg, 0, len(seg.Blocks), render.Options{WithHandles: true}).Text
	if !strings.Contains(md, "| Alpha |\n<!-- merged: tbl1:r2c1 spans 1×2 -->") || strings.Contains(md, "| Alpha | 1 |") {
		t.Fatalf("merged render:\n%s", md)
	}
	if !strings.Contains(render.Plain(seg, 0, len(seg.Blocks), render.Options{}).Text, "Alpha\n") {
		t.Fatal("plain render should skip the covered cell")
	}
	_ = d
}

func TestPlainCommentMarks(t *testing.T) {
	d, seg := body(t)
	marks := []render.Mark{{TabID: d.Tabs[0].ID, Start: 29, End: 41, Thread: render.Thread{ID: "c1", Handle: "p3"}}, {TabID: d.Tabs[0].ID, Start: 60, End: 68, Thread: render.Thread{ID: "c2", Handle: "p3"}}}
	text := render.Plain(seg, 0, len(seg.Blocks), render.Options{Marks: marks}).Text
	if !strings.Contains(text, "Revenue grew{>>c:c1<<} a lot in Q3.{>>c:c2<<}") {
		t.Fatalf("plain marks:\n%s", text)
	}
	res := render.Markdown(seg, 0, len(seg.Blocks), render.Options{MaxChars: 60})
	if !res.Truncated || res.To != 4 || seg.Blocks[res.To].Handle != res.ContinueFrom {
		t.Fatalf("To on truncation: %+v", res)
	}
	if res := render.Markdown(seg, 2, 5, render.Options{}); res.To != 5 || res.Truncated {
		t.Fatalf("To on full render: %+v", res)
	}
}

func TestMarkerOffsetsAndMergedHeader(t *testing.T) {
	d, seg := body(t)
	// Two marks ending inside one run: "Revenue grew a lot" starts at 29.
	marks := []render.Mark{{TabID: d.Tabs[0].ID, Start: 29, End: 36, Thread: render.Thread{ID: "c1", Handle: "p3"}}, {TabID: d.Tabs[0].ID, Start: 37, End: 41, Thread: render.Thread{ID: "c2", Handle: "p3"}}}
	md := render.Markdown(seg, 0, len(seg.Blocks), render.Options{Marks: marks}).Text
	if !strings.Contains(md, "Revenue{>>c:c1<<} grew{>>c:c2<<} a lot") {
		t.Fatalf("marker offsets:\n%s", md)
	}
	if txt := render.Plain(seg, 0, len(seg.Blocks), render.Options{Marks: marks}).Text; !strings.Contains(txt, "Revenue{>>c:c1<<} grew{>>c:c2<<} a lot") {
		t.Fatalf("plain marker offsets:\n%s", txt)
	}
	// A merged header row keeps the delimiter row in step.
	var tbl *doc.Table
	for _, b := range seg.Blocks {
		if b.Table != nil {
			tbl = b.Table
		}
	}
	tbl.Cells[0][0].ColSpan = 2
	tbl.Cells[0][1].MergedInto = tbl.Cells[0][0]
	md = render.Markdown(seg, 0, len(seg.Blocks), render.Options{}).Text
	if !strings.Contains(md, "| **Name** |\n| --- |\n| Alpha | 1 |") {
		t.Fatalf("merged header:\n%s", md)
	}
}
