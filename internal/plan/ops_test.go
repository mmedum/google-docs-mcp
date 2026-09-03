package plan

import (
	"errors"
	"strings"
	"testing"
)

func seg() Segment { return Segment{ID: "", TabID: "t.0", Start: 1, End: 100} }

func kinds(t *testing.T, r *Result) string {
	t.Helper()
	var ks []string
	for _, req := range r.Requests {
		v := view(t, req)
		switch v.kind {
		case "insertText":
			ks = append(ks, "ins@"+itoa(v.index))
		case "deleteContentRange":
			ks = append(ks, "del"+rangeStr(v.rng))
		default:
			ks = append(ks, v.kind+rangeStr(v.rng))
		}
	}
	return strings.Join(ks, " ")
}

func rangeStr(r [2]int64) string { return "[" + itoa(r[0]) + "," + itoa(r[1]) + ")" }

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func TestMinimalEdits(t *testing.T) {
	edits := MinimalEdits("Revenue grew a lot in Q3.", "Revenue grew substantially in Q3.", 29)
	if len(edits) != 1 || edits[0].Pos != 42 || edits[0].Delete != 5 || edits[0].Insert != "substantially" {
		t.Fatalf("edits = %+v", edits)
	}
	edits = MinimalEdits("one two three", "one 🎉 three four", 0)
	// "two" replaced at 4, " four" appended at 13, descending order.
	if len(edits) != 2 || edits[0].Pos != 13 || edits[0].Insert != " four" || edits[1].Pos != 4 || edits[1].Delete != 3 || edits[1].Insert != "🎉" {
		t.Fatalf("edits = %+v", edits)
	}
	reqs := EditRequests(edits, seg())
	var ks []string
	for _, r := range reqs {
		v := view(t, r)
		ks = append(ks, v.kind)
	}
	if strings.Join(ks, " ") != "insertText deleteContentRange insertText" {
		t.Fatalf("requests = %v", ks)
	}
	if MinimalEdits("same", "same", 3) != nil {
		t.Fatal("identical texts should yield no edits")
	}
}

func TestPlanReplaceMinimal(t *testing.T) {
	op := Op{Seq: 0, Kind: OpReplace, Seg: seg(), Description: "p3",
		Target: &Rng{Start: 29, End: 55, TabID: "t.0"}, TargetIsBlock: true, TargetText: "Revenue grew a lot in Q3.",
		Fragment: frag(t, "Revenue grew substantially in Q3.")}
	res, err := Plan([]Op{op}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := kinds(t, res); got != "del[42,47) ins@42" {
		t.Fatalf("requests = %s", got)
	}
	if !res.Summary[0].Minimal || res.Summary[0].Requests != 2 {
		t.Fatalf("summary = %+v", res.Summary)
	}
	op.Fragment = frag(t, "Revenue grew a lot in Q3.")
	res, err = Plan([]Op{op}, Options{})
	if err != nil || len(res.Requests) != 0 || !res.Summary[0].Minimal {
		t.Fatalf("no-op replace: %+v %v", res, err)
	}
}

func TestPlanReplaceWholeBlocks(t *testing.T) {
	s := seg()
	frag2 := frag(t, "New heading\n\n- item")
	cases := []struct {
		name   string
		target Rng
		want   string
	}{
		{"middle block", Rng{Start: 29, End: 69}, "del[29,69) ins@29"},
		{"last block", Rng{Start: 90, End: 100}, "del[89,99) ins@89"},
		{"only block", Rng{Start: 1, End: 100}, "del[1,99) ins@1"},
		{"partial text", Rng{Start: 30, End: 40}, "del[30,40) ins@30"},
	}
	for _, tc := range cases {
		op := Op{Kind: OpReplace, Seg: s, Target: &tc.target, TargetIsBlock: tc.name != "partial text", TargetText: "x\ny", Fragment: frag2}
		res, err := Plan([]Op{op}, Options{})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		got := kinds(t, res)
		if !strings.HasPrefix(got, tc.want) {
			t.Errorf("%s: got %s, want prefix %s", tc.name, got, tc.want)
		}
		ins := view(t, res.Requests[1]).body["text"].(string)
		switch tc.name {
		case "middle block":
			if !strings.HasSuffix(ins, "\n") || strings.HasPrefix(ins, "\n") {
				t.Errorf("middle block text %q should end with newline", ins)
			}
		case "last block":
			if !strings.HasPrefix(ins, "\n") || strings.HasSuffix(ins, "\n") {
				t.Errorf("last block text %q should start with newline", ins)
			}
		case "only block", "partial text":
			if strings.HasPrefix(ins, "\n") || strings.HasSuffix(ins, "\n") {
				t.Errorf("%s text %q should have no boundary newlines", tc.name, ins)
			}
		}
	}
}

func TestPlanInsertDeleteOrderingAndOverlap(t *testing.T) {
	s := seg()
	ops := []Op{
		{Seq: 0, Kind: OpInsert, Seg: s, Insert: &Loc{Index: 10, TabID: "t.0"}, Fragment: frag(t, "early"), Description: "before p2"},
		{Seq: 1, Kind: OpDelete, Seg: s, Target: &Rng{Start: 50, End: 60, TabID: "t.0"}, TargetIsBlock: true, Description: "p5"},
		{Seq: 2, Kind: OpAppend, Seg: s, Insert: &Loc{Index: 99, TabID: "t.0"}, AtEnd: true, Fragment: frag(t, "tail"), Description: "end"},
		{Seq: 3, Kind: OpTextStyle, Seg: s, Target: &Rng{Start: 5, End: 8, TabID: "t.0"}, Params: Params{Text: TextStyleSpec{Bold: new(true)}}, Description: "word"},
		{Seq: 4, Kind: OpReplaceAll, Seg: s, Params: Params{Find: "a", Replace: "b"}},
	}
	res, err := Plan(ops, Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := kinds(t, res)
	// Formatting first, then content descending (append 99, delete 50, insert 10), then replace_all.
	if !strings.HasPrefix(got, "updateTextStyle[5,8) ins@99 ") || !strings.Contains(got, " del[50,60) ") || !strings.Contains(got, " ins@10 ") || !strings.HasSuffix(got, " replaceAllText[0,0)") {
		t.Fatalf("order wrong: %s", got)
	}
	if strings.Index(got, "del[50,60)") > strings.Index(got, "ins@10") {
		t.Fatalf("delete should precede the lower insert: %s", got)
	}
	if len(res.Summary) != 5 || res.Summary[2].Kind != OpAppend {
		t.Fatalf("summary = %+v", res.Summary)
	}
	// Overlapping ops are refused.
	bad := []Op{
		{Seq: 0, Kind: OpDelete, Seg: s, Target: &Rng{Start: 10, End: 20}, Description: "a"},
		{Seq: 1, Kind: OpInsert, Seg: s, Insert: &Loc{Index: 15}, Fragment: frag(t, "x"), Description: "b"},
	}
	if _, err := Plan(bad, Options{}); err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("overlap not detected: %v", err)
	}
	bad[1] = Op{Seq: 1, Kind: OpReplace, Seg: s, Target: &Rng{Start: 15, End: 30}, Fragment: frag(t, "x"), Description: "b"}
	if _, err := Plan(bad, Options{}); err == nil {
		t.Fatal("range overlap not detected")
	}
	// Same ranges in different segments do not overlap.
	bad[1].Seg = Segment{ID: "kix.h1", TabID: "t.0", Start: 0, End: 50}
	if _, err := Plan(bad, Options{}); err != nil {
		t.Fatalf("different segments should not overlap: %v", err)
	}
}

func TestPlanDeleteEdges(t *testing.T) {
	s := seg()
	for _, tc := range []struct {
		target Rng
		block  bool
		want   string
	}{
		{Rng{Start: 40, End: 50}, true, "del[40,50)"},
		{Rng{Start: 90, End: 100}, true, "del[89,99)"},
		{Rng{Start: 1, End: 100}, true, "del[1,99)"},
		{Rng{Start: 95, End: 100}, false, "del[95,100)"},
	} {
		res, err := Plan([]Op{{Kind: OpDelete, Seg: s, Target: &tc.target, TargetIsBlock: tc.block}}, Options{})
		if err != nil || kinds(t, res) != tc.want {
			t.Errorf("%+v: got %s %v, want %s", tc.target, kinds(t, res), err, tc.want)
		}
	}
}

func TestGuard(t *testing.T) {
	s := seg()
	anchors := []Anchor{{Kind: "comment", ID: "c1", Start: 12, End: 20}, {Kind: "suggestion", ID: "s1", Start: 15, End: 18}, {Kind: "suggestion", ID: "s2", Start: 16, End: 17}}
	op := Op{Seq: 0, Kind: OpDelete, Seg: s, Target: &Rng{Start: 10, End: 30}, TargetIsBlock: true, Description: "p2", Anchors: anchors}
	_, err := Plan([]Op{op}, Options{Mode: ModeDirect})
	if !errors.Is(err, ErrBlocked) || !strings.Contains(err.Error(), "1 comment (c1) and 2 suggestions (s1, s2)") || !strings.Contains(err.Error(), "force: true") {
		t.Fatalf("guard: %v", err)
	}
	res, err := Plan([]Op{op}, Options{Mode: ModeDirect, Force: true})
	if err != nil || len(res.Requests) != 1 || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "forced") {
		t.Fatalf("forced: %+v %v", res, err)
	}
	res, err = Plan([]Op{op}, Options{Mode: ModeSuggest})
	if err != nil || len(res.Requests) != 1 || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "suggestion") {
		t.Fatalf("suggest: %+v %v", res, err)
	}
	// Inserts never trigger the guard.
	ins := Op{Kind: OpInsert, Seg: s, Insert: &Loc{Index: 5}, Fragment: frag(t, "x"), Anchors: anchors}
	if _, err := Plan([]Op{ins}, Options{}); err != nil {
		t.Fatal(err)
	}
}

func TestCommentModeProposals(t *testing.T) {
	s := seg()
	ops := []Op{
		{Seq: 0, Kind: OpReplace, Seg: s, Target: &Rng{Start: 10, End: 20}, TargetText: "old words", Fragment: frag(t, "new words"), Description: "p2"},
		{Seq: 1, Kind: OpDelete, Seg: s, Target: &Rng{Start: 30, End: 40}, TargetText: "gone", Description: "p3"},
		{Seq: 2, Kind: OpInsert, Seg: s, Insert: &Loc{Index: 50}, CommentAnchor: &Rng{Start: 50, End: 60}, Fragment: frag(t, "added"), Description: "after p4"},
		{Seq: 3, Kind: OpTextStyle, Seg: s, Target: &Rng{Start: 5, End: 8}, TargetText: "word", Params: Params{Text: TextStyleSpec{Bold: new(true), Italic: new(false), Font: "Arial", SizePt: 11, Foreground: "#ff0000", Link: "https://x"}}},
		{Seq: 4, Kind: OpParagraphStyle, Seg: s, Target: &Rng{Start: 5, End: 8}, TargetText: "para", Params: Params{Para: ParagraphStyleSpec{NamedStyle: "HEADING_2", Alignment: "CENTER", LineSpacing: 150, IndentStartPt: floatp(36), KeepWithNext: new(true)}}},
		{Seq: 5, Kind: OpBullets, Seg: s, Target: &Rng{Start: 5, End: 8}, Params: Params{Bullets: "numbered"}},
		{Seq: 6, Kind: OpReplaceAll, Seg: s, Params: Params{Find: "a", Replace: "b"}, CommentAnchor: &Rng{Start: 1, End: 2}},
		{Seq: 7, Kind: OpClearFormatting, Seg: s, Target: &Rng{Start: 5, End: 8}, TargetText: "x"},
		{Seq: 8, Kind: OpPageBreak, Seg: s, Insert: &Loc{Index: 9}, CommentAnchor: &Rng{Start: 9, End: 10}, Description: "after p1"},
		{Seq: 9, Kind: OpCreateHeader, Seg: s, Fragment: frag(t, "Header text"), CommentAnchor: &Rng{Start: 1, End: 2}},
	}
	res, err := Plan(ops, Options{Mode: ModeComment})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Requests) != 0 || len(res.Proposals) != len(ops) {
		t.Fatalf("comment mode: %d requests %d proposals", len(res.Requests), len(res.Proposals))
	}
	want := []string{
		"Proposed change to “old words”:\n\nnew words",
		"Proposed deletion of “gone”.",
		"Proposed insertion at after p4:\n\nadded",
		"Proposed formatting for “word”: bold, not italic, font Arial, 11pt, colour #ff0000, link https://x.",
		"Proposed paragraph style for “para”: heading 2, aligned center, line spacing 150%, indent 36pt, keep with next.",
		"Proposed: make this a numbered list.",
		"Proposed: replace every “a” with “b” in this tab.",
		"Proposed: clear the formatting of “x”.",
		"Proposed page break at after p1.",
		"Proposed header:\n\nHeader text",
	}
	for i, w := range want {
		if res.Proposals[i].Content != w {
			t.Errorf("proposal %d = %q, want %q", i, res.Proposals[i].Content, w)
		}
	}
	if res.Proposals[2].Range.Start != 50 || res.Proposals[0].Quote != "old words" {
		t.Fatalf("anchors: %+v", res.Proposals[:3])
	}
	// An op with nothing to anchor to is refused in comment mode.
	if _, err := Plan([]Op{{Kind: OpInsert, Seg: s, Insert: &Loc{Index: 1}, Fragment: frag(t, "x")}}, Options{Mode: ModeComment}); err == nil {
		t.Fatal("insert without anchor should fail in comment mode")
	}
}

func TestValidation(t *testing.T) {
	s := seg()
	bad := []Op{
		{Kind: OpInsert, Seg: s, Fragment: frag(t, "x")},
		{Kind: OpReplace, Seg: s, Target: &Rng{}},
		{Kind: OpTextStyle, Seg: s, Target: &Rng{}},
		{Kind: OpParagraphStyle, Seg: s, Target: &Rng{}},
		{Kind: OpBullets, Seg: s, Target: &Rng{}, Params: Params{Bullets: "stars"}},
		{Kind: OpReplaceAll, Seg: s},
		{Kind: OpKind("explode"), Seg: s},
		{Kind: OpCreateFooter, Seg: s},
	}
	for _, op := range bad {
		if _, err := Plan([]Op{op}, Options{}); err == nil {
			t.Errorf("%s should fail validation", op.Kind)
		}
	}
}

func TestFollowupsAndFormats(t *testing.T) {
	s := seg()
	ops := []Op{
		{Seq: 0, Kind: OpCreateHeader, Seg: s, Fragment: frag(t, "Draft")},
		{Seq: 1, Kind: OpFootnote, Seg: s, Insert: &Loc{Index: 20, TabID: "t.0"}, Fragment: frag(t, "Source"), Description: "after word"},
		{Seq: 2, Kind: OpBullets, Seg: s, Target: &Rng{Start: 30, End: 40}, Params: Params{Bullets: "none"}},
		{Seq: 3, Kind: OpBullets, Seg: s, Target: &Rng{Start: 30, End: 40}, Params: Params{Bullets: "checkbox"}},
		{Seq: 4, Kind: OpClearFormatting, Seg: s, Target: &Rng{Start: 30, End: 40}},
		{Seq: 5, Kind: OpParagraphStyle, Seg: s, Target: &Rng{Start: 30, End: 40}, Params: Params{Para: ParagraphStyleSpec{NamedStyle: "TITLE"}}},
		{Seq: 6, Kind: OpPageBreak, Seg: s, Insert: &Loc{Index: 60}},
	}
	res, err := Plan(ops, Options{Mode: ModeSuggest})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Followups) != 2 || res.Followups[0].Kind != OpFootnote || res.Followups[1].Kind != OpCreateHeader {
		t.Fatalf("followups = %+v", res.Followups)
	}
	got := kinds(t, res)
	// Formats first, content highest-index first, lists last (they shift
	// what follows), then global replacements.
	if !strings.HasPrefix(got, "updateTextStyle[30,40) updateParagraphStyle[30,40) insertPageBreak[0,0) createFootnote[0,0) createHeader[0,0) deleteParagraphBullets[30,40) createParagraphBullets[30,40)") {
		t.Fatalf("order: %s", got)
	}
	if !SuggestModeUnsupported["deleteTab"] {
		t.Fatal("helpers")
	}
}

func TestKindRegistryCoversEveryTool(t *testing.T) {
	want := map[Tool]string{
		ToolEdit:   "insert, append, replace, delete, replace_all, insert_break, insert_footnote, create_header, create_footer, delete_header or delete_footer",
		ToolFormat: "text_style, paragraph_style, bullets or clear_formatting",
		ToolTable:  "insert_table, set_cells, insert_rows, delete_rows, insert_columns, delete_columns, merge_cells, unmerge_cells, style_cells or pin_header_rows",
		ToolObject: "insert_object",
	}
	total := 0
	for tool, list := range want {
		if got := KindList(tool); got != list {
			t.Errorf("KindList(%s) = %q", tool, got)
		}
		for _, k := range KindsOf(tool) {
			total++
			info, ok := Info(k)
			if !ok || info.Tool != tool {
				t.Errorf("%s: info = %+v, %t", k, info, ok)
			}
			if ToolTable.Has(k) != (tool == ToolTable) {
				t.Errorf("%s: ToolTable.Has = %t", k, ToolTable.Has(k))
			}
		}
	}
	if total != len(kindTable) {
		t.Errorf("%d kinds listed by tool, %d in the registry", total, len(kindTable))
	}
	if _, ok := Info("bogus"); ok || ToolEdit.Has("bogus") || Noun("bogus") != "" {
		t.Error("unknown kind found")
	}
	// Every kind that creates a segment names the reply it comes back in.
	replies := map[OpKind]string{}
	for _, r := range SegmentReplies() {
		replies[r.Kind] = r.Reply
	}
	wantReplies := map[OpKind]string{OpFootnote: "createFootnote", OpCreateHeader: "createHeader", OpCreateFooter: "createFooter"}
	if len(replies) != len(wantReplies) {
		t.Errorf("segment replies = %v", replies)
	}
	for k, v := range wantReplies {
		if replies[k] != v {
			t.Errorf("%s reply = %q, want %q", k, replies[k], v)
		}
	}
	for _, k := range []OpKind{OpCreateHeader, OpDeleteHeader} {
		if Noun(k) != "header" {
			t.Errorf("%s noun = %q", k, Noun(k))
		}
	}
	deleting := []OpKind{OpReplace, OpDelete, OpReplaceAll, OpSetCells, OpDeleteRows, OpDeleteColumns, OpMergeCells, OpDeleteHeader, OpDeleteFooter}
	for _, k := range deleting {
		if !Deletes(k) {
			t.Errorf("%s should delete", k)
		}
	}
	for _, k := range []OpKind{OpInsert, OpAppend, OpTextStyle, OpInsertTable, OpUnmergeCells, OpInsertObject, OpCreateHeader} {
		if Deletes(k) {
			t.Errorf("%s should not delete", k)
		}
	}
	// Validation refuses what the registry says an op must carry.
	for _, op := range []Op{
		{Seq: 1, Kind: OpDelete},
		{Seq: 2, Kind: OpInsert, Fragment: frag(t, "x")},
		{Seq: 3, Kind: OpInsertRows},
		{Seq: 4, Kind: OpDeleteHeader},
		{Seq: 5, Kind: OpReplace, Target: &Rng{Start: 1, End: 2}},
		{Seq: 6, Kind: "bogus"},
	} {
		if err := validate(&op); err == nil {
			t.Errorf("op %d (%s): validated", op.Seq, op.Kind)
		}
	}
}

func TestFollowupsNeedContentOrData(t *testing.T) {
	cases := []struct {
		op   Op
		want bool
	}{
		{Op{Kind: OpCreateHeader, Fragment: frag(t, "x")}, true},
		{Op{Kind: OpCreateHeader}, false},
		{Op{Kind: OpFootnote, Fragment: frag(t, "x")}, true},
		{Op{Kind: OpInsertTable, Table: TableParams{Data: [][]string{{"a"}}}}, true},
		{Op{Kind: OpInsertTable}, false},
		{Op{Kind: OpInsert, Fragment: frag(t, "x")}, false},
	}
	for _, c := range cases {
		if got := c.op.NeedsFollowup(); got != c.want {
			t.Errorf("%s: NeedsFollowup = %t", c.op.Kind, got)
		}
	}
}
